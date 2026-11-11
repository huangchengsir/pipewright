package dbmigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// Target is an initialized, exclusively owned migration destination. Construction
// never overwrites an existing business schema. Runtime startup must hold the
// separate local migration lock while using it.
type Target struct {
	activated        bool
	db               *store.Store
	taskID, identity string
}
type TableProof struct {
	Table  string `json:"table"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type CopyReport struct {
	ManifestSHA256 string       `json:"manifestSha256"`
	Tables         []TableProof `json:"tables"`
}

const batchRows = 64
const maxRowBytes = 64 << 20

func InitializeTarget(ctx context.Context, db *store.Store, taskID string) (*Target, error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return nil, ErrInvalidState
	}
	if err := CheckEmpty(ctx, db); err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		return nil, err
	}
	id, err := db.Identity(ctx)
	if err != nil {
		return nil, ErrIdentity
	}
	target := &Target{db: db, taskID: taskID, identity: id}
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, table := range Manifest() {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+store.Identifier(db.Dialect, table.Name)).Scan(&count); err != nil {
			return nil, ErrMismatch
		}
		if count != int64(len(table.SeedRows)) {
			return nil, ErrTargetNotEmpty
		}
		for _, seed := range table.SeedRows {
			row, err := normalizeRow(table, seed)
			if err != nil {
				return nil, err
			}
			if err := verifyRow(ctx, tx, db.Dialect, table, row); err != nil {
				return nil, ErrTargetNotEmpty
			}
			where, args := rowWhere(db.Dialect, table, row, true)
			result, err := tx.ExecContext(ctx, "DELETE FROM "+store.Identifier(db.Dialect, table.Name)+" WHERE "+where, args...)
			if err != nil {
				return nil, ErrTargetNotEmpty
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				return nil, ErrTargetNotEmpty
			}
		}
	}
	if err := target.insertReceipt(ctx, tx, "__initialization__", 0, 0, ManifestDigest()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrCommitUnknown
	}
	return target, nil
}

// ResumeTarget only recognizes a receipt bound to the externally validated task
// and exact target identity. A partial/unknown initialization is never replayed.
func ResumeTarget(ctx context.Context, db *store.Store, taskID, identity string) (*Target, error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return nil, ErrInvalidState
	}
	id, err := db.Identity(ctx)
	if err != nil || id != identity {
		return nil, ErrIdentity
	}
	t := &Target{db: db, taskID: taskID, identity: id}
	n, digest, err := t.receipt(ctx, "__initialization__", 0)
	if err != nil || n != 0 || digest != ManifestDigest() {
		return nil, ErrMismatch
	}
	return t, nil
}
func (t *Target) Identity() string { return t.identity }

// CopySnapshot copies a consistent source snapshot and verifies every target row
// before recording each batch. The controller must first stop the normal process
// and hold its local lock; this function does not claim to freeze runtime writers
// or activate a target. Its success means data verification only.
func CopySnapshot(ctx context.Context, source *store.Store, target *Target) (*CopyReport, error) {
	return snapshot(ctx, source, target, true)
}

// VerifySnapshot independently rereads both databases and their durable receipts.
// It never inserts rows or receipts, including when a prior copy was incomplete.
func VerifySnapshot(ctx context.Context, source *store.Store, target *Target) (*CopyReport, error) {
	return snapshot(ctx, source, target, false)
}
func snapshot(ctx context.Context, source *store.Store, target *Target, write bool) (*CopyReport, error) {
	return snapshotProgress(ctx, source, target, write, nil)
}
func snapshotProgress(ctx context.Context, source *store.Store, target *Target, write bool, progress func(TableProof) error) (*CopyReport, error) {
	sourceID, err := source.Identity(ctx)
	if err != nil || sourceID == target.identity {
		return nil, ErrIdentity
	}
	if id, err := target.db.Identity(ctx); err != nil || id != target.identity {
		return nil, ErrIdentity
	}
	opts := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	if source.Dialect == store.DM {
		opts.Isolation = sql.LevelSerializable
	}
	if source.Dialect == store.SQLServer {
		opts.ReadOnly = false
	}
	if source.Dialect == store.SQLite {
		opts = nil
	}
	snapshot, err := source.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	defer snapshot.Rollback()
	if err := checkManifest(ctx, snapshot, source.Dialect); err != nil {
		return nil, err
	}
	report := &CopyReport{ManifestSHA256: ManifestDigest(), Tables: []TableProof{}}
	for _, table := range Manifest() {
		proof, err := copyTable(ctx, snapshot, source.Dialect, target, table, write)
		if err != nil {
			return nil, fmt.Errorf("dbmigration: table %s: %w", table.Name, err)
		}
		report.Tables = append(report.Tables, proof)
		if progress != nil {
			if err = progress(proof); err != nil {
				return nil, err
			}
		}
	}
	if err := snapshot.Commit(); err != nil {
		return nil, ErrCommitUnknown
	}
	return report, nil
}
func copyTable(ctx context.Context, source *sql.Tx, d store.Dialect, target *Target, table Table, write bool) (TableProof, error) {
	proof := TableProof{Table: table.Name}
	h := sha256.New()
	keys := table.keys()
	if len(keys) == 0 {
		return proof, ErrUnsupported
	}
	order := make([]string, len(keys))
	for i, key := range keys {
		order[i] = store.Identifier(d, table.Columns[key].Name)
	}
	rows, err := source.QueryContext(ctx, "SELECT "+columnsSQL(d, table)+" FROM "+store.Identifier(d, table.Name)+" ORDER BY "+strings.Join(order, ","))
	if err != nil {
		return proof, err
	}
	defer rows.Close()
	batch := make([][]any, 0, batchRows)
	number := int64(0)
	bytesInBatch := 0
	flush := func() error {
		err := target.writeBatch(ctx, table, number, batch, write)
		if err == nil {
			number++
			batch = nil
			bytesInBatch = 0
		}
		return err
	}
	for rows.Next() {
		values := make([]any, len(table.Columns))
		dest := make([]any, len(values))
		for i := range dest {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return proof, ErrMismatch
		}
		row, err := normalizeRow(table, values)
		if err != nil {
			return proof, err
		}
		size := 0
		for _, value := range row {
			switch v := value.(type) {
			case string:
				size += len(v)
				if target.db.Dialect == store.PostgreSQL && strings.ContainsRune(v, 0) {
					return proof, errors.New("dbmigration: PostgreSQL cannot preserve text containing NUL")
				}
			case []byte:
				size += len(v)
			default:
				size += 8
			}
		}
		if size > maxRowBytes {
			return proof, errors.New("dbmigration: row exceeds migration memory limit")
		}
		if len(batch) > 0 && (len(batch) >= batchRows || bytesInBatch+size > 8<<20) {
			if err := flush(); err != nil {
				return proof, err
			}
		}
		batch = append(batch, row)
		bytesInBatch += size
		proof.Rows++
		hashRow(h, table, row)
	}
	if err := rows.Err(); err != nil {
		return proof, err
	}
	if len(batch) > 0 || number == 0 {
		if err := flush(); err != nil {
			return proof, err
		}
	}
	var targetCount int64
	if err := target.db.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+store.Identifier(target.db.Dialect, table.Name)).Scan(&targetCount); err != nil || targetCount != target.expectedCount(table.Name, proof.Rows) {
		return proof, ErrMismatch
	}
	proof.SHA256 = hex.EncodeToString(h.Sum(nil))
	return proof, nil
}
func columnsSQL(d store.Dialect, table Table) string {
	cols := make([]string, len(table.Columns))
	for i, col := range table.Columns {
		cols[i] = store.Identifier(d, col.Name)
	}
	return strings.Join(cols, ",")
}
func rowWhere(d store.Dialect, table Table, row []any, all bool) (string, []any) {
	var parts []string
	var args []any
	for i, col := range table.Columns {
		if !all && col.Key == 0 {
			continue
		}
		name := store.Identifier(d, col.Name)
		if row[i] == nil {
			parts = append(parts, name+" IS NULL")
		} else {
			parts = append(parts, name+"=?")
			args = append(args, row[i])
		}
	}
	return strings.Join(parts, " AND "), args
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func verifyRow(ctx context.Context, q queryer, d store.Dialect, table Table, want []any) error {
	where, args := rowWhere(d, table, want, false)
	values := make([]any, len(want))
	dest := make([]any, len(want))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := q.QueryRowContext(ctx, "SELECT "+columnsSQL(d, table)+" FROM "+store.Identifier(d, table.Name)+" WHERE "+where, args...).Scan(dest...); err != nil {
		return ErrMismatch
	}
	got, err := normalizeRow(table, values)
	if err != nil {
		return err
	}
	a, b := sha256.New(), sha256.New()
	hashRow(a, table, want)
	hashRow(b, table, got)
	if !strings.EqualFold(hex.EncodeToString(a.Sum(nil)), hex.EncodeToString(b.Sum(nil))) {
		return ErrMismatch
	}
	return nil
}
func (t *Target) receipt(ctx context.Context, table string, number int64) (int64, string, error) {
	var count int64
	var digest string
	err := t.db.DB.QueryRowContext(ctx, "SELECT row_count,content_sha256 FROM database_migration_batches WHERE migration_id=? AND table_name=? AND batch_no=?", t.taskID, table, number).Scan(&count, &digest)
	return count, digest, err
}
func (t *Target) insertReceipt(ctx context.Context, tx *sql.Tx, table string, number, count int64, digest string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO database_migration_batches(migration_id,table_name,batch_no,row_count,content_sha256,committed_at) VALUES(?,?,?,?,?,?)", t.taskID, table, number, count, digest, time.Now().UnixMilli())
	return err
}
func (t *Target) writeBatch(ctx context.Context, table Table, number int64, batch [][]any, write bool) error {
	if t.activated && write {
		return ErrInvalidState
	}
	hash := sha256.New()
	hashField(hash, []byte(ManifestDigest()))
	for _, row := range batch {
		hashRow(hash, table, row)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	count, recorded, err := t.receipt(ctx, table.Name, number)
	if err == nil {
		if count != int64(len(batch)) || recorded != digest {
			return ErrMismatch
		}
		for _, row := range batch {
			if err := t.verifyTransferredRow(ctx, table, row); err != nil {
				return err
			}
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !write {
		return ErrMismatch
	}
	tx, err := t.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	marks := make([]string, len(table.Columns))
	for i := range marks {
		marks[i] = "?"
		if table.Columns[i].Kind == Binary {
			marks[i] = store.BinaryPlaceholder(t.db.Dialect)
		}
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+store.Identifier(t.db.Dialect, table.Name)+" ("+columnsSQL(t.db.Dialect, table)+") VALUES ("+strings.Join(marks, ",")+")")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range batch {
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			return err
		}
		if err := verifyRow(ctx, tx, t.db.Dialect, table, row); err != nil {
			return err
		}
	}
	if err := t.insertReceipt(ctx, tx, table.Name, number, int64(len(batch)), digest); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return ErrCommitUnknown
	}
	return nil
}

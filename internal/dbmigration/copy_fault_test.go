package dbmigration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

func TestCopyInterruptionAfterCommittedBatches(t *testing.T) {
	ctx := context.Background()
	source := database(t, true)
	destination := database(t, false)
	for i := 0; i < 150; i++ {
		if _, e := source.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", fmt.Sprintf("credential-%03d", i), "fixture", "git_token", "", []byte{byte(i)}, "masked", "2026-11-11T00:00:00Z", "2026-11-11T00:00:00Z"); e != nil {
			t.Fatal(e)
		}
	}
	task := uuid.NewString()
	target, e := InitializeTarget(ctx, destination, task)
	if e != nil {
		t.Fatal(e)
	}
	interrupted := errors.New("controlled stop after durable table batches")
	_, e = snapshotProgress(ctx, source, target, true, func(p TableProof) error {
		if p.Table == "credentials" {
			return interrupted
		}
		return nil
	})
	if !errors.Is(e, interrupted) {
		t.Fatal(e)
	}
	var rows, batches int
	if e = destination.DB.QueryRow("SELECT COUNT(*) FROM credentials").Scan(&rows); e != nil || rows != 150 {
		t.Fatal("committed rows absent", rows, e)
	}
	if e = destination.DB.QueryRow("SELECT COUNT(*) FROM database_migration_batches WHERE table_name=?", "credentials").Scan(&batches); e != nil || batches < 2 {
		t.Fatal("expected multiple committed batches", batches, e)
	}
	resumed, e := ResumeTarget(ctx, destination, task, target.Identity())
	if e != nil {
		t.Fatal(e)
	}
	report, e := CopySnapshot(ctx, source, resumed)
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Tables) != 106 {
		t.Fatal("resume incomplete")
	}
	var after int
	if e = destination.DB.QueryRow("SELECT COUNT(*) FROM database_migration_batches WHERE table_name=?", "credentials").Scan(&after); e != nil || after != batches {
		t.Fatal("committed batches replayed", after, e)
	}
	if _, e = VerifySnapshot(ctx, source, resumed); e != nil {
		t.Fatal(e)
	}
}

// These tests inject the two possible commit outcomes at the driver boundary.
// They are deterministic acknowledgement-loss simulations, not network faults.
type uncertainCommitConnector struct {
	base     driver.Driver
	dsn      string
	fail     atomic.Bool
	before   bool
	attempts atomic.Int32
}

func (c *uncertainCommitConnector) Connect(context.Context) (driver.Conn, error) {
	v, e := c.base.Open(c.dsn)
	if e != nil {
		return nil, e
	}
	return &uncertainCommitConn{Conn: v, control: c}, nil
}
func (c *uncertainCommitConnector) Driver() driver.Driver { return c.base }

type uncertainCommitConn struct {
	driver.Conn
	control *uncertainCommitConnector
}

func (c *uncertainCommitConn) Begin() (driver.Tx, error) {
	tx, e := c.Conn.Begin()
	if e != nil {
		return nil, e
	}
	return &uncertainCommitTx{Tx: tx, control: c.control}, nil
}
func (c *uncertainCommitConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	v, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return c.Begin()
	}
	tx, e := v.BeginTx(ctx, o)
	if e != nil {
		return nil, e
	}
	return &uncertainCommitTx{Tx: tx, control: c.control}, nil
}

type uncertainCommitTx struct {
	driver.Tx
	control *uncertainCommitConnector
}

func (t *uncertainCommitTx) Commit() error {
	t.control.attempts.Add(1)
	if t.control.fail.CompareAndSwap(true, false) {
		if t.control.before {
			if e := t.Tx.Rollback(); e != nil {
				return e
			}
		} else {
			if e := t.Tx.Commit(); e != nil {
				return e
			}
		}
		return errors.New("controlled lost commit acknowledgement")
	}
	return t.Tx.Commit()
}
func TestBatchUnknownCommitRequiresExplicitReceiptReconciliation(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit_not_applied_%t", before), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "target.db")
			db, e := store.Connect(store.OpenConfig{Driver: "sqlite", DSN: path})
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			target, e := InitializeTarget(ctx, db, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			fault := &uncertainCommitConnector{base: db.DB.Driver(), dsn: path, before: before}
			fault.fail.Store(true)
			wrapped := sql.OpenDB(fault)
			defer wrapped.Close()
			wrapped.SetMaxOpenConns(1)
			target.db = &store.Store{DB: wrapped, Dialect: store.SQLite}
			var table Table
			for _, v := range Manifest() {
				if v.Name == "credentials" {
					table = v
					break
				}
			}
			values := map[string]any{"id": "unknown-commit", "name": "fixture", "type": "git_token", "scope": "", "username": "", "ciphertext": []byte{0, 255}, "masked_value": "masked", "last_used_at": nil, "created_at": "2026-11-11T00:00:00Z", "updated_at": "2026-11-11T00:00:00Z"}
			row := make([]any, len(table.Columns))
			for i, c := range table.Columns {
				var ok bool
				row[i], ok = values[c.Name]
				if !ok {
					t.Fatal("fixture missing column", c.Name)
				}
			}
			e = target.writeBatch(ctx, table, 0, [][]any{row}, true)
			if !errors.Is(e, ErrCommitUnknown) {
				t.Fatal("commit ambiguity not surfaced", e)
			}
			if fault.attempts.Load() != 1 {
				t.Fatal("automatic commit retry")
			}
			var rows, receipts int
			if e = db.DB.QueryRow("SELECT COUNT(*) FROM credentials").Scan(&rows); e != nil {
				t.Fatal(e)
			}
			if e = db.DB.QueryRow("SELECT COUNT(*) FROM database_migration_batches WHERE table_name=?", "credentials").Scan(&receipts); e != nil {
				t.Fatal(e)
			}
			expected := 1
			if before {
				expected = 0
			}
			if rows != expected || receipts != expected {
				t.Fatal("data/receipt atomicity lost", rows, receipts)
			}
			if e = target.writeBatch(ctx, table, 0, [][]any{row}, true); e != nil {
				t.Fatal("explicit receipt reconciliation failed", e)
			}
			expectedAttempts := int32(1)
			if before {
				expectedAttempts = 2
			}
			if fault.attempts.Load() != expectedAttempts {
				t.Fatal("committed write replayed")
			}
			if e = db.DB.QueryRow("SELECT COUNT(*) FROM credentials").Scan(&rows); e != nil || rows != 1 {
				t.Fatal("reconciled rows incorrect", rows, e)
			}
		})
	}
}

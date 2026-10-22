package configstate

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

var (
	ErrNotFound = errors.New("configstate: project not found")
	ErrConflict = errors.New("configstate: revision conflict")
	ErrBusy     = errors.New("configstate: project has active work")
	ErrInvalid  = errors.New("configstate: invalid state")
	ErrOwner    = errors.New("configstate: activity owner mismatch")
)

const (
	EpochPolicy   = "policy"
	EpochSecrets  = "secret_catalog"
	EpochExecutor = "executor"
)

// EnsureProject initializes only an existing project's control row. Read paths
// must use ReadRevision, which never creates a row or a project.
func EnsureProject(ctx context.Context, q DBTX, db *sql.DB, id string) error {
	if id == "" {
		return ErrInvalid
	}
	var exists int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id=?", id).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return ErrNotFound
	}
	_, err := q.ExecContext(ctx, "INSERT INTO project_config_versions(project_id,revision) VALUES (?,1) "+store.DoNothingSuffix(store.DialectOf(db), []string{"project_id"}), id)
	return err
}
func ReadRevision(ctx context.Context, q DBTX, id string) (int64, error) {
	var rev int64
	var deleted sql.NullInt64
	err := q.QueryRowContext(ctx, "SELECT revision,deleted_at FROM project_config_versions WHERE project_id=?", id).Scan(&rev, &deleted)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && deleted.Valid) {
		return 0, ErrNotFound
	}
	return rev, err
}
func LockProject(ctx context.Context, q DBTX, db *sql.DB, id string) (int64, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := EnsureProject(lockCtx, q, db, id); err != nil {
		return 0, err
	}
	stmt := "SELECT revision,deleted_at FROM project_config_versions WHERE project_id=?"
	if store.DialectOf(db) == store.MySQL {
		stmt += " FOR UPDATE"
	}
	var rev int64
	var deleted sql.NullInt64
	if err := q.QueryRowContext(lockCtx, stmt, id).Scan(&rev, &deleted); err != nil {
		return 0, err
	}
	if deleted.Valid {
		return 0, ErrNotFound
	}
	return rev, nil
}
func LockProjects(ctx context.Context, q DBTX, db *sql.DB, ids ...string) error {
	items := append([]string(nil), ids...)
	sort.Strings(items)
	for i, id := range items {
		if i > 0 && id == items[i-1] {
			continue
		}
		if _, err := LockProject(ctx, q, db, id); err != nil {
			return err
		}
	}
	return nil
}
func BumpRevision(ctx context.Context, q DBTX, id string, expected int64) (int64, error) {
	if expected < 1 || expected == math.MaxInt64 {
		return 0, ErrInvalid
	}
	r, err := q.ExecContext(ctx, "UPDATE project_config_versions SET revision=revision+1 WHERE project_id=? AND revision=? AND deleted_at IS NULL", id, expected)
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrConflict
	}
	return expected + 1, nil
}
func MarkDeleted(ctx context.Context, q DBTX, id string, expected int64) error {
	if expected < 1 || expected == math.MaxInt64 {
		return ErrInvalid
	}
	r, err := q.ExecContext(ctx, "UPDATE project_config_versions SET revision=revision+1,deleted_at=? WHERE project_id=? AND revision=? AND deleted_at IS NULL", time.Now().UnixMilli(), id, expected)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ReadEpoch(ctx context.Context, q DBTX, name string) (int64, error) {
	var value int64
	err := q.QueryRowContext(ctx, "SELECT value FROM config_control_epochs WHERE name=?", name).Scan(&value)
	return value, err
}
func LockEpochs(ctx context.Context, q DBTX, db *sql.DB, names ...string) error {
	items := append([]string(nil), names...)
	sort.Strings(items)
	for i, name := range items {
		if i > 0 && name == items[i-1] {
			continue
		}
		stmt := "SELECT value FROM config_control_epochs WHERE name=?"
		if store.DialectOf(db) == store.MySQL {
			stmt += " FOR UPDATE"
		}
		var value int64
		lockCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := q.QueryRowContext(lockCtx, stmt, name).Scan(&value)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
func BumpEpoch(ctx context.Context, q DBTX, name string) error {
	r, err := q.ExecContext(ctx, "UPDATE config_control_epochs SET value=value+1 WHERE name=? AND value<?", name, int64(math.MaxInt64))
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalid
	}
	return nil
}
func CheckIdle(ctx context.Context, q DBTX, id string) error {
	var count int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM pipeline_runs WHERE project_id=? AND status IN ('queued','running','waiting_approval')", id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBusy
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_activity WHERE project_id=? AND released_at IS NULL", id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBusy
	}
	return nil
}

type Activity struct {
	ID, ProjectID, RunID, OwnerEpoch, Kind, Phase string
	CreatedAt, HeartbeatAt                        int64
}

func validPhase(p string) bool {
	return p == "queued" || p == "executing" || p == "finalizing" || p == "cleanup_unknown"
}
func AcquireActivityTx(ctx context.Context, q DBTX, db *sql.DB, in Activity) (*Activity, error) {
	if in.ProjectID == "" || in.OwnerEpoch == "" || in.Kind == "" || !validPhase(in.Phase) {
		return nil, ErrInvalid
	}
	if _, err := LockProject(ctx, q, db, in.ProjectID); err != nil {
		return nil, err
	}
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	in.CreatedAt = time.Now().UnixMilli()
	in.HeartbeatAt = in.CreatedAt
	var run any
	if in.RunID != "" {
		run = in.RunID
	}
	_, err := q.ExecContext(ctx, "INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES (?,?,?,?,?,?,?,?)", in.ID, in.ProjectID, run, in.OwnerEpoch, in.Kind, in.Phase, in.CreatedAt, in.HeartbeatAt)
	if err != nil {
		return nil, err
	}
	return &in, nil
}
func SetActivityPhaseTx(ctx context.Context, q DBTX, id, owner, phase string) error {
	if !validPhase(phase) {
		return ErrInvalid
	}
	r, err := q.ExecContext(ctx, "UPDATE project_activity SET phase=?,heartbeat_at=? WHERE activity_id=? AND owner_epoch=? AND released_at IS NULL", phase, time.Now().UnixMilli(), id, owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrOwner
	}
	return nil
}
func ReleaseActivityTx(ctx context.Context, q DBTX, id, owner string) error {
	r, err := q.ExecContext(ctx, "UPDATE project_activity SET phase='released',released_at=?,heartbeat_at=? WHERE activity_id=? AND owner_epoch=? AND released_at IS NULL", time.Now().UnixMilli(), time.Now().UnixMilli(), id, owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var current string
	var released sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT owner_epoch,released_at FROM project_activity WHERE activity_id=?", id).Scan(&current, &released); err != nil {
		return err
	}
	if current == owner && released.Valid {
		return nil
	}
	return ErrOwner
}

package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/completedrefs"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"time"
)

var ErrBlocked = errors.New("maintenance: active or unresolved work")
var localLeaseOwner = uuid.NewString()

type Blocker struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}
type BlockedError struct{ Blockers []Blocker }

func (e *BlockedError) Error() string        { return ErrBlocked.Error() }
func (e *BlockedError) Is(target error) bool { return target == ErrBlocked }
func Enter(ctx context.Context, db *sql.DB, kind string) (func(), error) {
	if db == nil {
		return func() {}, nil
	}
	id := uuid.NewString()
	created := time.Now().UnixMilli()
	err := configstate.WithWriteTx(ctx, db, func(q configstate.DBTX) error {
		if e := configstate.CheckMaintenanceAdmission(ctx, q); e != nil {
			return e
		}
		_, e := q.ExecContext(ctx, "INSERT INTO selfupdate_leases(id,kind,created_at) VALUES(?,?,?)", id, kind, created)
		return e
	})
	if err != nil {
		return nil, err
	}
	return func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = completedrefs.Complete(c, db, "maintenance", id, localLeaseOwner, func(retry context.Context) error {
			_, e := db.ExecContext(retry, "DELETE FROM selfupdate_leases WHERE id=? AND kind=? AND created_at=?", id, kind, created)
			return e
		})
	}, nil
}
func Fence(ctx context.Context, db *sql.DB, task string) error {
	return configstate.WithWriteTx(ctx, db, func(q configstate.DBTX) error {
		if e := configstate.CheckMaintenanceAdmission(ctx, q); e != nil {
			return e
		}
		queries := []struct{ kind, sql string }{
			{"terminal_sessions", "SELECT COUNT(*) FROM selfupdate_leases WHERE kind='terminal_pty'"},
			{"admitted_work", "SELECT (SELECT COUNT(*) FROM selfupdate_leases WHERE kind<>'terminal_pty') + (SELECT COUNT(*) FROM oauth_credential_uses)"},
			{"runs", "SELECT COUNT(*) FROM pipeline_runs WHERE status IN ('queued','running','waiting_approval')"},
			{"activity_holds", "SELECT COUNT(*) FROM project_activity WHERE released_at IS NULL"},
			{"guards", "SELECT COUNT(*) FROM release_guards WHERE status IN ('queued','binding','observing','stopping')"},
			{"config_operations", "SELECT COUNT(*) FROM mcp_config_operations WHERE state IN ('pending','unknown')"},
			{"ops_runs", "SELECT COUNT(*) FROM ops_chat_runs WHERE status IN ('queued','planning','running','analyzing')"}}
		blocked := &BlockedError{}
		for _, query := range queries {
			var n int
			if e := q.QueryRowContext(ctx, query.sql).Scan(&n); e != nil {
				return e
			}
			if n > 0 {
				blocked.Blockers = append(blocked.Blockers, Blocker{Kind: query.kind, Count: n})
			}
		}
		if len(blocked.Blockers) > 0 {
			return blocked
		}
		_, e := q.ExecContext(ctx, "UPDATE selfupdate_control SET task_id=? WHERE id=1", task)
		return e
	})
}
func Unfence(ctx context.Context, db *sql.DB, task string) error {
	_, e := db.ExecContext(ctx, "UPDATE selfupdate_control SET task_id='' WHERE id=1 AND task_id=?", task)
	return e
}

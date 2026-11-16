package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// 审批状态枚举。
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Record 是一条审批门记录(供 UI 展示 / 审计)。
type Record struct {
	StageID              string   `json:"stageId"`
	StageName            string   `json:"stageName"`
	Status               string   `json:"status"`
	DecidedBy            string   `json:"decidedBy"`
	DecidedAt            string   `json:"decidedAt"`
	CreatedAt            string   `json:"createdAt"`
	AccessAction         string   `json:"accessAction,omitempty"`
	AccessEnvironmentIDs []string `json:"accessEnvironmentIds,omitempty"`
}

// Store 持久化审批门记录(参数化 SQL)。
type Store struct {
	db *sql.DB
}

// NewStore 构造审批记录持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreatePending 登记一条待批记录(同 run+stage upsert,重入时复位为 pending)。
func (s *Store) CreatePending(ctx context.Context, runID, stageID, stageName string) error {
	return s.createPending(ctx, runID, stageID, stageName, "")
}
func (s *Store) CreateEnvironmentPending(ctx context.Context, runID, stageID, stageName, environment string) error {
	return s.createPending(ctx, runID, stageID, stageName, environment)
}
func (s *Store) CreatePromotionPending(ctx context.Context, runID, stageID, stageName, environment string) error {
	return s.createPendingScope(ctx, runID, stageID, stageName, environment, tenancy.PromotionApprovalScope)
}
func (s *Store) createPending(ctx context.Context, runID, stageID, stageName, environment string) error {
	return s.createPendingScope(ctx, runID, stageID, stageName, environment, tenancy.ApprovalScope)
}
func (s *Store) createPendingScope(ctx context.Context, runID, stageID, stageName, environment string, scope func(context.Context, *sql.DB, string, string) (string, string, error)) error {
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockCompletionAuthorization)
	return configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		action, environmentID, err := scope(tx, s.db, runID, environment)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		d := store.DialectOf(s.db)
		_, err = q.ExecContext(tx,
			store.InsertSQL(d, "run_approvals",
				[]string{"id", "run_id", "stage_id", "stage_name", "status", "decided_by", "decided_at", "created_at", "access_action", "access_environment_ids"}, []string{"?", "?", "?", "?", "'pending'", "''", "''", "?", "?", "?"},
				[]string{"run_id", "stage_id"}, []string{"status", "decided_by", "decided_at", "stage_name", "access_action", "access_environment_ids"}),
			uuid.NewString(), runID, stageID, stageName, now, action, environmentID,
		)
		if err != nil {
			return fmt.Errorf("approval: create pending: %w", err)
		}
		return nil
	})
}

// Decide 记录一次决定(approved/rejected/其它如 timeout|canceled 走 status=rejected + decidedBy 标注)。
func (s *Store) Decide(ctx context.Context, runID, stageID, status, decidedBy string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx,
		`UPDATE run_approvals SET status = ?, decided_by = ?, decided_at = ?
		 WHERE run_id = ? AND stage_id = ?`,
		status, decidedBy, now, runID, stageID,
	)
	if err != nil {
		return fmt.Errorf("approval: decide: %w", err)
	}
	if affected, e := result.RowsAffected(); e != nil || affected != 1 {
		return fmt.Errorf("approval: decision was not persisted")
	}
	return nil
}

// ListForRun 返回某运行的全部审批记录(按 created_at 升序)。
func (s *Store) ListForRun(ctx context.Context, runID string) ([]Record, error) {
	if err := tenancy.Check(ctx, s.db, "run.read", "run", runID); err != nil {
		return nil, err
	}
	pred, err := tenancy.QueryPredicate(ctx, s.db, "run.read", "a.run_id")
	if err != nil {
		return nil, err
	}
	args := append([]any{runID}, pred.Args...)
	rows, err := s.db.QueryContext(ctx,
		`SELECT stage_id, stage_name, status, decided_by, decided_at, created_at, COALESCE(access_action,''),COALESCE(access_environment_ids,'')
 FROM run_approvals a WHERE run_id = ? AND (`+pred.SQL+`) ORDER BY created_at ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("approval: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		var r Record
		var encoded string
		if err := rows.Scan(&r.StageID, &r.StageName, &r.Status, &r.DecidedBy, &r.DecidedAt, &r.CreatedAt, &r.AccessAction, &encoded); err != nil {
			return nil, fmt.Errorf("approval: scan: %w", err)
		}
		r.AccessEnvironmentIDs, err = tenancy.ApprovalEnvironmentIDs(r.AccessAction, encoded)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var ErrNotWaiting = errors.New("approval: not waiting")

func (s *Store) MultiTenant(ctx context.Context) (bool, error) { return tenancy.Enabled(ctx, s.db) }

// Resolve authorizes and persists before delivering the decision. Coordinator
// wake-up is not authority: worker side effects still verify their fixed plan.
func (s *Store) Resolve(ctx context.Context, coord *Coordinator, runID, stageID string, approved bool, legacyActor string) (string, error) {
	if coord == nil {
		return "", ErrNotWaiting
	}
	on, err := tenancy.Enabled(ctx, s.db)
	if err != nil {
		return "", err
	}
	if !on {
		if !coord.Resolve(Key(runID, stageID), Decision{Approved: approved, Actor: legacyActor}) {
			return "", ErrNotWaiting
		}
		return legacyActor, nil
	}
	ctx = configstate.WithWriteAdmission(ctx, tenancy.LockCompletionAuthorization)
	actor := ""
	delivered := false
	err = configstate.WithWriteTxContext(ctx, s.db, func(tx context.Context, q configstate.DBTX) error {
		if err := tenancy.Check(tx, s.db, "run.read", "run", runID); err != nil {
			return err
		}
		var action, environment string
		if err := q.QueryRowContext(tx, `SELECT COALESCE(access_action,''),COALESCE(access_environment_ids,'') FROM run_approvals WHERE run_id=? AND stage_id=? AND status='pending'`, runID, stageID).Scan(&action, &environment); err != nil {
			return ErrNotWaiting
		}
		var err error
		actor, err = tenancy.CheckApproval(tx, s.db, runID, action, environment)
		if err != nil {
			return err
		}
		if !coord.IsWaiting(Key(runID, stageID)) {
			return ErrNotWaiting
		}
		status := StatusRejected
		if approved {
			status = StatusApproved
		}
		result, err := q.ExecContext(tx, `UPDATE run_approvals SET status=?,decided_by=?,decided_at=? WHERE run_id=? AND stage_id=? AND status='pending'`, status, actor, time.Now().UTC().Format(time.RFC3339), runID, stageID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return ErrNotWaiting
		}
		if !configstate.AfterCommit(tx, func() { delivered = coord.Resolve(Key(runID, stageID), Decision{Approved: approved, Actor: actor}) }) {
			return tenancy.ErrConflict
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !delivered {
		return "", ErrNotWaiting
	}
	return actor, nil
}

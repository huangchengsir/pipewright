package approval

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// 审批状态枚举。
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Record 是一条审批门记录(供 UI 展示 / 审计)。
type Record struct {
	StageID   string `json:"stageId"`
	StageName string `json:"stageName"`
	Status    string `json:"status"`
	DecidedBy string `json:"decidedBy"`
	DecidedAt string `json:"decidedAt"`
	CreatedAt string `json:"createdAt"`
}

// Store 持久化审批门记录(参数化 SQL)。
type Store struct {
	db *sql.DB
}

// NewStore 构造审批记录持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreatePending 登记一条待批记录(同 run+stage upsert,重入时复位为 pending)。
func (s *Store) CreatePending(ctx context.Context, runID, stageID, stageName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_approvals (id, run_id, stage_id, stage_name, status, decided_by, decided_at, created_at)
		 VALUES (?, ?, ?, ?, 'pending', '', '', ?)
		 ON CONFLICT(run_id, stage_id) DO UPDATE SET
		   status = 'pending', decided_by = '', decided_at = '', stage_name = excluded.stage_name`,
		uuid.NewString(), runID, stageID, stageName, now,
	)
	if err != nil {
		return fmt.Errorf("approval: create pending: %w", err)
	}
	return nil
}

// Decide 记录一次决定(approved/rejected/其它如 timeout|canceled 走 status=rejected + decidedBy 标注)。
func (s *Store) Decide(ctx context.Context, runID, stageID, status, decidedBy string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE run_approvals SET status = ?, decided_by = ?, decided_at = ?
		 WHERE run_id = ? AND stage_id = ?`,
		status, decidedBy, now, runID, stageID,
	)
	if err != nil {
		return fmt.Errorf("approval: decide: %w", err)
	}
	return nil
}

// ListForRun 返回某运行的全部审批记录(按 created_at 升序)。
func (s *Store) ListForRun(ctx context.Context, runID string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT stage_id, stage_name, status, decided_by, decided_at, created_at
		 FROM run_approvals WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("approval: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.StageID, &r.StageName, &r.Status, &r.DecidedBy, &r.DecidedAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("approval: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

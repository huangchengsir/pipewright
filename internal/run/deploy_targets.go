package run

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// deploy_targets.go 是「部署目标结果」在 run 领域层的搬运形状(FR-10 / Story 4.2)。
//
// 部署执行逻辑在 internal/deploy(import run 取产物 + 写部署结果 + 更新 run 终态);run 包
// 本身**不** import deploy(避免环)。run 包只持有 DeployTarget 的持久化/读取能力,供
// internal/deploy 写、httpapi run-detail 读(填 3-1 冻结的 targets slot)。
//
// status 枚举冻结(对齐 run-detail targets 子 DTO):
//
//	pending | deploying | success | failed | rolled_back
//
// 4-4 回滚置 rolled_back;4-5 多机扇出新增多条;均不改此形状。

// 部署目标结果状态枚举(冻结;对齐 run-detail targets 子 DTO 的 status)。
const (
	// TargetPending 表示已登记、尚未开始部署。
	TargetPending = "pending"
	// TargetDeploying 表示部署进行中。
	TargetDeploying = "deploying"
	// TargetSuccess 表示该机部署成功(终态)。
	TargetSuccess = "success"
	// TargetFailed 表示该机部署失败(终态;message 人读原因,绝无明文密钥)。
	TargetFailed = "failed"
	// TargetRolledBack 表示该机已回滚(终态;4-4 填充语义,本期仅为合法枚举)。
	TargetRolledBack = "rolled_back"
)

// IsValidTargetStatus 报告 status 是否为冻结枚举之一。
func IsValidTargetStatus(s string) bool {
	switch s {
	case TargetPending, TargetDeploying, TargetSuccess, TargetFailed, TargetRolledBack:
		return true
	default:
		return false
	}
}

// DeployTarget 是一次部署在一台目标服务器上的结果(对齐冻结 run-detail targets 子 DTO)。
//
//   - ServerID   : 目标服务器引用 id。
//   - ServerName : 部署时快照的展示名(服务器改名/删除后历史结果仍可读)。
//   - Status     : pending | deploying | success | failed | rolled_back(冻结枚举)。
//   - Message    : 人读摘要(成功摘要 / 失败原因;**绝无明文密钥**)。
//   - StartedAt  : 部署开始时刻。
//   - FinishedAt : 部署结束时刻(未结束为 nil)。
type DeployTarget struct {
	ID         string
	RunID      string
	ServerID   string
	ServerName string
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// SaveDeployTargets 持久化一次部署的多机结果(参数化 SQL;单事务,全成或全不入,
// 避免半截 targets 致 run-detail 渲染不一致)。非法 status → ErrInvalidTargetStatus;
// run 不存在 → ErrNotFound(外键失败)。空切片为合法 no-op。
func (s *service) SaveDeployTargets(ctx context.Context, runID string, targets []DeployTarget) error {
	for i := range targets {
		if !IsValidTargetStatus(targets[i].Status) {
			return ErrInvalidTargetStatus
		}
	}
	if len(targets) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("run: begin deploy targets tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range targets {
		t := targets[i]
		if t.ID == "" {
			t.ID = uuid.NewString()
		}
		if t.StartedAt.IsZero() {
			t.StartedAt = time.Now().UTC()
		}
		var finished any
		if t.FinishedAt != nil {
			finished = t.FinishedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO deploy_targets
			   (id, run_id, server_id, server_name, status, message, started_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, runID, t.ServerID, t.ServerName, t.Status, t.Message,
			t.StartedAt.UTC().Format(time.RFC3339), finished,
		); err != nil {
			if isForeignKeyErr(err) {
				return ErrNotFound
			}
			return fmt.Errorf("run: insert deploy target: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("run: commit deploy targets: %w", err)
	}
	return nil
}

// SetDeployTerminal 在部署后据每机结果置 run 终态(success|partial_failed|failed)。
// 部署在 run 已是成功终态后发生,常规转移图禁止「终态→终态」,故此处直接覆盖
// status + finished_at(CAS 不要求 from)。仅接受这三个终态;非法 status → ErrInvalidStatus;
// run 不存在 → ErrNotFound。覆盖后经事件总线发布 status 事件(SSE 订阅者收敛)。
func (s *service) SetDeployTerminal(ctx context.Context, runID, status string) error {
	switch status {
	case StatusSuccess, StatusPartialFailed, StatusFailed:
	default:
		return ErrInvalidStatus
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE pipeline_runs SET status = ?, finished_at = ? WHERE id = ?`,
		status, now, runID)
	if err != nil {
		return fmt.Errorf("run: set deploy terminal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.bus.publish(Event{Kind: EventStatus, RunID: runID, Status: status})
	return nil
}

// ListDeployTargets 取某次运行的全部部署目标结果(按 started_at 升序,rowid 破并列;
// 无部署 → 空切片)。run 不存在不报错(返回空切片);HTTP 层据 run 存在性决定 404。
// 参数化 SQL;不全量驻留无关数据。
func (s *service) ListDeployTargets(ctx context.Context, runID string) ([]DeployTarget, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, server_id, server_name, status, message, started_at, finished_at
		 FROM deploy_targets WHERE run_id = ? ORDER BY started_at ASC, rowid ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("run: load deploy targets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []DeployTarget{}
	for rows.Next() {
		var (
			t          DeployTarget
			startedStr string
			finishStr  sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.RunID, &t.ServerID, &t.ServerName,
			&t.Status, &t.Message, &startedStr, &finishStr); err != nil {
			return nil, fmt.Errorf("run: scan deploy target: %w", err)
		}
		if ts, perr := time.Parse(time.RFC3339, startedStr); perr == nil {
			t.StartedAt = ts.UTC()
		}
		if t.FinishedAt, err = parseNullTime(finishStr); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run: iterate deploy targets: %w", err)
	}
	return out, nil
}

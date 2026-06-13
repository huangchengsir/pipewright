// Package retention 实现运行数据的保留策略与定期清理(防止 runs/run_logs/run_steps/
// artifacts 无限增长撑爆磁盘)。
//
// 策略(全局单例 retention_config):
//   - Enabled:总开关,默认关(避免升级即删数据,须显式开启)。
//   - KeepPerProject:每项目保留最近 N 条**终态**运行(0=不限)。
//   - MaxAgeDays:删除创建时间早于 N 天的**终态**运行(0=不限)。
//
// 安全铁律:只删终态运行(success/failed);进行中(queued/running/waiting_approval)绝不动。
// 删 run 时——deploy_targets 是唯一未配 ON DELETE CASCADE 的子表,显式先删;其余子表
// (run_steps/run_logs/run_artifacts/反馈/审批/测试报告/晋级)经外键级联连带删除。
// 清理为 best-effort:出错记录并返回,绝不影响在跑的运行。
package retention

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Config 是全局保留策略。
type Config struct {
	Enabled        bool
	KeepPerProject int
	MaxAgeDays     int
}

// Service 读写保留配置并执行清理(经参数化 SQL 触库)。
type Service struct {
	db *sql.DB
}

// NewService 构造保留服务。
func NewService(db *sql.DB) *Service { return &Service{db: db} }

// GetConfig 读取全局保留配置(单行 id=1;缺行返回零值不报错)。
func (s *Service) GetConfig(ctx context.Context) (Config, error) {
	var (
		enabled, keep, age int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled, keep_per_project, max_age_days FROM retention_config WHERE id = 1`,
	).Scan(&enabled, &keep, &age)
	if err == sql.ErrNoRows {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("retention: get config: %w", err)
	}
	return Config{Enabled: enabled != 0, KeepPerProject: keep, MaxAgeDays: age}, nil
}

// SetConfig 持久化保留配置(负值归一为 0;upsert 单行)。
func (s *Service) SetConfig(ctx context.Context, c Config) error {
	if c.KeepPerProject < 0 {
		c.KeepPerProject = 0
	}
	if c.MaxAgeDays < 0 {
		c.MaxAgeDays = 0
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE retention_config SET enabled = ?, keep_per_project = ?, max_age_days = ?, updated_at = ? WHERE id = 1`,
		enabled, c.KeepPerProject, c.MaxAgeDays, now)
	if err != nil {
		return fmt.Errorf("retention: set config: %w", err)
	}
	// 行不存在(迁移种子未跑)→ 兜底插入。
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO retention_config (id, enabled, keep_per_project, max_age_days, updated_at) VALUES (1, ?, ?, ?, ?)`,
			enabled, c.KeepPerProject, c.MaxAgeDays, now); err != nil {
			return fmt.Errorf("retention: insert config: %w", err)
		}
	}
	return nil
}

// terminalStatuses 是可被清理的终态(success/failed;取消已并入 failed)。
var terminalStatuses = []string{"success", "failed"}

// Prune 按当前策略裁剪终态运行,返回删除条数。未启用或无任何条件时返回 0,不删。
func (s *Service) Prune(ctx context.Context, now time.Time) (int, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return 0, err
	}
	if !cfg.Enabled || (cfg.KeepPerProject <= 0 && cfg.MaxAgeDays <= 0) {
		return 0, nil
	}

	ids, err := s.prunableIDs(ctx, cfg, now)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := s.deleteRuns(ctx, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// prunableIDs 计算应删除的终态运行 id:超出每项目保留条数 或 早于年龄阈值。
// 在 Go 端按 (project, created_at desc) 排名,避免依赖各方言窗口函数,逻辑可单测。
func (s *Service) prunableIDs(ctx context.Context, cfg Config, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, created_at FROM pipeline_runs
		 WHERE status IN ('success','failed')
		 ORDER BY project_id, created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("retention: list terminal runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cutoff := ""
	if cfg.MaxAgeDays > 0 {
		cutoff = now.UTC().AddDate(0, 0, -cfg.MaxAgeDays).Format(time.RFC3339)
	}

	var toDelete []string
	perProject := map[string]int{}
	for rows.Next() {
		var id, projectID, createdAt string
		if err := rows.Scan(&id, &projectID, &createdAt); err != nil {
			return nil, fmt.Errorf("retention: scan run: %w", err)
		}
		perProject[projectID]++
		rank := perProject[projectID]
		overCount := cfg.KeepPerProject > 0 && rank > cfg.KeepPerProject
		// created_at 为 UTC RFC3339,可按字符串比较年龄。
		tooOld := cutoff != "" && createdAt < cutoff
		if overCount || tooOld {
			toDelete = append(toDelete, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retention: iterate runs: %w", err)
	}
	return toDelete, nil
}

// 非级联子表:这些表带 run_id 但**未配 ON DELETE CASCADE**(run_logs 无外键、deploy_targets
// 为 RESTRICT),删 run 前须显式清。run_logs 是最大增长源(日志文本),尤其重要。其余子表
// (run_steps/run_artifacts/反馈/审批/测试报告/晋级历史)均级联,随删 run 自动清除。
// 注:webhook_deliveries 为去重账本(删之可能导致重投被重复处理),不在此清。
var nonCascadeChildTables = []string{"run_logs", "deploy_targets"}

// deleteRuns 分批在事务内删除运行:先删非级联子表,再删 run(级联其余子表)。
func (s *Service) deleteRuns(ctx context.Context, ids []string) error {
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		if err := s.deleteChunk(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteChunk(ctx context.Context, ids []string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("retention: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, tbl := range nonCascadeChildTables {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+tbl+` WHERE run_id IN (`+placeholders+`)`, args...); err != nil {
			return fmt.Errorf("retention: delete %s: %w", tbl, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM pipeline_runs WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("retention: delete runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("retention: commit: %w", err)
	}
	return nil
}

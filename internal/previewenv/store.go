package previewenv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/store"
)

// Store 持久化预览环境 + 预览配置(参数化 SQL,两方言一致)。
type Store struct {
	db *sql.DB
}

// NewStore 构造持久层。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const envColumns = `id, project_id, pipeline_id, pr_number, branch, server_id, route_id, subdomain, status, created_at, reclaimed_at`

// list 返回某项目全部预览环境(创建时间倒序);projectID 为空 → 全部。
func (s *Store) list(ctx context.Context, projectID string) ([]PreviewEnv, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if projectID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+envColumns+` FROM preview_envs ORDER BY created_at DESC, id`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+envColumns+` FROM preview_envs WHERE project_id = ? ORDER BY created_at DESC, id`, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("previewenv: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]PreviewEnv, 0)
	for rows.Next() {
		e, serr := scanEnv(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("previewenv: iterate: %w", err)
	}
	return out, nil
}

// listActive 返回全部 active 预览环境(创建时间倒序),供自动回收 sweeper 逐个核查 PR 状态。
func (s *Store) listActive(ctx context.Context) ([]PreviewEnv, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+envColumns+` FROM preview_envs WHERE status = ? ORDER BY created_at DESC, id`, StatusActive)
	if err != nil {
		return nil, fmt.Errorf("previewenv: list active: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]PreviewEnv, 0)
	for rows.Next() {
		e, serr := scanEnv(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("previewenv: iterate active: %w", err)
	}
	return out, nil
}

// get 读取单条预览环境;不存在 → ErrNotFound。
func (s *Store) get(ctx context.Context, id string) (*PreviewEnv, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+envColumns+` FROM preview_envs WHERE id = ?`, id)
	e, err := scanEnv(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return e, nil
}

// getActiveByPR 取某项目某 PR 的 active 预览环境;不存在 → ErrNotFound。
func (s *Store) getActiveByPR(ctx context.Context, projectID string, prNumber int) (*PreviewEnv, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+envColumns+` FROM preview_envs WHERE project_id = ? AND pr_number = ? AND status = ?`,
		projectID, prNumber, StatusActive)
	e, err := scanEnv(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return e, nil
}

// getByPR 取某项目某 PR 的任意预览环境(不论状态);不存在 → ErrNotFound。
func (s *Store) getByPR(ctx context.Context, projectID string, prNumber int) (*PreviewEnv, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+envColumns+` FROM preview_envs WHERE project_id = ? AND pr_number = ?`,
		projectID, prNumber)
	e, err := scanEnv(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return e, nil
}

// insert 落库一条新预览环境。(project_id, pr_number) 唯一冲突由调用方先查后 upsert 规避。
func (s *Store) insert(ctx context.Context, e *PreviewEnv) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO preview_envs (`+envColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.PipelineID, e.PRNumber, e.Branch, e.ServerID, e.RouteID, e.Subdomain,
		e.Status, fmtTime(&e.CreatedAt), fmtTimePtr(e.ReclaimedAt))
	if err != nil {
		if store.IsUniqueErr(err) {
			return ErrUniqueViolation
		}
		return fmt.Errorf("previewenv: insert: %w", err)
	}
	return nil
}

// ErrUniqueViolation 表示 (project_id, pr_number) 唯一约束冲突(并发重复部署);调用方据此重查后更新。
var ErrUniqueViolation = errors.New("previewenv: duplicate project/pr")

// updateOnRedeploy 在同一 PR 重新部署时刷新环境(路由/子域名/分支/服务器/状态回 active),不重复建行。
func (s *Store) updateOnRedeploy(ctx context.Context, id, routeID, subdomain, branch, serverID, pipelineID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE preview_envs
		   SET route_id = ?, subdomain = ?, branch = ?, server_id = ?, pipeline_id = ?, status = ?, reclaimed_at = ''
		 WHERE id = ?`,
		routeID, subdomain, branch, serverID, pipelineID, StatusActive, id)
	if err != nil {
		return fmt.Errorf("previewenv: update on redeploy: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// markReclaimed 把某环境标记为已回收(记录回收时刻)。
func (s *Store) markReclaimed(ctx context.Context, id string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE preview_envs SET status = ?, reclaimed_at = ? WHERE id = ?`,
		StatusReclaimed, fmtTime(&now), id)
	if err != nil {
		return fmt.Errorf("previewenv: mark reclaimed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- 预览配置 ---------------------------------------------------------------

// getConfig 读取某项目预览配置;无 → (nil, nil)(调用方回退零值)。
func (s *Store) getConfig(ctx context.Context, projectID string) (*Config, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT project_id, enabled, dns_provider_id, base_domain FROM preview_configs WHERE project_id = ?`, projectID)
	var (
		c       Config
		enabled int
	)
	if err := row.Scan(&c.ProjectID, &enabled, &c.DNSProviderID, &c.BaseDomain); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("previewenv: get config: %w", err)
	}
	c.Enabled = enabled != 0
	return &c, nil
}

// upsertConfig 写入/更新某项目预览配置。
func (s *Store) upsertConfig(ctx context.Context, c Config) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// 先尝试 UPDATE,影响 0 行再 INSERT(两方言一致,不依赖 ON CONFLICT / ON DUPLICATE 语法差异)。
	res, err := s.db.ExecContext(ctx,
		`UPDATE preview_configs SET enabled = ?, dns_provider_id = ?, base_domain = ?, updated_at = ? WHERE project_id = ?`,
		enabled, c.DNSProviderID, c.BaseDomain, now, c.ProjectID)
	if err != nil {
		return fmt.Errorf("previewenv: update config: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO preview_configs (project_id, enabled, dns_provider_id, base_domain, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ProjectID, enabled, c.DNSProviderID, c.BaseDomain, now, now)
	if err != nil {
		if store.IsUniqueErr(err) {
			// 并发插入冲突:回退再 UPDATE 一次(幂等收敛)。
			_, uerr := s.db.ExecContext(ctx,
				`UPDATE preview_configs SET enabled = ?, dns_provider_id = ?, base_domain = ?, updated_at = ? WHERE project_id = ?`,
				enabled, c.DNSProviderID, c.BaseDomain, now, c.ProjectID)
			return uerr
		}
		return fmt.Errorf("previewenv: insert config: %w", err)
	}
	return nil
}

// --- 扫描/时间辅助 ----------------------------------------------------------

// newEnvID 生成预览环境 id。
func newEnvID() string { return uuid.NewString() }

// scanner 抽象 *sql.Row 与 *sql.Rows 的 Scan。
type scanner interface {
	Scan(dest ...any) error
}

func scanEnv(sc scanner) (*PreviewEnv, error) {
	var (
		e            PreviewEnv
		createdStr   string
		reclaimedStr string
	)
	if err := sc.Scan(
		&e.ID, &e.ProjectID, &e.PipelineID, &e.PRNumber, &e.Branch, &e.ServerID, &e.RouteID,
		&e.Subdomain, &e.Status, &createdStr, &reclaimedStr,
	); err != nil {
		return nil, err
	}
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("previewenv: parse created_at: %w", err)
	}
	e.CreatedAt = created
	if reclaimedStr != "" {
		if t, perr := time.Parse(time.RFC3339, reclaimedStr); perr == nil {
			e.ReclaimedAt = &t
		}
	}
	return &e, nil
}

// fmtTime 把时间格式化为 RFC3339 UTC 串。
func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// fmtTimePtr 把可空时间格式化为串(nil → 空串)。
func fmtTimePtr(t *time.Time) string { return fmtTime(t) }

package run

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// concurrency.go 实现项目级并发上限配置(FR-8-10 并发/队列控制)。
//
// 每项目一份 max_concurrent:该项目同时处于 running 的运行数上限。0 = 不限项目级
// (仅受全局 PIPEWRIGHT_MAX_CONCURRENT 约束)。超限的运行保持 queued 直到该项目有空槽。
// WorkerPool 在调度前经 ConcurrencyLimits.LimitFor 取项目上限做准入判断(FIFO 队列)。

// maxProjectConcurrency 是项目级上限的 sane 上界(防误填巨值耗尽全局槽 / 资源)。
const maxProjectConcurrency = 64

// 并发配置领域错误(错误体不含敏感数据)。
var (
	// ErrConcProjectNotFound 表示引用的项目不存在。
	ErrConcProjectNotFound = errors.New("run: concurrency project not found")
	// ErrInvalidConcurrency 表示并发上限非法(<0 或 > maxProjectConcurrency)。
	ErrInvalidConcurrency = errors.New("run: invalid concurrency limit")
)

// ConcurrencyConfig 是项目并发配置领域模型。
type ConcurrencyConfig struct {
	// MaxConcurrent 是项目级同时运行上限;0 = 不限项目级(沿用全局上限)。
	MaxConcurrent int
	UpdatedAt     time.Time
}

// ConcurrencyLimits 是 WorkerPool 调度准入所需的只读端(查项目上限)。
// 经此接口解耦:pool 不直接触库、纯单测可注入内存 fake。
type ConcurrencyLimits interface {
	// LimitFor 返回某项目的并发上限(0 = 不限项目级)。查不到/出错时返回 0(优雅降级:
	// 不让配置读失败阻断调度,退化为仅受全局上限约束)。
	LimitFor(ctx context.Context, projectID string) int
}

// ConcurrencyService 定义项目并发配置领域接口(HTTP 读写 + 调度准入只读)。
type ConcurrencyService interface {
	ConcurrencyLimits
	// Get 返回项目并发配置;无配置 → 返回 0(不限项目级)空配置(非错误)。
	Get(ctx context.Context, projectID string) (*ConcurrencyConfig, error)
	// Save 校验(0..maxProjectConcurrency)→ 持久化(upsert)→ 回读。
	// 项目不存在 → ErrConcProjectNotFound;非法值 → ErrInvalidConcurrency。
	Save(ctx context.Context, projectID string, maxConcurrent int) (*ConcurrencyConfig, error)
}

type concurrencyService struct {
	db *sql.DB
}

// NewConcurrencyService 构造项目并发配置 Service(经参数化 SQL 触库;无 init 副作用、不驻留)。
func NewConcurrencyService(db *sql.DB) ConcurrencyService { return &concurrencyService{db: db} }

func (s *concurrencyService) Get(ctx context.Context, projectID string) (*ConcurrencyConfig, error) {
	var (
		maxc    int
		updated string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT max_concurrent, updated_at FROM project_concurrency WHERE project_id = ?`,
		strings.TrimSpace(projectID),
	).Scan(&maxc, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return &ConcurrencyConfig{}, nil // 未配置 → 不限项目级
	}
	if err != nil {
		return nil, fmt.Errorf("run: load concurrency config: %w", err)
	}
	t, _ := time.Parse(time.RFC3339, updated)
	return &ConcurrencyConfig{MaxConcurrent: maxc, UpdatedAt: t}, nil
}

func (s *concurrencyService) Save(ctx context.Context, projectID string, maxConcurrent int) (*ConcurrencyConfig, error) {
	projectID = strings.TrimSpace(projectID)
	if maxConcurrent < 0 || maxConcurrent > maxProjectConcurrency {
		return nil, ErrInvalidConcurrency
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_concurrency (project_id, max_concurrent, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(project_id) DO UPDATE SET
		   max_concurrent = excluded.max_concurrent,
		   updated_at     = excluded.updated_at`,
		projectID, maxConcurrent, now, now,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrConcProjectNotFound
		}
		return nil, fmt.Errorf("run: upsert concurrency config: %w", err)
	}
	return s.Get(ctx, projectID)
}

// LimitFor 实现 ConcurrencyLimits:取项目上限(0 = 不限项目级;读失败优雅降级为 0)。
func (s *concurrencyService) LimitFor(ctx context.Context, projectID string) int {
	cfg, err := s.Get(ctx, projectID)
	if err != nil || cfg == nil {
		return 0
	}
	return cfg.MaxConcurrent
}

package cron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

// 领域错误。
var (
	// ErrProjectNotFound 表示引用的项目不存在。
	ErrProjectNotFound = errors.New("cron: project not found")
	// ErrInvalidExpression 表示 cron 表达式非法。
	ErrInvalidExpression = errors.New("cron: invalid expression")
	// ErrEnabledNeedsExpression 表示启用了定时但未填表达式。
	ErrEnabledNeedsExpression = errors.New("cron: enabled cron requires an expression")
)

// Config 是项目定时配置领域模型。
type Config struct {
	Expression string
	Branch     string
	Enabled    bool
	UpdatedAt  time.Time
}

// SaveInput 是保存入参。
type SaveInput struct {
	Expression string
	Branch     string
	Enabled    bool
}

// Service 定义定时配置领域接口(同时实现调度器所需的 Store)。
type Service interface {
	Store
	// Get 返回项目定时配置;无配置 → 返回禁用态空配置(非错误)。项目不存在不区分(交由上层 Get 校验)。
	Get(ctx context.Context, projectID string) (*Config, error)
	// Save 校验(启用须有合法表达式;表达式非空则必合法)→ 持久化(upsert)→ 回读。
	// 项目不存在 → ErrProjectNotFound。
	Save(ctx context.Context, projectID string, in SaveInput) (*Config, error)
}

type service struct {
	db *sql.DB
}

// NewService 构造定时配置 Service(经参数化 SQL 触库;无 init 副作用、不驻留)。
func NewService(db *sql.DB) Service { return &service{db: db} }

func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	var (
		expr, branch, updated string
		enabled               int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT expression, branch, enabled, updated_at FROM project_crons WHERE project_id = ?`,
		projectID,
	).Scan(&expr, &branch, &enabled, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return &Config{}, nil // 未配置 → 禁用态空配置
	}
	if err != nil {
		return nil, fmt.Errorf("cron: load config: %w", err)
	}
	t, _ := time.Parse(time.RFC3339, updated)
	return &Config{Expression: expr, Branch: branch, Enabled: enabled != 0, UpdatedAt: t}, nil
}

func (s *service) Save(ctx context.Context, projectID string, in SaveInput) (*Config, error) {
	expr := strings.TrimSpace(in.Expression)
	branch := strings.TrimSpace(in.Branch)

	if in.Enabled && expr == "" {
		return nil, ErrEnabledNeedsExpression
	}
	if expr != "" {
		if err := Valid(expr); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidExpression, err.Error())
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO project_crons (project_id, expression, branch, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?) `+
			store.UpsertSuffix(store.DialectOf(s.db), []string{"project_id"},
				[]string{"expression", "branch", "enabled", "updated_at"}),
		projectID, expr, branch, enabled, now, now,
	)
	if err != nil {
		if isForeignKeyErr(err) {
			return nil, ErrProjectNotFound
		}
		return nil, fmt.Errorf("cron: upsert config: %w", err)
	}
	return s.Get(ctx, projectID)
}

// ListEnabled 实现 Store:返回所有启用且表达式非空的定时配置(供调度器扫描)。
func (s *service) ListEnabled(ctx context.Context) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, expression, branch FROM project_crons WHERE enabled = 1 AND expression <> ''`)
	if err != nil {
		return nil, fmt.Errorf("cron: list enabled: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ProjectID, &e.Expression, &e.Branch); err != nil {
			return nil, fmt.Errorf("cron: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// isForeignKeyErr 判定是否为外键约束失败(项目不存在)。
func isForeignKeyErr(err error) bool { return store.IsForeignKeyErr(err) }

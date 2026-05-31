// Package chain 实现流水线串联(FR-8-11):上游流水线成功落终态后,按每项目配置的
// 「下游目标」列表,自动触发一个或多个下游流水线运行(可跨项目 / 同项目不同分支),
// 用于搭建 CD 链(如「应用构建成功 → 触发部署基础设施流水线」)。
//
// 两块:
//   - Service(本文件):每项目下游目标列表的配置存取(GET/PUT 端点支撑;CRUD 经参数化 SQL)。
//   - 串联终态钩子(hook.go):复用 run.WorkerPool 既有终态钩子槽(WithNotifyHook 同签名),
//     在 run 落 success 时查下游目标 → 经 run.Service.Create 以 TriggerChain 创建下游运行。
//
// 环路安全(必做):下游运行携带 chain_depth = 上游 +1;钩子拒绝超过 MaxDepth(默认 5)的串联,
// 并二次检测「项目已在本串联链路径中」兜底自环 / 互环。详见 hook.go。
//
// run 包不 import chain;chain 经接口依赖 run.Service(钩子在 main 装配,解耦,仿 notify/diagnose)。
package chain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// maxTargetsPerProject 限制单项目下游目标数量(防滥用 / 一次成功扇出过多)。
const maxTargetsPerProject = 32

// 领域错误。错误体不含敏感数据。
var (
	// ErrProjectNotFound 表示(上游)项目不存在。
	ErrProjectNotFound = errors.New("chain: project not found")
	// ErrDownstreamNotFound 表示某个下游目标项目不存在。
	ErrDownstreamNotFound = errors.New("chain: downstream project not found")
	// ErrSelfChain 表示下游目标指向上游项目自身(直接自环;禁止配置)。
	ErrSelfChain = errors.New("chain: downstream cannot be the upstream project itself")
	// ErrTooManyTargets 表示下游目标数量超过上限。
	ErrTooManyTargets = errors.New("chain: too many downstream targets")
	// ErrDuplicateTarget 表示同一(下游项目,分支)在一次保存中重复出现。
	ErrDuplicateTarget = errors.New("chain: duplicate downstream target")
)

// Target 是一个下游串联目标(上游成功后要触发的一个下游流水线)。
type Target struct {
	// DownstreamProjectID 是被触发的下游项目 id。
	DownstreamProjectID string
	// Branch 是触发下游时使用的分支;空 = 下游项目默认分支(由 run.Create 解析)。
	Branch string
	// Enabled 控制该目标是否参与触发(禁用则保留配置但不触发)。
	Enabled bool
}

// Config 是某上游项目的串联配置(下游目标列表)。
type Config struct {
	Targets []Target
}

// Service 定义串联配置领域接口(GET/PUT 端点 + 串联钩子查询共用)。
type Service interface {
	// Get 返回某上游项目的串联配置;无配置 → 空列表(非错误)。
	Get(ctx context.Context, projectID string) (*Config, error)
	// Save 全量替换某上游项目的下游目标列表(校验后删旧整写;单事务)。
	// 校验:上游项目存在;每个下游项目存在且非自身;数量 ≤ 上限;(下游,分支)不重复。
	Save(ctx context.Context, projectID string, targets []Target) (*Config, error)
	// ListEnabled 返回某上游项目「启用」的下游目标(供串联钩子在成功时查询)。
	ListEnabled(ctx context.Context, projectID string) ([]Target, error)
}

type service struct {
	db *sql.DB
}

// NewService 构造串联配置 Service(经参数化 SQL 触库;无 init 副作用、不驻留)。
func NewService(db *sql.DB) Service { return &service{db: db} }

func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT downstream_project_id, branch, enabled
		   FROM project_chain_targets
		  WHERE project_id = ?
		  ORDER BY created_at ASC, id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("chain: load config: %w", err)
	}
	defer func() { _ = rows.Close() }()

	cfg := &Config{Targets: []Target{}}
	for rows.Next() {
		var (
			t       Target
			enabled int
		)
		if err := rows.Scan(&t.DownstreamProjectID, &t.Branch, &enabled); err != nil {
			return nil, fmt.Errorf("chain: scan target: %w", err)
		}
		t.Enabled = enabled != 0
		cfg.Targets = append(cfg.Targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chain: iterate targets: %w", err)
	}
	return cfg, nil
}

func (s *service) Save(ctx context.Context, projectID string, targets []Target) (*Config, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, ErrProjectNotFound
	}
	if err := s.ensureProjectExists(ctx, projectID); err != nil {
		return nil, err
	}
	if len(targets) > maxTargetsPerProject {
		return nil, ErrTooManyTargets
	}

	// 规整 + 校验:trim,自环拒绝,下游存在校验,(下游,分支)去重。
	seen := map[string]bool{}
	clean := make([]Target, 0, len(targets))
	for _, t := range targets {
		dpid := strings.TrimSpace(t.DownstreamProjectID)
		branch := strings.TrimSpace(t.Branch)
		if dpid == "" {
			return nil, ErrDownstreamNotFound
		}
		if dpid == projectID {
			return nil, ErrSelfChain
		}
		key := dpid + "\x00" + branch
		if seen[key] {
			return nil, ErrDuplicateTarget
		}
		seen[key] = true
		if err := s.ensureProjectExists(ctx, dpid); err != nil {
			if errors.Is(err, ErrProjectNotFound) {
				return nil, ErrDownstreamNotFound
			}
			return nil, err
		}
		clean = append(clean, Target{DownstreamProjectID: dpid, Branch: branch, Enabled: t.Enabled})
	}

	// 全量替换:删旧整写,单事务(避免半写)。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("chain: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM project_chain_targets WHERE project_id = ?`, projectID); err != nil {
		return nil, fmt.Errorf("chain: delete old targets: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, t := range clean {
		enabled := 0
		if t.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_chain_targets
			   (id, project_id, downstream_project_id, branch, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), projectID, t.DownstreamProjectID, t.Branch, enabled, now, now,
		); err != nil {
			return nil, fmt.Errorf("chain: insert target: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chain: commit: %w", err)
	}
	return s.Get(ctx, projectID)
}

func (s *service) ListEnabled(ctx context.Context, projectID string) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT downstream_project_id, branch
		   FROM project_chain_targets
		  WHERE project_id = ? AND enabled = 1
		  ORDER BY created_at ASC, id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("chain: list enabled: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []Target{}
	for rows.Next() {
		t := Target{Enabled: true}
		if err := rows.Scan(&t.DownstreamProjectID, &t.Branch); err != nil {
			return nil, fmt.Errorf("chain: scan enabled: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ensureProjectExists 校验项目存在(上游 / 下游共用)。
func (s *service) ensureProjectExists(ctx context.Context, projectID string) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectNotFound
		}
		return fmt.Errorf("chain: check project: %w", err)
	}
	return nil
}

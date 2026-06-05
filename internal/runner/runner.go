// Package runner 是「远程构建 runner 配置」领域层(FR-8-14 远程 runner 池续)。
//
// 每项目可指定一台已登记的目标服务器(target.Server)作**远程构建机**:配置后,该项目的构建从中控机
// 下沉到该远程机执行(控制机本地克隆 → 经 SSH 把工作区传到远程 → 远程容器内跑构建/脚本 → 取回日志)。
// 未配 / 空 = 本地构建(行为不变)。本包只管「项目→runner 服务器 id」的存取与校验;真实远程执行在
// build 包的远程阶段执行器,按本配置派发。
package runner

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
)

// 领域错误。
var (
	// ErrProjectNotFound 表示项目不存在。
	ErrProjectNotFound = errors.New("runner: project not found")
	// ErrServerNotFound 表示指定的 runner 服务器不存在。
	ErrServerNotFound = errors.New("runner: runner server not found")
)

// Config 是某项目的远程 runner 配置(RunnerServerID 空 = 本地构建)。
type Config struct {
	ProjectID      string
	RunnerServerID string
}

// ServerExister 抽象「校验服务器是否存在」(target.Service 即满足;避免 runner 直依赖 target)。
type ServerExister interface {
	Exists(ctx context.Context, serverID string) bool
}

// Service 是项目 runner 配置读写接口。
type Service interface {
	// Get 取某项目的 runner 配置(无行 → RunnerServerID 空,即本地构建)。
	Get(ctx context.Context, projectID string) (*Config, error)
	// Save 设/清某项目的 runner 服务器(空串 = 清,回本地构建)。非空时校验项目与服务器都存在。
	Save(ctx context.Context, projectID, runnerServerID string) (*Config, error)
	// RunnerFor 取某项目的远程 runner 服务器 id(无 → "", false),供构建派发快速判定。
	RunnerFor(ctx context.Context, projectID string) (string, bool)
}

type service struct {
	db      *sql.DB
	servers ServerExister
}

// New 构造 runner 配置服务。servers 用于 Save 时校验 runner 服务器存在(可为 nil:跳过校验)。
func New(db *sql.DB, servers ServerExister) Service {
	return &service{db: db, servers: servers}
}

func (s *service) Get(ctx context.Context, projectID string) (*Config, error) {
	cfg := &Config{ProjectID: projectID}
	row := s.db.QueryRowContext(ctx, `SELECT runner_server_id FROM project_runners WHERE project_id = ?`, projectID)
	switch err := row.Scan(&cfg.RunnerServerID); {
	case errors.Is(err, sql.ErrNoRows):
		return cfg, nil // 无配置 = 本地构建
	case err != nil:
		return nil, err
	}
	return cfg, nil
}

func (s *service) Save(ctx context.Context, projectID, runnerServerID string) (*Config, error) {
	runnerServerID = strings.TrimSpace(runnerServerID)

	// 校验项目存在。
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	// 非空时校验 runner 服务器存在(防配了不存在的机)。
	if runnerServerID != "" && s.servers != nil && !s.servers.Exists(ctx, runnerServerID) {
		return nil, ErrServerNotFound
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO project_runners (project_id, runner_server_id, created_at, updated_at)
		VALUES (?, ?, ?, ?) `+
		store.UpsertSuffix(store.DialectOf(s.db), []string{"project_id"}, []string{"runner_server_id", "updated_at"}),
		projectID, runnerServerID, now, now)
	if err != nil {
		return nil, err
	}
	return &Config{ProjectID: projectID, RunnerServerID: runnerServerID}, nil
}

func (s *service) RunnerFor(ctx context.Context, projectID string) (string, bool) {
	cfg, err := s.Get(ctx, projectID)
	if err != nil || cfg.RunnerServerID == "" {
		return "", false
	}
	return cfg.RunnerServerID, true
}

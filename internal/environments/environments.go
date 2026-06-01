// Package environments 把「环境」做成可观测的一等只读对象(对标 GitLab environments):
// 按环境聚合部署历史(哪个 run、何时、什么产物、成功/失败、目标机、谁触发),并标出每环境
// 当前「活跃版本」(最近一次全成功部署),为一键回滚提供「上一次成功部署」的定位。
//
// 设计要点(务实、轻量、诚实):
//   - **零迁移、纯查询既有表聚合**:数据已全在 pipeline_runs(resolved_environment / trigger_* /
//     时间 / status)+ deploy_targets(每机结果)+ run_artifacts(产物)。本包只做只读 JOIN +
//     内存分组,不新增表、不改 deploy 写路径(避免动 internal/deploy 与 run.Service 接口)。
//   - **环境来源**:run.resolved_environment(webhook 分支映射解析出的目标环境)。只统计实际发生过
//     部署(有 deploy_targets 行)的 run —— 没部署过的 run 不进环境时间线。
//   - **活跃版本**:某环境时间线里最近一次「全机 success」的部署即当前活跃版本。
//   - **回滚目标**:活跃版本之前最近一次「全机 success」的部署,即「上一次成功部署」;回滚 = 用
//     那次 run 的同一产物 + 同一组目标机经既有 deploy 链路重发(执行编排在 httpapi 层注入 deploy.Service)。
//
// 本包只 import database/sql(+ 标准库);不 import run/deploy(避免环),与 promotion.Store 同范式。
package environments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 领域错误。错误体绝无敏感数据(密钥 / 明文 / 内部栈)。
var (
	// ErrProjectNotFound 表示项目不存在。
	ErrProjectNotFound = errors.New("environments: project not found")
	// ErrEnvNotFound 表示该项目下无此环境的部署历史。
	ErrEnvNotFound = errors.New("environments: environment has no deployment history")
	// ErrNoRollbackTarget 表示该环境没有「上一次成功部署」可回滚(历史不足 / 仅一次成功)。
	ErrNoRollbackTarget = errors.New("environments: no previous successful deployment to roll back to")
)

// 部署聚合状态(据该次部署的全部 deploy_targets 归并)。
const (
	// DeployStatusSuccess 表示该次部署全部目标机 success。
	DeployStatusSuccess = "success"
	// DeployStatusPartialFailed 表示有成功也有失败/回滚目标。
	DeployStatusPartialFailed = "partial_failed"
	// DeployStatusFailed 表示全部目标机失败/回滚。
	DeployStatusFailed = "failed"
)

// TargetSummary 是某次部署在一台目标机上的结果摘要(只读视图;绝无明文密钥)。
type TargetSummary struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	Status     string `json:"status"` // run.Target* 枚举:pending|deploying|success|failed|rolled_back
}

// Artifact 是某次部署所发布产物的只读摘要。
type Artifact struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // image|jar|dist|archive
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

// Deployment 是某环境时间线上的一次部署事件(按环境聚合的核心单元)。
type Deployment struct {
	RunID       string          `json:"runId"`
	Status      string          `json:"status"`      // success|partial_failed|failed(据 targets 归并)
	Commit      string          `json:"commit"`      // 触发提交短 SHA(可空)
	Branch      string          `json:"branch"`      // 触发分支
	TriggeredBy string          `json:"triggeredBy"` // 触发者(展示;绝无敏感数据)
	DeployedAt  string          `json:"deployedAt"`  // 该次部署最晚一台目标机结束时刻(RFC3339;未结束回退到开始时刻)
	Active      bool            `json:"active"`      // 是否为该环境当前活跃版本(最近一次全成功)
	Targets     []TargetSummary `json:"targets"`     // 每台目标机结果
	Artifacts   []Artifact      `json:"artifacts"`   // 该 run 产物
	ServerIDs   []string        `json:"-"`           // 该次部署涉及的目标机 id(供回滚重发;不出 JSON)

	envName string // 该部署所属环境(内部分组用;不出 JSON)
}

// EnvironmentTimeline 是某环境的部署时间线(最近在前)。
type EnvironmentTimeline struct {
	Environment string       `json:"environment"`
	Active      *Deployment  `json:"active"`      // 当前活跃版本(最近一次全成功);无则 nil
	Deployments []Deployment `json:"deployments"` // 最近 N 次(按部署时间降序)
}

// Service 是环境部署历史的只读聚合 + 回滚定位领域接口。
type Service struct {
	db *sql.DB
}

// NewService 构造只读聚合 Service(纯参数化 SQL;无 init 副作用、不驻留)。
func NewService(db *sql.DB) *Service { return &Service{db: db} }

// defaultHistoryLimit 是每环境时间线默认返回的部署条数上限(防超大项目一次性回全量)。
const defaultHistoryLimit = 50

// ListEnvironments 返回某项目按环境聚合的全部部署时间线(每环境最近 limit 次;最近在前)。
// limit<=0 用默认上限。项目不存在 → ErrProjectNotFound。无任何部署 → 空切片(非错误)。
func (s *Service) ListEnvironments(ctx context.Context, projectID string, limit int) ([]EnvironmentTimeline, error) {
	if err := s.assertProject(ctx, projectID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	byEnv, order, err := s.loadDeploymentsByEnv(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]EnvironmentTimeline, 0, len(order))
	for _, env := range order {
		deps := byEnv[env]
		markActive(deps)
		var active *Deployment
		for i := range deps {
			if deps[i].Active {
				a := deps[i]
				active = &a
				break
			}
		}
		if len(deps) > limit {
			deps = deps[:limit]
		}
		out = append(out, EnvironmentTimeline{Environment: env, Active: active, Deployments: deps})
	}
	return out, nil
}

// EnvironmentHistory 返回单个环境的部署时间线(最近 limit 次)。
// 项目不存在 → ErrProjectNotFound;该环境无部署历史 → ErrEnvNotFound。
func (s *Service) EnvironmentHistory(ctx context.Context, projectID, env string, limit int) (EnvironmentTimeline, error) {
	env = strings.TrimSpace(env)
	all, err := s.ListEnvironments(ctx, projectID, limit)
	if err != nil {
		return EnvironmentTimeline{}, err
	}
	for i := range all {
		if all[i].Environment == env {
			return all[i], nil
		}
	}
	return EnvironmentTimeline{}, ErrEnvNotFound
}

// RollbackTarget 是「上一次成功部署」的定位结果(供 httpapi 层经 deploy.Service 重发)。
type RollbackTarget struct {
	Environment  string   // 目标环境
	RunID        string   // 上一次成功部署的源运行 id
	ArtifactID   string   // 该次部署发布的产物 id(首个可发布产物;无则空 → 由 HTTP 层 422)
	Artifact     Artifact // 产物摘要
	ServerIDs    []string // 该次部署涉及的目标机 id(原样重发)
	CurrentRunID string   // 当前活跃版本的源运行 id(展示「从 X 回滚到 Y」)
}

// ResolveRollback 定位某环境「上一次成功部署」作为回滚目标(当前活跃版本之前最近一次全成功)。
//
// 规则:取该环境时间线 → 找当前活跃(最近全成功)→ 再往前找下一次全成功 = 回滚目标。
// 若无活跃版本(从未全成功过)或活跃之前再无全成功 → ErrNoRollbackTarget。
// 项目不存在 → ErrProjectNotFound;环境无历史 → ErrEnvNotFound。
//
// 本方法只**定位**,不执行;回滚执行(重发上一个产物到原目标机)由 httpapi 层经注入的
// deploy.Service.Deploy 完成,复用既有部署链路(健康门控 / 多机扇出 / 失败诊断全复用)。
func (s *Service) ResolveRollback(ctx context.Context, projectID, env string) (RollbackTarget, error) {
	tl, err := s.EnvironmentHistory(ctx, projectID, env, defaultHistoryLimit)
	if err != nil {
		return RollbackTarget{}, err
	}
	// 找当前活跃版本下标(最近一次全成功)。
	activeIdx := -1
	for i := range tl.Deployments {
		if tl.Deployments[i].Active {
			activeIdx = i
			break
		}
	}
	if activeIdx < 0 {
		return RollbackTarget{}, ErrNoRollbackTarget
	}
	// 在活跃之后(时间更早)找下一次全成功 = 回滚目标。
	for i := activeIdx + 1; i < len(tl.Deployments); i++ {
		d := tl.Deployments[i]
		if d.Status != DeployStatusSuccess {
			continue
		}
		rt := RollbackTarget{
			Environment:  env,
			RunID:        d.RunID,
			ServerIDs:    d.ServerIDs,
			CurrentRunID: tl.Deployments[activeIdx].RunID,
		}
		if len(d.Artifacts) > 0 {
			art := pickDeployableArtifact(d.Artifacts)
			rt.ArtifactID = art.ID
			rt.Artifact = art
		}
		return rt, nil
	}
	return RollbackTarget{}, ErrNoRollbackTarget
}

// pickDeployableArtifact 选一次部署可重发的产物(优先 dist/jar/archive/image 中首个;否则取首个)。
// 与 deploy.DeployForStage 的「首个可发布产物」语义对齐(顺序稳定:产物已按创建序返回)。
func pickDeployableArtifact(arts []Artifact) Artifact {
	for _, a := range arts {
		switch a.Type {
		case "dist", "jar", "archive", "image":
			return a
		}
	}
	return arts[0]
}

// assertProject 校验项目存在(项目不存在 → ErrProjectNotFound)。
func (s *Service) assertProject(ctx context.Context, projectID string) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, strings.TrimSpace(projectID)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProjectNotFound
	}
	if err != nil {
		return fmt.Errorf("environments: assert project: %w", err)
	}
	return nil
}

// markActive 在一组按时间降序的部署中,把最近一次「全成功」标记为活跃(其余 Active=false)。
func markActive(deps []Deployment) {
	for i := range deps {
		if deps[i].Status == DeployStatusSuccess {
			deps[i].Active = true
			return
		}
	}
}

// sortByDeployedAtDesc 按部署时刻降序(最近在前);时刻并列用 runID 破并列保证稳定。
func sortByDeployedAtDesc(deps []Deployment) {
	sort.SliceStable(deps, func(i, j int) bool {
		if deps[i].DeployedAt != deps[j].DeployedAt {
			return deps[i].DeployedAt > deps[j].DeployedAt
		}
		return deps[i].RunID > deps[j].RunID
	})
}

// aggregateTargetStatus 据一次部署的全部目标机状态归并出该次部署的整体状态。
//   - 全 success            → success
//   - 全 failed/rolled_back → failed
//   - 其余(混合 / 含进行中)→ partial_failed
func aggregateTargetStatus(targets []TargetSummary) string {
	if len(targets) == 0 {
		return DeployStatusFailed
	}
	allOK, allBad := true, true
	for _, t := range targets {
		ok := t.Status == "success"
		bad := t.Status == "failed" || t.Status == "rolled_back"
		if !ok {
			allOK = false
		}
		if !bad {
			allBad = false
		}
	}
	switch {
	case allOK:
		return DeployStatusSuccess
	case allBad:
		return DeployStatusFailed
	default:
		return DeployStatusPartialFailed
	}
}

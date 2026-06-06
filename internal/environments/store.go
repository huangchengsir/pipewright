package environments

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// store.go 实现「按环境聚合部署历史」的只读查询(纯参数化 SQL,零迁移,JOIN 既有表):
//
//	pipeline_runs(resolved_environment / trigger_* / status)
//	  ⨝ deploy_targets(每机结果;有行 = 实际部署过)
//	  ⨝ run_artifacts(产物)
//
// 只统计 resolved_environment 非空 **且** 存在 deploy_targets 行的 run —— 即「实际向某环境部署过」。
// 时间线按「该次部署最晚一台目标机结束时刻」降序(未结束回退到开始时刻),最近在前。

// loadDeploymentsByEnv 取某项目全部已部署 run,按环境分组(每组按部署时刻降序)。
// 返回 (env→[]Deployment, env 出现顺序);env 顺序按各环境最近部署时刻降序(活跃环境靠前)。
func (s *Service) loadDeploymentsByEnv(ctx context.Context, projectID string) (map[string][]Deployment, []string, error) {
	// 1) 拉「有部署目标的 run + 其环境/触发元数据」。一行一目标机,后续内存按 run 折叠。
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.resolved_environment, r.trigger_commit, r.trigger_branch, r.trigger_actor,
		       dt.server_id, dt.server_name, dt.status, dt.started_at, dt.finished_at
		FROM pipeline_runs r
		JOIN deploy_targets dt ON dt.run_id = r.id
		WHERE r.project_id = ? AND TRIM(r.resolved_environment) <> ''
		ORDER BY r.id ASC, dt.started_at ASC, dt.id ASC`, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("environments: query deployments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type acc struct {
		dep    Deployment
		latest string // 最晚目标机结束/开始时刻(决定部署时刻)
	}
	byRun := map[string]*acc{}
	runOrder := []string{}
	for rows.Next() {
		var (
			runID, env, commit, branch, actor string
			serverID, serverName, status      string
			startedAt                         string
			finishedAt                        sql.NullString
		)
		if err := rows.Scan(&runID, &env, &commit, &branch, &actor,
			&serverID, &serverName, &status, &startedAt, &finishedAt); err != nil {
			return nil, nil, fmt.Errorf("environments: scan deployment row: %w", err)
		}
		a, ok := byRun[runID]
		if !ok {
			a = &acc{dep: Deployment{
				RunID:       runID,
				Commit:      commit,
				Branch:      branch,
				TriggeredBy: actor,
				Targets:     []TargetSummary{},
				Artifacts:   []Artifact{},
				ServerIDs:   []string{},
			}}
			a.dep.envName = env
			byRun[runID] = a
			runOrder = append(runOrder, runID)
		}
		a.dep.Targets = append(a.dep.Targets, TargetSummary{ServerID: serverID, ServerName: serverName, Status: status})
		a.dep.ServerIDs = append(a.dep.ServerIDs, serverID)
		// 部署时刻取最晚的 finished_at(回退 started_at)。字符串 RFC3339 可直接字典序比较。
		when := startedAt
		if finishedAt.Valid && finishedAt.String > when {
			when = finishedAt.String
		}
		if when > a.latest {
			a.latest = when
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("environments: iterate deployments: %w", err)
	}

	// 2) 为每个 run 补产物 + 归并状态 + 落部署时刻。
	for _, runID := range runOrder {
		a := byRun[runID]
		arts, aerr := s.loadArtifacts(ctx, runID)
		if aerr != nil {
			return nil, nil, aerr
		}
		a.dep.Artifacts = arts
		a.dep.Status = aggregateTargetStatus(a.dep.Targets)
		a.dep.DeployedAt = a.latest
	}

	// 3) 按环境分组(每组按部署时刻降序);记录环境出现顺序(按最近部署降序)。
	byEnv := map[string][]Deployment{}
	envLatest := map[string]string{}
	for _, runID := range runOrder {
		d := byRun[runID].dep
		env := d.envName
		byEnv[env] = append(byEnv[env], d)
		if d.DeployedAt > envLatest[env] {
			envLatest[env] = d.DeployedAt
		}
	}
	for env := range byEnv {
		deps := byEnv[env]
		sortByDeployedAtDesc(deps)
		byEnv[env] = deps
	}
	order := make([]string, 0, len(byEnv))
	for env := range byEnv {
		order = append(order, env)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if envLatest[order[i]] != envLatest[order[j]] {
			return envLatest[order[i]] > envLatest[order[j]]
		}
		return order[i] < order[j]
	})
	return byEnv, order, nil
}

// loadArtifacts 取某 run 的全部产物摘要(按创建序;供时间线展示 + 回滚选品)。
func (s *Service) loadArtifacts(ctx context.Context, runID string) ([]Artifact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, name, reference FROM run_artifacts
		 WHERE run_id = ? ORDER BY created_at ASC, id ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("environments: query artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.Type, &a.Name, &a.Reference); err != nil {
			return nil, fmt.Errorf("environments: scan artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

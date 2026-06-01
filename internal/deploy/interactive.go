package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// interactive.go 实现「交互式分批部署」(P0 · 对标云效 firstBatchPause)+ 运行时续发/中止。
//
// 流程:Deploy(strategy=interactive) 发首批(同金丝雀子集)→ 首批全过则其余登记 pending 并**暂停**
// (run 不置终态,保持成功);用户在运行详情按「继续部署」(ContinueDeploy 续发其余)或「中止」
// (AbortDeploy 标记其余为已中止、保留旧版本、不动已部署批次)。首批失败 → 不暂停,直接中止其余
// (= 金丝雀失败语义,安全:其余机仍跑旧版本)。
//
// 复用既有原语:canaryCount(首批量)、deployFanout(单批并行)、deployWithStrategy(续发)、
// UpsertDeployTargets / overallStatusOfTargets(逐目标更新 + 全量重算终态)。无新表、无迁移。

// ContinueInput 是一次「续发暂停中其余目标」请求(复用上次产物 + 配置,同 RetryInput 由前端带回)。
type ContinueInput struct {
	RunID       string
	ArtifactID  string
	Config      map[string]string
	HealthCheck *HealthCheck
	// Strategy 是续发其余批次的策略(默认 rolling;一般首批已验证,其余直接铺)。
	Strategy string
}

// AbortInput 是一次「中止分批部署」请求(只需 RunID;标记 pending 为已中止,不动已部署批次)。
type AbortInput struct {
	RunID string
}

// pendingResult 合成「已登记、等待续发」的 pending 结果(交互式首批通过后,其余机的占位)。
func pendingResult(srv *target.Server, msg string) TargetResult {
	now := time.Now().UTC()
	return TargetResult{
		ServerID:   srv.ID,
		ServerName: srv.Name,
		Status:     run.TargetPending,
		Message:    msg,
		StartedAt:  now,
	}
}

// deployInteractiveFirstBatch 发首批并决定是否暂停。返回 (按 servers 顺序对齐的结果, paused)。
//   - 首批全过且有其余 → 其余登记 pending,paused=true(调用方不置终态)。
//   - 首批未全过 → 中止其余(标 failed 人读),paused=false(调用方据结果置终态)。
//   - 无其余(单机/全在首批)→ 直接返回首批结果,paused=false。
func (s *service) deployInteractiveFirstBatch(ctx context.Context, servers []*target.Server, a run.Artifact, cfg map[string]string, hc *HealthCheck) ([]TargetResult, bool) {
	total := len(servers)
	if total == 0 {
		return nil, false
	}
	n := canaryCount(cfg, total)
	results := make([]TargetResult, total)

	firstRes := s.deployFanout(ctx, servers[:n], a, cfg, hc)
	copy(results[:n], firstRes)

	rest := servers[n:]
	if len(rest) == 0 {
		return results, false // 无其余可分批 → 等同一次性部署,正常置终态。
	}

	if allSuccess(firstRes) {
		for i, srv := range rest {
			results[n+i] = pendingResult(srv, fmt.Sprintf("首批 %d 台已部署成功,待确认:点「继续部署」发布其余 %d 台,或「中止」保留旧版本", n, len(rest)))
		}
		return results, true // 暂停,等人确认。
	}
	// 首批未全过 → 中止其余(安全:其余机仍运行旧版本)。
	for i, srv := range rest {
		results[n+i] = abortedResult(srv, fmt.Sprintf("首批 %d 台未全部成功,已中止后续 %d 台(本机未部署,仍运行旧版本)", n, len(rest)))
	}
	return results, false
}

// ContinueDeploy 见接口注释:续发 pending 目标。
func (s *service) ContinueDeploy(ctx context.Context, in ContinueInput) ([]TargetResult, error) {
	rn, existing, pendingIDs, artifact, err := s.loadPending(ctx, in.RunID, in.ArtifactID)
	if err != nil {
		return nil, err
	}
	_ = rn

	// 解析 pending 服务器(任一不存在 → 422,整次拒绝)。
	servers := make([]*target.Server, 0, len(pendingIDs))
	for _, sid := range pendingIDs {
		srv, gerr := s.targets.Get(ctx, sid)
		if gerr != nil {
			if errors.Is(gerr, target.ErrNotFound) {
				return nil, ErrServerNotFound
			}
			return nil, gerr
		}
		servers = append(servers, srv)
	}

	// 续发其余批次(默认 rolling;首批已验证,无需再金丝雀)。
	strategy := NormalizeStrategy(in.Strategy)
	if strategy == StrategyInteractive {
		strategy = StrategyRolling // 续发不再二次暂停。
	}
	res := s.deployWithStrategy(ctx, servers, *artifact, in.Config, in.HealthCheck, strategy)

	return s.upsertAndRecompute(ctx, in.RunID, res, existing)
}

// AbortDeploy 见接口注释:把 pending 标记为已中止(failed),不动已部署批次。
func (s *service) AbortDeploy(ctx context.Context, in AbortInput) ([]TargetResult, error) {
	existing, err := s.runs.ListDeployTargets(ctx, in.RunID)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		return nil, ErrRunNotDeployed
	}
	now := time.Now().UTC()
	aborted := make([]TargetResult, 0)
	for i := range existing {
		if existing[i].Status != run.TargetPending {
			continue
		}
		fin := now
		aborted = append(aborted, TargetResult{
			ServerID:   existing[i].ServerID,
			ServerName: existing[i].ServerName,
			Status:     run.TargetFailed,
			Message:    "已中止分批部署:本机未部署,保留旧版本",
			StartedAt:  existing[i].StartedAt,
			FinishedAt: &fin,
		})
	}
	if len(aborted) == 0 {
		return nil, ErrNoPendingTargets
	}
	return s.upsertAndRecompute(ctx, in.RunID, aborted, existing)
}

// loadPending 校验 run + 取 pending 目标集合 + 定位产物(Continue 用)。
func (s *service) loadPending(ctx context.Context, runID, artifactID string) (*run.Run, []run.DeployTarget, []string, *run.Artifact, error) {
	rn, err := s.runs.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, run.ErrNotFound) {
			return nil, nil, nil, nil, ErrRunNotFound
		}
		return nil, nil, nil, nil, err
	}
	existing, err := s.runs.ListDeployTargets(ctx, runID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(existing) == 0 {
		return nil, nil, nil, nil, ErrRunNotDeployed
	}
	pendingIDs := make([]string, 0)
	for i := range existing {
		if existing[i].Status == run.TargetPending {
			pendingIDs = append(pendingIDs, existing[i].ServerID)
		}
	}
	if len(pendingIDs) == 0 {
		return nil, nil, nil, nil, ErrNoPendingTargets
	}
	arts, err := s.runs.ListArtifacts(ctx, runID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var artifact *run.Artifact
	for i := range arts {
		if arts[i].ID == artifactID {
			a := arts[i]
			artifact = &a
			break
		}
	}
	if artifact == nil {
		return nil, nil, nil, nil, ErrArtifactNotFound
	}
	return rn, existing, pendingIDs, artifact, nil
}

// upsertAndRecompute 逐目标 upsert 本批结果 → 据全量最新目标重算 run 终态 → 返回全量 targets。
// 若重算后仍有失败 → best-effort 触发 AI 诊断(不阻断)。
func (s *service) upsertAndRecompute(ctx context.Context, runID string, batch []TargetResult, _ []run.DeployTarget) ([]TargetResult, error) {
	dts := make([]run.DeployTarget, 0, len(batch))
	for _, r := range batch {
		dts = append(dts, run.DeployTarget{
			RunID:      runID,
			ServerID:   r.ServerID,
			ServerName: r.ServerName,
			Status:     r.Status,
			Message:    r.Message,
			StartedAt:  r.StartedAt,
			FinishedAt: r.FinishedAt,
		})
	}
	if err := s.runs.UpsertDeployTargets(ctx, runID, dts); err != nil {
		return nil, err
	}
	all, err := s.runs.ListDeployTargets(ctx, runID)
	if err != nil {
		return nil, err
	}
	final := overallStatusOfTargets(all)
	if err := s.runs.SetDeployTerminal(ctx, runID, final); err != nil {
		return nil, err
	}
	out := make([]TargetResult, 0, len(all))
	for i := range all {
		t := all[i]
		out = append(out, TargetResult{
			ServerID:   t.ServerID,
			ServerName: t.ServerName,
			Status:     t.Status,
			Message:    t.Message,
			StartedAt:  t.StartedAt,
			FinishedAt: t.FinishedAt,
		})
	}
	s.seedDiagnosisOnFailure(ctx, runID, final, out)
	return out, nil
}

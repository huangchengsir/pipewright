package promotion

import (
	"context"
	"errors"
	"fmt"
)

// RunInfo 是晋级编排所需的源运行最小信息(由 RunLookup 提供)。
type RunInfo struct {
	ID        string
	ProjectID string
	Status    string // 须为成功终态方可晋级
}

// RunLookup 给出源运行的状态/归属(run.Service 的最小适配,避免 import 整个 run 包做编排)。
type RunLookup interface {
	// LookupRun 返回源运行信息;不存在 → ErrRunNotFound。
	LookupRun(ctx context.Context, runID string) (RunInfo, error)
}

// SuccessStatus 是「运行成功」的判定串(对齐 run.StatusSuccess="success",避免反向依赖)。
const SuccessStatus = "success"

// SecretResolver 把 secret 变量的 credentialId 解密为明文(经保险库)。
// **仅供进程内注入**;返回明文绝不回 HTTP。credential 不存在/未配置保险库 → 返回 error,
// 由调用方决定降级(本包把该变量标记为不可解析并跳过注入,不让晋级失败于缺失凭据)。
type SecretResolver interface {
	Reveal(credentialID string) (string, error)
}

// Gate 是晋级审批门(复用 approval 内核):对 gated 目标环境,阻塞等待人工批准/拒绝。
// 返回 (true,nil)=批准;(false,nil)=拒绝;(_,err)=取消/超时等。key 唯一标识该晋级门。
// 实现(在 httpapi 装配)负责登记待批 + 阻塞 approval.Coordinator + 决定记录,**不复制审批逻辑**。
type Gate interface {
	Await(ctx context.Context, key string, info GateInfo) (approved bool, actor string, err error)
}

// GateInfo 给审批门展示用上下文(无敏感数据)。
type GateInfo struct {
	PromotionID       string
	SourceRunID       string
	TargetEnvironment string
}

// Coordinator 编排一次晋级:校验源运行成功 → 算下一目标 → 防跳级/越尾/重复 →
// gated 走审批门 → 落记录。复用注入的 Gate(approval 内核)与 RunLookup(run 状态)。
type Coordinator struct {
	store  *Store
	runs   RunLookup
	gate   Gate // 可为 nil:无 gate 装配时,gated 环境晋级直接拒绝(fail-closed,绝不擅自放行)
	secret SecretResolver
}

// NewCoordinator 构造晋级编排器。gate 为 nil 时 gated 环境晋级被拒(fail-closed)。
// secret 为 nil 时 secret 变量不解析注入(降级:仅注入非 secret 变量)。
func NewCoordinator(store *Store, runs RunLookup, gate Gate, secret SecretResolver) *Coordinator {
	return &Coordinator{store: store, runs: runs, gate: gate, secret: secret}
}

// PromoteResult 是一次晋级请求的结果。
type PromoteResult struct {
	Record   Record
	Approved bool // gated 时表示审批结果;非 gated 恒 true
}

// Promote 把源运行晋级到「链上下一级」目标环境(可指定 target 做显式校验;空则取下一级)。
//
// 流程:
//  1. 取源运行(须 success)。
//  2. 取项目环境链(须已配置)。
//  3. 算当前已达环境 → 下一目标;校验 target(防跳级/越尾)。
//  4. 防重复:该 run→该环境已有 pending/promoted → ErrAlreadyPromoted。
//  5. gated 目标:登记 pending 记录 → 经 Gate 阻塞等待 → 批准 promoted / 拒绝 rejected。
//     非 gated:直接 promoted。
//
// actor 为操作者(审计/展示)。注意:gated 路径会阻塞直到审批决定(或 ctx 取消)。
func (c *Coordinator) Promote(ctx context.Context, runID, target, actor string) (*PromoteResult, error) {
	info, err := c.runs.LookupRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if info.Status != SuccessStatus {
		return nil, ErrRunNotSuccessful
	}

	chain, err := c.store.GetChain(ctx, info.ProjectID)
	if err != nil {
		return nil, err
	}

	curEnv, err := c.store.currentEnvForRun(ctx, runID, chain)
	if err != nil {
		return nil, err
	}

	// target 留空 → 取下一级。给定 target 时:先校验它在链上,再(在防跳级前)查重复——
	// 这样「重新晋级一个已达环境」明确报 ErrAlreadyPromoted(而非误报 ErrSkipEnv)。
	if target == "" {
		target, err = chain.nextTarget(curEnv)
		if err != nil {
			return nil, err
		}
	} else if chain.IndexOf(target) < 0 {
		return nil, ErrUnknownEnv
	}

	active, err := c.store.hasActivePromotion(ctx, runID, target)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrAlreadyPromoted
	}

	// 防跳级/越尾:target 须恰为「当前已达环境的下一级」。
	if err := chain.validateTarget(curEnv, target); err != nil {
		return nil, err
	}

	gated := chain.Gated(target)
	if !gated {
		rec := Record{
			ProjectID:         info.ProjectID,
			SourceRunID:       runID,
			FromEnvironment:   curEnv,
			TargetEnvironment: target,
			Status:            StatusPromoted,
			PromotedBy:        actor,
		}
		id, cerr := c.store.createRecord(ctx, rec)
		if cerr != nil {
			return nil, cerr
		}
		rec.ID = id
		rec.Status = StatusPromoted
		return &PromoteResult{Record: rec, Approved: true}, nil
	}

	// gated 目标:无 Gate 装配 → fail-closed(登记 rejected,绝不擅自放行)。
	stageKey := promotionStageKey(runID, target)
	rec := Record{
		ProjectID:         info.ProjectID,
		SourceRunID:       runID,
		FromEnvironment:   curEnv,
		TargetEnvironment: target,
		Status:            StatusPending,
		ApprovalStage:     stageKey,
		PromotedBy:        actor,
	}
	id, cerr := c.store.createRecord(ctx, rec)
	if cerr != nil {
		return nil, cerr
	}
	rec.ID = id

	if c.gate == nil {
		_ = c.store.decideRecord(ctx, id, StatusRejected, "no_gate")
		rec.Status = StatusRejected
		return &PromoteResult{Record: rec, Approved: false}, ErrGateRejected
	}

	approved, by, gerr := c.gate.Await(ctx, stageKey, GateInfo{
		PromotionID:       id,
		SourceRunID:       runID,
		TargetEnvironment: target,
	})
	if by == "" {
		by = actor
	}
	if gerr != nil {
		_ = c.store.decideRecord(ctx, id, StatusRejected, by)
		rec.Status = StatusRejected
		return &PromoteResult{Record: rec, Approved: false}, gerr
	}
	if !approved {
		_ = c.store.decideRecord(ctx, id, StatusRejected, by)
		rec.Status = StatusRejected
		return &PromoteResult{Record: rec, Approved: false}, ErrGateRejected
	}
	if derr := c.store.decideRecord(ctx, id, StatusPromoted, by); derr != nil {
		return nil, derr
	}
	rec.Status = StatusPromoted
	rec.PromotedBy = by
	return &PromoteResult{Record: rec, Approved: true}, nil
}

// ResolveEnvVars 解析某项目某环境的全部作用域变量为可注入的明文 K=V(secret 经保险库解密)。
// secret 解密失败(凭据缺失/保险库未配)→ 跳过该变量(不让注入因缺失凭据而崩),但记于返回的
// unresolved 列表(供调用方记日志/告警,绝不含明文)。无 SecretResolver 时所有 secret 变量进 unresolved。
func (c *Coordinator) ResolveEnvVars(ctx context.Context, projectID, env string) (vars []ResolvedVar, unresolved []string, err error) {
	raw, err := c.store.loadRawVariables(ctx, projectID, env)
	if err != nil {
		return nil, nil, err
	}
	vars = make([]ResolvedVar, 0, len(raw))
	for _, v := range raw {
		if !v.Secret {
			vars = append(vars, ResolvedVar{Key: v.Key, Value: v.Value})
			continue
		}
		if c.secret == nil || v.CredentialID == "" {
			unresolved = append(unresolved, v.Key)
			continue
		}
		plain, rerr := c.secret.Reveal(v.CredentialID)
		if rerr != nil {
			unresolved = append(unresolved, v.Key) // 错误体绝无明文
			continue
		}
		vars = append(vars, ResolvedVar{Key: v.Key, Value: plain, Secret: true})
	}
	return vars, unresolved, nil
}

// promotionStageKey 由 runID + target 组成晋级审批门唯一 stageId(复用 approval.Key 语义)。
// 形如 "promote:<target>";审批门内核以 (runID, stageId) 组合键解析,故 runID 不必再编入 stageId。
func promotionStageKey(runID, target string) string {
	_ = runID
	return fmt.Sprintf("promote:%s", target)
}

// IsTerminalErr 报告 err 是否为「晋级被拒/失败」类终态错误(供 HTTP 层区分 409/422)。
func IsTerminalErr(err error) bool {
	return errors.Is(err, ErrGateRejected) ||
		errors.Is(err, ErrRunNotSuccessful) ||
		errors.Is(err, ErrAlreadyPromoted) ||
		errors.Is(err, ErrAlreadyAtTop) ||
		errors.Is(err, ErrSkipEnv)
}

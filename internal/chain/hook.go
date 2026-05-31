package chain

import (
	"context"
	"log"
	"time"

	"github.com/huangchengsir/pipewright/internal/run"
)

// DefaultMaxDepth 是默认串联深度上限(根运行 depth=0;每向下游串联一层 +1)。
// 超过此深度的串联被钩子拒绝,作为无限链的硬性兜底。
const DefaultMaxDepth = 5

// hookTimeout 限制单次串联钩子的总耗时(查目标 + 建下游运行),防慢 DB 把钩子 goroutine 挂死。
const hookTimeout = 30 * time.Second

// NewHook 把 chain.Service + run.Service 适配为 run.WorkerPool 的 best-effort 终态钩子
// (与 WithNotifyHook 同签名,供 main 在装配时与通知钩子组合到同一终态槽;run 包不 import chain)。
//
// 行为:仅在 run 落 **success** 终态时动作。读上游运行 → 取其 chain_depth → 环路安全门:
//   - 深度门:上游 depth+1 > maxDepth → 拒绝(不再串联),记日志返回。
//   - 路径门:沿 chain_source_run_id 上溯祖先链,若上游项目已在本链路径中(自环 / 互环)→ 拒绝。
//
// 通过后,查该上游项目「启用」的下游目标,逐个经 run.Service.Create 以 TriggerChain 创建下游运行,
// 携带 ChainSourceRunID = 上游 runID、ChainDepth = 上游 depth+1(溯源 + 深度递增)。
//
// best-effort:自带超时;任一下游创建失败仅记日志续建其余;**绝不影响上游 run 终态**。
// maxDepth ≤ 0 时回落 DefaultMaxDepth。
func NewHook(chains Service, runs run.Service, maxDepth int) func(ctx context.Context, runID, finalStatus string) {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	return func(ctx context.Context, runID, finalStatus string) {
		if chains == nil || runs == nil {
			return
		}
		if finalStatus != run.StatusSuccess {
			return // 仅成功触发串联;其余终态不串联。
		}

		hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
		defer cancel()

		upstream, err := runs.Get(hookCtx, runID)
		if err != nil {
			log.Printf("[chain] run %s: 取上游运行失败(跳过串联):%v", runID, err)
			return
		}

		nextDepth := upstream.Trigger.ChainDepth + 1
		if nextDepth > maxDepth {
			// 深度门:硬性兜底,拒绝继续串联(防无限链 / 配置成环)。
			log.Printf("[chain] run %s(项目 %s):串联深度达上限 %d,停止串联(环路安全)",
				runID, upstream.ProjectID, maxDepth)
			return
		}

		targets, err := chains.ListEnabled(hookCtx, upstream.ProjectID)
		if err != nil {
			log.Printf("[chain] 项目 %s:查下游目标失败(跳过串联):%v", upstream.ProjectID, err)
			return
		}
		if len(targets) == 0 {
			return // 无启用目标:无串联。
		}

		// 路径门:收集本串联链已出现的祖先项目(含上游自身),沿 chain_source_run_id 上溯。
		// 用于二次兜底自环 / 互环:即便深度未到上限,也拒绝触发「已在本链路径中」的下游项目。
		ancestors := ancestorProjects(hookCtx, runs, upstream, maxDepth)

		for _, t := range targets {
			if ancestors[t.DownstreamProjectID] {
				log.Printf("[chain] run %s:下游项目 %s 已在本串联链路径中,跳过(环路安全)",
					runID, t.DownstreamProjectID)
				continue
			}
			created, cerr := runs.Create(hookCtx, t.DownstreamProjectID, run.Trigger{
				Type:             run.TriggerChain,
				Branch:           t.Branch,
				Actor:            "chain",
				ChainSourceRunID: runID,
				ChainDepth:       nextDepth,
			})
			if cerr != nil {
				log.Printf("[chain] run %s → 下游项目 %s:创建下游运行失败(续建其余):%v",
					runID, t.DownstreamProjectID, cerr)
				continue
			}
			log.Printf("[chain] run %s(项目 %s)成功 → 串联触发下游运行 %s(项目 %s,深度 %d)",
				runID, upstream.ProjectID, created.ID, t.DownstreamProjectID, nextDepth)
		}
	}
}

// ancestorProjects 沿 chain_source_run_id 上溯,收集本串联链中已出现的项目 id 集合
// (含当前上游项目自身)。上溯步数受 maxDepth+1 钳制(深度门已保证链不超此长度),
// 任一上溯取数失败即停(best-effort)。该集合用于路径门二次环路检测。
func ancestorProjects(ctx context.Context, runs run.Service, upstream *run.Run, maxDepth int) map[string]bool {
	seen := map[string]bool{upstream.ProjectID: true}
	srcID := upstream.Trigger.ChainSourceRunID
	// 钳制上溯步数:深度门保证 depth ≤ maxDepth,故祖先不超过 maxDepth 层;+1 余量防御。
	for steps := 0; srcID != "" && steps <= maxDepth; steps++ {
		anc, err := runs.Get(ctx, srcID)
		if err != nil {
			break
		}
		if seen[anc.ProjectID] {
			break // 已见(防御性早停;正常链不应重复)。
		}
		seen[anc.ProjectID] = true
		srcID = anc.Trigger.ChainSourceRunID
	}
	return seen
}

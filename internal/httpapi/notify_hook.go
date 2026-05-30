package httpapi

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/huangchengsir/pipewright/internal/notify"
	"github.com/huangchengsir/pipewright/internal/run"
)

// NewNotifyHook 把 run.Service + notify.Service 适配为 run.WorkerPool 的 best-effort 通知钩子
// (供 main 装配,使 run 包不 import notify;仿 NewDiagnoseHook 解耦)。
//
// 钩子在 run 落终态后被 worker 在独立 goroutine 调用:它「run 终态 → event → 构 Payload →
// notify.Router.RouteEvent」。RouteEvent 内部已是 best-effort(未配路由不发、单渠道失败续发);
// 此处再自带超时(防 webhook 慢响应把 goroutine 挂死),失败仅记日志,**绝不影响 run 终态**。
//
// run 终态 → 事件映射(冻结):
//   - success        → build_succeeded
//   - failed         → build_failed
//   - partial_failed → deploy_failed
//   - rolled_back    → rollback
//
// 其余终态/非终态不映射任何事件(不发)。
func NewNotifyHook(runs run.Service, notifySvc notify.Service) func(ctx context.Context, runID, finalStatus string) {
	return func(ctx context.Context, runID, finalStatus string) {
		if runs == nil || notifySvc == nil {
			return
		}
		event, ok := eventForStatus(finalStatus)
		if !ok {
			return // 无对应事件:不发。
		}

		// best-effort:自带超时;父 ctx 取消(停机)时一并取消。
		hookCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		// 取运行元数据构造 TemplateVars(项目名/分支/commit/状态/耗时/runId/errorSummary)。
		// 取失败仅记日志、用降级 vars(仅含事件/状态/runId)继续:通知 best-effort,不因取数
		// 失败而完全不发。errorSummary 经 notify.MaskErrorSummary 尽力脱敏(绝无明文 secret)。
		var (
			projectName, branch, commit, errorSummary string
			durationMs                                int64
		)
		if r, err := runs.Get(hookCtx, runID); err != nil {
			log.Printf("[notify] run %s: 取运行元数据失败(用降级 vars):%v", runID, err)
		} else {
			projectName = r.ProjectName
			branch = r.Trigger.Branch
			commit = r.Trigger.Commit
			durationMs = runDurationMs(r)
			errorSummary = notify.SummarizeFailure(r.FailureLog)
		}

		vars := notify.TemplateVars{
			Project:      projectName,
			Branch:       branch,
			Commit:       notify.ShortCommit(commit),
			Status:       finalStatus,
			Event:        event,
			RunID:        runID,
			ErrorSummary: errorSummary,
		}
		if durationMs > 0 {
			vars.DurationMs = strconv.FormatInt(durationMs, 10)
		}

		// 按渠道渲染模板(无模板 → 平台默认,5-2 行为不变)→ SendVia。RouteEventVars 始终返回
		// nil;此处兜底防御。
		if err := notifySvc.RouteEventVars(hookCtx, event, vars); err != nil {
			log.Printf("[notify] run %s: 事件 %s 路由 best-effort 失败:%v", runID, event, err)
		}
	}
}

// eventForStatus 把 run 终态映射为冻结的通知事件;无对应事件返回 ("", false)。
func eventForStatus(status string) (string, bool) {
	switch status {
	case run.StatusSuccess:
		return notify.EventBuildSucceeded, true
	case run.StatusFailed:
		return notify.EventBuildFailed, true
	case run.StatusPartialFailed:
		return notify.EventDeployFailed, true
	case run.StatusRolledBack:
		return notify.EventRollback, true
	default:
		return "", false
	}
}

// runDurationMs 计算运行耗时(毫秒);缺 StartedAt/FinishedAt 时返回 0(payload 据此省略)。
func runDurationMs(r *run.Run) int64 {
	if r == nil || r.StartedAt == nil || r.FinishedAt == nil {
		return 0
	}
	d := r.FinishedAt.Sub(*r.StartedAt)
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

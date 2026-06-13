package httpapi

import (
	"context"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/approval"
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
func NewNotifyHook(runs run.Service, notifySvc notify.Service, secretSrc *RunSecretSource) func(ctx context.Context, runID, finalStatus string) {
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
			projectID, projectName, branch, commit, errorSummary string
			durationMs                                           int64
		)
		if r, err := runs.Get(hookCtx, runID); err != nil {
			log.Printf("[notify] run %s: 取运行元数据失败(用降级 vars):%v", runID, err)
		} else {
			projectID = r.ProjectID
			projectName = r.ProjectName
			branch = r.Trigger.Branch
			commit = r.Trigger.Commit
			durationMs = runDurationMs(r)
			// errorSummary 出网脱敏(红线修):先用该 run 真实凭据 Masker 替换(项目/registry/secret
			// 变量的明文),再过 notify 的关键字/形态正则兜底——双层确保通知正文绝无明文凭据。
			errorSummary = maskerFor(hookCtx, secretSrc, runID).Scrub(notify.SummarizeFailure(r.FailureLog))
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

		// 按项目维度路由(Story 5.4):projectID 有项目级路由 → 整体覆盖(只走项目级);否则回退
		// 全局。projectID 空(取数失败降级)→ 等价全局路径。按渠道渲染模板(无模板 → 平台默认,
		// 5-2 行为不变)→ SendVia。RouteEventForProject 始终返回 nil;此处兜底防御。
		if err := notifySvc.RouteEventForProject(hookCtx, projectID, event, vars); err != nil {
			log.Printf("[notify] run %s: 事件 %s 路由 best-effort 失败:%v", runID, event, err)
		}
	}
}

// approvalLinkTTL 是签名审批链接的有效期(审批人节奏可较慢,与门兜底超时一致)。
const approvalLinkTTL = 24 * time.Hour

// NewApprovalNotifier 构造审批门「需要审批」通知钩子(供 main 注入 NewApprovalGate)。
//
// run 进入 waiting_approval 后,本钩子 best-effort 地:签发签名审批链接(signer + publicURL)→
// 构 TemplateVars(含 ActionURL)→ notifySvc.RouteEventForProject(EventApprovalRequired)。
//   - signer 禁用(无 master key)/ publicURL 为空 / notifySvc 为 nil → 跳过(不发链接、不发通知),
//     绝不阻塞门。
//   - 自带超时(防慢渠道挂死 goroutine);RouteEventForProject 内部已 best-effort(未配路由不发)。
//   - 链接、token 绝不写日志。
func NewApprovalNotifier(notifySvc notify.Service, signer *approval.Signer, publicURL string) ApprovalNotifier {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if notifySvc == nil || signer == nil || !signer.Enabled() || base == "" {
		// 依赖不全:返回 nil,门据此跳过通知(功能优雅关闭)。
		return nil
	}
	return func(ctx context.Context, projectID, projectName, runID, stageID string) {
		token := signer.Sign(runID, stageID, time.Now().Add(approvalLinkTTL))
		if token == "" {
			return // 签名器禁用(防御);不发链接。
		}
		link := base + "/approvals?token=" + url.QueryEscape(token)

		vars := notify.TemplateVars{
			Project:   projectName,
			Status:    "waiting_approval",
			Event:     notify.EventApprovalRequired,
			RunID:     runID,
			ActionURL: link,
		}

		// 异步发:门随后阻塞在 coord.Wait 上等待决定,通知不应拖慢进入等待。脱离父 ctx
		// (run 取消不应中断已发起的通知)+ 自带 30s 超时(防慢渠道挂死 goroutine)。best-effort,绝不冒泡。
		go func() {
			hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := notifySvc.RouteEventForProject(hookCtx, projectID, notify.EventApprovalRequired, vars); err != nil {
				log.Printf("[notify] run %s: 审批通知路由 best-effort 失败:%v", runID, err)
			}
		}()
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

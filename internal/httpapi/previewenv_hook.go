package httpapi

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/huangchengsir/pipewright/internal/previewenv"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// previewenv_hook.go 装配「PR 部署成功 → 分配预览环境」的终态钩子(R4 E4.1)。
//
// 复用通知/PR 状态钩子的终态时机:run 落 **success** 且属于某 PR、且其项目**开启了预览** →
// 据本次部署的上游容器分配 pr-<n>-<proj>.base 预览域名(DNS-01 + 反代路由),落 PreviewEnv。
//
// **优雅降级铁律**:本钩子全程 best-effort + recover-safe —— 项目未开启 / 无 DNS / 解析不到 PR 号
// 或上游容器 / 分配失败,一律静默或仅记日志,**绝不影响(更别说阻断)部署**。
//
// 诚实说明「PR 号 / 上游容器」从哪来(数据模型现状):
//   - run 模型目前**不直接**携带 PR 号。本钩子从 run.Trigger.Branch 尽力解析(pr-<n> / pull/<n> /
//     纯数字分支),解析不到就静默不分配 —— 这覆盖「PR 分支约定带号」的常见场景,且永不误伤普通部署。
//   - 上游容器/端口取自该项目在目标主机上**已有的反代路由**(刚部署的应用通常已有一条对外路由),
//     取不到则静默。这样无需改 run/部署链路即可复用既有信息,诚实而非臆造。

// PreviewProvisioner 抽象本钩子消费的「分配/刷新预览环境」能力(由 previewenv.service 实现)。
type PreviewProvisioner interface {
	GetConfig(ctx context.Context, projectID string) (*previewenv.Config, error)
	Provision(ctx context.Context, in previewenv.ProvisionInput) (*previewenv.PreviewEnv, error)
}

// prBranchRes 从分支名尽力解析 PR 号的几种约定形态(均大小写不敏感)。
var (
	prPrefixRe = regexp.MustCompile(`^(?:pr|pull|mr|merge-requests?)[-/](\d+)$`)
	allDigitRe = regexp.MustCompile(`^\d+$`)
)

// parsePRNumber 从分支名尽力解析 PR 号;解析不到返回 0(钩子据此静默不分配)。
func parsePRNumber(branch string) int {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return 0
	}
	if m := prPrefixRe.FindStringSubmatch(b); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	if allDigitRe.MatchString(b) {
		if n, err := strconv.Atoi(b); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// NewPreviewProvisionHook 构造 PR 部署成功 → 预览环境分配的终态钩子。
//
// 依赖任一为 nil → 返回静默 no-op 钩子(功能优雅关闭,绝不影响 run 终态)。
func NewPreviewProvisionHook(
	runs run.Service,
	preview PreviewProvisioner,
	proxySvc proxy.Service,
	servers target.Service,
) func(ctx context.Context, runID, finalStatus string) {
	if runs == nil || preview == nil || proxySvc == nil || servers == nil {
		return func(context.Context, string, string) {}
	}
	return func(ctx context.Context, runID, finalStatus string) {
		// recover-safe:本钩子任何 panic 都不得冒泡到 worker(绝不影响 run 终态)。
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[preview] run %s: provision 钩子 panic 已恢复:%v", runID, rec)
			}
		}()

		if finalStatus != run.StatusSuccess {
			return // 仅成功部署分配预览。
		}
		r, err := runs.Get(ctx, runID)
		if err != nil || r == nil {
			return
		}
		prNumber := parsePRNumber(r.Trigger.Branch)
		if prNumber == 0 {
			return // 非 PR 约定分支:静默(普通部署不分配预览)。
		}

		// 项目未开启预览 → 提前静默退出(省去后续解析开销)。
		cfg, cerr := preview.GetConfig(ctx, r.ProjectID)
		if cerr != nil || cfg == nil || !cfg.Enabled {
			return
		}

		// 目标主机:取本次运行解析出的目标服务器(手动触发可能为空 → 静默)。
		serverID := ""
		if len(r.Trigger.ResolvedTargetServerIDs) > 0 {
			serverID = strings.TrimSpace(r.Trigger.ResolvedTargetServerIDs[0])
		}
		if serverID == "" {
			return
		}

		// 上游容器/端口:取该主机上该项目**已有的一条反代路由**(刚部署应用通常已有对外路由)。
		// 取不到 → 静默(无从知道预览该指向哪个容器,诚实降级,绝不臆造)。
		container, port, ok := firstUpstreamForServer(ctx, proxySvc, serverID)
		if !ok {
			return
		}

		// 宿主机公网 IPv4(A 记录指向它):据 server.Host 解析(非用户自由文本)。
		srv, serr := servers.Get(ctx, serverID)
		if serr != nil || srv == nil {
			return
		}
		hostIP, iperr := resolveHostIPv4(ctx, srv.Host)
		if iperr != nil {
			return
		}

		env, perr := preview.Provision(ctx, previewenv.ProvisionInput{
			ProjectID:         r.ProjectID,
			PRNumber:          prNumber,
			Branch:            r.Trigger.Branch,
			ServerID:          serverID,
			UpstreamContainer: container,
			UpstreamPort:      port,
			HostIP:            hostIP,
		})
		if perr != nil {
			log.Printf("[preview] run %s: PR #%d 预览环境分配失败(不影响部署):%v", runID, prNumber, perr)
			return
		}
		if env != nil {
			log.Printf("[preview] run %s: PR #%d 预览环境就绪 → %s", runID, prNumber, env.Subdomain)
		}
	}
}

// firstUpstreamForServer 取某主机上首条 enabled 反代路由的上游容器/端口(供预览反代复用部署目标)。
// 无路由 → ok=false。best-effort:列表失败也 ok=false(静默降级)。
func firstUpstreamForServer(ctx context.Context, proxySvc proxy.Service, serverID string) (string, int, bool) {
	routes, err := proxySvc.List(ctx, serverID)
	if err != nil {
		return "", 0, false
	}
	for _, rt := range routes {
		if rt.Enabled && strings.TrimSpace(rt.UpstreamContainer) != "" && rt.UpstreamPort > 0 {
			return rt.UpstreamContainer, rt.UpstreamPort, true
		}
	}
	return "", 0, false
}

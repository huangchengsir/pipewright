package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 容器 AI 诊断 / 看日志(AI moat)。POST /api/servers/{id}/containers/{containerId}/diagnose。
//
// 取该容器最近日志(docker logs)+ 状态摘要,喂给 ai.Diagnose 出「根因假说 + 置信度 + 修复
// 建议 + 可粘贴修复脚本 + 证据行」。同一端点既服务「日志一键 AI 分析」也服务「异常容器一键
// 诊断」(都是日志 → 诊断)。
//
// 铁律:
//   - AC-SEC-02:containerId 严格白名单(reDockerTgt:首字符非 `-`、无 shell 元字符),命令 array 化。
//   - 出网前脱敏:日志进 prompt 前经 Masker(ai.Diagnose 内部 Scrub)。
//   - 优雅降级:AI 未配 / 超时 / 不可解析 → 200 + status=unavailable + 人读 reason,**绝不 500**。

const diagnoseLogTail = "200"

// makeContainerDiagnoseHandler 返回 POST /api/servers/{id}/containers/{containerId}/diagnose
// (认证 + CSRF;读容器日志 + 调 AI)。
func makeContainerDiagnoseHandler(svc target.Service, aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		containerID := chi.URLParam(r, "containerId")
		if !reDockerTgt.MatchString(containerID) || len(containerID) > 256 {
			writeError(w, http.StatusBadRequest, "invalid_container_target", "容器名非法")
			return
		}
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeServerError(w, err)
			return
		}

		// AI 未注入 → 直接降级 unavailable(不去读日志,省一次 SSH)。
		if aiSvc == nil {
			writeJSON(w, http.StatusOK, &ai.Diagnosis{
				Status:      "unavailable",
				Reason:      "AI 未配置,无法诊断。请到「设置 › AI」配置模型后重试。",
				GeneratedAt: time.Now().UTC(),
			})
			return
		}

		cctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// 取日志(docker logs 同时走 stdout/stderr,合并)。失败不致命:空日志也能让 AI 据状态推断。
		logText := fetchContainerLogs(cctx, svc, id, containerID)
		if strings.TrimSpace(logText) == "" {
			logText = "(该容器无日志输出)"
		}

		diag, err := aiSvc.Diagnose(cctx, ai.DiagnoseInput{
			FailureLog:  logText,
			StepName:    "容器 " + containerID,
			ProjectName: "",
			Masker:      mask.NewMasker(),
		})
		if err != nil || diag == nil {
			// ai.Diagnose 仅极少内部错回 error;统一降级 unavailable(绝不 500 / 绝无密钥)。
			writeJSON(w, http.StatusOK, &ai.Diagnosis{
				Status:      "unavailable",
				Reason:      "诊断暂不可用,请稍后重试。",
				GeneratedAt: time.Now().UTC(),
			})
			return
		}
		writeJSON(w, http.StatusOK, diag)
	}
}

// fetchContainerLogs 取容器最近 N 行日志(stdout+stderr 合并;失败返回空串,由调用方兜底)。
func fetchContainerLogs(ctx context.Context, svc target.Service, serverID, containerID string) string {
	res, err := svc.Exec(ctx, serverID, []string{"docker", "logs", "--tail", diagnoseLogTail, containerID})
	if err != nil {
		return ""
	}
	var b strings.Builder
	if s := strings.TrimSpace(res.Stdout); s != "" {
		b.WriteString(s)
	}
	if s := strings.TrimSpace(res.Stderr); s != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	out := b.String()
	if len(out) > 32<<10 {
		out = out[len(out)-(32<<10):] // 取末尾 32KiB(最近日志最相关)
	}
	return out
}

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/mask"
)

// ai_command.go 是「AI 运维终端助手」HTTP 层(运维终端 P1 · 护城河 · AI moat)。
//
// POST /api/ai/command  {nl, context:{os,shell,container,cwd}}  → {command, explanation, risk, reason}
// POST /api/ai/explain  {command, context}                     → {explanation, risk}
//
// 二者为「AI 运维终端」右栏助手供能:中文 → 命令卡 / 命令解释。均过 auth + CSRF(写方法)。
//
// **优雅降级**:生成本就需要 AI —— 未配/未启用 → HTTP 200 + available=false + 人读 reason
// (前端提示「去设置配 AI」,终端 + 复制粘贴 P0 照常可用);LLM 调用/解析失败 → 200 +
// available=true + reason(绝无密钥),命令/解释为空。请求非法 → 400;服务未装配 → 503。
//
// **危险护城河**:命令的最终 risk 由领域层 classifyCommandRisk 确定性复核(rm -rf、mkfs、dd、
// 关机重启等无条件升级 danger),不依赖 AI 在线/诚实——前端据此拦截直接执行、强制二次确认。

// ---- 命令助手 DTO(契约形状;camelCase;绝无明文密钥) ----

// commandContextDTO 是请求里的终端上下文(全部可空)。
type commandContextDTO struct {
	OS        string `json:"os"`
	Shell     string `json:"shell"`
	Container string `json:"container"`
	Cwd       string `json:"cwd"`
}

func (c commandContextDTO) toAI() ai.CommandContext {
	return ai.CommandContext{OS: c.OS, Shell: c.Shell, Container: c.Container, Cwd: c.Cwd}
}

// commandSuggestDTO 是 /ai/command 响应。available=false 时仅 reason 有意义(AI 未配)。
type commandSuggestDTO struct {
	Available   bool   `json:"available"`
	Command     string `json:"command"`
	Explanation string `json:"explanation"`
	Risk        string `json:"risk"`   // safe | write | danger
	Reason      string `json:"reason"` // 风险/注意点 或 降级原因(人读;绝无密钥)
	GeneratedAt string `json:"generatedAt"`
}

// explainCommandDTO 是 /ai/explain 响应。
type explainCommandDTO struct {
	Available   bool   `json:"available"`
	Explanation string `json:"explanation"`
	Risk        string `json:"risk"`
	Reason      string `json:"reason"`
	GeneratedAt string `json:"generatedAt"`
}

// completeCommandDTO 是 /ai/complete 响应(P2 智能补全)。available=false → AI 未配(前端走本地字典)。
type completeCommandDTO struct {
	Available  bool   `json:"available"`
	Completion string `json:"completion"`
}

// makeAICommandHandler 返回 POST /api/ai/command handler(中文 → 命令卡)。
func makeAICommandHandler(aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if aiSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 命令助手未初始化")
			return
		}
		ctx := r.Context()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			NL      string            `json:"nl"`
			Context commandContextDTO `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if strings.TrimSpace(req.NL) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "请描述你想做的操作")
			return
		}

		out, err := aiSvc.CommandSuggest(ctx, ai.CommandSuggestInput{
			NL:      req.NL,
			Context: req.Context.toAI(),
			Masker:  mask.NewMasker(),
		})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, commandSuggestDTO{
				Available:   true,
				Command:     out.Command,
				Explanation: out.Explanation,
				Risk:        out.Risk,
				Reason:      out.Reason,
				GeneratedAt: out.GeneratedAt.UTC().Format(time.RFC3339),
			})
		case errors.Is(err, ai.ErrAINotConfigured):
			writeJSON(w, http.StatusOK, commandSuggestDTO{
				Available: false,
				Reason:    reasonAINotConfigured,
			})
		default:
			writeJSON(w, http.StatusOK, commandSuggestDTO{
				Available: true,
				Reason:    "AI 生成命令失败:" + humanizeGenerateErr(err),
			})
		}
	}
}

// makeAIComposeHandler 返回 POST /api/ai/compose handler(中文 → docker-compose.yml)。
// 未配 AI → 200 + available=false(前端提示去设置配)。
func makeAIComposeHandler(aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if aiSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 助手未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			NL string `json:"nl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if strings.TrimSpace(req.NL) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "请描述你想要的 compose")
			return
		}
		out, err := aiSvc.GenerateCompose(r.Context(), ai.GenerateComposeInput{NL: req.NL, Masker: mask.NewMasker()})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{
				"available":   true,
				"yaml":        out.YAML,
				"generatedAt": out.GeneratedAt.UTC().Format(time.RFC3339),
			})
		case errors.Is(err, ai.ErrAINotConfigured):
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": reasonAINotConfigured})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"available": true, "yaml": "", "reason": "AI 生成 compose 失败:" + humanizeGenerateErr(err)})
		}
	}
}

// makeAICompleteHandler 返回 POST /api/ai/complete handler(P2 智能补全:前缀 → 完整命令)。
// 未配 AI → 200 + available=false(前端退化为本地常用命令字典兜底)。
func makeAICompleteHandler(aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if aiSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 命令助手未初始化")
			return
		}
		ctx := r.Context()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Partial string            `json:"partial"`
			Context commandContextDTO `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if strings.TrimSpace(req.Partial) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "缺少补全前缀")
			return
		}

		out, err := aiSvc.CompleteCommand(ctx, ai.CompleteCommandInput{
			Partial: req.Partial,
			Context: req.Context.toAI(),
			Masker:  mask.NewMasker(),
		})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, completeCommandDTO{Available: true, Completion: out.Completion})
		case errors.Is(err, ai.ErrAINotConfigured):
			writeJSON(w, http.StatusOK, completeCommandDTO{Available: false})
		default:
			// 补全失败静默降级(前端有本地字典兜底):available=true + 空补全,不打扰用户。
			writeJSON(w, http.StatusOK, completeCommandDTO{Available: true, Completion: ""})
		}
	}
}

// makeAIExplainHandler 返回 POST /api/ai/explain handler(解释一条命令 + 确定性风险等级)。
func makeAIExplainHandler(aiSvc ai.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if aiSvc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "AI 命令助手未初始化")
			return
		}
		ctx := r.Context()

		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Command string            `json:"command"`
			Context commandContextDTO `json:"context"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if strings.TrimSpace(req.Command) == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "请提供要解释的命令")
			return
		}

		out, err := aiSvc.ExplainCommand(ctx, ai.ExplainCommandInput{
			Command: req.Command,
			Context: req.Context.toAI(),
			Masker:  mask.NewMasker(),
		})
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, explainCommandDTO{
				Available:   true,
				Explanation: out.Explanation,
				Risk:        out.Risk,
				GeneratedAt: out.GeneratedAt.UTC().Format(time.RFC3339),
			})
		case errors.Is(err, ai.ErrAINotConfigured):
			writeJSON(w, http.StatusOK, explainCommandDTO{
				Available: false,
				Reason:    reasonAINotConfigured,
			})
		default:
			writeJSON(w, http.StatusOK, explainCommandDTO{
				Available: true,
				Reason:    "AI 解释命令失败:" + humanizeGenerateErr(err),
			})
		}
	}
}

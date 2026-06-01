package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/trigger"
)

// webhookURLPrefix 是 webhook 接收端点路径前缀(冻结契约:/api/webhooks/<token>)。
// 注:接收端点本身 = Story 3-2,本期只在配置里返回该 URL。
const webhookURLPrefix = "/api/webhooks/"

// triggerDTO 是触发配置对外响应体(冻结契约;camelCase;密钥仅掩码,绝无明文)。
type triggerDTO struct {
	WebhookURL          string             `json:"webhookUrl"`
	WebhookSecretMasked string             `json:"webhookSecretMasked"`
	Events              eventsDTO          `json:"events"`
	BranchMappings      []branchMappingDTO `json:"branchMappings"`
	UnmatchedPolicy     string             `json:"unmatchedPolicy"`
	// PathFilters 为路径过滤 glob 列表(monorepo · P0);空 = 不启用(放行一切)。
	PathFilters []string `json:"pathFilters"`
}

type eventsDTO struct {
	Push        bool `json:"push"`
	Tag         bool `json:"tag"`
	PullRequest bool `json:"pullRequest"`
	Release     bool `json:"release"`
}

type branchMappingDTO struct {
	ID              string   `json:"id"`
	BranchPattern   string   `json:"branchPattern"`
	Environment     string   `json:"environment"`
	TargetServerIDs []string `json:"targetServerIds"`
}

// toTriggerDTO 把领域 Config 转为契约 DTO(密钥仅掩码)。
func toTriggerDTO(c *trigger.Config) triggerDTO {
	mappings := make([]branchMappingDTO, 0, len(c.BranchMappings))
	for _, m := range c.BranchMappings {
		ids := m.TargetServerIDs
		if ids == nil {
			ids = []string{}
		}
		mappings = append(mappings, branchMappingDTO{
			ID:              m.ID,
			BranchPattern:   m.BranchPattern,
			Environment:     m.Environment,
			TargetServerIDs: ids,
		})
	}
	filters := c.PathFilters
	if filters == nil {
		filters = []string{}
	}
	return triggerDTO{
		WebhookURL:          webhookURLPrefix + c.WebhookToken,
		WebhookSecretMasked: c.WebhookSecretMasked,
		Events: eventsDTO{
			Push:        c.Events.Push,
			Tag:         c.Events.Tag,
			PullRequest: c.Events.PullRequest,
			Release:     c.Events.Release,
		},
		BranchMappings:  mappings,
		UnmatchedPolicy: c.UnmatchedPolicy,
		PathFilters:     filters,
	}
}

// writeTriggerError 把领域错误映射为契约错误码/状态码;绝不回显密钥明文/密文/栈。
func writeTriggerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, trigger.ErrVaultUnconfigured):
		writeError(w, http.StatusUnprocessableEntity, "vault_unconfigured", "保险库未配置 master key,无法生成或读取 webhook 签名密钥")
	case errors.Is(err, trigger.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, trigger.ErrNotFound):
		writeError(w, http.StatusNotFound, "trigger_not_found", "触发配置不存在")
	case errors.Is(err, trigger.ErrInvalidBranchPattern):
		writeError(w, http.StatusUnprocessableEntity, "invalid_branch_pattern", "分支模式不能为空且需为合法通配(如 main、release/*)")
	case errors.Is(err, trigger.ErrInvalidPolicy):
		writeError(w, http.StatusUnprocessableEntity, "invalid_unmatched_policy", "未匹配策略只能为 record 或 ignore")
	case errors.Is(err, trigger.ErrInvalidPathFilter):
		writeError(w, http.StatusUnprocessableEntity, "invalid_path_filter", "路径过滤 glob 不能含空白(如 backend/**、*.go)")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeGetTriggerHandler 返回 GET /api/projects/{id}/trigger handler。
// 首次访问无配置 → 惰性生成默认(token + 加密签名密钥)并返回;密钥仅掩码。
func makeGetTriggerHandler(svc trigger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "触发配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		cfg, err := svc.Get(r.Context(), id)
		if err != nil {
			writeTriggerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toTriggerDTO(cfg))
	}
}

// makeSaveTriggerHandler 返回 PUT /api/projects/{id}/trigger handler。
// 保存 events / 分支映射 / 未匹配策略;不触碰密钥。校验失败 → 422 定位到项。
func makeSaveTriggerHandler(svc trigger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "触发配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Events struct {
				Push        bool `json:"push"`
				Tag         bool `json:"tag"`
				PullRequest bool `json:"pullRequest"`
				Release     bool `json:"release"`
			} `json:"events"`
			BranchMappings []struct {
				ID              string   `json:"id"`
				BranchPattern   string   `json:"branchPattern"`
				Environment     string   `json:"environment"`
				TargetServerIDs []string `json:"targetServerIds"`
			} `json:"branchMappings"`
			UnmatchedPolicy string   `json:"unmatchedPolicy"`
			PathFilters     []string `json:"pathFilters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		mappings := make([]trigger.BranchMapping, 0, len(req.BranchMappings))
		for _, m := range req.BranchMappings {
			mappings = append(mappings, trigger.BranchMapping{
				ID:              m.ID,
				BranchPattern:   m.BranchPattern,
				Environment:     m.Environment,
				TargetServerIDs: m.TargetServerIDs,
			})
		}
		cfg, err := svc.Save(r.Context(), id, trigger.SaveInput{
			Events: trigger.Events{
				Push:        req.Events.Push,
				Tag:         req.Events.Tag,
				PullRequest: req.Events.PullRequest,
				Release:     req.Events.Release,
			},
			BranchMappings:  mappings,
			UnmatchedPolicy: req.UnmatchedPolicy,
			PathFilters:     req.PathFilters,
		})
		if err != nil {
			writeTriggerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toTriggerDTO(cfg))
	}
}

// makeResetTriggerSecretHandler 返回 POST /api/projects/{id}/trigger/secret/reset handler。
// 重新生成并加密签名密钥(旧值失效),完整明文仅在此响应里一次性回显(供粘贴 Gitee)。
// 成功后追加 trigger_secret_reset 审计;detail **绝不含密钥明文/密文**,仅记掩码。
func makeResetTriggerSecretHandler(svc trigger.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "触发配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		res, err := svc.ResetSecret(r.Context(), id)
		if err != nil {
			writeTriggerError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionTriggerSecretReset,
			TargetType: audit.TargetTrigger,
			TargetID:   id,
			// detail 仅记掩码(已脱敏值);即便 Masker 也会兜底,绝不写明文密钥。
			Detail: map[string]any{"webhookSecretMasked": res.Masked},
			IP:     clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]string{
			"webhookSecret":       res.Secret, // 完整明文,仅此一次
			"webhookSecretMasked": res.Masked,
		})
	}
}

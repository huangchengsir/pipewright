package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/previewenv"
)

// previewenv.go 装配「Per-PR 预览环境」(R4 E4.1)的 HTTP API:
//   - GET    /api/preview-envs?projectId=          列预览环境
//   - POST   /api/preview-envs/{id}/reclaim        手动回收某预览环境(删路由 + 标记)
//   - GET    /api/projects/{id}/preview-config      取项目预览配置
//   - PUT    /api/projects/{id}/preview-config      设项目预览配置(开关 + DNS 提供商 + 根域)
//
// 写操作过 auth + CSRF + 审计;DNS token 全程走 R3 vault 路径,DTO/审计/日志绝无密钥。

const (
	auditActionPreviewEnvReclaim   = "preview.env.reclaim"
	auditActionPreviewConfigUpdate = "preview.config.update"
	auditTargetPreviewEnv          = "preview_env"
	auditTargetPreviewConfig       = "preview_config"
)

// PreviewService 抽象本 httpapi 消费的预览环境能力(便于测试注入;由 previewenv.New 实现)。
type PreviewService interface {
	List(ctx context.Context, projectID string) ([]previewenv.PreviewEnv, error)
	Get(ctx context.Context, id string) (*previewenv.PreviewEnv, error)
	GetConfig(ctx context.Context, projectID string) (*previewenv.Config, error)
	SetConfig(ctx context.Context, in previewenv.Config) (*previewenv.Config, error)
	Reclaim(ctx context.Context, projectID string, prNumber int) error
}

// previewEnvDTO 是预览环境对外响应体(冻结契约;camelCase)。
type previewEnvDTO struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	PipelineID  string `json:"pipelineId"`
	PRNumber    int    `json:"prNumber"`
	Branch      string `json:"branch"`
	ServerID    string `json:"serverId"`
	Subdomain   string `json:"subdomain"`
	RouteID     string `json:"routeId"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	ReclaimedAt string `json:"reclaimedAt"`
}

func toPreviewEnvDTO(e previewenv.PreviewEnv) previewEnvDTO {
	reclaimed := ""
	if e.ReclaimedAt != nil {
		reclaimed = e.ReclaimedAt.UTC().Format(time.RFC3339)
	}
	return previewEnvDTO{
		ID:          e.ID,
		ProjectID:   e.ProjectID,
		PipelineID:  e.PipelineID,
		PRNumber:    e.PRNumber,
		Branch:      e.Branch,
		ServerID:    e.ServerID,
		Subdomain:   e.Subdomain,
		RouteID:     e.RouteID,
		Status:      e.Status,
		CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
		ReclaimedAt: reclaimed,
	}
}

// previewConfigDTO 是项目预览配置对外响应体(冻结契约;camelCase)。
type previewConfigDTO struct {
	ProjectID     string `json:"projectId"`
	Enabled       bool   `json:"enabled"`
	DNSProviderID string `json:"dnsProviderId"`
	BaseDomain    string `json:"baseDomain"`
}

func toPreviewConfigDTO(c previewenv.Config) previewConfigDTO {
	return previewConfigDTO{
		ProjectID:     c.ProjectID,
		Enabled:       c.Enabled,
		DNSProviderID: c.DNSProviderID,
		BaseDomain:    c.BaseDomain,
	}
}

// writePreviewError 把预览环境领域错误映射为契约错误码/状态码。
func writePreviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, previewenv.ErrNotFound):
		writeError(w, http.StatusNotFound, "preview_env_not_found", "预览环境不存在")
	case errors.Is(err, previewenv.ErrInvalidProject):
		writeError(w, http.StatusBadRequest, "invalid_preview_config", "请指定项目")
	case errors.Is(err, previewenv.ErrInvalidPR):
		writeError(w, http.StatusBadRequest, "invalid_preview", "PR 号非法")
	case errors.Is(err, previewenv.ErrConfigMissingProvider):
		writeError(w, http.StatusBadRequest, "invalid_preview_config", "开启 PR 预览需选择一个 DNS 提供商")
	case errors.Is(err, previewenv.ErrConfigInvalidBaseDomain):
		writeError(w, http.StatusBadRequest, "invalid_preview_config", "根域格式非法,请填写有效域名(如 preview.example.com)")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListPreviewEnvsHandler 返回 GET /api/preview-envs?projectId= → { items: [...] }(只读)。
func makeListPreviewEnvsHandler(svc PreviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "预览环境服务未初始化")
			return
		}
		projectID := strings.TrimSpace(r.URL.Query().Get("projectId"))
		envs, err := svc.List(r.Context(), projectID)
		if err != nil {
			writePreviewError(w, err)
			return
		}
		out := make([]previewEnvDTO, 0, len(envs))
		for _, e := range envs {
			out = append(out, toPreviewEnvDTO(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// makeReclaimPreviewEnvHandler 返回 POST /api/preview-envs/{id}/reclaim(auth + CSRF)→ { ok: true }。
func makeReclaimPreviewEnvHandler(svc PreviewService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "预览环境服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		env, err := svc.Get(r.Context(), id)
		if err != nil {
			writePreviewError(w, err)
			return
		}
		if err := svc.Reclaim(r.Context(), env.ProjectID, env.PRNumber); err != nil {
			writePreviewError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionPreviewEnvReclaim,
			TargetType: auditTargetPreviewEnv,
			TargetID:   env.ID,
			Detail:     map[string]any{"projectId": env.ProjectID, "prNumber": env.PRNumber, "subdomain": env.Subdomain},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// makeGetPreviewConfigHandler 返回 GET /api/projects/{id}/preview-config → Config DTO(只读)。
func makeGetPreviewConfigHandler(svc PreviewService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "预览环境服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		cfg, err := svc.GetConfig(r.Context(), projectID)
		if err != nil {
			writePreviewError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPreviewConfigDTO(*cfg))
	}
}

// makeSetPreviewConfigHandler 返回 PUT /api/projects/{id}/preview-config(auth + CSRF + 审计)→ Config DTO。
func makeSetPreviewConfigHandler(svc PreviewService, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "预览环境服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var in struct {
			Enabled       bool   `json:"enabled"`
			DNSProviderID string `json:"dnsProviderId"`
			BaseDomain    string `json:"baseDomain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.SetConfig(r.Context(), previewenv.Config{
			ProjectID:     projectID,
			Enabled:       in.Enabled,
			DNSProviderID: in.DNSProviderID,
			BaseDomain:    in.BaseDomain,
		})
		if err != nil {
			writePreviewError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionPreviewConfigUpdate,
			TargetType: auditTargetPreviewConfig,
			TargetID:   cfg.ProjectID,
			Detail:     map[string]any{"enabled": cfg.Enabled, "dnsProviderId": cfg.DNSProviderID, "baseDomain": cfg.BaseDomain},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, toPreviewConfigDTO(*cfg))
	}
}

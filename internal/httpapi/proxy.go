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
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 审计 action / target(自动 HTTPS 反代路由写操作)。复用既有 audit.Recorder;detail 绝无敏感信息。
const (
	auditActionProxyRouteCreate  = "proxy.route.create"
	auditActionProxyRouteDelete  = "proxy.route.delete"
	auditActionProxyRouteEnable  = "proxy.route.enable"
	auditActionProxyRouteDisable = "proxy.route.disable"
	auditTargetProxyRoute        = "proxy_route"
)

// proxyRouteDTO 是反代路由对外响应体(冻结契约;camelCase;与前端约定字段名严格一致)。
type proxyRouteDTO struct {
	ID                string `json:"id"`
	ServerID          string `json:"serverId"`
	Domain            string `json:"domain"`
	UpstreamContainer string `json:"upstreamContainer"`
	UpstreamPort      int    `json:"upstreamPort"`
	TLSMode           string `json:"tlsMode"`
	Enabled           bool   `json:"enabled"`
	CertStatus        string `json:"certStatus"`
	CertDetail        string `json:"certDetail"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// toProxyRouteDTO 把领域 Route 转为契约 DTO。
func toProxyRouteDTO(r proxy.Route) proxyRouteDTO {
	return proxyRouteDTO{
		ID:                r.ID,
		ServerID:          r.ServerID,
		Domain:            r.Domain,
		UpstreamContainer: r.UpstreamContainer,
		UpstreamPort:      r.UpstreamPort,
		TLSMode:           r.TLSMode,
		Enabled:           r.Enabled,
		CertStatus:        r.CertStatus,
		CertDetail:        r.CertDetail,
		CreatedAt:         r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:         r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeProxyError 把领域错误映射为契约错误码/状态码;绝不回显明文/栈。
// 编排类失败(端口冲突 / Caddy 起不来 / apply 失败 / SSH 不可达)→ 502/503 + 人话;
// 校验类 → 400/409/422;不存在 → 404。
func writeProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxy.ErrNotFound):
		writeError(w, http.StatusNotFound, "route_not_found", "反代路由不存在")
	case errors.Is(err, proxy.ErrDomainTaken):
		writeError(w, http.StatusConflict, "domain_taken", "该域名已被其它反代路由占用")
	case errors.Is(err, proxy.ErrInvalidDomain):
		writeError(w, http.StatusBadRequest, "invalid_route", "域名格式非法,请填写有效的 FQDN(如 app.example.com)")
	case errors.Is(err, proxy.ErrEmptyServerID):
		writeError(w, http.StatusBadRequest, "invalid_route", "请选择目标主机")
	case errors.Is(err, proxy.ErrEmptyContainer):
		writeError(w, http.StatusBadRequest, "invalid_route", "请填写上游容器名")
	case errors.Is(err, proxy.ErrInvalidContainer):
		writeError(w, http.StatusBadRequest, "invalid_route", "上游容器名含非法字符")
	case errors.Is(err, proxy.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "invalid_route", "上游端口必须在 1..65535 之间")
	case errors.Is(err, proxy.ErrPortConflict):
		writeError(w, http.StatusConflict, "port_conflict", "目标主机 80/443 端口被占用,无法启动反代(Let's Encrypt 校验需要 80)")
	case errors.Is(err, proxy.ErrCaddyStart):
		writeError(w, http.StatusBadGateway, "caddy_start_failed", "启动反代容器失败,请确认目标主机已安装 docker 且有权限")
	case errors.Is(err, proxy.ErrUpstreamConnect):
		writeError(w, http.StatusBadGateway, "upstream_connect_failed", "上游容器接入反代网络失败,请确认容器名正确且正在运行")
	case errors.Is(err, proxy.ErrApply):
		writeError(w, http.StatusBadGateway, "apply_failed", "下发反代配置失败")
	// target(SSH)层错误:复用其语义映射。
	case errors.Is(err, target.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key,无法取 SSH 凭据")
	case errors.Is(err, target.ErrNotFound):
		writeError(w, http.StatusUnprocessableEntity, "server_not_found", "目标主机不存在")
	case errors.Is(err, target.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_error", "引用的 SSH 凭据不存在")
	case errors.Is(err, target.ErrAuth):
		writeError(w, http.StatusBadGateway, "ssh_auth_failed", "SSH 认证失败:密钥或口令无效,或无登录权限")
	case errors.Is(err, target.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusBadGateway, "ssh_unreachable", "无法连接目标主机:端口未开放、主机不可达或超时")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListProxyRoutesHandler 返回 GET /api/proxy/routes?serverId= → { items: [...] }。
func makeListProxyRoutesHandler(svc proxy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
		routes, err := svc.List(r.Context(), serverID)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		out := make([]proxyRouteDTO, 0, len(routes))
		for _, rt := range routes {
			out = append(out, toProxyRouteDTO(rt))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// makeCreateProxyRouteHandler 返回 POST /api/proxy/routes(认证 + CSRF;写)→ Route(201)。
func makeCreateProxyRouteHandler(svc proxy.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var in struct {
			ServerID          string `json:"serverId"`
			Domain            string `json:"domain"`
			UpstreamContainer string `json:"upstreamContainer"`
			UpstreamPort      int    `json:"upstreamPort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		route, err := svc.Create(r.Context(), proxy.CreateInput{
			ServerID:          in.ServerID,
			Domain:            in.Domain,
			UpstreamContainer: in.UpstreamContainer,
			UpstreamPort:      in.UpstreamPort,
		})
		if err != nil {
			writeProxyError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionProxyRouteCreate,
			TargetType: auditTargetProxyRoute,
			TargetID:   route.ID,
			Detail:     map[string]any{"serverId": route.ServerID, "domain": route.Domain, "upstreamContainer": route.UpstreamContainer, "upstreamPort": route.UpstreamPort},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toProxyRouteDTO(*route))
	}
}

// makeSetProxyRouteEnabledHandler 返回 POST /api/proxy/routes/{id}/enabled(认证 + CSRF)→ Route。
func makeSetProxyRouteEnabledHandler(svc proxy.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<12)
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		if err := svc.SetEnabled(r.Context(), id, in.Enabled); err != nil {
			writeProxyError(w, err)
			return
		}
		routes, err := svc.List(r.Context(), "")
		if err != nil {
			writeProxyError(w, err)
			return
		}
		action := auditActionProxyRouteEnable
		if !in.Enabled {
			action = auditActionProxyRouteDisable
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     action,
			TargetType: auditTargetProxyRoute,
			TargetID:   id,
			Detail:     map[string]any{"enabled": in.Enabled},
			IP:         clientIP(r),
		})
		for _, rt := range routes {
			if rt.ID == id {
				writeJSON(w, http.StatusOK, toProxyRouteDTO(rt))
				return
			}
		}
		// 理论上 SetEnabled 成功后必能查到;查不到按不存在处理。
		writeError(w, http.StatusNotFound, "route_not_found", "反代路由不存在")
	}
}

// makeRefreshProxyRouteHandler 返回 POST /api/proxy/routes/{id}/refresh(认证 + CSRF)→ Route。
// 主动 TLS 握手探测回写 cert_status(只读探测,不改路由本身,故不记审计)。
func makeRefreshProxyRouteHandler(svc proxy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		route, err := svc.RefreshStatus(r.Context(), id)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toProxyRouteDTO(*route))
	}
}

// makeDeleteProxyRouteHandler 返回 DELETE /api/proxy/routes/{id}(认证 + CSRF)→ { ok: true }。
func makeDeleteProxyRouteHandler(svc proxy.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeProxyError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionProxyRouteDelete,
			TargetType: auditTargetProxyRoute,
			TargetID:   id,
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

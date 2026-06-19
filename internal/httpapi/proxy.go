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
	auditActionProxyRouteUpdate  = "proxy.route.update"
	auditTargetProxyRoute        = "proxy_route"

	// 反代环境(pipewright-caddy 容器)移除审计。detail 仅 serverId,无敏感信息。
	auditActionProxyCaddyRemove = "proxy.caddy.remove"
	auditTargetProxyCaddy       = "proxy_caddy"
)

// proxyRedirectDTO 是单条重定向规则对外响应体(camelCase;冻结契约)。
type proxyRedirectDTO struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"`
}

// proxyPathRuleDTO 是单条路径路由规则对外响应体(R3 E3.5;camelCase;冻结契约)。
type proxyPathRuleDTO struct {
	Path              string `json:"path"`
	UpstreamContainer string `json:"upstreamContainer"`
	UpstreamPort      int    `json:"upstreamPort"`
}

// proxyRouteConfigDTO 是路由高级配置对外响应体(R2/R3;冻结契约)。
// 绝不外泄 bcrypt 哈希/明文口令:仅以 basicAuthEnabled 布尔告知前端「是否已设认证」。
// R3:dnsProviderId(DNS-01 引用,绝不外泄 token)+ pathRules(路径路由)。
type proxyRouteConfigDTO struct {
	Aliases          []string           `json:"aliases"`
	ForceHTTPS       bool               `json:"forceHttps"`
	HSTS             bool               `json:"hsts"`
	SecurityHeaders  bool               `json:"securityHeaders"`
	Compression      bool               `json:"compression"`
	BasicAuthUser    string             `json:"basicAuthUser"`
	BasicAuthEnabled bool               `json:"basicAuthEnabled"`
	IPAllow          []string           `json:"ipAllow"`
	IPDeny           []string           `json:"ipDeny"`
	Redirects        []proxyRedirectDTO `json:"redirects"`
	DNSProviderID    string             `json:"dnsProviderId"`
	PathRules        []proxyPathRuleDTO `json:"pathRules"`
	// R4:多上游负载均衡 / 协议(gRPC h2c、WS、TCP 透传)。
	Upstreams      []proxyUpstreamDTO `json:"upstreams"`
	LBPolicy       string             `json:"lbPolicy"`
	HealthURI      string             `json:"healthUri"`
	HealthInterval string             `json:"healthInterval"`
	WebSocket      bool               `json:"websocket"`
	GRPC           bool               `json:"grpc"`
	TCPPassthrough *proxyTCPDTO       `json:"tcpPassthrough"`
}

// proxyUpstreamDTO 是额外上游(R4 负载均衡;主上游之外的后端)。
type proxyUpstreamDTO struct {
	Container string `json:"container"`
	Port      int    `json:"port"`
}

// proxyTCPDTO 是 TCP 透传配置(R4;caddy-l4 layer4)。
type proxyTCPDTO struct {
	ListenPort        int    `json:"listenPort"`
	UpstreamContainer string `json:"upstreamContainer"`
	UpstreamPort      int    `json:"upstreamPort"`
}

// proxyRouteDTO 是反代路由对外响应体(冻结契约;camelCase;与前端约定字段名严格一致)。
type proxyRouteDTO struct {
	ID                string              `json:"id"`
	ServerID          string              `json:"serverId"`
	Domain            string              `json:"domain"`
	UpstreamContainer string              `json:"upstreamContainer"`
	UpstreamPort      int                 `json:"upstreamPort"`
	TLSMode           string              `json:"tlsMode"`
	Enabled           bool                `json:"enabled"`
	CertStatus        string              `json:"certStatus"`
	CertDetail        string              `json:"certDetail"`
	Config            proxyRouteConfigDTO `json:"config"`
	CreatedAt         string              `json:"createdAt"`
	UpdatedAt         string              `json:"updatedAt"`
}

// toProxyRouteConfigDTO 把领域 RouteConfig 转为契约 DTO(剥离 bcrypt 哈希;空切片归一化为 []())。
func toProxyRouteConfigDTO(c proxy.RouteConfig) proxyRouteConfigDTO {
	reds := make([]proxyRedirectDTO, 0, len(c.Redirects))
	for _, rd := range c.Redirects {
		reds = append(reds, proxyRedirectDTO{From: rd.From, To: rd.To, Status: rd.Status})
	}
	prs := make([]proxyPathRuleDTO, 0, len(c.PathRules))
	for _, pr := range c.PathRules {
		prs = append(prs, proxyPathRuleDTO{Path: pr.Path, UpstreamContainer: pr.UpstreamContainer, UpstreamPort: pr.UpstreamPort})
	}
	ups := make([]proxyUpstreamDTO, 0, len(c.Upstreams))
	for _, u := range c.Upstreams {
		ups = append(ups, proxyUpstreamDTO{Container: u.Container, Port: u.Port})
	}
	var tcp *proxyTCPDTO
	if c.TCPPassthrough != nil {
		tcp = &proxyTCPDTO{ListenPort: c.TCPPassthrough.ListenPort, UpstreamContainer: c.TCPPassthrough.UpstreamContainer, UpstreamPort: c.TCPPassthrough.UpstreamPort}
	}
	return proxyRouteConfigDTO{
		Aliases:          orEmptySlice(c.Aliases),
		ForceHTTPS:       c.ForceHTTPS,
		HSTS:             c.HSTS,
		SecurityHeaders:  c.SecurityHeaders,
		Compression:      c.Compression,
		BasicAuthUser:    c.BasicAuthUser,
		BasicAuthEnabled: c.BasicAuthHash != "", // 绝不回显哈希/口令,只给「是否已设认证」
		IPAllow:          orEmptySlice(c.IPAllow),
		IPDeny:           orEmptySlice(c.IPDeny),
		Redirects:        reds,
		DNSProviderID:    c.DNSProviderID, // 仅引用 id,绝不外泄 token
		PathRules:        prs,
		Upstreams:        ups,
		LBPolicy:         c.LBPolicy,
		HealthURI:        c.HealthURI,
		HealthInterval:   c.HealthInterval,
		WebSocket:        c.WebSocket,
		GRPC:             c.GRPC,
		TCPPassthrough:   tcp,
	}
}

// orEmptySlice 把 nil 切片归一化为非 nil 空切片(JSON 输出 [] 而非 null,前端契约稳定)。
func orEmptySlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
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
		Config:            toProxyRouteConfigDTO(r.Config),
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
	case errors.Is(err, proxy.ErrInvalidAlias):
		writeError(w, http.StatusBadRequest, "invalid_route", "别名域名格式非法,请填写有效的 FQDN(如 www.example.com)")
	case errors.Is(err, proxy.ErrInvalidCIDR):
		writeError(w, http.StatusBadRequest, "invalid_route", "IP 白名单/黑名单含非法网段,请填写合法的 CIDR(如 10.0.0.0/8)或 IP")
	case errors.Is(err, proxy.ErrInvalidRedirect):
		writeError(w, http.StatusBadRequest, "invalid_route", "重定向规则非法:路径含非法字符,或状态码不在 301/302/307/308 之内")
	case errors.Is(err, proxy.ErrInvalidBasicAuthUser):
		writeError(w, http.StatusBadRequest, "invalid_route", "Basic Auth 用户名含非法字符")
	case errors.Is(err, proxy.ErrWildcardNeedsDNS):
		writeError(w, http.StatusBadRequest, "invalid_route", "通配符域名(*.example.com)必须先绑定一个 DNS 提供商(走 DNS-01 签发)")
	case errors.Is(err, proxy.ErrInvalidDNSProvider):
		writeError(w, http.StatusUnprocessableEntity, "invalid_route", "引用的 DNS 提供商不存在或不可用")
	case errors.Is(err, proxy.ErrInvalidPathRule):
		writeError(w, http.StatusBadRequest, "invalid_route", "路径路由规则非法:路径须以 / 起且仅含安全字符,上游容器/端口须合法")
	case errors.Is(err, proxy.ErrBcrypt):
		writeError(w, http.StatusInternalServerError, "internal", "口令哈希失败")
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
			DNSProviderID     string `json:"dnsProviderId"`
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
			DNSProviderID:     in.DNSProviderID,
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

// proxyUpdateBody 是 PUT /api/proxy/routes/{id} 的请求体(冻结契约)。
type proxyUpdateBody struct {
	UpstreamContainer string `json:"upstreamContainer"`
	UpstreamPort      int    `json:"upstreamPort"`
	Config            struct {
		Aliases         []string `json:"aliases"`
		ForceHTTPS      bool     `json:"forceHttps"`
		HSTS            bool     `json:"hsts"`
		SecurityHeaders bool     `json:"securityHeaders"`
		Compression     bool     `json:"compression"`
		BasicAuthUser   string   `json:"basicAuthUser"`
		IPAllow         []string `json:"ipAllow"`
		IPDeny          []string `json:"ipDeny"`
		Redirects       []struct {
			From   string `json:"from"`
			To     string `json:"to"`
			Status int    `json:"status"`
		} `json:"redirects"`
		DNSProviderID string `json:"dnsProviderId"`
		PathRules     []struct {
			Path              string `json:"path"`
			UpstreamContainer string `json:"upstreamContainer"`
			UpstreamPort      int    `json:"upstreamPort"`
		} `json:"pathRules"`
		Upstreams []struct {
			Container string `json:"container"`
			Port      int    `json:"port"`
		} `json:"upstreams"`
		LBPolicy       string `json:"lbPolicy"`
		HealthURI      string `json:"healthUri"`
		HealthInterval string `json:"healthInterval"`
		WebSocket      bool   `json:"websocket"`
		GRPC           bool   `json:"grpc"`
		TCPPassthrough *struct {
			ListenPort        int    `json:"listenPort"`
			UpstreamContainer string `json:"upstreamContainer"`
			UpstreamPort      int    `json:"upstreamPort"`
		} `json:"tcpPassthrough"`
	} `json:"config"`
	BasicAuthPassword string `json:"basicAuthPassword"`
}

// makeUpdateProxyRouteHandler 返回 PUT /api/proxy/routes/{id}(认证 + CSRF)→ Route。
// 更新上游 + 高级配置(R2:别名 / 访问控制 / 头 / 压缩 / 重定向);basicAuthPassword 非空则
// bcrypt 哈希进配置,basicAuthUser 为空则清认证。审计 detail 绝无明文口令/哈希。
func makeUpdateProxyRouteHandler(svc proxy.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var in proxyUpdateBody
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		reds := make([]proxy.Redirect, 0, len(in.Config.Redirects))
		for _, rd := range in.Config.Redirects {
			reds = append(reds, proxy.Redirect{From: rd.From, To: rd.To, Status: rd.Status})
		}
		prs := make([]proxy.PathRule, 0, len(in.Config.PathRules))
		for _, pr := range in.Config.PathRules {
			prs = append(prs, proxy.PathRule{Path: pr.Path, UpstreamContainer: pr.UpstreamContainer, UpstreamPort: pr.UpstreamPort})
		}
		ups := make([]proxy.Upstream, 0, len(in.Config.Upstreams))
		for _, u := range in.Config.Upstreams {
			ups = append(ups, proxy.Upstream{Container: u.Container, Port: u.Port})
		}
		var tcp *proxy.TCPConfig
		if in.Config.TCPPassthrough != nil {
			tcp = &proxy.TCPConfig{ListenPort: in.Config.TCPPassthrough.ListenPort, UpstreamContainer: in.Config.TCPPassthrough.UpstreamContainer, UpstreamPort: in.Config.TCPPassthrough.UpstreamPort}
		}
		route, err := svc.Update(r.Context(), id, proxy.UpdateInput{
			UpstreamContainer: in.UpstreamContainer,
			UpstreamPort:      in.UpstreamPort,
			Config: proxy.RouteConfig{
				Aliases:         in.Config.Aliases,
				ForceHTTPS:      in.Config.ForceHTTPS,
				HSTS:            in.Config.HSTS,
				SecurityHeaders: in.Config.SecurityHeaders,
				Compression:     in.Config.Compression,
				BasicAuthUser:   in.Config.BasicAuthUser,
				IPAllow:         in.Config.IPAllow,
				IPDeny:          in.Config.IPDeny,
				Redirects:       reds,
				DNSProviderID:   in.Config.DNSProviderID,
				PathRules:       prs,
				Upstreams:       ups,
				LBPolicy:        in.Config.LBPolicy,
				HealthURI:       in.Config.HealthURI,
				HealthInterval:  in.Config.HealthInterval,
				WebSocket:       in.Config.WebSocket,
				GRPC:            in.Config.GRPC,
				TCPPassthrough:  tcp,
			},
			BasicAuthPassword: in.BasicAuthPassword,
		})
		if err != nil {
			writeProxyError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionProxyRouteUpdate,
			TargetType: auditTargetProxyRoute,
			TargetID:   route.ID,
			Detail: map[string]any{
				"domain":            route.Domain,
				"upstreamContainer": route.UpstreamContainer,
				"upstreamPort":      route.UpstreamPort,
				"aliases":           route.Config.Aliases,
				"basicAuthEnabled":  route.Config.BasicAuthHash != "",
				"ipAllow":           route.Config.IPAllow,
				"ipDeny":            route.Config.IPDeny,
			},
			IP: clientIP(r),
		})
		writeJSON(w, http.StatusOK, toProxyRouteDTO(*route))
	}
}

// proxyOverviewItemDTO 是证书总览大盘单项(Route DTO + 主机展示名 serverName)。冻结契约。
type proxyOverviewItemDTO struct {
	proxyRouteDTO
	ServerName string `json:"serverName"`
}

// makeProxyOverviewHandler 返回 GET /api/proxy/overview → { items: [...] }(只读,无审计)。
// 跨全部主机聚合所有路由 + 主机展示名 + 证书状态,供跨主机证书总览大盘(E2.4)。
func makeProxyOverviewHandler(svc proxy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		items, err := svc.Overview(r.Context())
		if err != nil {
			writeProxyError(w, err)
			return
		}
		out := make([]proxyOverviewItemDTO, 0, len(items))
		for _, it := range items {
			out = append(out, proxyOverviewItemDTO{
				proxyRouteDTO: toProxyRouteDTO(it.Route),
				ServerName:    it.ServerName,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// proxyCaddyStatusDTO 是反代环境探测对外响应体(camelCase;冻结契约)。
type proxyCaddyStatusDTO struct {
	ServerID   string `json:"serverId"`
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	Image      string `json:"image"`
	Ports      string `json:"ports"`
	RouteCount int    `json:"routeCount"`
}

// makeProxyCaddyStatusHandler 返回 GET /api/proxy/caddy?serverId=<id>(认证;只读探测,无审计)。
// serverId 为空 → 400;传输/SSH 错误 → 502/503;容器不存在 → { installed:false }(非错误)。
func makeProxyCaddyStatusHandler(svc proxy.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
		if serverID == "" {
			writeError(w, http.StatusBadRequest, "invalid_route", "请选择目标主机")
			return
		}
		st, err := svc.CaddyStatus(r.Context(), serverID)
		if err != nil {
			writeProxyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, proxyCaddyStatusDTO{
			ServerID:   st.ServerID,
			Installed:  st.Installed,
			Running:    st.Running,
			Image:      st.Image,
			Ports:      st.Ports,
			RouteCount: st.RouteCount,
		})
	}
}

// makeRemoveProxyCaddyHandler 返回 DELETE /api/proxy/caddy?serverId=<id>(认证 + CSRF + 审计)→ { ok: true }。
// 停止并删除 pipewright-caddy 容器(保留证书卷);幂等。serverId 为空 → 400。
func makeRemoveProxyCaddyHandler(svc proxy.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "反代服务未初始化")
			return
		}
		serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
		if serverID == "" {
			writeError(w, http.StatusBadRequest, "invalid_route", "请选择目标主机")
			return
		}
		if err := svc.RemoveCaddy(r.Context(), serverID); err != nil {
			writeProxyError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionProxyCaddyRemove,
			TargetType: auditTargetProxyCaddy,
			TargetID:   serverID,
			Detail:     map[string]any{"serverId": serverID},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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

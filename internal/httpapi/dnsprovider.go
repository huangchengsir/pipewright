package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/dnsprovider"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/target"
)

// 审计 action / target(DNS 提供商 + 子域名分配写操作)。detail 绝无 token / 凭据明文。
const (
	auditActionDNSProviderCreate = "dns.provider.create"
	auditActionDNSProviderDelete = "dns.provider.delete"
	auditActionDNSProviderVerify = "dns.provider.verify"
	auditActionSubdomainAlloc    = "proxy.subdomain.allocate"
	auditTargetDNSProvider       = "dns_provider"
	auditTargetProxySubdomain    = "proxy_subdomain"
)

// dnsProviderDTO 是 DNS 提供商对外响应体(冻结契约;camelCase)。绝不外泄 token:
// 仅以 credentialConfigured 布尔告知前端「是否已绑凭据」。
type dnsProviderDTO struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Name                 string `json:"name"`
	BaseDomain           string `json:"baseDomain"`
	CredentialConfigured bool   `json:"credentialConfigured"`
	CreatedAt            string `json:"createdAt"`
}

// toDNSProviderDTO 把领域 Provider 转为契约 DTO(剥离 credentialId,仅暴露「是否已绑凭据」)。
func toDNSProviderDTO(p dnsprovider.Provider) dnsProviderDTO {
	return dnsProviderDTO{
		ID:                   p.ID,
		Type:                 p.Type,
		Name:                 p.Name,
		BaseDomain:           p.BaseDomain,
		CredentialConfigured: strings.TrimSpace(p.CredentialID) != "",
		CreatedAt:            p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// writeDNSProviderError 把 DNS 提供商领域错误映射为契约错误码/状态码;绝不回显明文/token。
func writeDNSProviderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, dnsprovider.ErrNotFound):
		writeError(w, http.StatusNotFound, "dns_provider_not_found", "DNS 提供商不存在")
	case errors.Is(err, dnsprovider.ErrInvalidType):
		writeError(w, http.StatusBadRequest, "invalid_dns_provider", "DNS 提供商类型非法(仅支持 cloudflare / dnspod / alidns)")
	case errors.Is(err, dnsprovider.ErrEmptyName):
		writeError(w, http.StatusBadRequest, "invalid_dns_provider", "请填写 DNS 提供商展示名")
	case errors.Is(err, dnsprovider.ErrEmptyCredentialID):
		writeError(w, http.StatusBadRequest, "invalid_dns_provider", "请选择 API 凭据")
	case errors.Is(err, dnsprovider.ErrInvalidBaseDomain):
		writeError(w, http.StatusBadRequest, "invalid_dns_provider", "根域格式非法,请填写有效的域名(如 example.com)")
	case errors.Is(err, dnsprovider.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_error", "引用的 API 凭据不存在")
	case errors.Is(err, dnsprovider.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key,无法取 DNS 凭据")
	case errors.Is(err, dnsprovider.ErrInvalidCredential):
		writeError(w, http.StatusBadRequest, "invalid_dns_credential", "DNS 凭据格式非法(DNSPod 填 ID,Token;阿里云填 AccessKeyId,AccessKeySecret)")
	case errors.Is(err, dnsprovider.ErrProviderNotImplemented):
		writeError(w, http.StatusNotImplemented, "not_implemented", "该提供商暂未实现自动建 A 记录(DNS-01 证书签发仍可用,请手动添加解析)")
	case errors.Is(err, dnsprovider.ErrVerifyFailed):
		writeError(w, http.StatusBadGateway, "dns_verify_failed", "DNS 校验失败:凭据无效、无该域权限或 API 不可达")
	case errors.Is(err, dnsprovider.ErrEnsureRecord):
		writeError(w, http.StatusBadGateway, "dns_record_failed", "建立 A 记录失败,请确认凭据有该域 DNS 编辑权限")
	case errors.Is(err, dnsprovider.ErrAllocate):
		writeError(w, http.StatusBadGateway, "subdomain_alloc_failed", "子域名分配失败,请稍后重试")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusBadGateway, "dns_unreachable", "连接 DNS API 超时")
	default:
		// 底层(proxy 编排 / target SSH)错误复用既有 proxy 错误映射。
		writeProxyError(w, err)
	}
}

// makeListDNSProvidersHandler 返回 GET /api/dns/providers → { items: [...] }(只读,无审计)。
func makeListDNSProvidersHandler(svc dnsprovider.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "DNS 提供商服务未初始化")
			return
		}
		providers, err := svc.List(r.Context())
		if err != nil {
			writeDNSProviderError(w, err)
			return
		}
		out := make([]dnsProviderDTO, 0, len(providers))
		for _, p := range providers {
			out = append(out, toDNSProviderDTO(p))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// makeCreateDNSProviderHandler 返回 POST /api/dns/providers(认证 + CSRF)→ Provider(201)。
func makeCreateDNSProviderHandler(svc dnsprovider.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "DNS 提供商服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var in struct {
			Type         string `json:"type"`
			Name         string `json:"name"`
			CredentialID string `json:"credentialId"`
			BaseDomain   string `json:"baseDomain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		p, err := svc.Create(r.Context(), dnsprovider.CreateInput{
			Type:         in.Type,
			Name:         in.Name,
			CredentialID: in.CredentialID,
			BaseDomain:   in.BaseDomain,
		})
		if err != nil {
			writeDNSProviderError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionDNSProviderCreate,
			TargetType: auditTargetDNSProvider,
			TargetID:   p.ID,
			Detail:     map[string]any{"type": p.Type, "name": p.Name, "baseDomain": p.BaseDomain}, // 绝无 token
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toDNSProviderDTO(*p))
	}
}

// makeDeleteDNSProviderHandler 返回 DELETE /api/dns/providers/{id}(认证 + CSRF)→ { ok: true }。
func makeDeleteDNSProviderHandler(svc dnsprovider.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "DNS 提供商服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeDNSProviderError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionDNSProviderDelete,
			TargetType: auditTargetDNSProvider,
			TargetID:   id,
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// makeVerifyDNSProviderHandler 返回 POST /api/dns/providers/{id}/verify(认证 + CSRF)→ { ok: true }。
// 取该提供商凭据明文 → 经 DNS API 校验 zone 可管理(token 用完即弃,绝不外泄)。
func makeVerifyDNSProviderHandler(svc dnsprovider.Service, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "DNS 提供商服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Verify(r.Context(), id); err != nil {
			writeDNSProviderError(w, err)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionDNSProviderVerify,
			TargetType: auditTargetDNSProvider,
			TargetID:   id,
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// subdomainDeps 是瞬时子域名分配端点的依赖:DNS 服务(分配)+ 目标服务器(解析宿主机 IP)+
// 反代服务(回读新建路由 DTO)。
type subdomainDeps struct {
	dns     dnsprovider.Service
	servers target.Service
	proxy   proxy.Service
}

// makeAllocateSubdomainHandler 返回 POST /api/proxy/subdomains(认证 + CSRF)→ Route DTO(201)。
// body: { providerId, serverId, upstreamContainer, upstreamPort }。
// 流程:解析宿主机公网 IP(据 server.Host,非用户自由文本)→ AllocateSubdomain(建 A 记录 + 建 DNS-01
// 路由)→ 回读新建路由 DTO 返回。token 全程不出现在任何响应/审计。
func makeAllocateSubdomainHandler(deps subdomainDeps, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.dns == nil || deps.servers == nil || deps.proxy == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "子域名分配服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var in struct {
			ProviderID        string `json:"providerId"`
			ServerID          string `json:"serverId"`
			UpstreamContainer string `json:"upstreamContainer"`
			UpstreamPort      int    `json:"upstreamPort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		// 据 server.Host 解析宿主机公网 IPv4(A 记录指向它;非用户自由文本,杜绝任意解析注入)。
		srv, err := deps.servers.Get(r.Context(), strings.TrimSpace(in.ServerID))
		if err != nil {
			writeProxyError(w, err)
			return
		}
		hostIP, ipErr := resolveHostIPv4(r.Context(), srv.Host)
		if ipErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "host_ip_unresolved", "无法解析目标主机的公网 IPv4,请确认主机地址正确")
			return
		}

		ref, err := deps.dns.AllocateSubdomain(r.Context(), dnsprovider.AllocateInput{
			ProviderID:        in.ProviderID,
			ServerID:          in.ServerID,
			UpstreamContainer: in.UpstreamContainer,
			UpstreamPort:      in.UpstreamPort,
			HostIP:            hostIP,
		})
		if err != nil {
			writeDNSProviderError(w, err)
			return
		}

		// 回读新建路由 DTO(子域名分配响应 = 一个普通 Route DTO)。
		routes, lerr := deps.proxy.List(r.Context(), in.ServerID)
		if lerr != nil {
			writeProxyError(w, lerr)
			return
		}
		recordAudit(r.Context(), rec, audit.Entry{
			Actor:      auditActor,
			Action:     auditActionSubdomainAlloc,
			TargetType: auditTargetProxySubdomain,
			TargetID:   ref.RouteID,
			Detail:     map[string]any{"domain": ref.Domain, "providerId": ref.ProviderID, "serverId": in.ServerID}, // 绝无 token/IP-secret
			IP:         clientIP(r),
		})
		for _, rt := range routes {
			if rt.ID == ref.RouteID {
				writeJSON(w, http.StatusCreated, toProxyRouteDTO(rt))
				return
			}
		}
		// 理论上分配成功后必能查到;查不到给最小引用回退(仍 201)。
		writeJSON(w, http.StatusCreated, map[string]any{"id": ref.RouteID, "domain": ref.Domain})
	}
}

// resolveHostIPv4 把 host(IP 或域名)解析为一个公网 IPv4 字符串(A 记录指向它)。
// host 本身是 IPv4 → 直接返回;是域名 → DNS 解析取首个 IPv4。无 IPv4 → 错误。
func resolveHostIPv4(ctx context.Context, host string) (string, error) {
	host = strings.TrimSpace(host)
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
		return "", errors.New("host is not IPv4")
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return ip.To4().String(), nil
		}
	}
	return "", errors.New("no IPv4 for host")
}

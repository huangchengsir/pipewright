package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// ---- OAuth 应用配置 DTO(冻结契约;camelCase;client_secret 仅掩码,绝无明文) ----

// oauthAppDTO 是 GET /api/oauth/apps 列表项与 PUT 响应体(冻结契约)。
// clientSecret 只暴露掩码 maskedSecret + configured 标志,绝无明文。
type oauthAppDTO struct {
	Provider     string  `json:"provider"`
	ClientID     string  `json:"clientId"`
	BaseURL      string  `json:"baseUrl"`
	Enabled      bool    `json:"enabled"`
	MaskedSecret string  `json:"maskedSecret"`
	Configured   bool    `json:"configured"`
	UpdatedAt    *string `json:"updatedAt"`
}

// toOAuthAppDTO 把领域 AppConfig 转契约 DTO(secret 仅掩码;updatedAt 未设为 null)。
func toOAuthAppDTO(c oauth.AppConfig) oauthAppDTO {
	dto := oauthAppDTO{
		Provider:     c.Provider,
		ClientID:     c.ClientID,
		BaseURL:      c.BaseURL,
		Enabled:      c.Enabled,
		MaskedSecret: c.MaskedSecret,
		Configured:   c.Configured,
	}
	if c.UpdatedAt != nil {
		s := c.UpdatedAt.UTC().Format(time.RFC3339)
		dto.UpdatedAt = &s
	}
	return dto
}

// writeOAuthError 把领域错误映射为契约错误码/状态码;绝不回显 client_secret 明文/密文。
func writeOAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, oauth.ErrInvalidProvider):
		writeError(w, http.StatusUnprocessableEntity, "invalid_provider", "provider 只能为 gitee、github、gitlab 或 custom")
	case errors.Is(err, oauth.ErrBaseURLRequired):
		writeError(w, http.StatusUnprocessableEntity, "base_url_required", "自建(custom)provider 必须配置 baseUrl")
	case errors.Is(err, oauth.ErrClientIDRequired):
		writeError(w, http.StatusUnprocessableEntity, "client_id_required", "clientId 不能为空")
	case errors.Is(err, oauth.ErrClientSecretRequired):
		writeError(w, http.StatusUnprocessableEntity, "client_secret_required", "该 provider 需要配置 client secret")
	case errors.Is(err, oauth.ErrAppNotConfigured):
		writeError(w, http.StatusUnprocessableEntity, "app_not_configured", "该 provider 的 OAuth 应用未配置或未启用")
	case errors.Is(err, oauth.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key,无法加密或读取 client secret")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListOAuthAppsHandler 返回 GET /api/oauth/apps handler(认证)。
// 返回所有已配置 provider 的应用视图(secret 仅掩码)。svc 为 nil → 503。
func makeListOAuthAppsHandler(svc oauth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "OAuth 服务未初始化")
			return
		}
		apps, err := svc.ListApps(r.Context())
		if err != nil {
			writeOAuthError(w, err)
			return
		}
		out := make([]oauthAppDTO, 0, len(apps))
		for _, a := range apps {
			out = append(out, toOAuthAppDTO(a))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// makeSaveOAuthAppHandler 返回 PUT /api/oauth/apps/{provider} handler(认证 + CSRF)。
// clientSecret 只写:省略/空保留既有,非空轮换(加密)。绝不在响应回明文。校验失败 → 422 定位。
func makeSaveOAuthAppHandler(svc oauth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "OAuth 服务未初始化")
			return
		}
		provider := chi.URLParam(r, "provider")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			ClientID string `json:"clientId"`
			BaseURL  string `json:"baseUrl"`
			// ClientSecret 用指针区分「省略(nil)」与「显式空串」;两者均保留既有。
			ClientSecret *string `json:"clientSecret"`
			Enabled      bool    `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.SaveApp(r.Context(), oauth.SaveAppInput{
			Provider:     provider,
			ClientID:     req.ClientID,
			BaseURL:      req.BaseURL,
			ClientSecret: req.ClientSecret,
			Enabled:      req.Enabled,
		})
		if err != nil {
			writeOAuthError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toOAuthAppDTO(*cfg))
	}
}

// vaultCreator 抽象 OAuth 把 token 存成 git_token 凭据所需的 vault 能力(便于测试)。
type vaultCreator interface {
	Create(in vault.CreateInput) (*vault.Credential, error)
}

// makeOAuthAuthorizeHandler 返回 GET /api/oauth/{provider}/authorize handler(认证)。
// 生成绑定当前会话的 state → 302 跳 provider 授权页;redirect_uri = {请求 origin}/api/oauth/{provider}/callback。
// 该 provider 未配置/未启用 → 422(JSON);state 生成失败 → 500。
func makeOAuthAuthorizeHandler(svc oauth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "OAuth 服务未初始化")
			return
		}
		provider := chi.URLParam(r, "provider")
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		// 用会话 token 作绑定身份(state 与发起授权的会话绑死,防 CSRF)。
		state, err := svc.IssueState(sess.Token)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
			return
		}
		redirectURI := requestOrigin(r) + "/api/oauth/" + url.PathEscape(provider) + "/callback"
		authURL, err := svc.AuthorizeURL(r.Context(), provider, state, redirectURI)
		if err != nil {
			writeOAuthError(w, err)
			return
		}
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// makeOAuthCallbackHandler 返回 GET /api/oauth/{provider}/callback handler(认证;豁免 CSRF,靠 state 校验)。
// 验 state(绑会话)→ Exchange(code→token→login)→ vault.Create 存 git_token 凭据 →
// 302 跳回 /settings/vault?connected={provider}&account={login};任一步失败 → 跳回 oauth_error(人读,无 secret)。
func makeOAuthCallbackHandler(svc oauth.Service, v vaultCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider := chi.URLParam(r, "provider")
		if svc == nil {
			redirectVaultError(w, r, provider, "oauth_unavailable")
			return
		}
		sess, ok := sessionFromContext(r.Context())
		if !ok || sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		q := r.URL.Query()
		code := q.Get("code")
		state := q.Get("state")

		// state CSRF 校验:必须匹配本会话发起的 state(一次性消费),否则拒绝。
		if !svc.ConsumeState(state, sess.Token) {
			redirectVaultError(w, r, provider, "invalid_state")
			return
		}
		if code == "" {
			redirectVaultError(w, r, provider, "missing_code")
			return
		}

		redirectURI := requestOrigin(r) + "/api/oauth/" + url.PathEscape(provider) + "/callback"
		res, err := svc.Exchange(r.Context(), provider, code, redirectURI)
		if err != nil {
			redirectVaultError(w, r, provider, oauthErrorReason(err))
			return
		}

		if v == nil {
			redirectVaultError(w, r, provider, "vault_unavailable")
			return
		}
		// 把 access_token 存成 git_token 凭据(name = {provider}:{login})。token 绝不回显/日志。
		_, cerr := v.Create(vault.CreateInput{
			Name:   provider + ":" + res.Login,
			Type:   vault.TypeGitToken,
			Scope:  "global",
			Secret: res.AccessToken,
		})
		if cerr != nil {
			if errors.Is(cerr, vault.ErrVaultUnconfigured) {
				redirectVaultError(w, r, provider, "vault_unavailable")
				return
			}
			redirectVaultError(w, r, provider, "store_failed")
			return
		}

		dest := "/settings/vault?connected=" + url.QueryEscape(provider) + "&account=" + url.QueryEscape(res.Login)
		http.Redirect(w, r, dest, http.StatusFound)
	}
}

// oauthErrorReason 把领域错误映射为人读、无 secret 的跳回原因码。
func oauthErrorReason(err error) string {
	switch {
	case errors.Is(err, oauth.ErrAppNotConfigured):
		return "app_not_configured"
	case errors.Is(err, oauth.ErrVaultUnconfigured):
		return "vault_unavailable"
	case errors.Is(err, oauth.ErrInvalidProvider):
		return "invalid_provider"
	default:
		return "exchange_failed"
	}
}

// redirectVaultError 302 跳回保险库设置页并带人读、无 secret 的 oauth_error 原因码。
func redirectVaultError(w http.ResponseWriter, r *http.Request, provider, reason string) {
	dest := "/settings/vault?oauth_error=" + url.QueryEscape(reason) + "&provider=" + url.QueryEscape(provider)
	http.Redirect(w, r, dest, http.StatusFound)
}

// requestOrigin 据请求推导来源 origin(scheme://host),用于构造 OAuth redirect_uri。
// scheme 取 TLS 在场判定(直连);host 取 r.Host(信任反代设置的 Host)。绝不信 X-Forwarded-*
// 拼 redirect_uri(可被伪造致开放重定向 / 把授权码导到攻击者域)。
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

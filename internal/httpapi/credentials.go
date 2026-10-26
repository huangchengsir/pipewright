package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// credentialDTO 是凭据对外响应体(冻结契约;camelCase;无明文/无密文)。
type credentialDTO struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Type        string           `json:"type"`
	Scope       string           `json:"scope"`
	Username    string           `json:"username"`
	MaskedValue string           `json:"maskedValue"`
	LastUsedAt  *string          `json:"lastUsedAt"` // RFC3339 或 null
	CreatedAt   string           `json:"createdAt"`
	OAuth       *vault.OAuthInfo `json:"oauth,omitempty"`
}

// toDTO 把领域 Credential 转为契约 DTO。
func toDTO(c *vault.Credential) credentialDTO {
	var lastUsed *string
	if c.LastUsedAt != nil {
		s := c.LastUsedAt.UTC().Format(time.RFC3339)
		lastUsed = &s
	}
	return credentialDTO{
		ID:          c.ID,
		Name:        c.Name,
		Type:        c.Type,
		Scope:       c.Scope,
		Username:    c.Username,
		MaskedValue: c.MaskedValue,
		LastUsedAt:  lastUsed,
		CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
		OAuth:       c.OAuth,
	}
}

// writeVaultError 把领域错误映射为契约错误码/状态码;绝不回显明文/密文。
func writeVaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, configstate.ErrMaintenance):
		writeError(w, 503, "system_installing", "System installation is in progress")
	case errors.Is(err, vault.ErrOAuthReauthorize), errors.Is(err, vault.ErrOAuthIdentity):
		writeError(w, 409, "oauth_reauthorization_required", "OAuth authorization must be renewed")
	case errors.Is(err, vault.ErrOAuthUncertain):
		writeError(w, 409, "oauth_refresh_uncertain", "OAuth result is uncertain; reconnect explicitly")
	case errors.Is(err, vault.ErrOAuthBusy), errors.Is(err, vault.ErrGitLeaseRequired):
		writeError(w, 409, "oauth_credential_busy", "OAuth credential is in use")
	case errors.Is(err, vault.ErrOAuthDetachRequired):
		writeError(w, 409, "oauth_detach_required", "Confirm disconnecting OAuth before replacing its secret")
	case errors.Is(err, vault.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
	case errors.Is(err, vault.ErrNotFound):
		writeError(w, http.StatusNotFound, "credential_not_found", "凭据不存在")
	case errors.Is(err, vault.ErrConflict):
		writeError(w, http.StatusConflict, "credential_conflict", "凭据已被其他操作修改,请刷新后重试")
	case errors.Is(err, vault.ErrCredentialInUse):
		writeError(w, http.StatusConflict, "credential_in_use", "凭据正被项目或流水线配置引用,无法删除;请先解除引用")
	case errors.Is(err, vault.ErrInvalidType):
		writeError(w, http.StatusBadRequest, "invalid_credential", "凭据类型非法")
	case errors.Is(err, vault.ErrEmptySecret):
		writeError(w, http.StatusBadRequest, "invalid_credential", "secret 不能为空")
	case errors.Is(err, vault.ErrEmptyName):
		writeError(w, http.StatusBadRequest, "invalid_credential", "name 不能为空")
	case errors.Is(err, vault.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "invalid_credential", "Registry username cannot contain colons or control characters.")
	default:
		// 包含 ErrDecrypt 等内部错误:不泄漏细节。
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListCredentialsHandler 返回 GET /api/credentials handler。
func makeListCredentialsHandler(v vault.Vault) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		creds, err := v.List()
		if err != nil {
			writeVaultError(w, err)
			return
		}
		out := make([]credentialDTO, 0, len(creds))
		for i := range creds {
			out = append(out, toDTO(&creds[i]))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// makeCreateCredentialHandler 返回 POST /api/credentials handler。
// 创建成功后追加 credential_create 审计(detail 仅元数据,经 Masker 脱敏,绝无明文)。
func makeCreateCredentialHandler(v vault.Vault, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 私钥可能较大,放宽到 1MB
		var req struct {
			Name     string  `json:"name"`
			Type     string  `json:"type"`
			Scope    string  `json:"scope"`
			Username *string `json:"username"`
			Secret   string  `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		username := ""
		if req.Username != nil {
			username = *req.Username
		}
		cred, err := v.Create(vault.CreateInput{
			Name:             req.Name,
			Type:             req.Type,
			Scope:            req.Scope,
			Username:         username,
			UsernameProvided: req.Username != nil,
			Secret:           req.Secret,
		})
		if err != nil {
			writeVaultError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCredentialCreate,
			TargetType: audit.TargetCredential,
			TargetID:   cred.ID,
			Detail:     map[string]any{"name": cred.Name, "type": cred.Type, "scope": cred.Scope},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toDTO(cred))
	}
}

// makeUpdateCredentialHandler 返回 PATCH /api/credentials/{id} handler。
// 更新成功后追加 credential_update 审计(detail 仅元数据 + 是否轮换密钥;绝无明文)。
func makeUpdateCredentialHandler(v vault.Vault, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Name        *string `json:"name"`
			Scope       *string `json:"scope"`
			Username    *string `json:"username"`
			Secret      *string `json:"secret"`
			DetachOAuth bool    `json:"detachOAuth"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cred, err := v.Update(id, vault.UpdateInput{
			Name:        req.Name,
			Scope:       req.Scope,
			Username:    req.Username,
			Secret:      req.Secret,
			DetachOAuth: req.DetachOAuth,
		})
		if err != nil {
			writeVaultError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCredentialUpdate,
			TargetType: audit.TargetCredential,
			TargetID:   cred.ID,
			Detail:     map[string]any{"name": cred.Name, "type": cred.Type, "scope": cred.Scope, "rotated": req.Secret != nil},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, toDTO(cred))
	}
}

// makeRevealCredentialHandler 返回 POST /api/credentials/{id}/reveal handler。
// 解密并回传明文(仅此一处对外暴露明文);每次查看追加 credential_reveal 审计,
// 谁在何时看过哪条凭据均留痕。POST + 登录态 + CSRF(写方法路由),不做成可预取的 GET。
func makeRevealCredentialHandler(v vault.Vault, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		id := chi.URLParam(r, "id")
		secret, err := v.Reveal(id)
		if err != nil {
			writeVaultError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCredentialReveal,
			TargetType: audit.TargetCredential,
			TargetID:   id,
			IP:         clientIP(r),
		})
		// 仅回传明文,绝不进日志/诊断;detail 不含 secret。
		writeJSON(w, http.StatusOK, map[string]string{"secret": secret})
	}
}

// makeDeleteCredentialHandler 返回 DELETE /api/credentials/{id} handler。
// 删除成功后追加 credential_delete 审计。
func makeDeleteCredentialHandler(v vault.Vault, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		id := chi.URLParam(r, "id")
		if err := v.Delete(id); err != nil {
			writeVaultError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionCredentialDelete,
			TargetType: audit.TargetCredential,
			TargetID:   id,
			IP:         clientIP(r),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

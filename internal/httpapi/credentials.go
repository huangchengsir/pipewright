package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/vault"
)

// credentialDTO 是凭据对外响应体(冻结契约;camelCase;无明文/无密文)。
type credentialDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Scope       string  `json:"scope"`
	MaskedValue string  `json:"maskedValue"`
	LastUsedAt  *string `json:"lastUsedAt"` // RFC3339 或 null
	CreatedAt   string  `json:"createdAt"`
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
		MaskedValue: c.MaskedValue,
		LastUsedAt:  lastUsed,
		CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// writeVaultError 把领域错误映射为契约错误码/状态码;绝不回显明文/密文。
func writeVaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vault.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
	case errors.Is(err, vault.ErrNotFound):
		writeError(w, http.StatusNotFound, "credential_not_found", "凭据不存在")
	case errors.Is(err, vault.ErrInvalidType):
		writeError(w, http.StatusBadRequest, "invalid_credential", "凭据类型非法")
	case errors.Is(err, vault.ErrEmptySecret):
		writeError(w, http.StatusBadRequest, "invalid_credential", "secret 不能为空")
	case errors.Is(err, vault.ErrEmptyName):
		writeError(w, http.StatusBadRequest, "invalid_credential", "name 不能为空")
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
func makeCreateCredentialHandler(v vault.Vault) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 私钥可能较大,放宽到 1MB
		var req struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Scope  string `json:"scope"`
			Secret string `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cred, err := v.Create(vault.CreateInput{
			Name:   req.Name,
			Type:   req.Type,
			Scope:  req.Scope,
			Secret: req.Secret,
		})
		if err != nil {
			writeVaultError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toDTO(cred))
	}
}

// makeUpdateCredentialHandler 返回 PATCH /api/credentials/{id} handler。
func makeUpdateCredentialHandler(v vault.Vault) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v == nil {
			writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Name   *string `json:"name"`
			Scope  *string `json:"scope"`
			Secret *string `json:"secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cred, err := v.Update(id, vault.UpdateInput{
			Name:   req.Name,
			Scope:  req.Scope,
			Secret: req.Secret,
		})
		if err != nil {
			writeVaultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDTO(cred))
	}
}

// makeDeleteCredentialHandler 返回 DELETE /api/credentials/{id} handler。
func makeDeleteCredentialHandler(v vault.Vault) http.HandlerFunc {
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
		w.WriteHeader(http.StatusNoContent)
	}
}

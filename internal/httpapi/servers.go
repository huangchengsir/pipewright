package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/target"
)

// serverDTO 是服务器对外响应体(冻结契约;camelCase;无明文/无私钥/无口令)。
type serverDTO struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	User           string `json:"user"`
	CredentialID   string `json:"credentialId"`
	CredentialName string `json:"credentialName"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// toServerDTO 把领域 Server 转为契约 DTO。
func toServerDTO(s *target.Server) serverDTO {
	return serverDTO{
		ID:             s.ID,
		Name:           s.Name,
		Host:           s.Host,
		Port:           s.Port,
		User:           s.User,
		CredentialID:   s.CredentialID,
		CredentialName: s.CredentialName,
		CreatedAt:      s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// serverTestDTO 是测试连接响应体(冻结契约)。error 失败时为人读串、成功时为 null。
type serverTestDTO struct {
	OK        bool    `json:"ok"`
	LatencyMs int64   `json:"latencyMs"`
	Output    string  `json:"output"`
	Error     *string `json:"error"`
}

// writeServerError 把领域错误映射为契约错误码/状态码;绝不回显明文/私钥/口令/栈。
func writeServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, target.ErrVaultUnconfigured):
		writeError(w, http.StatusServiceUnavailable, "vault_unconfigured", "保险库未配置 master key,无法取 SSH 凭据")
	case errors.Is(err, target.ErrNotFound):
		writeError(w, http.StatusNotFound, "server_not_found", "服务器不存在")
	case errors.Is(err, target.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_error", "引用的 SSH 凭据不存在")
	case errors.Is(err, target.ErrEmptyName):
		writeError(w, http.StatusBadRequest, "invalid_server", "服务器名称不能为空")
	case errors.Is(err, target.ErrEmptyHost):
		writeError(w, http.StatusBadRequest, "invalid_server", "主机地址不能为空")
	case errors.Is(err, target.ErrEmptyUser):
		writeError(w, http.StatusBadRequest, "invalid_server", "登录用户不能为空")
	case errors.Is(err, target.ErrEmptyCredentialID):
		writeError(w, http.StatusBadRequest, "invalid_server", "请选择 SSH 凭据")
	case errors.Is(err, target.ErrInvalidPort):
		writeError(w, http.StatusBadRequest, "invalid_server", "端口必须在 1..65535 之间")
	default:
		// 内部错误:不泄漏细节。
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeListServersHandler 返回 GET /api/servers handler → { items: [...] }。
func makeListServersHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		servers, err := svc.List(r.Context())
		if err != nil {
			writeServerError(w, err)
			return
		}
		out := make([]serverDTO, 0, len(servers))
		for _, s := range servers {
			out = append(out, toServerDTO(s))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// makeGetServerHandler 返回 GET /api/servers/{id} handler。
func makeGetServerHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		s, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toServerDTO(s))
	}
}

// makeCreateServerHandler 返回 POST /api/servers handler。
func makeCreateServerHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Name         string `json:"name"`
			Host         string `json:"host"`
			Port         int    `json:"port"`
			User         string `json:"user"`
			CredentialID string `json:"credentialId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		s, err := svc.Create(r.Context(), target.CreateInput{
			Name:         req.Name,
			Host:         req.Host,
			Port:         req.Port,
			User:         req.User,
			CredentialID: req.CredentialID,
		})
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toServerDTO(s))
	}
}

// makeUpdateServerHandler 返回 PUT /api/servers/{id} handler。
func makeUpdateServerHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Name         *string `json:"name"`
			Host         *string `json:"host"`
			Port         *int    `json:"port"`
			User         *string `json:"user"`
			CredentialID *string `json:"credentialId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		s, err := svc.Update(r.Context(), id, target.UpdateInput{
			Name:         req.Name,
			Host:         req.Host,
			Port:         req.Port,
			User:         req.User,
			CredentialID: req.CredentialID,
		})
		if err != nil {
			writeServerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toServerDTO(s))
	}
}

// makeDeleteServerHandler 返回 DELETE /api/servers/{id} handler。
func makeDeleteServerHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		if err := svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeServerError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeTestServerHandler 返回 POST /api/servers/{id}/test handler。
// 经 SSH 实连跑只读探测命令;失败映射为 200 + ok=false + 人读 error(绝不含凭据明文)。
// 仅服务器/凭据不存在、保险库未配置等定位类错误才走 writeServerError(404/422/503)。
func makeTestServerHandler(svc target.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "服务器服务未初始化")
			return
		}
		res, err := svc.Test(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeServerError(w, err)
			return
		}
		var errStr *string
		if res.Err != "" {
			s := res.Err
			errStr = &s
		}
		writeJSON(w, http.StatusOK, serverTestDTO{
			OK:        res.OK,
			LatencyMs: res.LatencyMs,
			Output:    res.Output,
			Error:     errStr,
		})
	}
}

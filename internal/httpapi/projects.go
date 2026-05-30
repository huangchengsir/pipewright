package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/project"
)

// projectDTO 是项目对外响应体(冻结契约;camelCase;无明文/无密文)。
// lastRunStatus / targetServers 本期为占位(null / []),数据由后续 story 填。
type projectDTO struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	RepoURL        string   `json:"repoUrl"`
	DefaultBranch  string   `json:"defaultBranch"`
	CredentialID   string   `json:"credentialId"`
	CredentialName string   `json:"credentialName"`
	LastRunStatus  *string  `json:"lastRunStatus"`
	TargetServers  []string `json:"targetServers"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
}

// toProjectDTO 把领域 Project 转为契约 DTO。
func toProjectDTO(p *project.Project) projectDTO {
	return projectDTO{
		ID:             p.ID,
		Name:           p.Name,
		RepoURL:        p.RepoURL,
		DefaultBranch:  p.DefaultBranch,
		CredentialID:   p.CredentialID,
		CredentialName: p.CredentialName,
		LastRunStatus:  nil,        // 本期占位:尚无运行
		TargetServers:  []string{}, // 本期占位:空集合
		CreatedAt:      p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeProjectError 把领域错误映射为契约错误码/状态码;绝不回显明文/凭据/栈。
func writeProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, project.ErrVaultUnconfigured):
		writeError(w, http.StatusUnprocessableEntity, "vault_unconfigured", "保险库未配置 master key,无法校验仓库连通")
	case errors.Is(err, project.ErrCredentialError),
		errors.Is(err, project.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_error", "凭据无效或无权限,无法访问该仓库")
	case errors.Is(err, project.ErrRepoUnreachable):
		writeError(w, http.StatusUnprocessableEntity, "repo_unreachable", "仓库地址不可达,请检查地址")
	case errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, project.ErrProjectHasActiveRuns):
		writeError(w, http.StatusConflict, "project_has_active_runs", "该项目有进行中的运行,无法删除")
	case errors.Is(err, project.ErrEmptyName):
		writeError(w, http.StatusBadRequest, "invalid_project", "项目名称不能为空")
	case errors.Is(err, project.ErrEmptyRepoURL):
		writeError(w, http.StatusBadRequest, "invalid_project", "仓库地址不能为空")
	case errors.Is(err, project.ErrEmptyCredentialID):
		writeError(w, http.StatusBadRequest, "invalid_project", "请选择仓库凭据")
	default:
		// 内部错误:不泄漏细节。
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// atoiDefault 解析十进制整数;空串/非法值返回 def。
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// makeListProjectsHandler 返回 GET /api/projects handler。
func makeListProjectsHandler(svc project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "项目服务未初始化")
			return
		}
		// 可选分页参数(默认第 1 页);响应保持冻结契约:仍是 projectDTO 数组。
		page := atoiDefault(r.URL.Query().Get("page"), 1)
		pageSize := atoiDefault(r.URL.Query().Get("pageSize"), project.DefaultPageSize)
		res, err := svc.ListPaged(r.Context(), page, pageSize)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		out := make([]projectDTO, 0, len(res.Items))
		for i := range res.Items {
			out = append(out, toProjectDTO(&res.Items[i]))
		}
		// 分页元信息经响应头暴露(不破坏数组契约;前端可选读取)。
		w.Header().Set("X-Total-Count", strconv.Itoa(res.Total))
		w.Header().Set("X-Page", strconv.Itoa(res.Page))
		w.Header().Set("X-Page-Size", strconv.Itoa(res.PageSize))
		writeJSON(w, http.StatusOK, out)
	}
}

// makeCreateProjectHandler 返回 POST /api/projects handler。
func makeCreateProjectHandler(svc project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "项目服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Name          string `json:"name"`
			RepoURL       string `json:"repoUrl"`
			CredentialID  string `json:"credentialId"`
			DefaultBranch string `json:"defaultBranch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		p, err := svc.Create(r.Context(), project.CreateInput{
			Name:          req.Name,
			RepoURL:       req.RepoURL,
			CredentialID:  req.CredentialID,
			DefaultBranch: req.DefaultBranch,
		})
		if err != nil {
			writeProjectError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toProjectDTO(p))
	}
}

// makeUpdateProjectHandler 返回 PATCH /api/projects/{id} handler。
func makeUpdateProjectHandler(svc project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "项目服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			Name          *string `json:"name"`
			DefaultBranch *string `json:"defaultBranch"`
			CredentialID  *string `json:"credentialId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		p, err := svc.Update(r.Context(), id, project.UpdateInput{
			Name:          req.Name,
			DefaultBranch: req.DefaultBranch,
			CredentialID:  req.CredentialID,
		})
		if err != nil {
			writeProjectError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toProjectDTO(p))
	}
}

// makeDeleteProjectHandler 返回 DELETE /api/projects/{id} handler。
func makeDeleteProjectHandler(svc project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "项目服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeProjectError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeTestCloneHandler 返回 POST /api/projects/test-clone handler(不落库)。
func makeTestCloneHandler(svc project.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "项目服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req struct {
			RepoURL      string `json:"repoUrl"`
			CredentialID string `json:"credentialId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		res, err := svc.TestClone(r.Context(), req.RepoURL, req.CredentialID)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"defaultBranch": res.DefaultBranch,
		})
	}
}

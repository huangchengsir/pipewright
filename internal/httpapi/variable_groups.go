package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/library"
)

// variable_groups.go 暴露变量组端点(FR-8-13 复用基座 · 对标云效变量组 / GitLab CI variable groups)。
//
//	GET    /api/variable-groups       → 列变量组(secret 项仅掩码)
//	POST   /api/variable-groups       → 建变量组(name + vars;secret 只传 credentialId)
//	GET    /api/variable-groups/{id}  → 取单变量组
//	PUT    /api/variable-groups/{id}  → 覆盖变量组
//	DELETE /api/variable-groups/{id}  → 删变量组
//
// secret 变量绝无明文:只存/回 credentialId + maskedValue(复用 buildVarDTO + reqVar)。
// 写操作过 auth + CSRF + 审计(detail 仅元数据:名称 + 变量 key,绝无明文/凭据值)。

// variableGroupDTO 是变量组对外 DTO(冻结契约;camelCase;secret 仅 credentialId + maskedValue)。
type variableGroupDTO struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Vars        []buildVarDTO `json:"vars"`
	CreatedAt   string        `json:"createdAt"`
	UpdatedAt   string        `json:"updatedAt"`
}

func toVariableGroupDTO(g *library.VarGroup) variableGroupDTO {
	return variableGroupDTO{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
		Vars:        toBuildVarDTOs(g.Vars),
		CreatedAt:   g.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   g.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeVarGroupError 把变量组领域错误映射为契约错误码/状态码。
func writeVarGroupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrVarGroupNotFound):
		writeError(w, http.StatusNotFound, "variable_group_not_found", "变量组不存在")
	case errors.Is(err, library.ErrVarGroupNameTaken):
		writeError(w, http.StatusConflict, "variable_group_name_taken", "变量组名已被占用")
	case errors.Is(err, library.ErrInvalidVarGroup):
		writeError(w, http.StatusUnprocessableEntity, "invalid_variable_group", err.Error())
	case errors.Is(err, library.ErrInvalidVar):
		writeError(w, http.StatusUnprocessableEntity, "invalid_variable", "变量 key 不能为空且组内不可重复;secret 变量须指定 credentialId")
	case errors.Is(err, library.ErrCredentialNotFound):
		writeError(w, http.StatusUnprocessableEntity, "credential_not_found", "引用的保险库凭据不存在")
	case errors.Is(err, library.ErrVaultUnconfigured):
		writeError(w, http.StatusUnprocessableEntity, "vault_unconfigured", "保险库未配置 master key,无法校验 secret 变量引用")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeListVarGroupsHandler(svc library.VarGroupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "变量组服务未初始化")
			return
		}
		list, err := svc.List(r.Context())
		if err != nil {
			writeVarGroupError(w, err)
			return
		}
		out := make([]variableGroupDTO, 0, len(list))
		for i := range list {
			out = append(out, toVariableGroupDTO(&list[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"variableGroups": out})
	}
}

func makeGetVarGroupHandler(svc library.VarGroupService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "变量组服务未初始化")
			return
		}
		g, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeVarGroupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toVariableGroupDTO(g))
	}
}

// varGroupReqBody 是建/改变量组的请求体(secret 项只传 credentialId,不传明文/掩码)。
type varGroupReqBody struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Vars        []reqVar `json:"vars"`
}

func (b varGroupReqBody) toInput() library.VarGroupInput {
	return library.VarGroupInput{
		Name:        b.Name,
		Description: b.Description,
		Vars:        toDomainVars(b.Vars),
	}
}

// auditVarKeys 取请求变量 key 列表(审计 detail;绝无明文/凭据值)。
func auditVarKeys(vars []reqVar) []string {
	keys := make([]string, 0, len(vars))
	for _, v := range vars {
		keys = append(keys, v.Key)
	}
	return keys
}

func makeCreateVarGroupHandler(svc library.VarGroupService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "变量组服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req varGroupReqBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		g, err := svc.Create(r.Context(), req.toInput())
		if err != nil {
			writeVarGroupError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionVarGroupCreate,
			TargetType: audit.TargetVarGroup,
			TargetID:   g.ID,
			Detail:     map[string]any{"name": g.Name, "keys": auditVarKeys(req.Vars)},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toVariableGroupDTO(g))
	}
}

func makeUpdateVarGroupHandler(svc library.VarGroupService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "变量组服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
		var req varGroupReqBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		g, err := svc.Update(r.Context(), id, req.toInput())
		if err != nil {
			writeVarGroupError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionVarGroupUpdate,
			TargetType: audit.TargetVarGroup,
			TargetID:   g.ID,
			Detail:     map[string]any{"name": g.Name, "keys": auditVarKeys(req.Vars)},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, toVariableGroupDTO(g))
	}
}

func makeDeleteVarGroupHandler(svc library.VarGroupService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "变量组服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeVarGroupError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionVarGroupDelete,
			TargetType: audit.TargetVarGroup,
			TargetID:   id,
			IP:         clientIP(r),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

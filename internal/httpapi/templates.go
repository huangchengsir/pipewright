package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/library"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

// templates.go 暴露流水线模板端点(FR-8-13 复用基座 · 对标 Jenkins Shared Library / 云效模板)。
//
//	GET    /api/templates                              → 列模板(画廊)
//	POST   /api/templates                              → 建模板(name + spec;走 pipeline 校验)
//	GET    /api/templates/{id}                         → 取单模板(含 stages)
//	DELETE /api/templates/{id}                         → 删模板
//	POST   /api/projects/{id}/pipeline/apply-template  → 把模板 stages 应用到项目流水线(校验后落库)
//
// 模板 spec 不含任何明文 secret(凭据走保险库引用)。写操作过 auth + CSRF + 审计(detail 仅元数据)。

// pipelineTemplateSummaryDTO 是模板列表项 DTO(不含完整 spec,只元数据 + 阶段数,列表更轻)。
type pipelineTemplateSummaryDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StageCount  int    `json:"stageCount"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// pipelineTemplateDTO 是模板详情 DTO(含完整 stages,复用流水线 stageDTO 形状)。
type pipelineTemplateDTO struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Stages      []stageDTO `json:"stages"`
	CreatedAt   string     `json:"createdAt"`
	UpdatedAt   string     `json:"updatedAt"`
}

func toPipelineTemplateSummaryDTO(t library.Template) pipelineTemplateSummaryDTO {
	return pipelineTemplateSummaryDTO{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		StageCount:  len(t.Spec.Stages),
		CreatedAt:   t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toPipelineTemplateDTO(t *library.Template) pipelineTemplateDTO {
	return pipelineTemplateDTO{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Stages:      toStageDTOs(t.Spec.Stages),
		CreatedAt:   t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writeTemplateError 把模板领域错误映射为契约错误码/状态码。
func writeTemplateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrTemplateNotFound):
		writeError(w, http.StatusNotFound, "template_not_found", "模板不存在")
	case errors.Is(err, library.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, library.ErrTemplateNameTaken):
		writeError(w, http.StatusConflict, "template_name_taken", "模板名已被占用")
	case errors.Is(err, library.ErrInvalidTemplate):
		writeError(w, http.StatusUnprocessableEntity, "invalid_template", err.Error())
	// 模板应用走 pipeline.Save,可能透出 pipeline 校验错误。
	case errors.Is(err, pipeline.ErrInvalidStage):
		writeError(w, http.StatusUnprocessableEntity, "invalid_stage", "模板阶段非法(阶段名空 / kind 非枚举 / 须恰一个源阶段)")
	case errors.Is(err, pipeline.ErrInvalidJob):
		writeError(w, http.StatusUnprocessableEntity, "invalid_job", "模板任务名与类型不能为空")
	case errors.Is(err, pipeline.ErrDuplicateID):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_id", "模板阶段或任务 id 重复")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeListPipelineTemplatesHandler(svc library.TemplateService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "模板服务未初始化")
			return
		}
		list, err := svc.List(r.Context())
		if err != nil {
			writeTemplateError(w, err)
			return
		}
		out := make([]pipelineTemplateSummaryDTO, 0, len(list))
		for _, t := range list {
			out = append(out, toPipelineTemplateSummaryDTO(t))
		}
		writeJSON(w, http.StatusOK, map[string]any{"templates": out})
	}
}

func makeGetPipelineTemplateHandler(svc library.TemplateService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "模板服务未初始化")
			return
		}
		t, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeTemplateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineTemplateDTO(t))
	}
}

func makeCreatePipelineTemplateHandler(svc library.TemplateService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "模板服务未初始化")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB
		var req struct {
			Name        string     `json:"name"`
			Description string     `json:"description"`
			Stages      []reqStage `json:"stages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		t, err := svc.Create(r.Context(), library.TemplateInput{
			Name:        req.Name,
			Description: req.Description,
			Spec:        pipeline.Spec{Stages: reqStagesToDomain(req.Stages)},
		})
		if err != nil {
			writeTemplateError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionTemplateCreate,
			TargetType: audit.TargetTemplate,
			TargetID:   t.ID,
			Detail:     map[string]any{"name": t.Name, "stageCount": len(t.Spec.Stages)},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusCreated, toPipelineTemplateDTO(t))
	}
}

func makeDeletePipelineTemplateHandler(svc library.TemplateService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "模板服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		if err := svc.Delete(r.Context(), id); err != nil {
			writeTemplateError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionTemplateDelete,
			TargetType: audit.TargetTemplate,
			TargetID:   id,
			IP:         clientIP(r),
		})
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeApplyTemplateHandler 返回 POST /api/projects/{id}/pipeline/apply-template handler。
// 收 {templateId};把模板 stages 经 pipeline.Save 应用到项目流水线(再走规范化/校验/渲染)→ 回项目流水线 DTO。
func makeApplyTemplateHandler(svc library.TemplateService, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "模板服务未初始化")
			return
		}
		projectID := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var req struct {
			TemplateID string `json:"templateId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		cfg, err := svc.Apply(r.Context(), req.TemplateID, projectID)
		if err != nil {
			writeTemplateError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionTemplateApply,
			TargetType: audit.TargetProject,
			TargetID:   projectID,
			Detail:     map[string]any{"templateId": req.TemplateID},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, toPipelineDTO(cfg))
	}
}

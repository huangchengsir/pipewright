package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangjiawei/devopstool/internal/pipeline"
)

// pipelineDTO 是流水线配置对外响应体(冻结契约;camelCase)。外层形状定死,
// 后续 2-4/2-5/2-6 只在 job.config 内填强 schema、不改外层形状。
type pipelineDTO struct {
	Stages    []stageDTO `json:"stages"`
	Yaml      string     `json:"yaml"`
	Status    string     `json:"status"`
	UpdatedAt string     `json:"updatedAt"`
}

type stageDTO struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Kind string   `json:"kind"`
	Jobs []jobDTO `json:"jobs"`
}

type jobDTO struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Summary string         `json:"summary"`
	Config  map[string]any `json:"config"`
}

// toPipelineDTO 把领域 Config 转为契约 DTO(切片/map 保证非 nil,JSON 输出 [] / {})。
func toPipelineDTO(c *pipeline.Config) pipelineDTO {
	stages := make([]stageDTO, 0, len(c.Spec.Stages))
	for _, st := range c.Spec.Stages {
		jobs := make([]jobDTO, 0, len(st.Jobs))
		for _, jb := range st.Jobs {
			cfg := jb.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			jobs = append(jobs, jobDTO{
				ID:      jb.ID,
				Name:    jb.Name,
				Type:    jb.Type,
				Summary: jb.Summary,
				Config:  cfg,
			})
		}
		stages = append(stages, stageDTO{ID: st.ID, Name: st.Name, Kind: st.Kind, Jobs: jobs})
	}
	return pipelineDTO{
		Stages:    stages,
		Yaml:      c.YAML,
		Status:    c.Status,
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// writePipelineError 把领域错误映射为契约错误码/状态码。
func writePipelineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, pipeline.ErrInvalidStage):
		writeError(w, http.StatusUnprocessableEntity, "invalid_stage", "阶段名不能为空且 kind 必须为 source/build/deploy/notify/custom 之一")
	case errors.Is(err, pipeline.ErrInvalidJob):
		writeError(w, http.StatusUnprocessableEntity, "invalid_job", "任务名与类型不能为空")
	case errors.Is(err, pipeline.ErrDuplicateID):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_id", "阶段或任务 id 重复")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

// makeGetPipelineHandler 返回 GET /api/projects/{id}/pipeline handler。
// 首次访问无配置 → 惰性生成默认种子(4 阶段)并返回。
func makeGetPipelineHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "流水线配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		cfg, err := svc.Get(r.Context(), id)
		if err != nil {
			writePipelineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineDTO(cfg))
	}
}

// makeSavePipelineHandler 返回 PUT /api/projects/{id}/pipeline handler。
// 收 {stages:[...]};服务端规范化(补 id、trim)→ 校验 → 渲染 YAML → 持久化(draft)→ 回读。
// 校验失败 → 422 定位到项;请求体限 256KB。
func makeSavePipelineHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "流水线配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB
		var req struct {
			Stages []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Kind string `json:"kind"`
				Jobs []struct {
					ID      string         `json:"id"`
					Name    string         `json:"name"`
					Type    string         `json:"type"`
					Summary string         `json:"summary"`
					Config  map[string]any `json:"config"`
				} `json:"jobs"`
			} `json:"stages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		stages := make([]pipeline.Stage, 0, len(req.Stages))
		for _, st := range req.Stages {
			jobs := make([]pipeline.Job, 0, len(st.Jobs))
			for _, jb := range st.Jobs {
				jobs = append(jobs, pipeline.Job{
					ID:      jb.ID,
					Name:    jb.Name,
					Type:    jb.Type,
					Summary: jb.Summary,
					Config:  jb.Config,
				})
			}
			stages = append(stages, pipeline.Stage{
				ID:   st.ID,
				Name: st.Name,
				Kind: st.Kind,
				Jobs: jobs,
			})
		}

		cfg, err := svc.Save(r.Context(), id, pipeline.Spec{Stages: stages})
		if err != nil {
			writePipelineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineDTO(cfg))
	}
}

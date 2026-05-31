package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/pipelineyaml"
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Needs        []string `json:"needs"`
	AllowFailure bool     `json:"allowFailure"`
	When         whenDTO  `json:"when"`
	Gate         bool     `json:"gate"`
	Jobs         []jobDTO `json:"jobs"`
}

type whenDTO struct {
	Branches []string `json:"branches"`
	Events   []string `json:"events"`
}

func toWhenDTO(w pipeline.When) whenDTO {
	b, e := w.Branches, w.Events
	if b == nil {
		b = []string{}
	}
	if e == nil {
		e = []string{}
	}
	return whenDTO{Branches: b, Events: e}
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
		needs := st.Needs
		if needs == nil {
			needs = []string{}
		}
		stages = append(stages, stageDTO{ID: st.ID, Name: st.Name, Kind: st.Kind, Needs: needs, AllowFailure: st.AllowFailure, When: toWhenDTO(st.When), Gate: st.Gate, Jobs: jobs})
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
				ID           string   `json:"id"`
				Name         string   `json:"name"`
				Kind         string   `json:"kind"`
				Needs        []string `json:"needs"`
				AllowFailure bool     `json:"allowFailure"`
				When         struct {
					Branches []string `json:"branches"`
					Events   []string `json:"events"`
				} `json:"when"`
				Gate bool `json:"gate"`
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
				ID:           st.ID,
				Name:         st.Name,
				Kind:         st.Kind,
				Needs:        st.Needs,
				AllowFailure: st.AllowFailure,
				When:         pipeline.When{Branches: st.When.Branches, Events: st.When.Events},
				Gate:         st.Gate,
				Jobs:         jobs,
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

// makeImportPipelineHandler 返回 POST /api/projects/{id}/pipeline/import handler(FR-8-12)。
//
// 收 {yaml:"...", save?:bool};把 `.pipewright.yml` 文档解析+校验为 pipeline.Config:
//   - 解析失败(语法/结构/领域校验)→ 422,错误码定位(invalid_yaml / invalid_stage / ...)。
//   - save=false(默认):仅返回解析后的预览 DTO,**不落库**(让用户先在画布看效果再决定)。
//   - save=true:把解析出的 spec 经 svc.Save 持久化(走与画布 PUT 同一套规范化/校验/渲染)后回读返回。
//
// 写方法过 auth + CSRF;请求体限 256KB;错误信息不含任何 secret 明文(env 仅以引用形式出现)。
func makeImportPipelineHandler(svc pipeline.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "流水线配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<18) // 256KB
		var req struct {
			YAML string `json:"yaml"`
			Save bool   `json:"save"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}

		parsed, err := pipelineyaml.Parse([]byte(req.YAML))
		if err != nil {
			writeYAMLParseError(w, err)
			return
		}

		// 预览(默认):不落库,直接回解析后的 DTO。
		if !req.Save {
			writeJSON(w, http.StatusOK, toPipelineDTO(&parsed))
			return
		}

		// 导入并持久化:走与画布 PUT 完全一致的 Save 路径(再校验 + 渲染 + 回读 + 项目存在性)。
		cfg, err := svc.Save(r.Context(), id, parsed.Spec)
		if err != nil {
			writePipelineError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toPipelineDTO(cfg))
	}
}

// writeYAMLParseError 把 pipelineyaml.Parse 的错误映射为契约错误码(422)。
// 领域校验错误(经 %w 挂在链上)优先精确映射;否则归为 invalid_yaml(message 已脱敏、人读)。
func writeYAMLParseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrInvalidStage):
		writeError(w, http.StatusUnprocessableEntity, "invalid_stage", err.Error())
	case errors.Is(err, pipeline.ErrInvalidJob):
		writeError(w, http.StatusUnprocessableEntity, "invalid_job", err.Error())
	case errors.Is(err, pipeline.ErrDuplicateID):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_id", err.Error())
	case errors.Is(err, pipelineyaml.ErrParse):
		writeError(w, http.StatusUnprocessableEntity, "invalid_yaml", err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid_yaml", "无法解析 .pipewright.yml")
	}
}

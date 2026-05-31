package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/chain"
)

// chain.go 暴露项目流水线串联配置端点(FR-8-11):上游成功后触发下游流水线。
//
// GET /api/projects/{id}/chain  → { downstream: [{ downstreamProjectId, branch, enabled }] }
// PUT /api/projects/{id}/chain  → 同上(需 CSRF)。校验在 chain.Service。
//
// 串联本身的触发在 internal/chain 的终态钩子(run 落 success 时);此处仅配置存取。

type chainTargetDTO struct {
	DownstreamProjectID string `json:"downstreamProjectId"`
	Branch              string `json:"branch"`
	Enabled             bool   `json:"enabled"`
}

type chainDTO struct {
	Downstream []chainTargetDTO `json:"downstream"`
}

func toChainDTO(c *chain.Config) chainDTO {
	out := chainDTO{Downstream: []chainTargetDTO{}}
	for _, t := range c.Targets {
		out.Downstream = append(out.Downstream, chainTargetDTO{
			DownstreamProjectID: t.DownstreamProjectID,
			Branch:              t.Branch,
			Enabled:             t.Enabled,
		})
	}
	return out
}

func writeChainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chain.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, chain.ErrDownstreamNotFound):
		writeError(w, http.StatusUnprocessableEntity, "downstream_not_found", "下游项目不存在")
	case errors.Is(err, chain.ErrSelfChain):
		writeError(w, http.StatusUnprocessableEntity, "self_chain", "下游目标不能是上游项目自身(自环)")
	case errors.Is(err, chain.ErrTooManyTargets):
		writeError(w, http.StatusUnprocessableEntity, "too_many_targets", "下游目标数量超过上限")
	case errors.Is(err, chain.ErrDuplicateTarget):
		writeError(w, http.StatusUnprocessableEntity, "duplicate_target", "同一下游项目 + 分支不可重复配置")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeGetChainHandler(svc chain.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "流水线串联服务未初始化")
			return
		}
		cfg, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeChainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChainDTO(cfg))
	}
}

func makeSaveChainHandler(svc chain.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "流水线串联服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<15)
		var req struct {
			Downstream []chainTargetDTO `json:"downstream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		targets := make([]chain.Target, 0, len(req.Downstream))
		for _, t := range req.Downstream {
			targets = append(targets, chain.Target{
				DownstreamProjectID: t.DownstreamProjectID,
				Branch:              t.Branch,
				Enabled:             t.Enabled,
			})
		}
		cfg, err := svc.Save(r.Context(), id, targets)
		if err != nil {
			writeChainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toChainDTO(cfg))
	}
}

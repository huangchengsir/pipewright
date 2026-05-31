package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/run"
)

// concurrency.go 暴露项目级并发上限配置端点(FR-8-10 并发/队列控制)。
//
// GET /api/projects/{id}/concurrency  → { maxConcurrent }
// PUT /api/projects/{id}/concurrency  → 同上(需 CSRF)。校验在 run.ConcurrencyService。
//
// maxConcurrent 是该项目同时 running 的运行数上限;0 = 不限项目级(仅受全局
// PIPEWRIGHT_MAX_CONCURRENT 约束)。超限的运行保持 queued 排队,待该项目有空槽再调度。

type concurrencyDTO struct {
	MaxConcurrent int `json:"maxConcurrent"`
}

func toConcurrencyDTO(c *run.ConcurrencyConfig) concurrencyDTO {
	return concurrencyDTO{MaxConcurrent: c.MaxConcurrent}
}

func writeConcurrencyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, run.ErrConcProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, run.ErrInvalidConcurrency):
		writeError(w, http.StatusUnprocessableEntity, "invalid_concurrency", "并发上限非法(须为 0..64 的整数;0 表示不限项目级)")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeGetConcurrencyHandler(svc run.ConcurrencyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "并发配置服务未初始化")
			return
		}
		cfg, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeConcurrencyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toConcurrencyDTO(cfg))
	}
}

func makeSaveConcurrencyHandler(svc run.ConcurrencyService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "并发配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var req struct {
			MaxConcurrent int `json:"maxConcurrent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.Save(r.Context(), id, req.MaxConcurrent)
		if err != nil {
			writeConcurrencyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toConcurrencyDTO(cfg))
	}
}

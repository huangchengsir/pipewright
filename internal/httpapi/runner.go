package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/runner"
)

// runner.go 暴露项目「远程构建 runner」配置端点(FR-8-14 远程 runner 池续):
//
//	GET /api/projects/{id}/runner  → { runnerServerId }
//	PUT /api/projects/{id}/runner  → 同上(需 CSRF;空串 = 清,回本地构建)。
//
// 配了 runner 服务器 → 该项目构建下沉到该远程机执行;空 = 本地。校验在 runner.Service(项目/服务器存在性)。

type runnerDTO struct {
	RunnerServerID string `json:"runnerServerId"`
}

func writeRunnerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runner.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, runner.ErrServerNotFound):
		writeError(w, http.StatusUnprocessableEntity, "runner_server_not_found", "指定的 runner 服务器不存在")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeGetRunnerHandler(svc runner.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "runner 配置服务未初始化")
			return
		}
		cfg, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeRunnerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, runnerDTO{RunnerServerID: cfg.RunnerServerID})
	}
}

func makeSaveRunnerHandler(svc runner.Service, aud audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "runner 配置服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var req runnerDTO
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.Save(r.Context(), id, req.RunnerServerID)
		if err != nil {
			writeRunnerError(w, err)
			return
		}
		recordAudit(r.Context(), aud, audit.Entry{
			Actor:      auditActor,
			Action:     audit.ActionProjectUpdate,
			TargetType: audit.TargetProject,
			TargetID:   id,
			Detail:     map[string]any{"runnerServerId": cfg.RunnerServerID},
			IP:         clientIP(r),
		})
		writeJSON(w, http.StatusOK, runnerDTO{RunnerServerID: cfg.RunnerServerID})
	}
}

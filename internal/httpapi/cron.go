package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/cron"
	"github.com/huangchengsir/pipewright/internal/run"
)

// cron.go 暴露项目定时(cron)触发配置端点(Epic 8 · Story 8-6)。
//
// GET /api/projects/{id}/cron  → { expression, branch, enabled, nextRun }
// PUT /api/projects/{id}/cron  → 同上(需 CSRF)。校验在 cron.Service。
//
// nextRun 是据当前表达式算出的下一次触发时刻(启用且表达式合法时;否则空)。

type cronDTO struct {
	Expression string `json:"expression"`
	Branch     string `json:"branch"`
	Enabled    bool   `json:"enabled"`
	// NextRun 是下一次触发时刻(RFC3339;未启用/非法/无下次 → 空串)。
	NextRun string `json:"nextRun"`
}

func toCronDTO(c *cron.Config) cronDTO {
	dto := cronDTO{Expression: c.Expression, Branch: c.Branch, Enabled: c.Enabled}
	if c.Enabled && c.Expression != "" {
		if sched, err := cron.Parse(c.Expression); err == nil {
			if next := sched.Next(time.Now().UTC()); !next.IsZero() {
				dto.NextRun = next.Format(time.RFC3339)
			}
		}
	}
	return dto
}

func writeCronError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, cron.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project_not_found", "项目不存在")
	case errors.Is(err, cron.ErrInvalidExpression):
		writeError(w, http.StatusUnprocessableEntity, "invalid_cron", "cron 表达式非法(须为 5 字段:分 时 日 月 周)")
	case errors.Is(err, cron.ErrEnabledNeedsExpression):
		writeError(w, http.StatusUnprocessableEntity, "cron_needs_expression", "启用定时触发须填写 cron 表达式")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "服务器内部错误")
	}
}

func makeGetCronHandler(svc cron.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "定时触发服务未初始化")
			return
		}
		cfg, err := svc.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeCronError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCronDTO(cfg))
	}
}

func makeSaveCronHandler(svc cron.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "internal", "定时触发服务未初始化")
			return
		}
		id := chi.URLParam(r, "id")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
		var req struct {
			Expression string `json:"expression"`
			Branch     string `json:"branch"`
			Enabled    bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体格式错误")
			return
		}
		cfg, err := svc.Save(r.Context(), id, cron.SaveInput{
			Expression: req.Expression,
			Branch:     req.Branch,
			Enabled:    req.Enabled,
		})
		if err != nil {
			writeCronError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toCronDTO(cfg))
	}
}

// NewScheduledRunCreator 把 run.Service 适配为 cron.Runner(供 main 装配调度器)。
func NewScheduledRunCreator(svc run.Service) cron.Runner {
	return scheduledRunAdapter{svc: svc}
}

type scheduledRunAdapter struct {
	svc run.Service
}

// CreateScheduledRun 实现 cron.Runner:以「定时」触发类型创建运行(branch 空 → run.Create 解析默认分支)。
func (a scheduledRunAdapter) CreateScheduledRun(ctx context.Context, projectID, branch string) (string, error) {
	r, err := a.svc.Create(ctx, projectID, run.Trigger{
		Type:   run.TriggerSchedule,
		Branch: branch,
		Actor:  "cron",
	})
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

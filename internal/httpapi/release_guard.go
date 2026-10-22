package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/releaseguard"
	"github.com/huangchengsir/pipewright/internal/run"
)

func WithReleaseGuard(s *releaseguard.Service) Option { return func(o *options) { o.releaseGuard = s } }

func guardQuery(r *http.Request, allowed ...string) error {
	if len(r.URL.RawQuery) > 8192 {
		return releaseguard.ErrInvalid
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return releaseguard.ErrInvalid
	}
	keys := map[string]bool{}
	for _, k := range allowed {
		keys[k] = true
	}
	for k, v := range q {
		if !keys[k] || len(v) != 1 {
			return releaseguard.ErrInvalid
		}
	}
	return nil
}

func guardDecode(w http.ResponseWriter, r *http.Request, out any, optional bool) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		return releaseguard.ErrInvalid
	}
	if optional && len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	// Reuse the HTTP layer's strict bounded JSON walker (including duplicate
	// keys, depth, unknown fields and trailing values), without a chat API call.
	if opsJSON(raw, out) != nil {
		return releaseguard.ErrInvalid
	}
	return nil
}

func guardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, releaseguard.ErrInvalid):
		writeError(w, 400, "release_guard_invalid", "发布护航请求无效")
	case errors.Is(err, releaseguard.ErrNotFound), errors.Is(err, deployattempt.ErrNotFound), errors.Is(err, run.ErrNotFound):
		writeError(w, 404, "release_guard_not_found", "发布护航对象不存在")
	case errors.Is(err, releaseguard.ErrConflict):
		writeError(w, 409, "release_guard_conflict", "发布护航计划或状态已变化,请刷新后重试")
	case errors.Is(err, releaseguard.ErrCapacity):
		writeError(w, 429, "release_guard_capacity", "发布护航采集容量已满")
	case errors.Is(err, releaseguard.ErrUnavailable):
		writeError(w, 503, "release_guard_unavailable", "发布护航当前不可用")
	default:
		writeError(w, 500, "internal", "服务器内部错误")
	}
}

func guardAudit(r *http.Request, aud audit.Recorder, action, id, project string) {
	recordAudit(r.Context(), aud, audit.Entry{Actor: auditActor, Action: action, TargetType: "release_guard", TargetID: id, Detail: map[string]any{"projectId": project}, IP: clientIP(r)})
}

func mountReleaseGuard(r chi.Router, s *releaseguard.Service, authn auth.Authenticator, aud audit.Recorder) {
	r.Get("/runs/{id}/deployment-attempts", guardHandler(s, aud, "attempts"))
	r.Post("/projects/{projectId}/release-guard-plans", guardHandler(s, aud, "prepare"))
	r.Post("/projects/{projectId}/release-guards", guardHandler(s, aud, "start"))
	r.Get("/projects/{projectId}/release-guards/{guardId}", guardHandler(s, aud, "snapshot"))
	r.Get("/projects/{projectId}/release-guards/{guardId}/evidence", guardHandler(s, aud, "evidence"))
	r.Get("/projects/{projectId}/release-guards/{guardId}/events", guardEvents(s, authn))
	r.Post("/projects/{projectId}/release-guards/{guardId}/stop", guardHandler(s, aud, "stop"))
	r.Post("/projects/{projectId}/release-guards/{guardId}/investigate", guardHandler(s, aud, "investigate"))
	r.Post("/projects/{projectId}/release-guards/{guardId}/analysis/preview", guardHandler(s, aud, "analysis_preview"))
	r.Get("/projects/{projectId}/release-guards/{guardId}/analysis", guardHandler(s, aud, "analyses"))
	r.Post("/projects/{projectId}/release-guards/{guardId}/analysis/{analysisId}/send", guardHandler(s, aud, "analysis_send"))
	r.Post("/projects/{projectId}/release-guards/{guardId}/analysis/{analysisId}/revoke", guardHandler(s, aud, "analysis_revoke"))
	r.Delete("/projects/{projectId}/release-guards/{guardId}", guardHandler(s, aud, "delete"))
}

func guardHandler(s *releaseguard.Service, aud audit.Recorder, op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			guardError(w, releaseguard.ErrUnavailable)
			return
		}
		keys := []string{}
		if op == "evidence" {
			keys = []string{"after", "limit"}
		}
		if err := guardQuery(r, keys...); err != nil {
			guardError(w, err)
			return
		}
		project, id := chi.URLParam(r, "projectId"), chi.URLParam(r, "guardId")
		var value any
		var err error
		status := 200
		switch op {
		case "attempts":
			var items []releaseguard.AttemptView
			items, err = s.ListAttempts(r.Context(), chi.URLParam(r, "id"))
			if err == nil {
				value = map[string]any{"items": items}
			}
		case "prepare":
			var in releaseguard.PrepareInput
			err = guardDecode(w, r, &in, false)
			if err == nil {
				value, err = s.Prepare(r.Context(), project, in)
			}
			if err == nil {
				p := value.(*releaseguard.Plan)
				guardAudit(r, aud, "release_guard_prepare", p.ID, project)
			}
		case "start":
			var in releaseguard.StartInput
			err = guardDecode(w, r, &in, false)
			if err == nil {
				value, err = s.Start(r.Context(), project, in)
			}
			if err == nil {
				report := value.(*releaseguard.Report)
				guardAudit(r, aud, "release_guard_start", report.ID, project)
			}
		case "snapshot":
			value, err = s.Snapshot(r.Context(), project, id)
		case "evidence":
			var after int64
			var limit int
			after, limit, err = guardPagination(r, true)
			if err == nil {
				value, err = s.Evidence(r.Context(), project, id, after, limit)
			}
		case "stop":
			var in struct{}
			err = guardDecode(w, r, &in, true)
			if err == nil {
				value, err = s.Stop(r.Context(), project, id)
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_stop", id, project)
			}
		case "investigate":
			var in struct{}
			err = guardDecode(w, r, &in, true)
			if err == nil {
				value, err = s.Investigate(r.Context(), project, id)
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_investigate", id, project)
			}
		case "analyses":
			var items []releaseguard.Analysis
			items, err = s.Analyses(r.Context(), project, id)
			if err == nil {
				value = map[string]any{"items": items}
			}
		case "analysis_preview":
			var in releaseguard.AnalysisInput
			err = guardDecode(w, r, &in, false)
			if err == nil {
				value, err = s.PreviewAnalysis(r.Context(), project, id, in)
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_analysis_preview", value.(*releaseguard.Analysis).ID, project)
			}
		case "analysis_send":
			var in releaseguard.SendAnalysisInput
			err = guardDecode(w, r, &in, false)
			if err == nil {
				value, err = s.SendAnalysis(r.Context(), project, id, chi.URLParam(r, "analysisId"), in)
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_analysis_send", value.(*releaseguard.Analysis).ID, project)
			}
		case "analysis_revoke":
			var in struct{}
			err = guardDecode(w, r, &in, true)
			if err == nil {
				value, err = s.RevokeAnalysis(r.Context(), project, id, chi.URLParam(r, "analysisId"))
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_analysis_revoke", value.(*releaseguard.Analysis).ID, project)
			}
		case "delete":
			var in struct{}
			err = guardDecode(w, r, &in, true)
			if err == nil {
				err = s.Delete(r.Context(), project, id)
			}
			if err == nil {
				guardAudit(r, aud, "release_guard_delete", id, project)
				status = 204
			}
		default:
			err = releaseguard.ErrInvalid
		}
		if err != nil {
			guardError(w, err)
			return
		}
		if status == 204 {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, value)
	}
}

func guardPagination(r *http.Request, withLimit bool) (int64, int, error) {
	after := int64(0)
	limit := 50
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 0 {
			return 0, 0, releaseguard.ErrInvalid
		}
		after = n
	}
	if withLimit {
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 100 {
				return 0, 0, releaseguard.ErrInvalid
			}
			limit = n
		}
	}
	return after, limit, nil
}

// The event cursor reads only committed local events. Reconnection never
// reissues a collection/deployment. JSON pagination and SSE share one cursor.
func guardEvents(s *releaseguard.Service, authn auth.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			guardError(w, releaseguard.ErrUnavailable)
			return
		}
		if err := guardQuery(r, "after"); err != nil {
			guardError(w, err)
			return
		}
		project, id := chi.URLParam(r, "projectId"), chi.URLParam(r, "guardId")
		after, _, err := guardPagination(r, false)
		if err != nil {
			guardError(w, err)
			return
		}
		if r.URL.Query().Get("after") == "" && r.Header.Get("Last-Event-ID") != "" {
			after, err = strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
			if err != nil || after < 0 {
				guardError(w, releaseguard.ErrInvalid)
				return
			}
		}
		page, err := s.Events(r.Context(), project, id, after)
		if err != nil {
			guardError(w, err)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			writeJSON(w, 200, page)
			return
		}
		cookie, e := r.Cookie(cookieSession)
		if e != nil || authn == nil {
			writeError(w, 401, "unauthorized", "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		write := func(kind string, seq int64, v any) bool {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			b, e := json.Marshal(v)
			if e != nil {
				return false
			}
			if seq >= 0 {
				if _, e = fmt.Fprintf(w, "id: %d\n", seq); e != nil {
					return false
				}
			}
			if _, e = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b); e != nil {
				return false
			}
			return controller.Flush() == nil
		}
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			if _, e = authn.Verify(cookie.Value); e != nil {
				write("auth", -1, map[string]string{"code": "unauthorized"})
				return
			}
			if page.Reset {
				write("reset", page.NextCursor, page)
				return
			}
			if len(page.Items) > 0 {
				if !write("events", page.NextCursor, page) {
					return
				}
				after = page.NextCursor
			} else if !write("heartbeat", -1, map[string]int64{"cursor": after}) {
				return
			}
			if len(page.Items) < 100 {
				snapshot, snapErr := s.Snapshot(r.Context(), project, id)
				if snapErr != nil {
					return
				}
				switch snapshot.ExecutionStatus {
				case "prepared", "queued", "binding", "observing", "stopping":
				default:
					return
				}
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
				}
			}
			page, err = s.Events(r.Context(), project, id, after)
			if err != nil {
				write("error", -1, map[string]string{"code": "release_guard_unavailable"})
				return
			}
		}
	}
}

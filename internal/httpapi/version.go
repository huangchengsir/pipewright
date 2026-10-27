package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"github.com/huangchengsir/pipewright/internal/version"
	"io"
	"net/http"
	"strings"
)

func makeCheckUpdateHandler(checker *version.Checker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, checker.Check(r.Context())) }
}

type updateResult struct {
	Mode    string           `json:"mode"`
	Status  string           `json:"status"`
	From    string           `json:"from"`
	To      string           `json:"to"`
	Message string           `json:"message"`
	Command string           `json:"command,omitempty"`
	TaskID  string           `json:"taskId,omitempty"`
	Task    *selfupdate.Task `json:"task,omitempty"`
}

func manualUpdate(w http.ResponseWriter, s *selfupdate.Service) bool {
	mode, can := version.Mode(), version.CanSelfReplace()
	if s != nil {
		mode, can = s.Capabilities()
	}
	if mode == version.ModeDocker {
		writeJSON(w, 200, updateResult{Mode: "docker", Status: "manual", Command: version.DockerUpgradeCommand()})
		return true
	}
	if !can {
		writeJSON(w, 200, updateResult{Mode: "binary", Status: "manual"})
		return true
	}
	return false
}
func updateError(w http.ResponseWriter, e error) {
	code, status := "update_unavailable", http.StatusServiceUnavailable
	if errors.Is(e, selfupdate.ErrInvalid) {
		code, status = "bad_request", 400
	}
	if errors.Is(e, selfupdate.ErrConflict) {
		code, status = "update_conflict", 409
	}
	if errors.Is(e, selfupdate.ErrNotFound) {
		code, status = "not_found", 404
	}
	writeError(w, status, code, "升级任务暂不可用，请核对原任务状态")
}
func auditUpdate(r *http.Request, a audit.Recorder, t *selfupdate.Task, action string) {
	if a == nil || t == nil {
		return
	}
	actor := auditActor
	_ = a.Record(r.Context(), audit.Entry{Actor: actor, Action: action, TargetType: "selfupdate_task", TargetID: t.ID, Detail: map[string]any{"target": t.Target, "state": t.State}})
}
func decodeUpdate(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeUpdateBody(w, r, v, false)
}
func decodeUpdateBody(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		if allowEmpty && e == io.EOF {
			return true
		}
		writeError(w, 400, "bad_request", "请求格式错误")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "bad_request", "请求格式错误")
		return false
	}
	return true
}
func makeUpdateTaskStartHandler(s *selfupdate.Service, a audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manualUpdate(w, s) {
			return
		}
		if s == nil {
			updateError(w, selfupdate.ErrUnavailable)
			return
		}
		var in selfupdate.Request
		if !decodeUpdate(w, r, &in) {
			return
		}
		t, e := s.Submit(r.Context(), in)
		if e != nil {
			updateError(w, e)
			return
		}
		auditUpdate(r, a, t, "selfupdate_submit")
		writeJSON(w, 202, updateResult{Mode: "binary", Status: "accepted", From: t.From, To: t.Target, TaskID: t.ID, Task: t})
	}
}
func makeSelfUpdateHandler(c *version.Checker, s *selfupdate.Service, a audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manualUpdate(w, s) {
			return
		}
		if s == nil {
			updateError(w, selfupdate.ErrUnavailable)
			return
		}
		info := c.Check(r.Context())
		if info.CheckError != "" {
			updateError(w, selfupdate.ErrUnavailable)
			return
		}
		if !info.UpdateAvailable {
			writeJSON(w, 200, updateResult{Status: "uptodate", From: info.Current, To: info.Latest})
			return
		}
		// Empty legacy requests are permanently bound to the same instance/target task.
		sum := sha256.Sum256([]byte(info.Latest))
		in := selfupdate.Request{Target: info.Latest}
		if !decodeUpdateBody(w, r, &in, true) {
			return
		}
		if in.Target != info.Latest {
			updateError(w, selfupdate.ErrConflict)
			return
		}
		if in.RequestID == "" {
			if existing, e := s.ForTarget(r.Context(), info.Latest); e == nil {
				writeJSON(w, 202, updateResult{Mode: "binary", Status: "accepted", From: existing.From, To: existing.Target, TaskID: existing.ID, Task: existing})
				return
			}
			in.RequestID = fmt.Sprintf("legacy-%x", sum)
		}
		t, e := s.Submit(r.Context(), in)
		if e != nil {
			updateError(w, e)
			return
		}
		auditUpdate(r, a, t, "selfupdate_submit")
		writeJSON(w, 202, updateResult{Mode: "binary", Status: "accepted", From: t.From, To: t.Target, TaskID: t.ID, Task: t})
	}
}
func makeUpdateTaskGetHandler(s *selfupdate.Service, current bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			if version.Mode() == version.ModeDocker || !version.CanSelfReplace() {
				writeJSON(w, 200, nil)
			} else {
				updateError(w, selfupdate.ErrUnavailable)
			}
			return
		}
		var t *selfupdate.Task
		var e error
		if current {
			t, e = s.Current(r.Context())
		} else {
			t, e = s.Get(r.Context(), chi.URLParam(r, "taskId"))
		}
		if current && errors.Is(e, selfupdate.ErrNotFound) {
			writeJSON(w, 200, nil)
			return
		}
		if e != nil {
			updateError(w, e)
			return
		}
		writeJSON(w, 200, t)
	}
}
func makeUpdateTaskResumeHandler(s *selfupdate.Service, a audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			updateError(w, selfupdate.ErrUnavailable)
			return
		}
		var in struct {
			RequestID string `json:"requestId"`
		}
		if !decodeUpdate(w, r, &in) {
			return
		}
		t, e := s.Resume(r.Context(), chi.URLParam(r, "taskId"), in.RequestID)
		if e != nil {
			updateError(w, e)
			return
		}
		auditUpdate(r, a, t, "selfupdate_resume")
		writeJSON(w, 202, t)
	}
}
func updateAdmissionMiddleware(s *selfupdate.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			websocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.Contains(path, "/terminal")
			write := r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS"
			cleanup := updateCompletionRoute(r.Method, path)
			control := r.Method == http.MethodPost && (path == "/api/version/update" || path == "/api/version/update-tasks" || updateRouteMatches(path, "/api/version/update-tasks/_/resume"))
			if (!write && !websocket) || cleanup || control {
				next.ServeHTTP(w, r)
				return
			}
			kind := "http_work"
			if websocket {
				kind = "terminal_pty"
			}
			release, e := s.Admit(r.Context(), kind)
			if e != nil {
				writeError(w, 503, "maintenance", "系统正在安装升级，请稍后重试")
				return
			}
			defer release()
			next.ServeHTTP(w, r)
		})
	}
}

func updateRouteMatches(path, pattern string) bool {
	got, want := strings.Split(path, "/"), strings.Split(pattern, "/")
	if len(got) != len(want) {
		return false
	}
	for i, part := range want {
		if part == "_" {
			if got[i] == "" || got[i] == "." || got[i] == ".." {
				return false
			}
		} else if got[i] != part {
			return false
		}
	}
	return true
}
func updateCompletionRoute(method, path string) bool {
	if method == http.MethodPost {
		for _, pattern := range []string{
			"/api/runs/_/cancel",
			"/api/projects/_/release-guards/_/stop",
			"/api/ai/ops/sessions/_/runs/_/cancel",
			"/api/servers/_/terminal/sessions/_/input",
			"/api/servers/_/containers/_/terminal/sessions/_/input",
		} {
			if updateRouteMatches(path, pattern) {
				return true
			}
		}
	}
	if method == http.MethodDelete {
		return updateRouteMatches(path, "/api/servers/_/terminal/sessions/_") || updateRouteMatches(path, "/api/servers/_/containers/_/terminal/sessions/_")
	}
	return false
}

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
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/opschat"
)

func WithOpsChat(s *opschat.Service) Option { return func(o *options) { o.opsChat = s } }

func mountOpsChat(r chi.Router, s *opschat.Service, authn auth.Authenticator) {
	r.Route("/ai/ops", func(r chi.Router) {
		for _, route := range []struct{ method, path, name string }{
			{"GET", "/capabilities", "capabilities"}, {"GET", "/tools", "tools"},
			{"GET", "/sessions", "list"}, {"POST", "/sessions", "create"},
			{"GET", "/sessions/{id}", "snapshot"}, {"PATCH", "/sessions/{id}", "patch"}, {"DELETE", "/sessions/{id}", "delete"},
			{"POST", "/sessions/{id}/activate", "activate"}, {"GET", "/sessions/{id}/entries", "entries"},
			{"GET", "/sessions/{id}/calls/{callId}", "call"}, {"POST", "/sessions/{id}/turns", "turn"},
			{"POST", "/sessions/{id}/runs/{runId}/cancel", "cancel"}, {"POST", "/sessions/{id}/calls/confirm", "confirm"},
			{"POST", "/sessions/{id}/runs/{runId}/confirmation", "reissue"},
			{"GET", "/sessions/{id}/analysis/preview", "preview"}, {"POST", "/sessions/{id}/analysis", "analysis"},
			{"POST", "/sessions/{id}/retry", "retry"},
		} {
			r.MethodFunc(route.method, route.path, opsHandler(s, route.name))
		}
		r.Get("/sessions/{id}/events", opsEventsHandler(s, authn, 15*time.Second))
	})
}

func opsHandler(s *opschat.Service, operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if operation != "list" && operation != "entries" && operation != "preview" {
			if err := opsQuery(r); err != nil {
				opsError(w, err)
				return
			}
		}
		if operation == "capabilities" {
			if s == nil {
				writeJSON(w, 200, opschat.Capabilities{})
				return
			}
			writeJSON(w, 200, s.Capabilities())
			return
		}
		if operation == "tools" {
			writeJSON(w, 200, opschat.Tools())
			return
		}
		if s == nil {
			opsError(w, opschat.ErrUnavailable)
			return
		}
		id := chi.URLParam(r, "id")
		var value any
		var err error
		status := http.StatusOK
		switch operation {
		case "list", "entries":
			key := "before"
			if err = opsQuery(r, key, "limit"); err != nil {
				break
			}
			limit := 0
			if raw := r.URL.Query().Get("limit"); raw != "" {
				limit, err = strconv.Atoi(raw)
				if err != nil {
					err = opschat.ErrInvalid
					break
				}
			}
			if operation == "list" {
				value, err = s.List(r.Context(), r.URL.Query().Get(key), limit)
			} else {
				value, err = s.Entries(r.Context(), id, r.URL.Query().Get(key), limit)
			}
		case "snapshot":
			value, err = s.Snapshot(r.Context(), id)
		case "call":
			value, err = s.Call(r.Context(), id, chi.URLParam(r, "callId"))
		case "create":
			var in opschat.CreateInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.Create(r.Context(), in)
				status = http.StatusCreated
			}
		case "patch":
			var in opschat.PatchInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.Patch(r.Context(), id, in)
			}
		case "delete":
			var empty struct{}
			if err = opsDecode(w, r, &empty, true); err == nil {
				err = s.Delete(r.Context(), id)
			}
			status = http.StatusNoContent
		case "activate", "cancel", "reissue":
			var empty struct{}
			if err = opsDecode(w, r, &empty, true); err != nil {
				break
			}
			switch operation {
			case "activate":
				err = s.Activate(r.Context(), id)
				value = map[string]bool{"ok": true}
			case "cancel":
				value, err = s.Cancel(r.Context(), id, chi.URLParam(r, "runId"))
				status = http.StatusAccepted
			case "reissue":
				value, err = s.ReissueConfirmation(r.Context(), id, chi.URLParam(r, "runId"))
			}
		case "turn":
			var in opschat.TurnInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.Submit(r.Context(), id, in)
				status = http.StatusAccepted
			}
		case "retry":
			var in opschat.RetryInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.RetryFailed(r.Context(), id, in)
				status = http.StatusAccepted
			}
		case "confirm":
			var in opschat.ConfirmInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.Confirm(r.Context(), id, in)
				status = http.StatusAccepted
			}
		case "preview":
			if err = opsQuery(r, "callIds"); err == nil {
				value, err = s.AnalysisPreview(r.Context(), id, strings.Split(r.URL.Query().Get("callIds"), ","))
			}
		case "analysis":
			var in opschat.AnalysisInput
			if err = opsDecode(w, r, &in, false); err == nil {
				value, err = s.Analyze(r.Context(), id, in)
				status = http.StatusAccepted
			}
		}
		if err != nil {
			opsError(w, err)
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, value)
	}
}

func opsError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "ops_storage_failed"
	switch {
	case errors.Is(err, opschat.ErrNotFound):
		status, code = 404, "ops_not_found"
	case errors.Is(err, opschat.ErrInvalid):
		status, code = 422, "ops_invalid"
	case errors.Is(err, opschat.ErrVerifyFirst):
		status, code = 409, "ops_verify_first"
	case errors.Is(err, opschat.ErrApproval):
		status, code = 409, "ops_confirmation_changed"
	case errors.Is(err, opschat.ErrConsent):
		status, code = 409, "ops_consent_changed"
	case errors.Is(err, opschat.ErrConflict):
		status, code = 409, "ops_conflict"
	case errors.Is(err, opschat.ErrQuota):
		status, code = 429, "ops_quota"
	case errors.Is(err, opschat.ErrUnavailable), errors.Is(err, opschat.ErrDecrypt), errors.Is(err, opschat.ErrLease):
		status, code = 503, "ops_unavailable"
	case errors.Is(err, opschat.ErrReset):
		status, code = 409, "ops_reset"
	}
	writeError(w, status, code, code)
}

func opsQuery(r *http.Request, keys ...string) error {
	if len(r.URL.RawQuery) > 8192 {
		return opschat.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opschat.ErrInvalid
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k, values := range query {
		if !allowed[k] || len(values) != 1 {
			return opschat.ErrInvalid
		}
	}
	return nil
}

func opsDecode(w http.ResponseWriter, r *http.Request, out any, optional bool) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		return opschat.ErrInvalid
	}
	if optional && len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	return opsJSON(raw, out)
}

func opsJSON(raw []byte, out any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || len(raw) > 64<<10 {
		return opschat.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return opschat.ErrInvalid
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					key, ok := k.(string)
					if !ok || seen[strings.ToLower(key)] {
						return opschat.ErrInvalid
					}
					seen[strings.ToLower(key)] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e := walk(depth + 1); e != nil {
						return e
					}
				}
			default:
				return opschat.ErrInvalid
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return opschat.ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return opschat.ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return opschat.ErrInvalid
	}
	return nil
}

func opsEventsHandler(s *opschat.Service, authn auth.Authenticator, interval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s == nil {
			opsError(w, opschat.ErrUnavailable)
			return
		}
		if err := opsQuery(r, "after"); err != nil {
			opsError(w, err)
			return
		}
		id := chi.URLParam(r, "id")
		cursor := r.URL.Query().Get("after")
		if cursor == "" {
			cursor = r.Header.Get("Last-Event-ID")
		}
		wake, unsubscribe, err := s.Subscribe(r.Context(), id)
		if err != nil {
			opsError(w, err)
			return
		}
		defer unsubscribe()
		page, err := s.Events(r.Context(), id, cursor, 100)
		if err != nil && !errors.Is(err, opschat.ErrReset) {
			opsError(w, err)
			return
		}
		cookie, err := r.Cookie(cookieSession)
		if err != nil || authn == nil {
			writeError(w, 401, "unauthorized", "unauthorized")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		write := func(event, eventID string, value any) bool {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			raw, e := json.Marshal(value)
			if e != nil {
				return false
			}
			if eventID != "" {
				if _, e = fmt.Fprintf(w, "id: %s\n", eventID); e != nil {
					return false
				}
			}
			if _, e = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); e != nil {
				return false
			}
			return controller.Flush() == nil
		}
		authorized := func() bool {
			if _, e := authn.Verify(cookie.Value); e != nil {
				write("auth", "", map[string]string{"code": "unauthorized"})
				return false
			}
			return true
		}
		if !authorized() {
			return
		}
		if page.Reset {
			write("reset", "", map[string]any{"watermark": page.Watermark})
			return
		}
		if !write("ready", "", map[string]any{"watermark": page.Watermark}) {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if !authorized() {
				return
			}
			for _, entry := range page.Entries {
				cursor = opschat.EventCursor(id, entry.Seq)
				if !write("entry", cursor, entry) {
					return
				}
			}
			if len(page.Entries) < 100 {
				select {
				case <-r.Context().Done():
					return
				case _, ok := <-wake:
					if !ok {
						write("unavailable", "", map[string]string{"code": "ops_unavailable"})
						return
					}
				case <-ticker.C:
					if !authorized() {
						return
					}
					if !s.Capabilities().Available {
						write("unavailable", "", map[string]string{"code": "ops_unavailable"})
						return
					}
					if !write("heartbeat", "", map[string]any{"watermark": page.Watermark}) {
						return
					}
				}
			}
			page, err = s.Events(r.Context(), id, cursor, 100)
			if errors.Is(err, opschat.ErrReset) {
				write("reset", "", map[string]any{"watermark": page.Watermark})
				return
			}
			if err != nil {
				write("error", "", map[string]string{"code": "ops_unavailable"})
				return
			}
		}
	}
}

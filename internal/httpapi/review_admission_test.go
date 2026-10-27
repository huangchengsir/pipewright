package httpapi

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/maintenance"
	"github.com/huangchengsir/pipewright/internal/selfupdate"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func reviewAdmission(t *testing.T) (*selfupdate.Service, func(string) error) {
	t.Helper()
	st := testStoreAuth(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "exe")
	if e := os.WriteFile(exe, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := selfupdate.New(st.DB, selfupdate.Options{Executable: exe, Directory: filepath.Join(dir, "work")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s, func(id string) error { return maintenance.Fence(context.Background(), st.DB, id) }
}
func TestReviewMaintenanceAllowsOnlyExactCompletionRoutes(t *testing.T) {
	s, fence := reviewAdmission(t)
	if e := fence("install"); e != nil {
		t.Fatal(e)
	}
	allowed := []struct{ method, path string }{
		{"POST", "/api/runs/run/cancel"}, {"POST", "/api/projects/project/release-guards/guard/stop"},
		{"POST", "/api/ai/ops/sessions/session/runs/run/cancel"},
		{"POST", "/api/servers/server/terminal/sessions/session/input"}, {"DELETE", "/api/servers/server/terminal/sessions/session"},
		{"POST", "/api/servers/server/containers/container/terminal/sessions/session/input"}, {"DELETE", "/api/servers/server/containers/container/terminal/sessions/session"},
		{"GET", "/api/servers/server/terminal/sessions/session"}, {"GET", "/api/servers/server/containers/container/terminal/sessions/session"},
	}
	blocked := []struct{ method, path string }{
		{"DELETE", "/api/proxy/caddy"}, {"DELETE", "/api/proxy/routes/route"}, {"DELETE", "/api/credentials/credential"}, {"DELETE", "/api/ai/ops/sessions/session"},
		{"POST", "/api/unrelated/cancel"}, {"POST", "/api/unrelated/stop"}, {"POST", "/api/servers/server/terminal/sessions"}, {"POST", "/api/servers/server/containers/container/terminal/sessions"},
		{"DELETE", "/api/servers/server/terminal/sessions/session/extra"},
	}
	for _, set := range []struct {
		cases  []struct{ method, path string }
		status int
	}{{allowed, 204}, {blocked, 503}} {
		for _, tc := range set.cases {
			t.Run(tc.method+tc.path, func(t *testing.T) {
				called := false
				handler := updateAdmissionMiddleware(s)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
				if w.Code != set.status || called != (set.status == 204) {
					t.Fatalf("status=%d called=%v want %d", w.Code, called, set.status)
				}
			})
		}
	}
}
func TestReviewDeleteHoldsAdmissionUntilHandlerReturns(t *testing.T) {
	s, fence := reviewAdmission(t)
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := updateAdmissionMiddleware(s)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-finish; w.WriteHeader(204) }))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("DELETE", "/api/proxy/caddy", nil))
	}()
	<-entered
	e := fence("install")
	close(finish)
	<-done
	if !errors.Is(e, maintenance.ErrBlocked) {
		t.Fatalf("in-flight remote deletion did not block installation: %v", e)
	}
	if e = fence("install"); e != nil {
		t.Fatal(e)
	}
}

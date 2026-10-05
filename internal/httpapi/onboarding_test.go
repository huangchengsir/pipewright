package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/onboarding"
)

type onboardingAuth struct{ auth.Authenticator }

func (onboardingAuth) Verify(token string) (*auth.Session, error) {
	if token != "valid" {
		return nil, auth.ErrSessionNotFound
	}
	return &auth.Session{CSRFToken: "csrf"}, nil
}

type onboardingReader struct {
	calls     int
	preferred string
}

func (s *onboardingReader) Status(_ context.Context, preferred string) onboarding.Snapshot {
	s.calls++
	s.preferred = preferred
	return onboarding.New(nil, onboarding.Capabilities{}).Status(context.Background(), preferred)
}

func TestOnboardingStatusAuthenticationAndContract(t *testing.T) {
	reader := &onboardingReader{}
	handler := New(nil, onboardingAuth{}, WithOnboarding(reader))
	for _, token := range []string{"", "invalid", "valid"} {
		req := httptest.NewRequest(http.MethodGet, "/api/onboarding/status?projectId=%20preferred%20", nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if token != "valid" {
			if w.Code != http.StatusUnauthorized || reader.calls != 0 {
				t.Fatalf("unauthorized status %d calls %d", w.Code, reader.calls)
			}
			continue
		}
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || reader.preferred != "preferred" || reader.calls != 1 {
			t.Fatalf("authenticated status %d: %s", w.Code, w.Body.String())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		keys := []string{"projectsState", "projectCount", "projects", "runsState", "runCount", "selectedProject", "successState", "success", "latestRun", "pipeline", "runtime"}
		if len(body) != len(keys) {
			t.Fatalf("contract keys: %v", body)
		}
		for _, key := range keys {
			if _, ok := body[key]; !ok {
				t.Fatalf("missing %s", key)
			}
		}
		for _, key := range []string{"projectCount", "runCount", "selectedProject", "success", "latestRun"} {
			if string(body[key]) != "null" {
				t.Fatalf("unknown %s must be null: %s", key, body[key])
			}
		}
	}
}

func TestOnboardingUnavailableAndReadOnlyMethod(t *testing.T) {
	reader := &onboardingReader{}
	for _, tc := range []struct {
		method   string
		provider bool
		code     int
	}{
		{http.MethodGet, false, http.StatusServiceUnavailable},
		{http.MethodPost, true, http.StatusMethodNotAllowed},
	} {
		var opts []Option
		if tc.provider {
			opts = append(opts, WithOnboarding(reader))
		}
		req := httptest.NewRequest(tc.method, "/api/onboarding/status", nil)
		req.AddCookie(&http.Cookie{Name: cookieSession, Value: "valid"})
		req.Header.Set("X-CSRF-Token", "csrf")
		w := httptest.NewRecorder()
		New(nil, onboardingAuth{}, opts...).ServeHTTP(w, req)
		if w.Code != tc.code || reader.calls != 0 {
			t.Fatalf("%s status %d calls %d: %s", tc.method, w.Code, reader.calls, w.Body.String())
		}
	}
}

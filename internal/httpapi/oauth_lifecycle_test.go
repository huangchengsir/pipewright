package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type lifecycleAuditService struct {
	oauth.Service
	err error
}

func (s *lifecycleAuditService) BeginAuthorization(context.Context, string, string, string, string, int64, bool) (string, error) {
	return "https://github.com/login/oauth/authorize?state=synthetic-state", s.err
}
func (s *lifecycleAuditService) CompleteAuthorization(context.Context, string, string, string, string, string) (string, string, error) {
	return "credential", "alice", s.err
}

type lifecycleAuditRecorder struct {
	audit.Recorder
	entries []audit.Entry
}

func (a *lifecycleAuditRecorder) Record(_ context.Context, e audit.Entry) error {
	a.entries = append(a.entries, e)
	return nil
}

func TestOAuthLifecycleAuditContainsOnlySafeMetadata(t *testing.T) {
	svc := &lifecycleAuditService{}
	rec := &lifecycleAuditRecorder{}
	request := func(method, endpoint, body string) *http.Request {
		r := httptest.NewRequest(method, endpoint, strings.NewReader(body))
		route := chi.NewRouteContext()
		route.URLParams.Add("provider", "github")
		route.URLParams.Add("id", "credential")
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, route)
		ctx = context.WithValue(ctx, contextKeySession, &auth.Session{Token: "synthetic-session"})
		return r.WithContext(ctx)
	}
	w := httptest.NewRecorder()
	makeOAuthReconnectHandler(svc, rec)(w, request(http.MethodPost, "http://instance.test/reconnect", `{"provider":"github","revision":1}`))
	if w.Code != 200 || len(rec.entries) != 1 {
		t.Fatal("successful reconnect was not audited", w.Code)
	}
	w = httptest.NewRecorder()
	makeOAuthCallbackHandler(svc, nil, rec)(w, request(http.MethodGet, "http://instance.test/callback?code=callback-code&state=callback-state", ""))
	if w.Code != 302 || len(rec.entries) != 2 {
		t.Fatal("successful authorization was not audited", w.Code)
	}
	for _, e := range rec.entries {
		if e.TargetID != "credential" || e.TargetType != audit.TargetCredential || e.Detail["provider"] != "github" || len(e.Detail) != 2 {
			t.Fatal("OAuth audit included unexpected metadata")
		}
		b, _ := json.Marshal(e)
		for _, secret := range []string{"synthetic-session", "synthetic-state", "callback-code", "callback-state", "client_secret", "access_token", "refresh_token"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("OAuth audit exposed exchange material")
			}
		}
	}
	svc.err = vault.ErrOAuthBusy
	makeOAuthReconnectHandler(svc, rec)(httptest.NewRecorder(), request(http.MethodPost, "http://instance.test/reconnect", `{"provider":"github","revision":1}`))
	makeOAuthCallbackHandler(svc, nil, rec)(httptest.NewRecorder(), request(http.MethodGet, "http://instance.test/callback?code=callback-code&state=callback-state", ""))
	if len(rec.entries) != 2 {
		t.Fatal("failed authorization was audited as successful")
	}
}

func TestOAuthReconnectRequiresAuthenticationCSRFAndExplicitAdoption(t *testing.T) {
	srv, client, csrf, v := setupOAuthServer(t, nil)
	c, e := v.Create(vault.CreateInput{Name: "not-inferred-from-name", Type: vault.TypeGitToken, Secret: "synthetic-existing-access"})
	if e != nil {
		t.Fatal(e)
	}
	response := doJSON(t, client, http.MethodPut, srv.URL+"/api/oauth/apps/github", csrf, `{"clientId":"fixture-client","clientSecret":"synthetic-app-secret","enabled":true}`)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("app setup", response.StatusCode)
	}
	endpoint := srv.URL + "/api/credentials/" + c.ID + "/oauth/reconnect"
	body := `{"provider":"github","revision":0,"adoptExisting":true}`
	for _, tc := range []struct {
		token, body string
		status      int
	}{{"", body, 403}, {csrf, `{"provider":"github","revision":0,"adoptExisting":false}`, 409}, {csrf, `{"provider":"unknown","revision":0,"adoptExisting":true}`, 400}, {csrf, body, 200}} {
		r := doJSON(t, client, http.MethodPost, endpoint, tc.token, tc.body)
		if r.StatusCode != tc.status {
			r.Body.Close()
			t.Fatalf("reconnect status=%d want %d", r.StatusCode, tc.status)
		}
		if tc.status == 200 {
			var result struct {
				AuthorizeURL string `json:"authorizeUrl"`
			}
			if e = json.NewDecoder(r.Body).Decode(&result); e != nil {
				t.Fatal(e)
			}
			u, e := url.Parse(result.AuthorizeURL)
			if e != nil || u.Host != "github.com" || u.Query().Get("scope") != "repo read:user" || u.Query().Get("state") == "" {
				t.Fatal("authorization endpoint or original scopes changed")
			}
			if strings.Contains(result.AuthorizeURL, "synthetic") {
				t.Fatal("authorization URL included a secret")
			}
		}
		r.Body.Close()
	}
	anonymous := newTestClient(t)
	r := doJSON(t, anonymous, http.MethodPost, endpoint, "", body)
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("anonymous reconnect admitted", r.StatusCode)
	}
	list, e := v.List()
	if e != nil || len(list) != 1 || list[0].ID != c.ID || list[0].OAuth != nil {
		t.Fatal("begin authorization changed an old credential")
	}
}

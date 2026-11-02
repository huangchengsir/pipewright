package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/huangchengsir/pipewright/internal/audit"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authlogin"
	"github.com/huangchengsir/pipewright/internal/mask"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/terminalhttp"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type platformAdapter struct{ calls atomic.Int32 }

func (a *platformAdapter) Authorize(_ context.Context, _ authlogin.AppConfig, p authlogin.AuthorizeParams) (string, error) {
	return "https://provider.invalid/?state=" + url.QueryEscape(p.State), nil
}
func (a *platformAdapter) Exchange(_ context.Context, _ authlogin.AppConfig, _ authlogin.ExchangeParams) (authlogin.Identity, error) {
	a.calls.Add(1)
	return authlogin.Identity{Provider: "github", Issuer: "https://github.com", Subject: "exact-id", DisplayLabel: "Fixture account"}, nil
}

type platformFixture struct {
	handler http.Handler
	s       *authlogin.Service
	auth    *auth.Service
	session *auth.Session
	store   *store.Store
	adapter *platformAdapter
}

func platformSetup(t *testing.T) *platformFixture {
	t.Helper()
	st := testStoreAuth(t)
	a := auth.NewService(st.DB, nil)
	if e := a.Bootstrap("admin", "testpass"); e != nil {
		t.Fatal(e)
	}
	session, e := a.Login("admin", "testpass")
	if e != nil {
		t.Fatal(e)
	}
	v := vault.New(st.DB, testMasterKey())
	adapter := &platformAdapter{}
	s := authlogin.New(st.DB, v, a, authlogin.Config{PublicURL: "https://public.example", Adapters: map[string]authlogin.Adapter{"github": adapter}})
	rec := audit.New(st.DB, mask.NewMasker(), nil)
	return &platformFixture{New(testWebFSAuth(), a, WithAccount(a), WithPlatformLogin(s), WithAudit(rec)), s, a, session, st, adapter}
}
func (f *platformFixture) request(t *testing.T, method, path, body string, authenticated, csrf bool, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://untrusted-upstream.invalid"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if authenticated {
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: f.session.Token})
	}
	if csrf {
		r.Header.Set(headerCsrf, f.session.CSRFToken)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func (f *platformFixture) save(t *testing.T) {
	t.Helper()
	w := f.request(t, "PUT", "/api/account/platform-login/github/app", `{"expectedVersion":1,"clientId":"dedicated-app","clientSecret":"canary-http-secret"}`, true, true, "")
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
}
func (f *platformFixture) seedBound(t *testing.T) {
	t.Helper()
	f.save(t)
	_, e := f.store.DB.Exec(`INSERT INTO login_identities(provider,issuer,subject,display_label,identity_generation,bound_at) VALUES('github','https://github.com','exact-id','Fixture account',1,?)`, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.DB.Exec(`UPDATE login_provider_settings SET enabled=1 WHERE provider='github'`); e != nil {
		t.Fatal(e)
	}
}
func platformStartDecode(t *testing.T, w *httptest.ResponseRecorder) (authlogin.StartResult, *http.Cookie, string) {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("start %d %s", w.Code, w.Body.String())
	}
	var start authlogin.StartResult
	if e := json.Unmarshal(w.Body.Bytes(), &start); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(start.AuthorizeURL)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "flow_") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no browser flow cookie")
	}
	return start, cookie, u.Query().Get("state")
}
func TestPlatformPublicBoundariesAndSettingsPrivacy(t *testing.T) {
	f := platformSetup(t)
	w := f.request(t, "GET", "/api/auth/platform/providers", "", false, false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"visible":true`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = f.request(t, "PUT", "/api/account/platform-login/visibility", `{}`, false, false, ""); w.Code != 401 {
		t.Fatal("anonymous write", w.Code)
	}
	if w = f.request(t, "PUT", "/api/account/platform-login/visibility", `{}`, true, false, ""); w.Code != 403 {
		t.Fatal("csrf missing", w.Code)
	}
	f.save(t)
	w = f.request(t, "GET", "/api/auth/platform/providers", "", false, false, "")
	for _, canary := range []string{"dedicated-app", "canary-http-secret", "clientId", "binding", "secret"} {
		if strings.Contains(w.Body.String(), canary) {
			t.Fatalf("public leaked %s", canary)
		}
	}
	w = f.request(t, "GET", "/api/account/platform-login", "", true, false, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "canary-http-secret") || !strings.Contains(w.Body.String(), `"hasSecret":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var detail string
	if e := f.store.DB.QueryRow(`SELECT detail_json FROM audit_log WHERE action='platform_app_save'`).Scan(&detail); e != nil || strings.Contains(detail, "canary-http-secret") || strings.Contains(detail, "dedicated-app") {
		t.Fatalf("audit privacy %v", e)
	}
	w = f.request(t, "GET", "/api/oauth/apps", "", false, false, "")
	if w.Code != 401 {
		t.Fatal("repository OAuth became public")
	}
}
func TestPlatformReauthWrongPasswordStaysAuthenticated(t *testing.T) {
	f := platformSetup(t)
	w := f.request(t, "POST", "/api/account/reauth", `{"currentPassword":"wrong"}`, true, true, "")
	if w.Code != 422 || !strings.Contains(w.Body.String(), "invalid_current_password") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request(t, "GET", "/api/auth/session", "", true, false, "")
	if w.Code != 200 {
		t.Fatal("wrong password revoked session")
	}
	w = f.request(t, "POST", "/api/account/reauth", `{"currentPassword":"testpass"}`, true, true, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "reauthenticatedUntil") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPlatformStartOriginAndSeparateFirstFlowCookies(t *testing.T) {
	f := platformSetup(t)
	f.seedBound(t)
	w := f.request(t, "POST", "/api/auth/platform/github/start", `{}`, false, false, "https://evil.example")
	if w.Code != 403 {
		t.Fatal("cross-site", w.Code)
	}
	if w = f.request(t, "POST", "/api/auth/platform/github/start", `{"subject":"forged"}`, false, false, "https://public.example"); w.Code != 400 {
		t.Fatal("untrusted identity body", w.Code)
	}
	first, c1, _ := platformStartDecode(t, f.request(t, "POST", "/api/auth/platform/github/start", `{"returnTo":"/runs"}`, false, false, "https://public.example"))
	second, c2, _ := platformStartDecode(t, f.request(t, "POST", "/api/auth/platform/github/start", `{}`, false, false, "https://public.example"))
	if first.FlowID == second.FlowID || c1.Name == c2.Name || !strings.HasPrefix(c1.Name, "__Host-") || !c1.Secure || !c1.HttpOnly || c1.Path != "/" || c1.Domain != "" || c1.SameSite != http.SameSiteLaxMode {
		t.Fatalf("independent cookies %+v %+v", c1, c2)
	}
	if f.adapter.calls.Load() != 0 {
		t.Fatal("start exchanged token")
	}
}
func TestPlatformCallbackLoginFreshSessionDespiteStaleCookie(t *testing.T) {
	f := platformSetup(t)
	f.seedBound(t)
	start, cookie, state := platformStartDecode(t, f.request(t, "POST", "/api/auth/platform/github/start", `{"returnTo":"/runs?status=failed"}`, false, false, "https://public.example"))
	if e := f.auth.Logout(f.session.Token); e != nil {
		t.Fatal(e)
	}
	path := "/api/auth/platform/github/callback?state=" + url.QueryEscape(state) + "&code=private-code-canary"
	w := f.request(t, "GET", path, "", true, false, "", cookie)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/login?platform_result=success&returnTo=") {
		t.Fatal(w.Code, w.Body.String(), w.Header().Get("Location"))
	}
	if strings.Contains(w.Header().Get("Location"), "private-code") || strings.Contains(w.Header().Get("Location"), state) || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("callback leaked sensitive query")
	}
	sessionCookies := 0
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieSession || c.Name == cookieCsrf {
			sessionCookies++
			if !c.Secure {
				t.Fatal("proxy auth cookie insecure")
			}
		}
		if c.Name == cookie.Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if sessionCookies != 2 || !cleared {
		t.Fatal("cookie completion incomplete")
	}
	replay := f.request(t, "GET", path, "", false, false, "", cookie)
	if replay.Code != 303 || strings.Contains(replay.Header().Get("Location"), "success") || f.adapter.calls.Load() != 1 {
		t.Fatal("replay accepted")
	}
	var cipher []byte
	if e := f.store.DB.QueryRow(`SELECT verifier_cipher FROM login_flows WHERE flow_id=?`, start.FlowID).Scan(&cipher); e != nil || len(cipher) > 0 {
		t.Fatal("verifier retained", e)
	}
}
func TestPlatformBindingExplicitConfirmAndNoTrustedSubjectInput(t *testing.T) {
	f := platformSetup(t)
	f.save(t)
	start, cookie, state := platformStartDecode(t, f.request(t, "POST", "/api/account/platform-login/github/bind/start", `{"expectedVersion":2}`, true, true, "https://public.example"))
	w := f.request(t, "GET", "/api/auth/platform/github/callback?state="+url.QueryEscape(state)+"&code=fixture-code", "", true, false, "", cookie)
	if w.Code != 303 || w.Header().Get("Location") != "/settings/account?platform_binding="+start.FlowID {
		t.Fatal(w.Code, w.Header())
	}
	var count int
	f.store.DB.QueryRow(`SELECT COUNT(*) FROM login_identities`).Scan(&count)
	if count != 0 {
		t.Fatal("callback auto-bound")
	}
	w = f.request(t, "GET", "/api/account/platform-login/bind/pending/"+start.FlowID, "", true, false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fixture account") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/account/platform-login/github/bind/confirm", `{"expectedVersion":2,"flowId":"`+start.FlowID+`","subject":"forged"}`, true, true, "")
	if w.Code != 400 {
		t.Fatal("trusted frontend identity", w.Code)
	}
	w = f.request(t, "POST", "/api/account/platform-login/github/bind/confirm", `{"expectedVersion":2,"flowId":"`+start.FlowID+`"}`, true, true, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPlatformHTTPSProxyCookiePolicyNativeLoginProbeLogout(t *testing.T) {
	f := platformSetup(t)
	for _, op := range []struct {
		method, path, body  string
		authenticated, csrf bool
	}{{"POST", "/api/auth/login", `{"username":"admin","password":"testpass"}`, false, false}, {"GET", "/api/auth/session", "", true, false}, {"POST", "/api/auth/logout", "", true, true}} {
		w := f.request(t, op.method, op.path, op.body, op.authenticated, op.csrf, "")
		if w.Code != 200 && w.Code != 204 {
			t.Fatal(op.path, w.Code)
		}
		for _, c := range w.Result().Cookies() {
			if !c.Secure || c.Domain != "" || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("%s insecure proxy cookie %+v", op.path, c)
			}
		}
	}
}
func TestPlatformSourceRevocationClosesActualSSE(t *testing.T) {
	f := platformSetup(t)
	f.seedBound(t)
	session, e := auth.NewSession(time.Now().UTC(), "github", 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.InsertSession(f.store.DB, session); e != nil {
		t.Fatal(e)
	}
	h := requireAuth(f.auth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.AddCookie(&http.Cookie{Name: cookieSession, Value: session.Token})
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if _, e = reader.ReadString('\n'); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.SetEnabled(context.Background(), f.session.Token, "github", 2, false); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := io.ReadAll(reader); done <- e }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE not closed on revoke")
	}
}
func TestPlatformRevocationClosesWebSocketAndDetachedHTTPTerminal(t *testing.T) {
	for _, channel := range []string{"ws", "http"} {
		t.Run(channel, func(t *testing.T) {
			f := platformSetup(t)
			f.seedBound(t)
			session, e := auth.NewSession(time.Now().UTC(), "github", 2, 1)
			if e != nil {
				t.Fatal(e)
			}
			if e = auth.InsertSession(f.store.DB, session); e != nil {
				t.Fatal(e)
			}
			dialer := &fakeInteractiveDialer{}
			v := vault.New(f.store.DB, testMasterKey())
			targets := target.New(f.store.DB, v, dialer)
			manager := terminalhttp.New()
			defer manager.Close()
			srv := httptest.NewServer(New(testWebFSAuth(), f.auth, WithVault(v), WithServers(targets), WithTerminalHTTP(manager)))
			defer srv.Close()
			client := newTestClient(t)
			client.Jar.SetCookies(mustURL(t, srv.URL), []*http.Cookie{{Name: cookieSession, Value: f.session.Token}, {Name: cookieCsrf, Value: f.session.CSRFToken}})
			id := newServerAPI(t, client, srv.URL, f.session.CSRFToken)
			if channel == "ws" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				hdr := http.Header{}
				hdr.Set("Cookie", cookieSession+"="+session.Token)
				conn, _, e := websocket.Dial(ctx, wsURL(srv.URL)+"/api/servers/"+id+"/terminal/ws-v2", &websocket.DialOptions{HTTPHeader: hdr})
				if e != nil {
					t.Fatal(e)
				}
				defer conn.CloseNow()
				if e = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"start"}`)); e != nil {
					t.Fatal(e)
				}
				if _, _, e = conn.Read(ctx); e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.SetEnabled(context.Background(), f.session.Token, "github", 2, false); e != nil {
					t.Fatal(e)
				}
				if _, _, e = conn.Read(ctx); e == nil {
					t.Fatal("websocket stayed open")
				}
			} else {
				client.Jar.SetCookies(mustURL(t, srv.URL), []*http.Cookie{{Name: cookieSession, Value: session.Token}, {Name: cookieCsrf, Value: session.CSRFToken}})
				response := terminalRequest(t, client, "POST", srv.URL+"/api/servers/"+id+"/terminal/sessions", session.CSRFToken, map[string]string{"requestId": "platform-lease-0001"})
				raw, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatal(response.StatusCode, string(raw))
				}
				if _, e = f.s.SetEnabled(context.Background(), f.session.Token, "github", 2, false); e != nil {
					t.Fatal(e)
				}
				deadline := time.Now().Add(2 * time.Second)
				for {
					dialer.sess.mu.Lock()
					closed := dialer.sess.closed
					dialer.sess.mu.Unlock()
					if closed {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("detached PTY authorization not revoked")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
		})
	}
}

func TestPlatformCallbackSeparatesCancellationAndProviderFailure(t *testing.T) {
	f := platformSetup(t)
	f.seedBound(t)
	for _, providerError := range []string{"access_denied", "temporarily_unavailable", "private-error-canary"} {
		_, cookie, state := platformStartDecode(t, f.request(t, "POST", "/api/auth/platform/github/start", `{}`, false, false, "https://public.example"))
		w := f.request(t, "GET", "/api/auth/platform/github/callback?state="+url.QueryEscape(state)+"&error="+providerError, "", false, false, "", cookie)
		expected := "unavailable"
		if providerError == "access_denied" {
			expected = "cancelled"
		}
		if w.Code != 303 || w.Header().Get("Location") != "/login?platform_result="+expected {
			t.Fatalf("error branch %s status=%d redirect=%s", providerError, w.Code, w.Header().Get("Location"))
		}
		if strings.Contains(w.Body.String(), "private-error-canary") {
			t.Fatal("provider text reflected")
		}
	}
	if f.adapter.calls.Load() != 0 {
		t.Fatal("provider error exchanged code")
	}
}
func TestPlatformCurrentSourceDisableStillRecordsAudit(t *testing.T) {
	f := platformSetup(t)
	f.seedBound(t)
	native := f.session
	external, e := auth.NewSession(time.Now().UTC(), "github", 2, 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.InsertSession(f.store.DB, external); e != nil {
		t.Fatal(e)
	}
	if _, e = f.auth.Reauthenticate(external.Token, "testpass"); e != nil {
		t.Fatal(e)
	}
	f.session = external
	w := f.request(t, "POST", "/api/account/platform-login/github/enabled", `{"expectedVersion":2,"enabled":false}`, true, true, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sessionRevoked":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var count int
	if e = f.store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='platform_enabled_change' AND target_id='github'`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("completed revoke audit absent: %d %v", count, e)
	}
	if _, e = f.auth.Verify(native.Token); e != nil {
		t.Fatal("password source affected", e)
	}
	if _, e = f.auth.Verify(external.Token); e == nil {
		t.Fatal("current source survived disable")
	}
}

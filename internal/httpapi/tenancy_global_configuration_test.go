package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/ai"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/dbmigration"
	"github.com/huangchengsir/pipewright/internal/oauth"
	"github.com/huangchengsir/pipewright/internal/releasesource"
	"github.com/huangchengsir/pipewright/internal/retention"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type settingsTransportFunc func(*http.Request) (*http.Response, error)

func (f settingsTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGlobalConfigurationHTTPAndDomainAuthorization(t *testing.T) {
	st := storetest.Open(t)
	db := st.DB
	ctx := context.Background()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	v := vault.New(db, &key)
	dir := t.TempDir()
	control, err := dbmigration.AcquireControl(dir, &key)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	migration := dbmigration.NewRuntime(control, dbmigration.ActiveConfig{Connection: dbmigration.Connection{Driver: "sqlite"}}, st, &key, false, func() error { return nil })
	defer migration.Close()

	// The probe validates the destination before calling the injected transport.
	const fixtureHost = "192.0.2.12"
	const fixtureBase = "https://" + fixtureHost

	calls := 0
	var after func()
	client := &http.Client{Transport: settingsTransportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != fixtureHost {
			t.Fatalf("unexpected fixture address: %s", req.URL.Host)
		}
		calls++
		if after != nil {
			after()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"safe-model"}],"has_more":true,"last_id":"safe-model"}`))}, nil
	})}
	ais := ai.New(db, v, client)
	oas := oauth.New(db, v, client)
	rs := retention.NewService(db)
	sourceCatalog := releasesource.New()
	sources := releasesource.NewSettings(db, sourceCatalog)
	if e := auth.NewService(db, nil).Bootstrap("admin", "global-http-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "global-http-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Reader", Pages: []string{"page.dashboard"}, Grants: []authz.Grant{{Action: "dashboard.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "configreader", DisplayName: "Config reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "configreader", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "global-reader-new-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	secret := "synthetic-settings-secret"
	aIn := ai.SaveInput{Provider: "openai", BaseURL: fixtureBase, Model: "fixture", APIKey: &secret, Enabled: true}
	oIn := oauth.SaveAppInput{Provider: "github", ClientID: "fixture", ClientSecret: &secret, Enabled: true}
	for _, fn := range []func() error{
		func() error {
			_, e := migration.ProbeConnection(user, dbmigration.Connection{Driver: "postgres", DSN: "must-not-connect"})
			return e
		},
		func() error {
			_, e := migration.Preflight(user, dbmigration.Connection{Driver: "postgres", DSN: "must-not-connect"}, false)
			return e
		},
		func() error { _, e := migration.Start(user, "missing", 1); return e },
		func() error { _, e := migration.Abort(user, "missing", 1); return e }, func() error { _, e := sources.Get(user); return e }, func() error {
			_, e := sources.Save(user, releasesource.SettingsInput{Source: releasesource.GitHub, ExpectedRevision: 1})
			return e
		}, func() error { _, e := rs.ReadConfiguration(user); return e }, func() error { return rs.SetConfig(user, retention.Config{Enabled: true}) }, func() error { _, e := ais.Get(user); return e }, func() error { _, e := ais.Save(user, aIn); return e }, func() error { _, e := ais.Test(user, ai.TestInput{}); return e }, func() error { _, e := ais.(ai.ModelLister).Models(user, ai.TestInput{}); return e }, func() error { _, e := oas.ListApps(user); return e }, func() error { _, e := oas.SaveApp(user, oIn); return e }} {
		if e := fn(); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("domain permission", e)
		}
	}
	if calls != 0 {
		t.Fatal("denied domain called network")
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithAISettings(ais), WithOAuth(oas), WithVault(v), WithRetention(rs), WithReleaseSources(sourceCatalog, sources), WithDatabaseMigration(migration))
	token, csrf := login.Token, login.CSRFToken
	request := func(method, path, body string, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		r.Header.Set("X-Pipewright-Team", team)
		if withCSRF {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	aBody := `{"provider":"openai","baseUrl":"` + fixtureBase + `","model":"fixture","apiKey":"synthetic-settings-secret","enabled":true}`
	oBody := `{"clientId":"fixture","clientSecret":"synthetic-settings-secret","enabled":true}`
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/settings/database", ""}, {"POST", "/api/settings/database/test-connection", `{"driver":"postgres","host":"must-not-connect"}`}, {"POST", "/api/settings/database/preflight", "{}"}, {"POST", "/api/settings/database/start", `{"taskId":"missing","revision":1}`}, {"POST", "/api/settings/database/abort", `{"taskId":"missing","revision":1}`}, {"GET", "/api/version/check", ""}, {"GET", "/api/version/source-settings", ""}, {"PUT", "/api/version/source-settings", `{"source":"github","expectedRevision":1}`}, {"POST", "/api/version/update", "{}"}, {"POST", "/api/version/update-tasks", "{}"}, {"GET", "/api/version/update-tasks/current", ""}, {"GET", "/api/version/update-tasks/unknown", ""}, {"POST", "/api/version/update-tasks/unknown/resume", "{}"}, {"GET", "/api/retention/config", ""}, {"PUT", "/api/retention/config", `{"enabled":true,"keepPerProject":2}`}, {"GET", "/api/settings/ai", ""}, {"PUT", "/api/settings/ai", aBody}, {"POST", "/api/settings/ai/test", "{}"}, {"POST", "/api/settings/ai/models", "{}"}, {"GET", "/api/oauth/apps", ""}, {"PUT", "/api/oauth/apps/github", oBody}} {
		w := request(tc.method, tc.path, tc.body, true)
		if w.Code != 403 {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"ai_config", "oauth_apps"} {
		var n int
		if e = db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatal("denied writes", table, n, e)
		}
	}
	if calls != 0 {
		t.Fatal("denied HTTP called network")
	}
	if cfg, err := rs.GetConfig(ctx); err != nil || cfg.Enabled {
		t.Fatal("denied retention write", cfg, err)
	}
	if cfg, err := sources.Preferred(ctx); err != nil || cfg != releasesource.GitHub {
		t.Fatal("runtime source preference", cfg, err)
	}
	if state, err := control.State(); err != nil || state != nil {
		t.Fatal("denied migration task", state, err)
	}
	token, csrf = admin.Token, admin.CSRFToken
	targetPath := filepath.Join(dir, "not-created.db")
	probeBody, _ := json.Marshal(map[string]string{"driver": "sqlite", "file": targetPath})
	if w := request("POST", "/api/settings/database/test-connection", string(probeBody), true); w.Code != 200 {
		t.Fatal("admin path check", w.Code, w.Body.String())
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatal("path check created target")
	}

	if w := request("PUT", "/api/version/source-settings", `{"source":"github","expectedRevision":1}`, true); w.Code != 200 {
		t.Fatal("admin source save", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/retention/config", `{"enabled":true}`, false); w.Code != 403 {
		t.Fatal("retention CSRF", w.Code)
	}
	for range 2 {
		if w := request("PUT", "/api/retention/config", `{"enabled":true,"keepPerProject":2,"maxAgeDays":-1}`, true); w.Code != 200 {
			t.Fatal("retention save", w.Code, w.Body.String())
		}
	}
	if cfg, err := rs.ReadConfiguration(adminCtx); err != nil || !cfg.Enabled || cfg.KeepPerProject != 2 || cfg.MaxAgeDays != 0 {
		t.Fatal("retention normalization", cfg, err)
	}
	if _, err := rs.Prune(ctx, time.Now()); err != nil {
		t.Fatal("background pruning without employee session", err)
	}
	if w := request("PUT", "/api/settings/ai", aBody, false); w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	for _, tc := range []struct{ method, path, body string }{{"PUT", "/api/settings/ai", aBody}, {"PUT", "/api/oauth/apps/github", oBody}, {"GET", "/api/settings/ai", ""}, {"GET", "/api/oauth/apps", ""}, {"POST", "/api/settings/ai/test", "{}"}} {
		w := request(tc.method, tc.path, tc.body, true)
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
			t.Fatal("admin config", tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("admin probe count", calls)
	}
	// Nested configuration save must obey the caller's transaction and rollback.
	rollback := errors.New("rollback")
	e = configstate.WithWriteTxContext(adminCtx, db, func(c context.Context, _ configstate.DBTX) error {
		if _, err := sources.Save(c, releasesource.SettingsInput{Source: releasesource.GitHub, ExpectedRevision: 2}); err != nil {
			return err
		}
		if err := rs.SetConfig(c, retention.Config{KeepPerProject: 99}); err != nil {
			return err
		}
		cfg, err := rs.ReadConfiguration(c)
		if err != nil || cfg.KeepPerProject != 99 {
			t.Fatal("retention read own transaction", cfg, err)
		}
		aIn.Model = "rolled-back"
		if _, e := ais.Save(c, aIn); e != nil {
			return e
		}
		oIn.ClientID = "rolled-back"
		if _, e := oas.SaveApp(c, oIn); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
	if cfg, err := rs.ReadConfiguration(adminCtx); err != nil || !cfg.Enabled || cfg.KeepPerProject != 2 {
		t.Fatal("retention rollback", cfg, err)
	}
	if cfg, err := sources.Get(adminCtx); err != nil || cfg.Revision != 2 {
		t.Fatal("source rollback", cfg, err)
	}
	cfg, e := ais.Get(adminCtx)
	if e != nil || cfg.Model != "fixture" {
		t.Fatal("AI rollback", e)
	}
	apps, e := oas.ListApps(adminCtx)
	if e != nil || len(apps) != 1 || apps[0].ClientID != "fixture" {
		t.Fatal("OAuth rollback", e)
	}
	// Revoke during first catalog page: no next page and no returned model list.
	after = func() {
		if e := accounts.Logout(ctx, admin.Token); e != nil {
			t.Fatal(e)
		}
	}
	provider := "claude"
	base := fixtureBase
	before := calls
	result, e := ais.(ai.ModelLister).Models(adminCtx, ai.TestInput{Provider: &provider, BaseURL: &base, APIKey: &secret})
	if !errors.Is(e, tenancy.ErrForbidden) || result != nil || calls != before+1 {
		raw, _ := json.Marshal(result)
		t.Fatal("catalog revocation", e, calls, string(raw))
	}
	if e := rs.SetConfig(adminCtx, retention.Config{}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logged out retention save", e)
	}
	if _, e = oas.SaveApp(adminCtx, oIn); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logged out config save", e)
	}
}

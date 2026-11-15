package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/authlogin"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/url"
	"strings"
	"testing"
)

func TestEmployeePlatformHTTPBindingAndConfigurationBoundary(t *testing.T) {
	ctx := context.Background()
	f := platformSetup(t)
	f.save(t)
	if _, err := f.store.DB.Exec(`UPDATE login_provider_settings SET enabled=1 WHERE provider='github'`); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(f.store.DB)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(f.store.DB)
	admin, err := accounts.Login(ctx, "admin", "testpass")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "employee", DisplayName: "Employee"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := accounts.Login(ctx, "employee", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	f.handler = New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithPlatformLogin(f.s))
	f.session = user.AuthSession()
	if w := f.request(t, "GET", "/api/account/platform-login", "", true, false, ""); w.Code != 403 {
		t.Fatal("forced password bypass", w.Code)
	}
	user, err = accounts.ChangePassword(ctx, user.Token, initial.Password, "employee-http-password")
	if err != nil {
		t.Fatal(err)
	}
	f.session = user.AuthSession()
	w := f.request(t, "GET", "/api/account/platform-login", "", true, false, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var settings authlogin.Snapshot
	if err = json.Unmarshal(w.Body.Bytes(), &settings); err != nil || settings.CanManage {
		t.Fatal(settings, err)
	}
	if strings.Contains(w.Body.String(), "dedicated-app") || strings.Contains(w.Body.String(), "canary-http-secret") {
		t.Fatal("global settings leaked")
	}
	version := settings.Providers[0].ConfigVersion
	for _, entry := range settings.Providers {
		if entry.Provider == "github" {
			version = entry.ConfigVersion
		}
	}
	body := func(v any) string { raw, _ := json.Marshal(v); return string(raw) }
	w = f.request(t, "POST", "/api/account/platform-login/github/enabled", body(map[string]any{"expectedVersion": version, "enabled": false}), true, true, "")
	if w.Code != 403 {
		t.Fatal("employee changed global enabled", w.Code, w.Body.String())
	}
	t.Run("employee-cannot-edit-platform-configuration", func(t *testing.T) {
		visibility := map[string]bool{}
		for _, provider := range authlogin.Providers {
			visibility[provider] = provider != "github"
		}
		for _, pair := range []struct{ path, body string }{
			{"/api/account/platform-login/visibility", body(map[string]any{"expectedVersion": 1, "visibility": visibility})},
			{"/api/account/platform-login/github/app", body(map[string]any{"expectedVersion": version, "clientId": "employee-proof", "clientSecret": "fixture-only"})},
		} {
			w := f.request(t, "PUT", pair.path, pair.body, true, true, "")
			if w.Code != 403 || strings.Contains(w.Body.String(), "fixture-only") {
				t.Fatalf("employee configuration route %s returned %d %s", pair.path, w.Code, w.Body.String())
			}
		}
	})
	w = f.request(t, "POST", "/api/account/reauth", `{"currentPassword":"employee-http-password"}`, true, true, "")
	if w.Code != 200 {
		t.Fatal("employee own reauth", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/account/platform-login/github/bind/start", body(map[string]any{"expectedVersion": version}), true, false, "")
	if w.Code != 403 {
		t.Fatal("bind missing CSRF", w.Code)
	}
	w = f.request(t, "POST", "/api/account/platform-login/github/bind/start", body(map[string]any{"expectedVersion": version}), true, true, "https://public.example")
	start, cookie, state := platformStartDecode(t, w)
	w = f.request(t, "GET", "/api/auth/platform/github/callback?state="+url.QueryEscape(state)+"&code=fixture", "", true, false, "", cookie)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "platform_binding="+start.FlowID) {
		t.Fatal("bind callback", w.Code, w.Header().Get("Location"))
	}
	w = f.request(t, "GET", "/api/account/platform-login/bind/pending/"+start.FlowID, "", true, false, "")
	if w.Code != 200 {
		t.Fatal("pending", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/account/platform-login/github/bind/confirm", body(map[string]any{"expectedVersion": version, "flowId": start.FlowID}), true, true, "")
	if w.Code != 200 {
		t.Fatal("confirm", w.Code, w.Body.String())
	}
	w = f.request(t, "POST", "/api/auth/platform/github/start", `{"returnTo":"/settings/account"}`, false, false, "https://public.example")
	_, cookie, state = platformStartDecode(t, w)
	w = f.request(t, "GET", "/api/auth/platform/github/callback?state="+url.QueryEscape(state)+"&code=fixture", "", false, false, "", cookie)
	if w.Code != 303 || w.Header().Get("Location") != "/login?platform_result=success&returnTo=%2Fsettings%2Faccount" {
		t.Fatal("employee login callback", w.Code, w.Header().Get("Location"))
	}
	var sessionToken string
	for _, item := range w.Result().Cookies() {
		if item.Name == cookieSession {
			sessionToken = item.Value
		}
	}
	signed, err := accounts.Verify(ctx, sessionToken)
	if err != nil || signed.User.ID != user.User.ID || signed.AuthSource != "github" {
		t.Fatal("employee callback session", signed.User.ID, err)
	}
	t.Run("own-platform-unbind-revokes-only-matching-session", func(t *testing.T) {
		accountInfo := f.request(t, "GET", "/api/account/platform-login", "", true, false, "")
		var current authlogin.Snapshot
		if accountInfo.Code != 200 || json.Unmarshal(accountInfo.Body.Bytes(), &current) != nil {
			t.Fatal("cannot load current binding")
		}
		currentVersion := version
		for _, item := range current.Providers {
			if item.Provider == "github" {
				currentVersion = item.ConfigVersion
			}
		}
		w := f.request(t, "DELETE", "/api/account/platform-login/github/binding", body(map[string]any{"expectedVersion": currentVersion}), true, true, "")
		if w.Code != 200 {
			t.Fatalf("no self unbind: %d %s", w.Code, w.Body.String())
		}
		if _, e := accounts.Verify(ctx, sessionToken); e == nil {
			t.Fatal("removed external identity still has a session")
		}
		if _, e := accounts.Verify(ctx, user.Token); e != nil {
			t.Fatal("own password session was lost", e)
		}
	})
}

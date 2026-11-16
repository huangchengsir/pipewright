package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
)

func TestTenantAccountHTTPLifecycleAndRestrictedSession(t *testing.T) {
	db := storetest.OpenDB(t)
	ctx := context.Background()
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "admin-password"); err != nil {
		t.Fatal(err)
	}
	state, err := tenancy.NewRepository(db).ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-password")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, state.Epoch), WithTenancyAccounts(accounts), WithAccount(legacy))
	request := func(method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			req.AddCookie(cookie)
			if cookie.Name == cookieCsrf {
				req.Header.Set(headerCsrf, cookie.Value)
			}
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	body, _ := json.Marshal(map[string]string{"username": "ALICE", "password": initial.Password})
	login := request("POST", "/api/auth/login", string(body), nil)
	if login.Code != 200 || !strings.Contains(login.Body.String(), `"username":"alice"`) || !strings.Contains(login.Body.String(), `"mustChangePassword":true`) {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	self := request("GET", "/api/access/self", "", cookies)
	if self.Code != 200 || strings.Contains(self.Body.String(), initial.Password) || strings.Contains(self.Body.String(), "securityGeneration") {
		t.Fatal(self.Code, self.Body.String())
	}
	for _, path := range []string{"/api/settings/mcp/tokens", "/api/account/platform-login", "/api/projects", "/api/version/check"} {
		out := request("GET", path, "", cookies)
		if out.Code != 403 || !strings.Contains(out.Body.String(), "password_change_required") {
			t.Fatal(path, out.Code, out.Body.String())
		}
	}
	passwordBody, _ := json.Marshal(map[string]string{"currentPassword": initial.Password, "newPassword": "alice-updated-password"})
	rotated := request("POST", "/api/access/self/password", string(passwordBody), cookies)
	if rotated.Code != 200 {
		t.Fatal(rotated.Code, rotated.Body.String())
	}
	if old := request("GET", "/api/auth/session", "", cookies); old.Code != 401 {
		t.Fatal("old cookie still active", old.Code)
	}
	newCookies := rotated.Result().Cookies()
	profile := request("PATCH", "/api/access/self", `{"displayName":"Alice Updated"}`, newCookies)
	if profile.Code != 204 {
		t.Fatal(profile.Code, profile.Body.String())
	}
	denied := request("POST", "/api/access/users", `{"Username":"intruder","DisplayName":"Intruder"}`, newCookies)
	if denied.Code != 403 {
		t.Fatal("ordinary account created user", denied.Code, denied.Body.String())
	}
	// Administrator-only fields are never mass-assigned through account DTOs.
	adminCookies := []*http.Cookie{{Name: cookieSession, Value: admin.Token}, {Name: cookieCsrf, Value: admin.CSRFToken}}
	invalid := request("POST", "/api/access/users", `{"Username":"extra","DisplayName":"Extra","InstanceAdmin":true}`, adminCookies)
	if invalid.Code != 400 {
		t.Fatal("administrator flag accepted", invalid.Code)
	}
	t.Run("deprecated-single-admin-account-actions-closed", func(t *testing.T) {
		for _, route := range []struct{ method, path, body string }{
			{"GET", "/api/account/sessions", ""},
			{"DELETE", "/api/account/sessions/unused", ""},
			{"POST", "/api/account/password", `{"currentPassword":"unused","newPassword":"fixture-only"}`},
		} {
			w := request(route.method, route.path, route.body, newCookies)
			if w.Code != 403 || strings.Contains(w.Body.String(), "sessions") {
				t.Fatalf("legacy admin route was reachable by employee: %s %d", route.method, w.Code)
			}
		}
		if w := request("GET", "/api/access/self", "", newCookies); w.Code != 200 {
			t.Fatalf("legacy rejection affected current account session: %d", w.Code)
		}
	})
	logout := request("POST", "/api/auth/logout", "", newCookies)
	if logout.Code != 204 {
		t.Fatal(logout.Code)
	}
	if ended := request("GET", "/api/auth/session", "", newCookies); ended.Code != 401 {
		t.Fatal("logout not revoked", ended.Code)
	}
	t.Run("employee-directory-and-account-actions", func(t *testing.T) {
		created := request(http.MethodPost, "/api/access/users", "{\"username\":\"bob\",\"displayName\":\"Bob\"}", adminCookies)
		var bob tenancy.InitialCredential
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &bob) != nil || bob.User.ID == "" || bob.User.InstanceAdmin || bob.Password == "" {
			t.Fatalf("employee creation failed, status=%d", created.Code)
		}
		bobPath := "/api/access/users/" + bob.User.ID
		list := func(name string, enabled bool) {
			t.Helper()
			out := request(http.MethodGet, "/api/access/users", "", adminCookies)
			var page tenancy.UserPage
			if out.Code != http.StatusOK || json.Unmarshal(out.Body.Bytes(), &page) != nil || strings.Contains(out.Body.String(), bob.Password) || len(page.Users) != 3 {
				t.Fatalf("unexpected employee directory, status=%d", out.Code)
			}
			for _, user := range page.Users {
				if user.ID == bob.User.ID && user.DisplayName == name && user.Enabled == enabled {
					return
				}
			}
			t.Fatal("expected exact employee metadata in directory")
		}
		list("Bob", true)

		alice, err := accounts.Login(ctx, "alice", "alice-updated-password")
		if err != nil {
			t.Fatal(err)
		}
		cookieJar := func(s tenancy.Session) []*http.Cookie {
			return []*http.Cookie{{Name: cookieSession, Value: s.Token}, {Name: cookieCsrf, Value: s.CSRFToken}}
		}
		aliceCookies := cookieJar(alice)
		for _, action := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/access/users", ""},
			{http.MethodPatch, bobPath, "{\"displayName\":\"Denied\"}"},
			{http.MethodPost, bobPath + "/reset-password", ""},
			{http.MethodPut, bobPath + "/enabled", "{\"enabled\":false}"},
		} {
			out := request(action.method, action.path, action.body, aliceCookies)
			if out.Code != http.StatusForbidden || strings.Contains(out.Body.String(), bob.Password) {
				t.Fatalf("employee directory action %s %s returned %d", action.method, action.path, out.Code)
			}
		}
		list("Bob", true)
		if out := request(http.MethodPatch, bobPath, "{\"displayName\":\"Bob Updated\"}", adminCookies); out.Code != http.StatusNoContent {
			t.Fatalf("administrator edit returned %d", out.Code)
		}
		list("Bob Updated", true)

		bobSession, err := accounts.Login(ctx, "bob", bob.Password)
		if err != nil {
			t.Fatal(err)
		}
		reset := request(http.MethodPost, bobPath+"/reset-password", "", adminCookies)
		var replacement tenancy.InitialCredential
		if reset.Code != http.StatusOK || json.Unmarshal(reset.Body.Bytes(), &replacement) != nil || replacement.User.ID != bob.User.ID || !replacement.User.MustChangePassword || replacement.Password == "" || replacement.Password == bob.Password {
			t.Fatalf("employee reset returned %d", reset.Code)
		}
		assertSession := func(s tenancy.Session, want int) {
			t.Helper()
			if out := request(http.MethodGet, "/api/auth/session", "", cookieJar(s)); out.Code != want {
				t.Fatalf("session returned %d, want %d", out.Code, want)
			}
		}
		assertSession(bobSession, http.StatusUnauthorized)
		bobSession, err = accounts.Login(ctx, "bob", replacement.Password)
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []struct {
			body    string
			enabled bool
		}{{"{\"enabled\":false}", false}, {"{\"enabled\":true}", true}} {
			if out := request(http.MethodPut, bobPath+"/enabled", state.body, adminCookies); out.Code != http.StatusNoContent {
				t.Fatalf("administrator set enabled returned %d", out.Code)
			}
			list("Bob Updated", state.enabled)
			assertSession(bobSession, http.StatusUnauthorized)
		}
		assertSession(alice, http.StatusOK)

		secondAlice, err := accounts.Login(ctx, "alice", "alice-updated-password")
		if err != nil {
			t.Fatal(err)
		}
		sessions := request(http.MethodGet, "/api/access/self/sessions", "", aliceCookies)
		var listed struct {
			Sessions []tenancy.SessionView `json:"sessions"`
		}
		if sessions.Code != http.StatusOK || json.Unmarshal(sessions.Body.Bytes(), &listed) != nil || len(listed.Sessions) != 2 {
			t.Fatalf("self session list returned %d with %d entries", sessions.Code, len(listed.Sessions))
		}
		for _, session := range listed.Sessions {
			if session.ID != alice.Actor.SessionHash && session.ID != secondAlice.Actor.SessionHash {
				t.Fatal("self session list returned a foreign session")
			}
		}
		const sessionPath = "/api/access/self/sessions/"
		if out := request(http.MethodDelete, sessionPath+admin.Actor.SessionHash, "", aliceCookies); out.Code != http.StatusForbidden {
			t.Fatalf("foreign session removal returned %d", out.Code)
		}
		assertSession(admin, http.StatusOK)
		if out := request(http.MethodDelete, sessionPath+secondAlice.Actor.SessionHash, "", aliceCookies); out.Code != http.StatusNoContent {
			t.Fatalf("own session removal returned %d", out.Code)
		}
		assertSession(secondAlice, http.StatusUnauthorized)
		assertSession(alice, http.StatusOK)
	})
}

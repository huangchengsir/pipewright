package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/dnsprovider"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantDNSHTTPAtomicCredentialsAndDeniedEffects(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	svc := dnsprovider.New(db, v, nil)
	if err := auth.NewService(db, nil).Bootstrap("admin", "dns-http-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "dns-http-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Project reader", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "dnshttp", DisplayName: "DNS", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "dnshttp", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "dns-http-user-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v), WithDNSProviders(svc))
	request := func(method, path, body, token, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/dns/providers"+path, strings.NewReader(body))
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: token})
		if csrf != "" {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: csrf})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	payload := `{"type":"cloudflare","name":"DNS fixture","baseDomain":"example.invalid","token":"synthetic-dns-token"}`
	count := func(table string) int {
		var n int
		if e := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	w := request("POST", "", payload, login.Token, login.CSRFToken)
	if w.Code != 403 || count("credentials") != 0 || count("dns_providers") != 0 {
		t.Fatal("employee create", w.Code, w.Body.String())
	}
	w = request("POST", "", payload, admin.Token, "")
	if w.Code != 403 || count("credentials") != 0 {
		t.Fatal("csrf", w.Code)
	}
	w = request("POST", "", strings.Replace(payload, "example.invalid", "invalid", 1), admin.Token, admin.CSRFToken)
	if w.Code != 400 || count("credentials") != 0 || count("dns_providers") != 0 {
		t.Fatal("rollback", w.Code, w.Body.String())
	}
	w = request("POST", "", payload, admin.Token, admin.CSRFToken)
	if w.Code != 201 || strings.Contains(w.Body.String(), "synthetic-dns-token") {
		t.Fatal("admin create", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path string }{{"GET", ""}, {"DELETE", "/" + created.ID}, {"POST", "/" + created.ID + "/verify"}, {"POST", "/unknown/verify"}} {
		w = request(tc.method, tc.path, "{}", login.Token, login.CSRFToken)
		if w.Code != 403 {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if count("credentials") != 1 || count("dns_providers") != 1 {
		t.Fatal("denied effects")
	}
	w = request("GET", "", "", admin.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), created.ID) || strings.Contains(w.Body.String(), "synthetic-dns-token") {
		t.Fatal("admin list", w.Code, w.Body.String())
	}
	if err = accounts.Logout(ctx, admin.Token); err != nil {
		t.Fatal(err)
	}
	w = request("DELETE", "/"+created.ID, "", admin.Token, admin.CSRFToken)
	if w.Code != 401 || count("dns_providers") != 1 {
		t.Fatal("logout delete", w.Code)
	}
}

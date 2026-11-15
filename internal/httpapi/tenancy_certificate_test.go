package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/proxy"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTenantCertificateHTTPReadDoesNotGrantRefresh(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	for _, id := range []string{"visible-cert", "hidden-cert"} {
		if _, err := db.Exec(`INSERT INTO proxy_routes(id,server_id,domain,upstream_container,upstream_port,tls_mode,enabled,cert_status,cert_detail,config,created_at,updated_at) VALUES (?,'fixture-host',?,'web',80,'auto',1,'pending','unchanged','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, id, id+".invalid"); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "certificate-http-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	mode, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "certificate-http-password")
	if err != nil {
		t.Fatal(err)
	}
	var team string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Certificate read only", Pages: []string{"page.certificates"}, Grants: []authz.Grant{{Action: "certificate.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "proxy_route", ID: "visible-cert"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "certuser", DisplayName: "Cert", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "certuser", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "certificate-http-new-password")
	if err != nil {
		t.Fatal(err)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithProxy(proxy.New(db, nil)))
	request := func(method, path, teamHeader string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: login.Token})
		r.Header.Set("X-Pipewright-Team", teamHeader)
		if csrf {
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: login.CSRFToken})
			r.Header.Set("X-CSRF-Token", login.CSRFToken)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/proxy/overview", team, false)
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != 1 || body.Items[0].ID != "visible-cert" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"visible-cert", 403}, {"hidden-cert", 404}, {"unknown", 404}} {
		w = request("POST", "/api/proxy/routes/"+tc.id+"/refresh", team, true)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	w = request("POST", "/api/proxy/routes/visible-cert/refresh", team, false)
	if w.Code != 403 {
		t.Fatal("csrf", w.Code)
	}
	w = request("GET", "/api/proxy/overview", "forged-team", false)
	if w.Code != 403 {
		t.Fatal("team override", w.Code, w.Body.String())
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM proxy_routes WHERE cert_status='pending' AND cert_detail='unchanged'`).Scan(&n); err != nil || n != 2 {
		t.Fatal("denied status write", n, err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/proxy/overview", team, false)
	if w.Code != 401 {
		t.Fatal("logout", w.Code)
	}
}

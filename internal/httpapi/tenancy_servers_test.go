package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
)

// Real cookies, middleware, domain services, encrypted vault and SQL. Only SSH
// transport is replaced; denied actions must never reach it.
func TestTenantServerHTTPButtonsAndDataScopes(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	c, e := v.Create(vault.CreateInput{Name: "SSH", Type: vault.TypeSSHPassword, Secret: "fixture-only-password"})
	if e != nil {
		t.Fatal(e)
	}
	dialer := &permissionContainerDialer{}
	svc := target.New(db, v, dialer)
	makeServer := func(name string) *target.Server {
		s, e := svc.Create(ctx, target.CreateInput{Name: name, Host: "192.0.2.1", User: "builder", CredentialID: c.ID})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	a, b, f := makeServer("allowed"), makeServer("outside"), makeServer("foreign")
	if e = auth.NewService(db, nil).Bootstrap("admin", "administrator-server-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	mode, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "administrator-server-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='server' AND resource_id=?`, other.ID, f.ID); e != nil {
		t.Fatal(e)
	}
	grants := []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{a.ID}}}, {Action: "credential.metadata", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{c.ID}}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Server member", Pages: []string{"page.servers", "page.vault"}, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "member", DisplayName: "Member", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	member, e := accounts.Login(ctx, "member", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	member, e = accounts.ChangePassword(ctx, member.Token, initial.Password, "member-server-password")
	if e != nil {
		t.Fatal(e)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithVault(v), WithServers(svc))
	request := func(method, path, body, selectedTeam string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Pipewright-Team", selectedTeam)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: member.Token})
		if csrf {
			r.Header.Set(headerCsrf, member.CSRFToken)
			r.AddCookie(&http.Cookie{Name: cookieCsrf, Value: member.CSRFToken})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	expect := func(method, path, body string, status int) {
		t.Helper()
		w := request(method, path, body, team, true)
		if w.Code != status {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "fixture-only-password") {
			t.Fatal("secret returned")
		}
	}
	unchanged := func() {
		t.Helper()
		var count int
		var name string
		if e = db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow(`SELECT name FROM servers WHERE id=?`, a.ID).Scan(&name); e != nil {
			t.Fatal(e)
		}
		if count != 3 || name != "allowed" || dialer.calls.Load() != 0 {
			t.Fatal("denied action changed data or dialed SSH", count, name, dialer.calls.Load())
		}
	}
	list := request("GET", "/api/servers", "", team, true)
	var rows struct {
		Items []serverDTO `json:"items"`
	}
	if e = json.Unmarshal(list.Body.Bytes(), &rows); e != nil || list.Code != 200 || len(rows.Items) != 1 || rows.Items[0].ID != a.ID {
		t.Fatal("SQL scoped list", list.Code, list.Body.String(), e)
	}
	expect("GET", "/api/servers/"+a.ID, "", 200)
	body := `{"name":"new","host":"192.0.2.2","user":"builder","credentialId":"` + c.ID + `"}`
	expect("POST", "/api/servers", body, 403)
	expect("PUT", "/api/servers/"+a.ID, `{"name":"changed"}`, 403)
	expect("DELETE", "/api/servers/"+a.ID, "", 403)
	expect("POST", "/api/servers/"+a.ID+"/test", `{}`, 403)
	unchanged()
	// Literal known IDs from another scope, team or a missing object behave alike.
	for _, id := range []string{b.ID, f.ID, "missing"} {
		expect("GET", "/api/servers/"+id, "", 404)
		expect("PUT", "/api/servers/"+id, `{"name":"changed"}`, 403)
		expect("DELETE", "/api/servers/"+id, "", 403)
		expect("POST", "/api/servers/"+id+"/test", `{}`, 403)
	}
	if w := request("GET", "/api/servers", "", other.ID, true); w.Code != 403 {
		t.Fatal("foreign team accepted", w.Code)
	}
	unchanged()
	save := func(next []authz.Grant) {
		t.Helper()
		var err error
		role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Server member", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: next})
		if err != nil {
			t.Fatal(err)
		}
	}
	edit := append(append([]authz.Grant{}, grants...), authz.Grant{Action: "server.edit", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{a.ID}}})
	save(edit)
	expect("PUT", "/api/servers/"+a.ID, `{"name":"changed"}`, 403)
	unchanged()
	edit = append(edit, authz.Grant{Action: "credential.use", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{c.ID}}})
	save(edit)
	if w := request("PUT", "/api/servers/"+a.ID, `{"name":"changed"}`, team, false); w.Code != 403 {
		t.Fatal("missing csrf accepted", w.Code)
	}
	unchanged()
	expect("PUT", "/api/servers/"+a.ID, `{"name":"changed"}`, 200)
	// A grant revoked after the page was read cannot be used by a saved button.
	save(grants)
	expect("PUT", "/api/servers/"+a.ID, `{"name":"stale"}`, 403)
	var name string
	if e = db.QueryRow(`SELECT name FROM servers WHERE id=?`, a.ID).Scan(&name); e != nil || name != "changed" {
		t.Fatal("revoked update", name, e)
	}
	// Creation needs explicit team-wide create/read and a usable credential.
	all := []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "server.create", Scope: authz.Scope{Kind: authz.ScopeTeam}}, grants[1], edit[len(edit)-1]}
	save(all)
	created := request("POST", "/api/servers", body, team, true)
	if created.Code != 201 {
		t.Fatal("create", created.Code, created.Body.String())
	}
	var result serverDTO
	if e = json.Unmarshal(created.Body.Bytes(), &result); e != nil || result.ID == "" {
		t.Fatal(e)
	}
	var owner string
	if e = db.QueryRow(`SELECT tenant_id FROM access_ownership WHERE resource_kind='server' AND resource_id=?`, result.ID).Scan(&owner); e != nil || owner != team {
		t.Fatal("creation owner", owner, e)
	}
	all = append(all, authz.Grant{Action: "server.delete", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{result.ID}}})
	save(all)
	expect("DELETE", "/api/servers/"+result.ID, "", 204)
	var n int
	if e = db.QueryRow(`SELECT COUNT(*) FROM servers WHERE id=?`, result.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("delete persisted", n, e)
	}
	if dialer.calls.Load() != 0 {
		t.Fatal("CRUD unexpectedly executed SSH")
	}
}

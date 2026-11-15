package httpapi

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardReadOnlyServerDoesNotDialOrClaimOffline(t *testing.T) {
	ctx := context.Background()
	db := storetest.OpenDB(t)
	v := vault.New(db, testMasterKey())
	credential, e := v.Create(vault.CreateInput{Name: "test SSH", Type: vault.TypeSSHKey, Secret: "fixture-password"})
	if e != nil {
		t.Fatal(e)
	}
	dialer := &permissionContainerDialer{}
	targets := target.New(db, v, dialer)
	server, e := targets.Create(ctx, target.CreateInput{Name: "dedicated fixture", Host: "127.0.0.1", User: "fixture", CredentialID: credential.ID})
	if e != nil {
		t.Fatal(e)
	}
	if e = auth.NewService(db, nil).Bootstrap("admin", "administrator-terminal-password"); e != nil {
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
	admin, e := accounts.Login(ctx, "admin", "administrator-terminal-password")
	if e != nil {
		t.Fatal(e)
	}
	grants := []authz.Grant{}
	for _, action := range []string{"server.inspect", "dashboard.read"} {
		grants = append(grants, authz.Grant{Action: action, Scope: authz.Scope{Kind: authz.ScopeTeam}})
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Terminal operator", Pages: []string{"page.servers", "page.dashboard"}, Grants: grants, RequiresSelectedBinding: false})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "operator", DisplayName: "Operator", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	employee, e := accounts.Login(ctx, "operator", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	employee, e = accounts.ChangePassword(ctx, employee.Token, initial.Password, "operator-terminal-password")
	if e != nil {
		t.Fatal(e)
	}
	router := New(testWebFSAuth(), tenancy.NewAuthenticator(accounts, mode.Epoch), WithTenancyAccounts(accounts), WithConfigManagement(db, nil, false), WithServers(targets))
	request := func(section string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/dashboard/"+section, nil)
		r.Header.Set("X-Pipewright-Team", team)
		r.AddCookie(&http.Cookie{Name: cookieSession, Value: employee.Token})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := request("servers")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var servers []map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &servers); e != nil || len(servers) != 1 || len(servers[0]) != 3 || servers[0]["id"] != server.ID {
		t.Fatal("minimal visible server", servers, e)
	}
	w = request("metrics")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var metrics struct {
		Items []serverMetricsDTO `json:"items"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &metrics); e != nil || len(metrics.Items) != 0 {
		t.Fatal("missing permission must be unknown not offline", metrics, e)
	}
	if dialer.calls.Load() != 0 {
		t.Fatal("read-only employee opened SSH", dialer.calls.Load())
	}
}

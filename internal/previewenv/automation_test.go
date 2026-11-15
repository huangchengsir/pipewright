package previewenv

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestEmployeePreviewSweepUsesSavedCeilingAndLiveRevocation(t *testing.T) {
	svc, _, routes := newTestSvc(t)
	db := svc.store.db
	ctx := context.Background()
	seedTenantPreviewResources(t, db)
	records := 0
	svc.SetRecordDeleter(tenancyRecordDeleter(func(context.Context, string, string) error { records++; return nil }))
	if e := auth.NewService(db, nil).Bootstrap("admin", "preview-admin-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "preview-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	grants := []authz.Grant{}
	pages := []string{}
	seen := map[string]bool{}
	for _, action := range []string{"project.read", "pipeline.read", "pipeline.edit", "preview.provision", "preview.read", "preview.reclaim", "server.inspect", "server.use", "proxy.read", "proxy.manage", "credential.metadata", "credential.use", "dns.use"} {
		d, _ := authz.Lookup(action)
		scope := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p1"}}
		if !d.ProjectScoped {
			scope = authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{map[string]string{"server": "server", "credential": "preview-fixture", "dns_provider": "prov-1"}[d.ResourceKind]}}
		}
		grants = append(grants, authz.Grant{Action: action, Scope: scope})
		if !seen[d.Page] {
			pages = append(pages, d.Page)
			seen[d.Page] = true
		}
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Preview operator", Pages: pages, Grants: grants})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "preview-operator", DisplayName: "Preview", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "preview-operator", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "preview-operator-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	choices, e := svc.Providers(user, "p1")
	if e != nil || len(choices) != 1 || choices[0].ID != "prov-1" {
		t.Fatal("minimal authorized provider", choices, e)
	}
	if _, e = svc.Providers(user, "unknown"); e == nil {
		t.Fatal("provider picker escaped project scope")
	}
	cfg := Config{ProjectID: "p1", Enabled: true, DNSProviderID: "prov-1", BaseDomain: "preview.example.com"}
	out, e := svc.SetConfig(user, cfg)
	if e != nil || out.AuthorizationState != "ready" {
		t.Fatal("explicit saved approval", out, e)
	}
	provision := func(pr int) {
		t.Helper()
		if _, e := svc.Provision(user, ProvisionInput{ProjectID: "p1", PRNumber: pr, ServerID: "server", UpstreamContainer: "app", UpstreamPort: 80, HostIP: "192.0.2.1"}); e != nil {
			t.Fatal(e)
		}
	}
	provision(1)
	provision(2)
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	checker := &fakeChecker{states: map[int]PRState{1: PRStateClosed, 2: PRStateOpen}}
	n, e := svc.SweepReclaim(ctx, checker)
	if e != nil || n != 1 || records != 1 || len(routes.deleted) != 1 {
		t.Fatal("logout must not revoke accepted automation", n, e, records, len(routes.deleted))
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Preview operator", ExpectedRevision: role.Revision, Pages: pages}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Providers(user, "p1"); e == nil {
		t.Fatal("logged out provider picker")
	}
	before := checker.calls
	checker.states[2] = PRStateClosed
	n, e = svc.SweepReclaim(ctx, checker)
	if e != nil || n != 0 || checker.calls != before || records != 1 || statusOf(t, svc, "p1", 2) != StatusActive {
		t.Fatal("revoked approval contacted provider or mutated", n, e, checker.calls, before)
	}
	out, e = svc.GetConfig(tenancy.WithActor(ctx, admin.Actor, team), "p1")
	if e != nil || out.AuthorizationState != "required" {
		t.Fatal("status must show reapproval required", out, e)
	}
}

package run

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
	"time"
)

func TestDashboardScopesIntersectEachModuleAtQueryTime(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	svc := New(db)
	first, second, foreignProject := seedProject(t, db), seedProject(t, db), seedProject(t, db)
	runIDs := []string{}
	for _, p := range []string{first, second, foreignProject} {
		r, e := svc.Create(ctx, p, Trigger{Type: TriggerManual, Branch: "main"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = svc.Cancel(ctx, r.ID); e != nil {
			t.Fatal(e)
		}
		runIDs = append(runIDs, r.ID)
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "admin-metrics-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	if e := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "admin-metrics-password")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='project' AND resource_id=?`, other.ID, foreignProject); e != nil {
		t.Fatal(e)
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics only", Pages: []string{"page.dora", "page.dashboard", "page.runs"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "dashboard.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "metrics", DisplayName: "Metrics", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "metrics", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "metrics-reader-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	dashboard, e := tenancy.WithDashboardScope(user, db)
	if e != nil {
		t.Fatal(e)
	}
	ms := NewMetricsService(svc)
	outside, e := ms.MetricsRecords(user, "", time.Time{})
	if e != nil || len(outside) != 2 {
		t.Fatal("ordinary module scope", len(outside), e)
	}
	rows, e := ms.MetricsRecords(dashboard, "", time.Time{})
	if e != nil || len(rows) != 1 || rows[0].ProjectID != first {
		t.Fatal("dashboard metrics intersection", rows, e)
	}
	listed, e := svc.List(dashboard, ListFilter{Page: 1, PageSize: 1})
	if e != nil || listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != runIDs[0] {
		t.Fatal("dashboard run count/paging", listed, e)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics only", Pages: []string{"page.dashboard", "page.runs", "page.dora"}, Grants: []authz.Grant{
		{Action: "dashboard.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}},
		{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{second}}},
		{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{second}}},
	}})
	if e != nil {
		t.Fatal(e)
	}
	rows, e = ms.MetricsRecords(dashboard, "", time.Time{})
	if e != nil || len(rows) != 0 {
		t.Fatal("disjoint scopes widened metrics", rows, e)
	}
	listed, e = svc.List(dashboard, ListFilter{Page: 1, PageSize: 1})
	if e != nil || listed.Total != 0 || len(listed.Items) != 0 {
		t.Fatal("disjoint scopes widened runs", listed, e)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics only", Pages: []string{"page.dora"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); e != nil {
		t.Fatal(e)
	}
	if _, e = tenancy.WithDashboardScope(user, db); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("missing dashboard action", e)
	}
	rows, e = ms.MetricsRecords(dashboard, "", time.Time{})
	if !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("retained dashboard ctx after revoke", rows, e)
	}
}

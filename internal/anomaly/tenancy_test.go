package anomaly

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestAlertReadUsesLiveServerScopeBeforeLimit(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	value := 90.0
	svc := New(db, stubCollector{snaps: []ServerMetricsSnapshot{{ServerID: "s1", ServerName: "Allowed", Available: true, DiskPercent: &value}, {ServerID: "s2", ServerName: "Hidden", Available: true, DiskPercent: &value}, {ServerID: "s3", ServerName: "Foreign", Available: true, DiskPercent: &value}}})
	defer svc.Close()
	if _, e := svc.CreateRule(ctx, CreateRuleInput{Metric: "disk", Operator: "gt", Threshold: 1, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.Check(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE anomaly_alerts SET created_at='2026-01-01T00:00:00Z' WHERE server_id='s1'`); e != nil {
		t.Fatal(e)
	}
	if e := auth.NewService(db, nil).Bootstrap("admin", "admin-alert-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(db)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(db)
	admin, e := accounts.Login(ctx, "admin", "admin-alert-password")
	if e != nil {
		t.Fatal(e)
	}
	var team string
	if e = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []struct{ id, team string }{{"s1", team}, {"s2", team}, {"s3", other.ID}} {
		if _, e = db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id) VALUES('server',?,?,?)`, s.id, s.team, admin.User.ID); e != nil {
			t.Fatal(e)
		}
	}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Alerts", Pages: []string{"page.anomalies", "page.servers", "page.dashboard"}, Grants: []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "anomaly.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s1"}}}, {Action: "dashboard.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "alerts", DisplayName: "Alerts", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := accounts.Login(ctx, "alerts", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = accounts.ChangePassword(ctx, login.Token, initial.Password, "alerts-reader-password")
	if e != nil {
		t.Fatal(e)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	rows, e := svc.ListAlerts(user, "", 1)
	if e != nil || len(rows) != 1 || rows[0].ServerID != "s1" {
		t.Fatal("filter before limit", rows, e)
	}
	for _, id := range []string{"s2", "s3", "unknown"} {
		rows, e = svc.ListAlerts(user, id, 1)
		if e != nil || len(rows) != 0 {
			t.Fatal("explicit server filter", id, rows, e)
		}
	}
	dashboard, e := tenancy.WithDashboardScope(user, db)
	if e != nil {
		t.Fatal(e)
	}
	rows, e = svc.ListAlerts(dashboard, "", 1)
	if e != nil || len(rows) != 1 {
		t.Fatal("server alert incorrectly treated as project", rows, e)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Alerts", Pages: []string{"page.anomalies", "page.servers"}, Grants: []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{"s2"}}}, {Action: "anomaly.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); e != nil {
		t.Fatal(e)
	}
	rows, e = svc.ListAlerts(user, "", 10)
	if e != nil || len(rows) != 1 || rows[0].ServerID != "s2" {
		t.Fatal("live prerequisite restriction", rows, e)
	}
	if _, e = svc.ListAlerts(dashboard, "", 10); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked dashboard context", e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.ListAlerts(user, "", 10); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logout", e)
	}
}

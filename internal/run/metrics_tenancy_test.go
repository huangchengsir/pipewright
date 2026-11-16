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

func TestDoraProjectScopesDoNotGrantRunDetails(t *testing.T) {
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
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics only", Pages: []string{"page.dora"}, Grants: []authz.Grant{{Action: "dora.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
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
	ms := NewMetricsService(svc)
	rows, e := ms.MetricsRecords(user, "", time.Time{})
	if e != nil || len(rows) != 1 || rows[0].RunID != runIDs[0] {
		t.Fatal("all-project metrics scope", rows, e)
	}
	for _, id := range []string{second, foreignProject, "unknown"} {
		rows, e = ms.MetricsRecords(user, id, time.Time{})
		if e != nil || len(rows) != 0 {
			t.Fatal("explicit out-of-scope project", id, rows, e)
		}
	}
	choices, e := ms.(MetricsProjectService).MetricsProjects(user)
	if e != nil || len(choices) != 1 || choices[0].ID != first {
		t.Fatal("independent minimal project picker", choices, e)
	}
	if _, e = svc.Get(user, runIDs[0]); !errors.Is(e, ErrNotFound) {
		t.Fatal("metrics permission granted raw run detail", e)
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics only", Pages: []string{"page.dora"}, Grants: []authz.Grant{}}); e != nil {
		t.Fatal(e)
	}
	rows, e = ms.MetricsRecords(user, "", time.Time{})
	if e != nil || len(rows) != 0 {
		t.Fatal("revoked metrics scope", rows, e)
	}
	choices, e = ms.(MetricsProjectService).MetricsProjects(user)
	if e != nil || len(choices) != 0 {
		t.Fatal("revoked picker scope", choices, e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = ms.MetricsRecords(user, "", time.Time{}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logged out metrics", e)
	}
}

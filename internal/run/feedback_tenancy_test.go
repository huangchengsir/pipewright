package run

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestFeedbackScopesAndWriteRevocation(t *testing.T) {
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
	fb := NewFeedbackService(db)
	for i, id := range runIDs {
		if _, err := fb.SaveFeedback(ctx, SaveFeedbackInput{RunID: id, Verdict: VerdictDown, CorrectRootCause: []string{"allowed correction", "hidden correction", "foreign correction"}[i]}); err != nil {
			t.Fatal(err)
		}
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
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Metrics only", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}}}})
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
	stats, e := fb.GetStats(user)
	if e != nil || stats.TotalFeedback != 1 || stats.ThumbsDown != 1 || len(stats.RecentCorrections) != 1 || stats.RecentCorrections[0].RunID != runIDs[0] || stats.RecentCorrections[0].CorrectRootCause != "allowed correction" {
		t.Fatal("filtered feedback", stats, e)
	}
	count := 0
	for _, bucket := range stats.RecentTrend {
		count += bucket.Count
	}
	if count != 1 {
		t.Fatal("filtered trend", stats.RecentTrend)
	}
	if _, e = fb.SaveFeedback(user, SaveFeedbackInput{RunID: runIDs[0], Verdict: VerdictUp}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("read-only feedback write", e)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics only", Pages: []string{"page.runs"}, Grants: []authz.Grant{
		{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}},
		{Action: "run.diagnose", Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{first}}},
	}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fb.SaveFeedback(user, SaveFeedbackInput{RunID: runIDs[0], Verdict: VerdictUp}); e != nil {
		t.Fatal("authorized feedback", e)
	}
	for _, id := range runIDs[1:] {
		if _, e = fb.SaveFeedback(user, SaveFeedbackInput{RunID: id, Verdict: VerdictUp}); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("out of scope feedback", id, e)
		}
	}
	for _, id := range runIDs[1:] {
		var verdict string
		if e = db.QueryRow(`SELECT verdict FROM diagnosis_feedback WHERE run_id=?`, id).Scan(&verdict); e != nil || verdict != VerdictDown {
			t.Fatal("denied write changed row", verdict, e)
		}
	}
	if _, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Metrics only", Pages: []string{"page.runs"}, Grants: []authz.Grant{}}); e != nil {
		t.Fatal(e)
	}
	if _, e = fb.SaveFeedback(user, SaveFeedbackInput{RunID: runIDs[0], Verdict: VerdictDown}); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked feedback write", e)
	}
	stats, e = fb.GetStats(user)
	if e != nil || stats.TotalFeedback != 0 || len(stats.RecentCorrections) != 0 || len(stats.RecentTrend) != 0 {
		t.Fatal("revoked stats", stats, e)
	}
	if e = accounts.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = fb.GetStats(user); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logged out stats", e)
	}
}

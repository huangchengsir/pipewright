package environments

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

func TestTenantEnvironmentHistoryScopesBeforeAggregation(t *testing.T) {
	s, db := testService(t)
	ctx := context.Background()
	project := seedProject(t, db)
	foreign := seedProject(t, db)
	for _, entry := range []struct{ p, e string }{{project, "test"}, {project, "prod"}, {foreign, "test"}} {
		for i := 0; i < 2; i++ {
			id := seedRun(t, db, entry.p, entry.e, "commit-"+entry.e)
			seedTarget(t, db, id, "server-"+entry.e, "success", time.Now().Add(time.Duration(i)*time.Minute))
			seedArtifact(t, db, id, "dist", entry.e, "artifact-"+entry.e)
		}
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "admin-environment-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team, envID string
	if err := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM access_environments WHERE project_id=? AND name='test'`, project).Scan(&envID); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-environment-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Other")
	if err != nil {
		t.Fatal(err)
	}
	// Move the complete fixture ownership, not only a display label.
	if _, err = db.Exec(`UPDATE access_ownership SET tenant_id=? WHERE project_id=? OR (resource_kind='project' AND resource_id=?)`, other.ID, foreign, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE access_environments SET tenant_id=? WHERE project_id=?`, other.ID, foreign); err != nil {
		t.Fatal(err)
	}
	scope := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{envID}, EnvironmentIDs: []string{envID}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Environment reader", Pages: []string{"page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{envID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "reader-environment-password")
	if err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	choices, err := s.ProjectChoices(actor)
	if err != nil || len(choices) != 1 || choices[0].ID != project {
		t.Fatal("project choices escaped environment scope", choices, err)
	}
	rows, err := s.ListEnvironments(actor, project, 1)
	if err != nil || len(rows) != 1 || rows[0].Environment != "test" || rows[0].EnvironmentID != envID || len(rows[0].Deployments) != 1 {
		t.Fatal("scoped aggregate", rows, err)
	}
	if rows[0].Deployments[0].Artifacts[0].Reference != "artifact-test" {
		t.Fatal("wrong artifact projection", rows)
	}
	for _, name := range []string{"prod", "unknown"} {
		if _, err = s.EnvironmentHistory(actor, project, name, 0); !errors.Is(err, ErrEnvNotFound) {
			t.Fatal("hidden detail", name, err)
		}
	}
	for _, id := range []string{foreign, "unknown"} {
		if _, err = s.ListEnvironments(actor, id, 0); !errors.Is(err, ErrProjectNotFound) {
			t.Fatal("project existence leak", id, err)
		}
	}
	if _, err = s.ResolveRollback(actor, project, "test"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("read granted rollback", err)
	}
	// Explicit rollback action, still bounded to the selected test environment.
	_, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{ExpectedRevision: role.Revision, Name: "Environment reader", Pages: []string{"page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}, {Action: "release.rollback", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	if target, err := s.ResolveRollback(actor, project, "test"); err != nil || target.ArtifactID == "" {
		t.Fatal("explicit rollback", target, err)
	}
	if _, err = s.ResolveRollback(actor, project, "prod"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("scope escaped", err)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListEnvironments(actor, project, 0); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked read", err)
	}
}

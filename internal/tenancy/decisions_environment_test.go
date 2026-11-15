package tenancy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestDecisionCannotRelabelStoredEnvironment(t *testing.T) {
	a, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Decision isolation")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('test-env',?,'p','test','test',1)`,
		`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('prod-env',?,'p','prod','production',1)`,
		`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('environment','test-env',?,'p','test-env')`,
		`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('environment','prod-env',?,'p','prod-env')`,
	} {
		if _, err = r.db.Exec(stmt, team); err != nil {
			t.Fatal(err)
		}
	}
	scope := authz.Scope{Kind: authz.ScopeTeam, EnvironmentPolicy: "test"}
	role, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Test rollback", Pages: []string{"page.environments"}, Grants: []authz.Grant{{Action: "environment.read", Scope: scope}, {Action: "release.rollback", Scope: scope}}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.Create(ctx, admin, CreateUserInput{Username: "envdecision", DisplayName: "Environment decision", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{"test-env", "prod-env"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.Login(ctx, "envdecision", created.Password)
	if err != nil {
		t.Fatal(err)
	}
	session, err = a.ChangePassword(ctx, session.Token, created.Password, "environment-decision-password")
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Decisions(ctx, session.Actor, team, []DecisionQuery{{Action: "release.rollback", ResourceID: "test-env", EnvironmentID: "test-env"}, {Action: "release.rollback", ResourceID: "prod-env", EnvironmentID: "prod-env"}, {Action: "release.rollback", ResourceID: "prod-env", EnvironmentID: "test-env"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed[0] || result.Allowed[1] {
		t.Fatalf("invalid positive/negative controls: %v", result.Allowed)
	}
	if result.Allowed[2] {
		t.Fatalf("stored production environment relabeled by presentation query: %v", result.Allowed)
	}
}

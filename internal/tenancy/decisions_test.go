package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestPermissionDecisionBatchUsesCurrentResourcesAndPolicies(t *testing.T) {
	a, r, admin := accountFixture(t)
	ctx := context.Background()
	team, e := r.CreateTeam(ctx, "Decisions")
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"p1", "p2"} {
		if _, e = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES('project',?,?,?,?)`, id, team, admin.UserID, id); e != nil {
			t.Fatal(e)
		}
	}
	role, e := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Edit one", Pages: []string{"page.projects"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "project.edit", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := a.Create(ctx, admin, CreateUserInput{Username: "decisionuser", DisplayName: "Decisions", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p1"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := a.Login(ctx, "decisionuser", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = a.ChangePassword(ctx, login.Token, initial.Password, "decision-user-password")
	if e != nil {
		t.Fatal(e)
	}
	inputs := []DecisionQuery{{Action: "project.edit", ResourceID: "p1"}, {Action: "project.edit", ResourceID: "p2"}, {Action: "project.read", ResourceID: "unknown"}, {Action: "future", ResourceID: "p1"}, {Action: "project.edit", Create: true}, {Action: "project.delete", ResourceID: "p1"}}
	out, e := r.Decisions(ctx, login.Actor, team, inputs)
	if e != nil {
		t.Fatal(e)
	}
	if out.UserID != login.User.ID || out.TeamID != team || out.CatalogVersion != authz.CatalogVersion || out.ModeEpoch == 0 || out.Revision == 0 {
		t.Fatal("unbound batch", out)
	}
	for i, got := range out.Allowed {
		if got != (i == 0) {
			t.Fatal("decision", i, got)
		}
	}
	if _, e = r.Decisions(ctx, login.Actor, team, make([]DecisionQuery, 65)); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("unbounded batch", e)
	}
	if e = a.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Decisions(ctx, login.Actor, team, inputs); !errors.Is(e, ErrForbidden) {
		t.Fatal("stale session decision", e)
	}
}

func TestRunTriggerDecisionUsesRealProjectAndNewRunOwner(t *testing.T) {
	a, r, admin := accountFixture(t)
	ctx := context.Background()
	team, e := r.CreateTeam(ctx, "Decisions")
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"p1", "p2"} {
		if _, e = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id) VALUES('project',?,?,?,?)`, id, team, admin.UserID, id); e != nil {
			t.Fatal(e)
		}
	}
	role, e := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Edit one", Pages: []string{"page.runs"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.start", Scope: authz.Scope{Kind: authz.ScopeOwn}}}})
	if e != nil {
		t.Fatal(e)
	}
	initial, e := a.Create(ctx, admin, CreateUserInput{Username: "decisionuser", DisplayName: "Decisions", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p1"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := a.Login(ctx, "decisionuser", initial.Password)
	if e != nil {
		t.Fatal(e)
	}
	login, e = a.ChangePassword(ctx, login.Token, initial.Password, "decision-user-password")
	if e != nil {
		t.Fatal(e)
	}
	inputs := []DecisionQuery{
		{Action: "run.start", ResourceID: "p1", Create: true},
		{Action: "run.start", ResourceID: "p2", Create: true},
		{Action: "run.start", ResourceID: "unknown", Create: true},
		{Action: "run.start", Create: true},
		{Action: "run.start", ResourceID: "p1"},
		{Action: "run.start", ResourceID: "p1", Create: true, EnvironmentID: "forged"},
		{Action: "run.cancel", ResourceID: "p1", Create: true},
	}

	out, e := r.Decisions(ctx, login.Actor, team, inputs)
	if e != nil {
		t.Fatal(e)
	}
	if out.UserID != login.User.ID || out.TeamID != team || out.CatalogVersion != authz.CatalogVersion || out.ModeEpoch == 0 || out.Revision == 0 {
		t.Fatal("unbound batch", out)
	}
	for i, got := range out.Allowed {
		if got != (i == 0) {
			t.Fatal("decision", i, got)
		}
	}
	if _, e = r.Decisions(ctx, login.Actor, team, make([]DecisionQuery, 65)); !errors.Is(e, ErrInvalidInput) {
		t.Fatal("unbounded batch", e)
	}
	if e = a.Logout(ctx, login.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Decisions(ctx, login.Actor, team, inputs); !errors.Is(e, ErrForbidden) {
		t.Fatal("stale session decision", e)
	}
}

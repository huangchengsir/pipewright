package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"testing"
)

func TestRoleMemberLifecycleAndLastAdministrator(t *testing.T) {
	accounts, r, actor := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Team")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	var teamAdmin authz.Role
	for _, role := range roles {
		if role.PresetKey == "team-admin" {
			teamAdmin = role
		}
	}
	self, err := r.SaveMember(ctx, actor, team, "", MemberInput{UserID: actor.UserID, Enabled: true, Bindings: []MemberRoleInput{{RoleID: teamAdmin.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.DeleteRole(ctx, actor, team, teamAdmin.ID, 1); !errors.Is(err, ErrProtectedAccount) {
		t.Fatal("last team-admin role deleted", err)
	}
	if _, err = r.SaveMember(ctx, actor, team, self.ID, MemberInput{UserID: actor.UserID, Enabled: false, ExpectedRevision: self.Revision}); !errors.Is(err, ErrProtectedAccount) {
		t.Fatal("last team administrator disabled", err)
	}
	custom, err := r.SaveRole(ctx, actor, team, "", RoleInput{Name: "Custom reader", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project','p',?,'p')`, team); err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, actor, CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := r.SaveMember(ctx, actor, team, "", MemberInput{UserID: initial.User.ID, Enabled: true, Bindings: []MemberRoleInput{{RoleID: custom.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Revision != 1 {
		t.Fatal(assigned)
	}
	session, err := accounts.Login(ctx, "alice", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	session, err = accounts.ChangePassword(ctx, session.Token, initial.Password, "alice-updated-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = Check(WithActor(ctx, session.Actor, team), r.db, "project.read", "project", "p"); err != nil {
		t.Fatal(err)
	}
	if err = r.DeleteRole(ctx, actor, team, custom.ID, custom.Revision); err != nil {
		t.Fatal("ordinary bound role could not be revoked", err)
	}
	if err = Check(WithActor(ctx, session.Actor, team), r.db, "project.read", "project", "p"); !errors.Is(err, ErrForbidden) {
		t.Fatal("deleted role retained authority", err)
	}
	if err = r.EnsurePresets(ctx, team); err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`UPDATE selfupdate_control SET task_id='migration' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveRole(ctx, actor, team, "", RoleInput{Name: "Blocked"}); !errors.Is(err, configstate.ErrMaintenance) {
		t.Fatal("role wrote during maintenance", err)
	}
}
func TestTeamManagerCannotDelegateSensitiveActionsOrCrossTeamRole(t *testing.T) {
	accounts, r, actor := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Team")
	if err != nil {
		t.Fatal(err)
	}
	roles, _ := r.Roles(ctx, team)
	var teamAdmin authz.Role
	for _, role := range roles {
		if role.PresetKey == "team-admin" {
			teamAdmin = role
		}
	}
	initial, err := accounts.Create(ctx, actor, CreateUserInput{Username: "manager", DisplayName: "Manager"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveMember(ctx, actor, team, "", MemberInput{UserID: initial.User.ID, Enabled: true, Bindings: []MemberRoleInput{{RoleID: teamAdmin.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "manager", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := accounts.ChangePassword(ctx, login.Token, initial.Password, "manager-updated-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.SaveRole(ctx, manager.Actor, team, "", RoleInput{Name: "Escalation", Pages: []string{"page.servers"}, Grants: []authz.Grant{{Action: "server.inspect", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "server.terminal", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatal("manager granted new terminal power", err)
	}
	if _, err = r.SaveRole(ctx, actor, team, "", RoleInput{Name: "Global page", Pages: []string{"page.system"}}); !errors.Is(err, authz.ErrInvalidPolicy) {
		t.Fatal("team role accepted global page", err)
	}
	other, _ := r.CreateTeam(ctx, "Other")
	otherRoles, _ := r.Roles(ctx, other)
	_, err = r.SaveMember(ctx, manager.Actor, team, "", MemberInput{UserID: actor.UserID, Enabled: true, Bindings: []MemberRoleInput{{RoleID: otherRoles[0].ID, Scope: authz.Scope{Kind: authz.ScopeNone}}}})
	if err == nil {
		t.Fatal("cross-team role assignment accepted")
	}
	if err = accounts.SetEnabled(ctx, actor, manager.User.ID, false); !errors.Is(err, ErrProtectedAccount) {
		t.Fatal("last team admin global account disabled", err)
	}
	// Editing only the existing template's label does not invent new permissions.
	_, err = r.SaveRole(ctx, manager.Actor, team, teamAdmin.ID, RoleInput{Name: "Team administration", Pages: teamAdmin.Pages, Grants: teamAdmin.Grants, ExpectedRevision: 1})
	if err != nil {
		t.Fatal("safe rename denied", err)
	}
}

func TestRoleReadPrerequisiteExpansionCannotAmplifyUnchangedWrite(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Delegation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id) VALUES('project','x',?,'x')`, team); err != nil {
		t.Fatal(err)
	}
	whole := authz.Scope{Kind: authz.ScopeTeam}
	selected := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"x"}}
	managerRole, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Limited manager", Pages: []string{"page.projects", "page.roles"}, Grants: []authz.Grant{{Action: "project.read", Scope: whole}, {Action: "project.edit", Scope: selected}, {Action: "role.read", Scope: whole}, {Action: "role.manage", Scope: whole}}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Restricted target", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: selected}, {Action: "project.edit", Scope: whole}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "delegator", DisplayName: "Delegator", Assignments: []MemberAssignment{{TeamID: team, RoleID: managerRole.ID, Scope: whole}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "delegator", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "delegator-next-password")
	if err != nil {
		t.Fatal(err)
	}
	input := RoleInput{Name: target.NameKey, Pages: target.Pages, Grants: []authz.Grant{{Action: "project.read", Scope: whole}, {Action: "project.edit", Scope: whole}}, ExpectedRevision: target.Revision}
	if _, err = r.SaveRole(ctx, login.Actor, team, target.ID, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("read expansion amplified unchanged write", err)
	}
	actual, err := r.ReadRoles(ctx, admin, team)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range actual {
		if role.ID == target.ID && role.Revision != target.Revision {
			t.Fatal("denied mutation persisted")
		}
	}
	input.Grants = target.Grants
	input.Name = "Safe rename"
	if _, err = r.SaveRole(ctx, login.Actor, team, target.ID, input); err != nil {
		t.Fatal("safe rename rejected", err)
	}
}

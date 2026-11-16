package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestManagementReadersRespectTeamAndRevocation(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeamAs(ctx, admin, "Readers")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.ReadRoles(ctx, admin, team.ID)
	if err != nil || len(roles) != 4 {
		t.Fatal(len(roles), err)
	}
	var viewer RoleView
	for _, role := range roles {
		if role.PresetKey == "viewer" {
			viewer = role
		}
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "reader", DisplayName: "Reader", Assignments: []MemberAssignment{{TeamID: team.ID, RoleID: viewer.ID, Scope: authz.Scope{Kind: authz.ScopeSelected}}}})
	if err != nil {
		t.Fatal(err)
	}
	members, err := r.ReadMembers(ctx, admin, team.ID)
	if err != nil || len(members) != 1 || members[0].UserID != initial.User.ID {
		t.Fatal(members, err)
	}
	s, err := accounts.Login(ctx, "reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	s, err = accounts.ChangePassword(ctx, s.Token, initial.Password, "reader-new-password")
	if err != nil {
		t.Fatal(err)
	}
	teams, err := r.Teams(ctx, s.Actor)
	if err != nil || len(teams) != 1 || teams[0].ID != team.ID {
		t.Fatal(teams, err)
	}
	noRoles, err := r.ReadRoles(ctx, s.Actor, team.ID)
	if err != nil || len(noRoles) != 0 {
		t.Fatal(noRoles, err)
	}
	noMembers, err := r.ReadMembers(ctx, s.Actor, team.ID)
	if err != nil || len(noMembers) != 0 {
		t.Fatal(noMembers, err)
	}
	other, err := r.CreateTeamAs(ctx, admin, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadRoles(ctx, s.Actor, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-team", err)
	}
	if _, err = r.LookupExisting(ctx, s.Actor, team.ID, "admin"); !errors.Is(err, ErrForbidden) {
		t.Fatal("directory lookup", err)
	}
	if _, err = r.CreateTeamAs(ctx, s.Actor, "Forbidden"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err = accounts.Logout(ctx, s.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Teams(ctx, s.Actor); !errors.Is(err, ErrForbidden) {
		t.Fatal("ended session", err)
	}
}

func TestRowSpecificManagementDoesNotRequireCreatePermission(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Row permissions")
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Target"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	managerRole, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Specific manager", Pages: []string{"page.roles"}, Grants: []authz.Grant{{Action: "role.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "role.manage", Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "role", ID: target.ID}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "rowmanager", DisplayName: "Row manager", Assignments: []MemberAssignment{{TeamID: team, RoleID: managerRole.ID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "rowmanager", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "row-manager-password")
	if err != nil {
		t.Fatal(err)
	}
	caps, err := r.ManagementCapabilities(ctx, login.Actor, team)
	if err != nil || caps["role.manage"] {
		t.Fatal(caps, err)
	}
	roles, err := r.ReadRoles(ctx, login.Actor, team)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, role := range roles {
		if role.ID == target.ID {
			found = role.CanManage
		}
		if role.ID == other.ID && role.CanManage {
			t.Fatal("other role editable")
		}
	}
	if !found {
		t.Fatal("target row did not expose edit capability")
	}
	if _, err = r.SaveRole(ctx, login.Actor, team, target.ID, RoleInput{Name: "Renamed target", ExpectedRevision: target.Revision}); err != nil {
		t.Fatal("selected role could not be edited", err)
	}
	if _, err = r.SaveRole(ctx, login.Actor, team, other.ID, RoleInput{Name: "Forbidden", ExpectedRevision: other.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

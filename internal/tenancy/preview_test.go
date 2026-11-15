package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestEffectivePreviewSeparatesDraftAndPreservesPolicy(t *testing.T) {
	accounts, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('project','p',?,?,'p','')`, team, admin.UserID); err != nil {
		t.Fatal(err)
	}
	role, err := r.SaveRole(ctx, admin, team, "", RoleInput{Name: "Reader", Pages: []string{"page.projects"}, Grants: []authz.Grant{{Action: "project.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin, CreateUserInput{Username: "previewuser", DisplayName: "Preview", Assignments: []MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	members, err := r.ReadMembers(ctx, admin, team)
	if err != nil || len(members) != 1 {
		t.Fatal(members, err)
	}
	input := PreviewInput{MemberID: members[0].ID, Action: "project.edit", ResourceID: "p", DraftRoleID: role.ID, Draft: &RoleInput{Name: "Reader", ExpectedRevision: role.Revision, Pages: role.Pages, RequiresSelectedBinding: true, Grants: append(append([]authz.Grant{}, role.Grants...), authz.Grant{Action: "project.edit", Scope: authz.Scope{Kind: authz.ScopeTeam}})}}
	restricted, restrictedErr := r.Preview(ctx, admin, team, input)
	if restrictedErr != nil || restricted.CurrentAllowed || restricted.DraftAllowed == nil || *restricted.DraftAllowed {
		t.Fatal("must-change subject appeared usable", restricted, restrictedErr)
	}
	login, err := accounts.Login(ctx, "previewuser", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "preview-user-password")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := r.Mode(ctx)
	out, err := r.Preview(ctx, admin, team, input)
	if err != nil || out.CurrentAllowed || out.DraftAllowed == nil || !*out.DraftAllowed {
		t.Fatal(out, err)
	}
	after, _ := r.Mode(ctx)
	if before != after {
		t.Fatal("preview mutated policy state")
	}
	input.Draft = nil
	input.DraftRoleID = ""
	out, err = r.Preview(ctx, admin, team, input)
	if err != nil || out.CurrentAllowed || out.DraftAllowed != nil {
		t.Fatal(out, err)
	}
	if _, err = r.Preview(ctx, login.Actor, team, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("member read bypass", err)
	}
	input.ResourceID = "hidden"
	if _, err = r.Preview(ctx, admin, team, input); !errors.Is(err, ErrForbidden) {
		t.Fatal("unknown resource", err)
	}
}

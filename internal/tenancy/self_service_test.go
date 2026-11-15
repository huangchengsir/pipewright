package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"testing"
)

func TestSelfSessionsAndOrphanAccountDirectory(t *testing.T) {
	a, r, admin := accountFixture(t)
	ctx := context.Background()
	initial, err := a.Create(ctx, admin, CreateUserInput{Username: "orphan", DisplayName: "Orphan"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.Users(ctx, admin, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, user := range page.Users {
		found = found || user.ID == initial.User.ID
	}
	if !found {
		t.Fatal("unassigned account missing")
	}
	s, err := a.Login(ctx, "orphan", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	s, err = a.ChangePassword(ctx, s.Token, initial.Password, "orphan-next-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Users(ctx, s.Actor, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary directory access", err)
	}
	second, err := a.Login(ctx, "orphan", "orphan-next-password")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := a.Sessions(ctx, s.Actor)
	if err != nil || len(sessions) != 2 {
		t.Fatal(sessions, err)
	}
	if err = a.RevokeSession(ctx, s.Actor, admin.SessionHash); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-account revoke", err)
	}
	if err = a.RevokeSession(ctx, s.Actor, second.Actor.SessionHash); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Verify(ctx, second.Token); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatal(err)
	}
	team, err := r.CreateTeam(ctx, "Lookup")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	var roleID string
	for _, role := range roles {
		if role.PresetKey == "team-admin" {
			roleID = role.ID
		}
	}
	if _, err = r.SaveMember(ctx, admin, team, "", MemberInput{UserID: s.User.ID, Enabled: true, Bindings: []MemberRoleInput{{RoleID: roleID, Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		_, err = r.LookupExisting(ctx, s.Actor, team, "missing")
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatal(i, err)
		}
	}
	_, err = r.LookupExisting(ctx, s.Actor, team, "admin")
	var limited *auth.ErrLockedOut
	if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatal("lookup rate limit", err)
	}
}

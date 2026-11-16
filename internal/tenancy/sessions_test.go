package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"testing"
	"time"
)

func TestInitialCredentialOneUseRestrictedSessionAndRotation(t *testing.T) {
	a, _, admin := accountFixture(t)
	ctx := context.Background()
	created, err := a.Create(ctx, admin, CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.Login(ctx, "alice", created.Password)
	if err != nil {
		t.Fatal(err)
	}
	if !session.User.MustChangePassword || session.ExpiresAt > time.Now().Add(6*time.Minute).Unix() {
		t.Fatal("initial session not restricted")
	}
	for _, op := range []string{"mcp.issue", "oauth.bind", "team.select", "project.read", "self.profile", "aiops.use", ""} {
		if RestrictedAllowed(session, op) {
			t.Fatal("restricted session permitted", op)
		}
	}
	if !RestrictedAllowed(session, "self.password") {
		t.Fatal("cannot complete initial password")
	}
	if _, err = a.Login(ctx, "alice", created.Password); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("temporary password replay", err)
	}
	if err = a.UpdateDisplayName(ctx, session.Actor, session.User.ID, "not yet"); !errors.Is(err, ErrForbidden) {
		t.Fatal("restricted profile write allowed", err)
	}
	if _, err = a.ChangePassword(ctx, session.Token, "wrong-password", "permanent-new-password"); !errors.Is(err, auth.ErrInvalidCurrentPassword) {
		t.Fatal(err)
	}
	rotated, err := a.ChangePassword(ctx, session.Token, created.Password, "permanent-new-password")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == session.Token || rotated.User.MustChangePassword {
		t.Fatal("not rotated")
	}
	if _, err = a.Verify(ctx, session.Token); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatal("old token retained", err)
	}
	if _, err = a.Verify(ctx, rotated.Token); err != nil {
		t.Fatal(err)
	}
	if err = a.UpdateDisplayName(ctx, rotated.Actor, rotated.User.ID, "Alice 2"); err != nil {
		t.Fatal(err)
	}
	if err = a.Logout(ctx, rotated.Token); err != nil {
		t.Fatal(err)
	}
	if err = a.UpdateDisplayName(ctx, rotated.Actor, rotated.User.ID, "stale session"); !errors.Is(err, ErrForbidden) {
		t.Fatal("logged out actor wrote profile", err)
	}
}
func TestExpiredTemporaryCredentialAndSession(t *testing.T) {
	a, _, admin := accountFixture(t)
	ctx := context.Background()
	created, err := a.Create(ctx, admin, CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`UPDATE access_users SET password_expires_at=1 WHERE id=?`, created.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Login(ctx, "alice", created.Password); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("expired temporary credential accepted", err)
	}
	reset, err := a.ResetPassword(ctx, admin, created.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.Login(ctx, "alice", reset.Password)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Unix(session.ExpiresAt+1, 0) }
	if _, err = a.Verify(ctx, session.Token); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatal("expired temporary session accepted", err)
	}
}
func TestBootstrapPasswordSynchronizesLegacyCredential(t *testing.T) {
	a, _, _ := accountFixture(t)
	ctx := context.Background()
	old := auth.NewService(a.db, nil)
	if err := old.Bootstrap("admin", "existing-admin-password"); err != nil {
		t.Fatal(err)
	}
	session, err := a.Login(ctx, "admin", "existing-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.ChangePassword(ctx, session.Token, "existing-admin-password", "replacement-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`UPDATE access_control SET enabled=0,mode_epoch=mode_epoch+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = old.Login("admin", "existing-admin-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("old password resurrected", err)
	}
	if _, err = old.Login("admin", "replacement-admin-password"); err != nil {
		t.Fatal("new password unavailable after mode off", err)
	}
	if _, err = a.Verify(ctx, session.Token); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatal("old tenant session retained", err)
	}
}
func TestUnknownRestrictedSessionAlwaysDenied(t *testing.T) {
	if RestrictedAllowed(Session{}, "self.password") {
		t.Fatal("empty session allowed")
	}
}

func TestCanonicalUsernameAndLockoutCannotBeSplitByCase(t *testing.T) {
	a, _, admin := accountFixture(t)
	ctx := context.Background()
	created, err := a.Create(ctx, admin, CreateUserInput{Username: "Foo", DisplayName: "Foo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Create(ctx, admin, CreateUserInput{Username: "foo", DisplayName: "Other"}); !errors.Is(err, ErrAccountConflict) {
		t.Fatal("case duplicate accepted", err)
	}
	variants := []string{"foo", "FOO", "Foo", "fOo", "foO"}
	for _, username := range variants {
		if _, err = a.Login(ctx, username, "incorrect"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	if _, err = a.Login(ctx, "FoO", created.Password); err == nil {
		t.Fatal("case variant bypassed lockout")
	} else {
		var locked *auth.ErrLockedOut
		if !errors.As(err, &locked) {
			t.Fatal(err)
		}
	}
}

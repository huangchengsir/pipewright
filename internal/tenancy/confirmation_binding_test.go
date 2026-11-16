package tenancy

import (
	"context"
	"testing"
)

func TestConfirmationBindingSeparatesSessionTeamAndRevocation(t *testing.T) {
	accounts, repo, actor := accountFixture(t)
	ctx := context.Background()
	team, e := repo.CreateTeam(ctx, "First")
	if e != nil {
		t.Fatal(e)
	}
	other, e := repo.CreateTeam(ctx, "Other")
	if e != nil {
		t.Fatal(e)
	}
	first, e := ConfirmationBinding(WithActor(ctx, actor, team), accounts.db)
	if e != nil || len(first) != 64 {
		t.Fatal(first, e)
	}
	repeated, e := ConfirmationBinding(WithActor(ctx, actor, team), accounts.db)
	if e != nil || repeated != first {
		t.Fatal("unstable binding", e)
	}
	secondTeam, e := ConfirmationBinding(WithActor(ctx, actor, other), accounts.db)
	if e != nil || secondTeam == first {
		t.Fatal("team not bound", e)
	}
	fresh, e := accounts.Login(ctx, "admin", "existing-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	next, e := ConfirmationBinding(WithActor(ctx, fresh.Actor, team), accounts.db)
	if e != nil || next == first {
		t.Fatal("new login reused confirmation binding", e)
	}
	if e = accounts.Logout(ctx, "admin-session"); e != nil {
		t.Fatal(e)
	}
	if _, e = ConfirmationBinding(WithActor(ctx, actor, team), accounts.db); e == nil {
		t.Fatal("logged out initiator accepted")
	}
	if _, e = accounts.db.Exec(`UPDATE access_users SET security_generation=security_generation+1 WHERE id='admin'`); e != nil {
		t.Fatal(e)
	}
	if _, e = ConfirmationBinding(WithActor(ctx, fresh.Actor, team), accounts.db); e == nil {
		t.Fatal("old security generation accepted")
	}
}

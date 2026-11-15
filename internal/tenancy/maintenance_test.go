package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"testing"
)

func TestAccountWritesRespectMigrationMaintenance(t *testing.T) {
	accounts, r, actor := accountFixture(t)
	ctx := context.Background()
	initial, err := accounts.Create(ctx, actor, CreateUserInput{Username: "waiting", DisplayName: "Waiting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = accounts.db.Exec(`UPDATE selfupdate_control SET task_id='db-migration-test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	before, err := r.Mode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = accounts.Create(ctx, actor, CreateUserInput{Username: "blocked", DisplayName: "Blocked"}); !errors.Is(err, configstate.ErrMaintenance) {
		t.Fatal("create bypassed maintenance", err)
	}
	if err = accounts.UpdateDisplayName(ctx, actor, actor.UserID, "Changed"); !errors.Is(err, configstate.ErrMaintenance) {
		t.Fatal("profile bypassed maintenance", err)
	}
	if _, err = r.CreateTeam(ctx, "Blocked team"); !errors.Is(err, configstate.ErrMaintenance) {
		t.Fatal("team bypassed maintenance", err)
	}
	if _, err = accounts.Login(ctx, "waiting", initial.Password); !errors.Is(err, configstate.ErrMaintenance) {
		t.Fatal("initial credential consumed during migration", err)
	}
	var consumed any
	if err = accounts.db.QueryRow(`SELECT password_consumed_at FROM access_users WHERE id=?`, initial.User.ID).Scan(&consumed); err != nil || consumed != nil {
		t.Fatal("temporary credential mutated", consumed, err)
	}
	after, err := r.Mode(ctx)
	if err != nil || after != before {
		t.Fatal("blocked writes changed revision", before, after, err)
	}
	// Normal sign-in and logout remain available for supervising/cancelling maintenance.
	session, err := accounts.Login(ctx, "admin", "existing-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = accounts.Logout(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = accounts.Verify(ctx, session.Token); err == nil {
		t.Fatal("maintenance logout failed")
	}
}

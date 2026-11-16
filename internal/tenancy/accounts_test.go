package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"strings"
	"testing"
	"time"
)

func accountFixture(t *testing.T) (*Accounts, *Repository, Actor) {
	t.Helper()
	r, s := testRepository(t)
	hash, err := auth.HashPassword("existing-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO access_users(id,username,username_key,display_name,password_hash,enabled,instance_admin,must_change_password,password_expires_at,security_generation,created_at,updated_at) VALUES('admin','admin','admin','Admin',?,1,1,0,NULL,1,1,1)`, hash)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`UPDATE access_control SET enabled=1,ever_enabled=1,bootstrap_user_id='admin' WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO access_sessions(token_hash,user_id,csrf_token,mode_epoch,security_generation,auth_source,created_at,expires_at,last_seen_at,password_verified_at) VALUES(?,'admin','csrf',1,1,'password',1,9999999999,1,1)`, tokenHash("admin-session"))
	if err != nil {
		t.Fatal(err)
	}
	return NewAccounts(s.DB), r, Actor{UserID: "admin", SessionHash: tokenHash("admin-session"), ModeEpoch: 1, SecurityGeneration: 1}
}
func TestCreateAccountAndBindingAtomic(t *testing.T) {
	a, r, actor := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "Team")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	var publisher string
	for _, role := range roles {
		if role.PresetKey == "publisher" {
			publisher = role.ID
		}
	}
	_, err = a.db.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('project','p',?,NULL,'p',NULL)`, team)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('test-id',?,'p','Testing','test',1)`, team)
	if err != nil {
		t.Fatal(err)
	}
	assignment := MemberAssignment{TeamID: team, RoleID: publisher, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}, EnvironmentIDs: []string{"test-id"}}}
	result, err := a.Create(ctx, actor, CreateUserInput{Username: "alice", DisplayName: "Alice", Assignments: []MemberAssignment{assignment}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Password) < 24 || !result.User.MustChangePassword || result.ExpiresAt <= time.Now().Unix() {
		t.Fatal("missing initial credential constraints")
	}
	var hash string
	if err = a.db.QueryRow(`SELECT password_hash FROM access_users WHERE id=?`, result.User.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == result.Password {
		t.Fatal("plaintext persisted")
	}
	ok, err := auth.VerifyPassword(result.Password, hash)
	if err != nil || !ok {
		t.Fatal("credential mismatch", err)
	}
	body, _ := json.Marshal(result.User)
	if strings.Contains(string(body), result.Password) || strings.Contains(string(body), hash) {
		t.Fatal("secret in profile")
	}
	var n int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM access_bindings b JOIN access_members m ON m.id=b.member_id WHERE m.user_id=?`, result.User.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	assignment.RoleID = "missing"
	_, err = a.Create(ctx, actor, CreateUserInput{Username: "rollback", DisplayName: "Rollback", Assignments: []MemberAssignment{assignment}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM access_users WHERE username='rollback'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial account left", n, err)
	}
}
func TestAccountProfileAndAdministratorBoundaries(t *testing.T) {
	a, _, actor := accountFixture(t)
	ctx := context.Background()
	created, err := a.Create(ctx, actor, CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	user := Actor{UserID: created.User.ID, ModeEpoch: 1, SecurityGeneration: 1}
	if _, err = a.Create(ctx, user, CreateUserInput{Username: "evil", DisplayName: "evil"}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err = a.UpdateDisplayName(ctx, user, "admin", "overwritten"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err = a.SetEnabled(ctx, actor, "admin", false); !errors.Is(err, ErrProtectedAccount) {
		t.Fatal(err)
	}
	if _, err = a.ResetPassword(ctx, actor, "admin"); !errors.Is(err, ErrProtectedAccount) {
		t.Fatal(err)
	}
	user.SecurityGeneration = 0
	if err = a.UpdateDisplayName(ctx, user, user.UserID, "Alice2"); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale user accepted", err)
	}
}
func TestResetAndDisableRevokeSessions(t *testing.T) {
	a, _, actor := accountFixture(t)
	ctx := context.Background()
	created, err := a.Create(ctx, actor, CreateUserInput{Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec(`INSERT INTO access_sessions(token_hash,user_id,csrf_token,mode_epoch,security_generation,auth_source,created_at,expires_at,last_seen_at,password_verified_at) VALUES('session',?,'csrf',1,1,'password',1,9999999999,1,NULL)`, created.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset, err := a.ResetPassword(ctx, actor, created.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Password == created.Password || reset.User.SecurityGeneration != 2 || !reset.User.MustChangePassword {
		t.Fatal("bad reset")
	}
	var n int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM access_sessions WHERE user_id=?`, created.User.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("sessions retained", n, err)
	}
	if err = a.SetEnabled(ctx, actor, created.User.ID, false); err != nil {
		t.Fatal(err)
	}
	var enabled, generation int
	if err = a.db.QueryRow(`SELECT enabled,security_generation FROM access_users WHERE id=?`, created.User.ID).Scan(&enabled, &generation); err != nil || enabled != 0 || generation != 3 {
		t.Fatal(enabled, generation, err)
	}
}

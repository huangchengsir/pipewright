package tenancy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/store"
	"path/filepath"
	"testing"
)

func testRepository(t *testing.T) (*Repository, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := NewRepository(s.DB)
	if err = r.InitializeControl(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r, s
}
func TestControlDefaultsAndModePlanning(t *testing.T) {
	r, _ := testRepository(t)
	ctx := context.Background()
	state, err := r.Mode(ctx)
	if err != nil || state.Enabled || state.EverEnabled || state.Epoch != 1 {
		t.Fatal(state, err)
	}
	if err = r.InitializeControl(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := r.Mode(ctx)
	if state != after {
		t.Fatal("restart changed state")
	}
	on := config.MultiTenantMode{Enabled: true, Explicit: true}
	if _, err = PlanMode(state, on, true, false); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if _, err = PlanMode(state, on, false, true); !errors.Is(err, ErrUnfinishedOperations) {
		t.Fatal(err)
	}
	onState, err := PlanMode(state, on, true, true)
	if err != nil || !onState.Enabled || !onState.EverEnabled || onState.Epoch != 2 {
		t.Fatal(onState, err)
	}
	if _, err = PlanMode(onState, config.MultiTenantMode{}, true, true); !errors.Is(err, ErrExplicitModeRequired) {
		t.Fatal(err)
	}
	off, err := PlanMode(onState, config.MultiTenantMode{Explicit: true}, true, true)
	if err != nil || off.Enabled || !off.EverEnabled || off.Epoch != 3 {
		t.Fatal(off, err)
	}
}
func TestRoleSeedsPreserveEditsAndDeletion(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "平台团队")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, team)
	if err != nil || len(roles) != 4 {
		t.Fatal(roles, err)
	}
	for _, role := range roles {
		if role.PresetKey != "team-admin" && !role.RequiresSelectedBinding {
			t.Fatal("binding restriction not persisted")
		}
	}
	id := roles[0].ID
	if _, err = s.DB.Exec(`UPDATE access_roles SET name=?,deleted=1,pages_json='[]',grants_json='[]' WHERE id=?`, "用户修改", id); err != nil {
		t.Fatal(err)
	}
	before, _ := r.Mode(ctx)
	if err = r.EnsurePresets(ctx, team); err != nil {
		t.Fatal(err)
	}
	after, _ := r.Mode(ctx)
	if before != after {
		t.Fatal("idempotent seed changed revision")
	}
	roles, err = r.Roles(ctx, team)
	if err != nil || len(roles) != 4 {
		t.Fatal(err)
	}
	for _, role := range roles {
		if role.ID == id && (!role.Deleted || role.NameKey != "用户修改" || len(role.Grants) != 0) {
			t.Fatal("seed overwrote custom policy")
		}
	}
}
func TestCrossTeamBindingsAndSeedRejectedByDatabase(t *testing.T) {
	r, s := testRepository(t)
	ctx := context.Background()
	a, err := r.CreateTeam(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.CreateTeam(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO access_users(id,username,username_key,display_name,password_hash,enabled,instance_admin,must_change_password,password_expires_at,security_generation,created_at,updated_at) VALUES('u','u','u','u',NULL,1,0,0,NULL,1,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO access_members(id,tenant_id,user_id,enabled,revision) VALUES('m',?,'u',1,1)`, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO access_bindings(id,tenant_id,member_id,role_id,scope_json) VALUES('bad',?,'m',?,'{}')`, a, roles[0].ID)
	if err == nil {
		t.Fatal("cross-team binding accepted")
	}
	_, err = s.DB.Exec(`INSERT INTO access_role_seeds(tenant_id,preset_key,role_id,seed_version) VALUES(?,'cross-team',?,1)`, a, roles[0].ID)
	if err == nil {
		t.Fatal("cross-team seed accepted")
	}
}
func TestStoredPresetsProduceValidEvaluator(t *testing.T) {
	r, _ := testRepository(t)
	ctx := context.Background()
	id, err := r.CreateTeam(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := r.Roles(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := r.Mode(ctx)
	if _, err = authz.New(authz.Snapshot{CatalogVersion: authz.CatalogVersion, ModeEpoch: state.Epoch, Revision: state.Revision, Roles: roles}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityRequiresMembershipAndReadsTrustedEnvironment(t *testing.T) {
	a, r, admin := accountFixture(t)
	ctx := context.Background()
	team, err := r.CreateTeam(ctx, "team")
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
	for _, stmt := range []string{`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('project','p',?,NULL,'p',NULL)`, `INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('env',?,'p','prod-looking-name','test',1)`, `INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,owner_id,project_id,environment_id) VALUES('run','r',?,NULL,'p','env')`} {
		if _, err = a.db.Exec(stmt, team); err != nil {
			t.Fatal(err)
		}
	}
	created, err := a.Create(ctx, admin, CreateUserInput{Username: "alice", DisplayName: "Alice", Assignments: []MemberAssignment{{TeamID: team, RoleID: publisher, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"p"}, EnvironmentIDs: []string{"env"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := a.Login(ctx, "alice", created.Password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Authority(ctx, session.Actor, team); !errors.Is(err, ErrForbidden) {
		t.Fatal("restricted session got authority", err)
	}
	session, err = a.ChangePassword(ctx, session.Token, created.Password, "new-permanent-password")
	if err != nil {
		t.Fatal(err)
	}
	authority, err := r.Authority(ctx, session.Actor, team)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := r.Resource(ctx, team, "run", "r")
	if err != nil || resource.EnvironmentPurpose != "test" {
		t.Fatal(resource, err)
	}
	allowed, err := authority.Service.Allowed(authority.Principal, "run.start", resource)
	if err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	if _, err = r.Resource(ctx, "another-team", "run", "r"); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross team resource", err)
	}
	if _, err = a.db.Exec(`UPDATE access_members SET enabled=0 WHERE user_id=?`, created.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Authority(ctx, session.Actor, team); !errors.Is(err, ErrForbidden) {
		t.Fatal("removed member still authorized", err)
	}
}

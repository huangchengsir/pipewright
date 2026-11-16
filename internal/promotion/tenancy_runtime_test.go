package promotion

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

type tenantPromotionGate func(context.Context, string, GateInfo) (bool, string, error)

func (f tenantPromotionGate) Await(c context.Context, k string, i GateInfo) (bool, string, error) {
	return f(c, k, i)
}

type tenantPromotionSecret struct {
	vault    vault.Vault
	calls    int
	callback func()
}

func (f *tenantPromotionSecret) Reveal(string) (string, error) {
	panic("unscoped secret resolver used")
}
func (f *tenantPromotionSecret) Use(ctx context.Context, id string) (string, error) {
	f.calls++
	plain, err := vault.Use(ctx, f.vault, id)
	if err != nil {
		return "", err
	}
	if f.callback != nil {
		f.callback()
	}
	return plain, nil
}
func TestTenantPromotionRequiresTargetAndRechecksAfterGate(t *testing.T) {
	st, db := testStore(t)
	ctx := context.Background()
	project := seedProject(t, db)
	foreign := seedProject(t, db)
	key := [32]byte{19}
	vlt := vault.New(db, &key)
	secret, err := vlt.Create(vault.CreateInput{Name: "promotion-variable", Type: vault.TypeGitToken, Secret: "synthetic-promotion-variable"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetVariables(ctx, project, "test", []Variable{{Key: "DEPLOY_TOKEN", Secret: true, CredentialID: secret.ID}}); err != nil {
		t.Fatal(err)
	}

	runs := fakeRuns{}
	for _, name := range []string{"allowed", "revoked", "changed", "readonly", "other"} {
		p := project
		if name == "other" {
			p = foreign
		}
		id := seedRun(t, db, p, SuccessStatus)
		runs[name] = RunInfo{ID: id, ProjectID: p, Status: SuccessStatus}
	}
	lookup := fakeRuns{}
	for _, r := range runs {
		lookup[r.ID] = r
	}
	chain := Chain{Environments: []EnvStage{{Name: "test", Gated: true}, {Name: "prod"}}}
	if _, err := st.SaveChain(ctx, project, chain); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveChain(ctx, foreign, chain); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewService(db, nil).Bootstrap("admin", "promotion-admin-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "promotion-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var team, testEnv string
	if err = db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT id FROM access_environments WHERE project_id=? AND name='test'`, project).Scan(&testEnv); err != nil {
		t.Fatal(err)
	}
	selected := authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{project}}
	roleInput := tenancy.RoleInput{Name: "Promotion", Pages: []string{"page.runs", "page.environments"}, Grants: []authz.Grant{{Action: "run.read", Scope: selected}, {Action: "environment.read", Scope: selected}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", roleInput)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "promoter", DisplayName: "Promoter", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{testEnv}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "promoter", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "promotion-employee-password")
	if err != nil {
		t.Fatal(err)
	}
	user := tenancy.WithActor(ctx, login.Actor, team)
	gates := 0
	co := NewCoordinator(st, lookup, tenantPromotionGate(func(context.Context, string, GateInfo) (bool, string, error) {
		gates++
		return true, "trusted-approver", nil
	}), nil)
	if _, err = co.Promote(user, runs["readonly"].ID, "test", "spoofed-admin"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("read promoted", err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM run_promotions`).Scan(&count); err != nil || count != 0 || gates != 0 {
		t.Fatal("denial side effect", count, gates, err)
	}
	roleInput.ExpectedRevision = role.Revision
	promotionScope := selected
	promotionScope.EnvironmentIDs = []string{testEnv}
	roleInput.Grants = append(roleInput.Grants, authz.Grant{Action: "release.promote", Scope: promotionScope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, roleInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = co.Promote(user, runs["other"].ID, "test", "spoofed-admin"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("other project promoted", err)
	}
	result, err := co.Promote(user, runs["allowed"].ID, "test", "spoofed-admin")
	if err != nil || result == nil || !result.Approved {
		t.Fatal("allowed target", result, err)
	}
	plan, err := st.ReadPlan(user, runs["readonly"].ID)
	if err != nil || plan.Next == nil || plan.Next.Name != "test" || plan.Next.EnvironmentID != testEnv {
		t.Fatal("minimal plan needs unrelated config read", plan, err)
	}
	resolver := &tenantPromotionSecret{vault: vlt}
	co.secret = resolver
	if values, _, e := co.ResolveEnvVars(user, project, "test"); !errors.Is(e, tenancy.ErrForbidden) || len(values) != 0 || resolver.calls != 0 {
		t.Fatal("no execution permission resolved variables", e)
	}
	roleInput.ExpectedRevision = role.Revision
	roleInput.Grants = append(roleInput.Grants, authz.Grant{Action: "deployment.execute", Scope: promotionScope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, roleInput)
	if err != nil {
		t.Fatal(err)
	}
	if values, _, e := co.ResolveEnvVars(user, project, "test"); !errors.Is(e, tenancy.ErrForbidden) || len(values) != 0 || resolver.calls != 0 {
		t.Fatal("no credential permission decrypted", e)
	}
	roleInput.ExpectedRevision = role.Revision
	roleInput.Pages = append(roleInput.Pages, "page.vault")
	credentialScope := authz.Scope{Kind: authz.ScopeSelected, ResourceIDs: []string{secret.ID}}
	roleInput.Grants = append(roleInput.Grants, authz.Grant{Action: "credential.metadata", Scope: credentialScope}, authz.Grant{Action: "credential.use", Scope: credentialScope})
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, roleInput)
	if err != nil {
		t.Fatal(err)
	}
	values, missing, e := co.ResolveEnvVars(user, project, "test")
	if e != nil || len(values) != 1 || values[0].Value != "synthetic-promotion-variable" || len(missing) != 0 || resolver.calls != 1 {
		t.Fatal("authorized credential consumption failed", e)
	}
	resolver.callback = func() {
		roleInput.ExpectedRevision = role.Revision
		roleInput.Grants = roleInput.Grants[:len(roleInput.Grants)-1]
		var e error
		role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, roleInput)
		if e != nil {
			t.Fatal(e)
		}
	}
	values, _, e = co.ResolveEnvVars(user, project, "test")
	if !errors.Is(e, tenancy.ErrForbidden) || len(values) != 0 || resolver.calls != 2 {
		t.Fatal("revocation during secret read returned data", e)
	}
	if _, err = co.Promote(user, runs["allowed"].ID, "prod", "spoofed-admin"); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("production target allowed", err)
	}
	co.gate = tenantPromotionGate(func(context.Context, string, GateInfo) (bool, string, error) {
		if _, e := db.Exec(`UPDATE project_environments SET chain_json=? WHERE project_id=?`, `[{"name":"test","gated":false},{"name":"prod"}]`, project); e != nil {
			t.Fatal(e)
		}
		return true, "trusted-approver", nil
	})
	result, err = co.Promote(user, runs["changed"].ID, "test", "spoofed-admin")
	if !errors.Is(err, tenancy.ErrConflict) || result == nil || result.Record.Status != StatusRejected {
		t.Fatal("changed chain accepted", result, err)
	}
	// Restore through the actual authorized config API, then revoke during wait.
	if _, err = st.SaveChain(tenancy.WithActor(ctx, admin.Actor, team), project, chain); err != nil {
		t.Fatal(err)
	}
	co.gate = tenantPromotionGate(func(context.Context, string, GateInfo) (bool, string, error) {
		if e := accounts.Logout(ctx, login.Token); e != nil {
			t.Fatal(e)
		}
		return true, "trusted-approver", nil
	})
	result, err = co.Promote(user, runs["revoked"].ID, "test", "spoofed-admin")
	if !errors.Is(err, tenancy.ErrForbidden) || result == nil || result.Record.Status != StatusRejected {
		t.Fatal("revoked promotion accepted", result, err)
	}
	if _, err = st.ListRecordsForRun(user, runs["allowed"].ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked history exposed", err)
	}
}

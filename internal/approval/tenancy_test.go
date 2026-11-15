package approval

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestApprovalEmployeeScopeRevocationAndDelivery(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','C','git_token','',X'00','m','now','now')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','P','https://example.invalid','main','c','now','now')`,
		`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('run','p','success','now')`,
	} {
		if _, e := st.DB.Exec(stmt); e != nil {
			t.Fatal(e)
		}
	}
	if e := auth.NewService(st.DB, nil).Bootstrap("admin", "administrator-approval-password"); e != nil {
		t.Fatal(e)
	}
	repo := tenancy.NewRepository(st.DB)
	if _, e := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); e != nil {
		t.Fatal(e)
	}
	var team string
	if e := st.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); e != nil {
		t.Fatal(e)
	}
	accounts := tenancy.NewAccounts(st.DB)
	admin, e := accounts.Login(ctx, "admin", "administrator-approval-password")
	if e != nil {
		t.Fatal(e)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	for _, id := range []string{"test", "production"} {
		if _, e = st.DB.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES(?,?,'p',?,?,1)`, id, team, id, id); e != nil {
			t.Fatal(e)
		}
	}
 for _,id:=range []string{"test","production"}{if _,e=st.DB.Exec(`INSERT INTO access_ownership(resource_kind,resource_id,tenant_id,project_id,environment_id) VALUES('environment',?,?,'p',?)`,id,team,id);e!=nil{t.Fatal(e)}}
	grants := []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.approve", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
	role, e := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Approver", Pages: []string{"page.runs"}, Grants: grants, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	create := func(name, project string) tenancy.Session {
		t.Helper()
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{project}, EnvironmentIDs: []string{"test", "production"}}}}})
		if e != nil {
			t.Fatal(e)
		}
		s, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		s, e = accounts.ChangePassword(ctx, s.Token, initial.Password, name+"-approval-password")
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	first := create("first-approver", "p")
	second := create("second-approver", "p")
	firstctx := tenancy.WithActor(ctx, first.Actor, team)
	secondctx := tenancy.WithActor(ctx, second.Actor, team)
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	e = configstate.WithWriteTxContext(adminctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
		if _, e := configstate.LockProject(tx, q, st.DB, "p"); e != nil {
			return e
		}
		return tenancy.ApproveRun(tx, st.DB, "run", "p", digest, []tenancy.ExecutionPermission{{Action: "run.start", ResourceID: "run"}})
	})
	if e != nil {
		t.Fatal(e)
	}
	job := tenancy.RunContext(ctx, st.DB, "run", digest)
	job = tenancy.WithExecutionVerifier(job, func(c context.Context) error { return tenancy.CheckRunBoundary(c, st.DB, "run") })
	store := NewStore(st.DB)
	coord := New()
	if e = store.CreatePending(job, "run", "stage", "CI gate"); e != nil {
		t.Fatal(e)
	}
	channel := coord.Wait(Key("run", "stage"))
	if _, e = store.Resolve(ctx, coord, "run", "stage", true, "forged"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("anonymous", e)
	}
	if _, e = store.Resolve(job, coord, "run", "stage", true, "forged"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("job may not approve itself", e)
	}
	items, e := store.ListForRun(firstctx, "run")
	if e != nil || len(items) != 1 || items[0].AccessAction != "run.approve" {
		t.Fatal(items, e)
	}
	decision, e := repo.Decisions(ctx, first.Actor, team, []tenancy.DecisionQuery{{Action: "run.approve", ResourceID: "run"}})
	if e != nil || !decision.Allowed[0] {
		t.Fatal(decision, e)
	}
	// Maintenance drains existing approvals; it must not admit new configuration.
	if _, e = st.DB.Exec(`UPDATE selfupdate_control SET task_id='db-migration-test' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if e = configstate.WithWriteTxContext(firstctx, st.DB, func(context.Context, configstate.DBTX) error { return nil }); !errors.Is(e, configstate.ErrMaintenance) {
		t.Fatal("new writes admitted", e)
	}
	actor, e := store.Resolve(firstctx, coord, "run", "stage", true, "forged")
	if e != nil || actor != first.Actor.UserID {
		t.Fatal(actor, e)
	}
	select {
	case d := <-channel:
		if !d.Approved || d.Actor != actor {
			t.Fatal(d)
		}
	default:
		t.Fatal("no delivery")
	}
	if _, e = store.Resolve(secondctx, coord, "run", "stage", false, "forged"); !errors.Is(e, ErrNotWaiting) {
		t.Fatal("second decision", e)
	}
	if _, e = st.DB.Exec(`UPDATE selfupdate_control SET task_id='' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	// A new gate must not retain the previous employee's authority after revocation.
	if e = store.CreatePending(job, "run", "second", "CI gate"); e != nil {
		t.Fatal(e)
	}
	coord.Wait(Key("run", "second"))
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Approver", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants[:1], RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Resolve(secondctx, coord, "run", "second", true, "forged"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("revoked", e)
	}
	var status string
	if e = st.DB.QueryRow(`SELECT status FROM run_approvals WHERE run_id='run' AND stage_id='second'`).Scan(&status); e != nil || status != StatusPending || !coord.IsWaiting(Key("run", "second")) {
		t.Fatal(status, e)
	}

	// One release gate can cover multiple environments; a test-only grant must
	// never approve the production member of that same trusted ceiling.
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Approver", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: []authz.Grant{grants[0], {Action: "release.approve", Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentPolicy: "test"}}}, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('release-run','p','success','now')`); e != nil {
		t.Fatal(e)
	}
	e = configstate.WithWriteTxContext(adminctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
		if e := tenancy.RecordOwnership(tx, st.DB, "run", "release-run", "p"); e != nil {
			return e
		}
		return tenancy.ApproveRun(tx, st.DB, "release-run", "p", digest, []tenancy.ExecutionPermission{{Action: "run.start", ResourceID: "release-run"}, {Action: "deployment.execute", ResourceID: "test", EnvironmentID: "test"}, {Action: "deployment.execute", ResourceID: "production", EnvironmentID: "production"}})
	})
	if e != nil {
		t.Fatal(e)
	}
	releaseJob := tenancy.RunContext(ctx, st.DB, "release-run", digest)
	releaseJob = tenancy.WithExecutionVerifier(releaseJob, func(c context.Context) error { return tenancy.CheckRunBoundary(c, st.DB, "release-run") })
	if e = store.CreatePending(releaseJob, "release-run", "deploy-gate", "release"); e != nil {
		t.Fatal(e)
	}
	releaseChannel := coord.Wait(Key("release-run", "deploy-gate"))
	if _, e = store.Resolve(firstctx, coord, "release-run", "deploy-gate", true, ""); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("partial environments approved entire release", e)
	}
	view, e := repo.Decisions(ctx, first.Actor, team, []tenancy.DecisionQuery{{Action: "release.approve", ResourceID: "release-run", EnvironmentID: "test"}, {Action: "release.approve", ResourceID: "release-run", EnvironmentID: "production"}, {Action: "release.approve", ResourceID: "run", EnvironmentID: "test"}})
	if e != nil || !view.Allowed[0] || view.Allowed[1] || view.Allowed[2] {
		t.Fatal(view, e)
	}
	role, e = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Approver", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: []authz.Grant{grants[0], {Action: "release.approve", Scope: authz.Scope{Kind: authz.ScopeTeam, EnvironmentIDs: []string{"test", "production"}}}}, RequiresSelectedBinding: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Resolve(secondctx, coord, "release-run", "deploy-gate", false, ""); e != nil {
		t.Fatal(e)
	}
	select {
	case d := <-releaseChannel:
		if d.Approved || d.Actor != second.Actor.UserID {
			t.Fatal(d)
		}
	default:
		t.Fatal("release rejection not delivered")
	}
}

package runretry

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

func TestRetryCurrentCallerScopeAndPersistentChild(t *testing.T) {
	st := storetest.Open(t)
	f := newFixture(t, st)
	ctx := context.Background()
	legacy := auth.NewService(st.DB, nil)
	if err := legacy.Bootstrap("admin", "administrator-retry-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(st.DB)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err := st.DB.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(st.DB)
	admin, err := accounts.Login(ctx, "admin", "administrator-retry-password")
	if err != nil {
		t.Fatal(err)
	}
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	snapshot, err := f.s.FrozenSnapshotForRelease(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := SnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	err = configstate.WithWriteTxContext(adminctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
		if _, err := configstate.LockProject(tx, q, st.DB, "project"); err != nil {
			return err
		}
		return tenancy.ApproveRun(tx, st.DB, "root", "project", digest, []tenancy.ExecutionPermission{{Action: "run.start", ResourceID: "root"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.retry", Scope: authz.Scope{Kind: authz.ScopeTeam}}}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Recovery", Pages: []string{"page.runs"}, Grants: grants, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) tenancy.Session {
		t.Helper()
		initial, e := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: name, DisplayName: name, Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{"project"}}}}})
		if e != nil {
			t.Fatal(e)
		}
		session, e := accounts.Login(ctx, name, initial.Password)
		if e != nil {
			t.Fatal(e)
		}
		session, e = accounts.ChangePassword(ctx, session.Token, initial.Password, name+"-retry-password")
		if e != nil {
			t.Fatal(e)
		}
		return session
	}
	first := create("first-retry")
	second := create("second-retry")
	actor := tenancy.WithActor(ctx, first.Actor, team)
	other := tenancy.WithActor(ctx, second.Actor, team)
	if _, err = f.s.Plan(ctx, "root", []string{f.nodes["b"].Key}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("missing principal", err)
	}
	plan, err := f.s.Plan(actor, "root", []string{f.nodes["b"].Key})
	if err != nil || !plan.CanSubmit {
		t.Fatal(plan, err)
	}
	// No run.start grant: retry is a distinct scheduling permission.
	input := submitFor(plan, "same-request")
	input.Scope = "admin"
	input.Initiator = "forged-admin"
	result, err := f.s.Submit(actor, "root", input)
	if err != nil {
		t.Fatal(err)
	}
	if f.dispatched.Load() != 1 {
		t.Fatal("dispatch", f.dispatched.Load())
	}
	var owner, initiator, scope string
	if err = st.DB.QueryRow(`SELECT user_id FROM access_run_authorizations WHERE run_id=?`, result.AttemptRunID).Scan(&owner); err != nil || owner != first.Actor.UserID {
		t.Fatal(owner, err)
	}
	if err = st.DB.QueryRow(`SELECT initiator,caller_scope FROM run_retry_requests WHERE attempt_id=?`, result.AttemptRunID).Scan(&initiator, &scope); err != nil || initiator != owner || scope == "admin" {
		t.Fatal(initiator, scope, err)
	}
	if _, err = f.s.Request(other, "root", scope, input.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("other employee read receipt", err)
	}
	if _, err = f.s.Request(actor, "root", "forged", input.RequestID); err != nil {
		t.Fatal("own derived scope", err)
	}
	job := tenancy.RunContext(ctx, st.DB, result.AttemptRunID, digest)
	if err = tenancy.CheckRunPermission(job, st.DB, "run.retry", result.AttemptRunID, ""); err != nil {
		t.Fatal(err)
	}
	if err = accounts.Logout(ctx, first.Token); err != nil {
		t.Fatal(err)
	}
	if err = tenancy.Check(job, st.DB, "pipeline.read", "project", "project"); err != nil {
		t.Fatal("logout killed fixed inputs", err)
	}
	role, err = repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Recovery", ExpectedRevision: role.Revision, Pages: role.Pages, Grants: grants[:1], RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Context(other, "root"); err != nil {
		t.Fatal("reader lost context", err)
	}
	if _, err = f.s.Plan(other, result.AttemptRunID, []string{f.nodes["b"].Key}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("reader planned network retry", err)
	}
	if _, err = f.s.Submit(other, "root", input); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoke submit", err)
	}
	if f.dispatched.Load() != 1 {
		t.Fatal("denial dispatched")
	}
	if err = tenancy.CheckRunPermission(job, st.DB, "run.retry", result.AttemptRunID, ""); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("revoked child continued", err)
	}
}

package run

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"strings"
	"testing"
)

type fixedAccessPlan struct {
	digest      string
	permissions []tenancy.ExecutionPermission
	afterCommit func()
}

func (f *fixedAccessPlan) Prepare(context.Context, string, Trigger) (*PreparedExecution, error) {
	return &PreparedExecution{}, nil
}
func (f *fixedAccessPlan) Approve(ctx context.Context, _ string, _ string, _ Trigger, _ *PreparedExecution) (string, []tenancy.ExecutionPermission, error) {
	if f.afterCommit != nil {
		configstate.AfterCommit(ctx, f.afterCommit)
	}
	return f.digest, f.permissions, nil
}
func (f *fixedAccessPlan) Verify(context.Context, string) error           { return nil }
func (f *fixedAccessPlan) Digest(context.Context, string) (string, error) { return f.digest, nil }

type accessProbeRunner struct{ called bool }

func (r *accessProbeRunner) Run(ctx context.Context, rn *Run, sink StepSink) error {
	r.called = true
	return sink.Plan(ctx, nil)
}
func TestTenancyRunAdmissionAndPersistentDispatch(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := seedProject(t, db)
	legacy := auth.NewService(db, nil)
	if err := legacy.Bootstrap("admin", "admin-run-password"); err != nil {
		t.Fatal(err)
	}
	repo := tenancy.NewRepository(db)
	if _, err := repo.ReconcileMode(ctx, config.MultiTenantMode{Enabled: true, Explicit: true}, true); err != nil {
		t.Fatal(err)
	}
	var team string
	if err := db.QueryRow(`SELECT default_team_id FROM access_control WHERE id=1`).Scan(&team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(db)
	admin, err := accounts.Login(ctx, "admin", "admin-run-password")
	if err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "CI Runner", Pages: []string{"page.runs"}, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.start", Scope: authz.Scope{Kind: authz.ScopeTeam}}}, RequiresSelectedBinding: true})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "ci-user", DisplayName: "CI User", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ProjectIDs: []string{project}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "ci-user", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "ci-user-run-password")
	if err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	if _, err = New(db).Create(actor, project, Trigger{}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("missing compiler accepted", err)
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("rejected admission leaked run", count)
	}
	logoutDone := false
	plan := &fixedAccessPlan{digest: strings.Repeat("a", 64), afterCommit: func() {
		if err := accounts.Logout(ctx, login.Token); err != nil {
			t.Fatal(err)
		}
		logoutDone = true
	}}
	svc := newService(db, WithExecutionAuthorizer(plan))
	created, err := svc.Create(actor, project, Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatal("pure CI", err)
	}
	if !logoutDone {
		t.Fatal("commit barrier did not run")
	}
	plan.afterCommit = nil
	if _, err = svc.Cancel(actor, created.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("run.start allowed cancellation", err)
	}

	runner := &accessProbeRunner{}
	pool := NewWorkerPool(svc, WithRunner(runner))
	pool.ctx = ctx
	pool.execute(created.ID)
	if !runner.called {
		t.Fatal("logout prevented approved dispatch")
	}
	var status string
	db.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, created.ID).Scan(&status)
	if status != StatusSuccess {
		t.Fatal(status)
	}
	// A durable suspension remains a suspension even after user revocation.
	adminctx := tenancy.WithActor(ctx, admin.Actor, team)
	pausedService := newService(db, WithExecutionAuthorizer(plan))
	paused, err := pausedService.Create(adminctx, project, Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	suspended := &suspendedAccessRunner{db: db}
	pausePool := NewWorkerPool(pausedService, WithRunner(suspended))
	pausePool.ctx = ctx
	pausePool.execute(paused.ID)
	if err = db.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, paused.ID).Scan(&status); err != nil || status != StatusWaitingAction {
		t.Fatal("suspension lost", status, err)
	}
	// Explicit cancellation releases the paused row before the next admission.
	if _, err = pausedService.Cancel(adminctx, paused.ID); err != nil {
		t.Fatal(err)
	}
	// Missing capability is never interpreted as bootstrap/system ownership.
	other := newService(db, WithExecutionAuthorizer(plan))
	second, err := other.Create(adminctx, project, Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM access_run_authorizations WHERE run_id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	denied := &accessProbeRunner{}
	blockedPool := NewWorkerPool(other, WithRunner(denied))
	blockedPool.ctx = ctx
	blockedPool.execute(second.ID)
	if denied.called {
		t.Fatal("missing capability executed")
	}
	db.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, second.ID).Scan(&status)
	if status != StatusFailed {
		t.Fatal(status)
	}
}

type suspendedAccessRunner struct {
	db           *sql.DB
	afterSuspend func()
}

func (r *suspendedAccessRunner) Run(ctx context.Context, rn *Run, sink StepSink) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_runs SET status=?,finished_at=NULL WHERE id=?`, StatusWaitingAction, rn.ID); err != nil {
		return err
	}
	if r.afterSuspend != nil {
		r.afterSuspend()
	}
	return ErrSuspended
}

package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/authz"
	"github.com/huangchengsir/pipewright/internal/config"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/tenancy"
	"testing"
)

func TestTenantKubernetesEvidenceAndRecoveryBoundaries(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	f, plan := releaseStoreFixture(t, st)
	for _, id := range []string{"run-a", "run-b", "run-foreign"} {
		if _, err := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,trigger_branch,trigger_commit,trigger_actor,created_at) VALUES(?,'project-1','success','manual','main','','admin','2026-11-13T00:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := auth.NewService(st.DB, nil).Bootstrap("admin", "admin-kubernetes-password"); err != nil {
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
	if _, err := st.DB.Exec(`INSERT INTO access_environments(id,tenant_id,project_id,name,purpose,revision) VALUES('env-production',?,'project-1','production','production',1)`, team); err != nil {
		t.Fatal(err)
	}
	accounts := tenancy.NewAccounts(st.DB)
	admin, err := accounts.Login(ctx, "admin", "admin-kubernetes-password")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := tenancy.WithActor(ctx, admin.Actor, team)
	other, err := repo.CreateTeamAs(ctx, admin.Actor, "Other evidence")
	if err != nil {
		t.Fatal(err)
	}
	// The operation/release records are real encrypted store rows. They remain
	// bound to their original run even when a caller substitutes a path run ID.
	operations := map[string]Operation{}
	releases := map[string]ReleaseRecord{}
	for _, id := range []string{"run-a", "run-b", "run-foreign"} {
		o, e := f.s.CreateOperation(adminCtx, OperationInput{RunID: id, NodeID: "deploy/job", Attempt: 1, OwnerEpoch: "owner-1", Intent: Intent{Authorization: f.a, Image: "registry.invalid/app/backend@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TimeoutSeconds: 300, Source: ImageSource{RunID: id, StageID: "build", JobID: "push", ArtifactID: "image-artifact", Commit: "cccccccccccccccccccccccccccccccccccccccc"}}})
		if e != nil {
			t.Fatal(e)
		}
		operations[id] = o
		r, e := f.s.CreateRelease(adminCtx, id, "release/node", "owner-release", plan)
		if e != nil {
			t.Fatal(e)
		}
		releases[id] = r
	}
	if _, err = st.DB.Exec(`UPDATE access_ownership SET tenant_id=? WHERE resource_kind='run' AND resource_id='run-foreign'`, other.ID); err != nil {
		t.Fatal(err)
	}
	role, err := repo.SaveRole(ctx, admin.Actor, team, "", tenancy.RoleInput{Name: "Evidence only", Pages: []string{"page.runs"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}}})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := accounts.Create(ctx, admin.Actor, tenancy.CreateUserInput{Username: "evidence-reader", DisplayName: "Evidence Reader", Assignments: []tenancy.MemberAssignment{{TeamID: team, RoleID: role.ID, Scope: authz.Scope{Kind: authz.ScopeSelected, ResourceRefs: []authz.ResourceRef{{Kind: "run", ID: "run-a"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := accounts.Login(ctx, "evidence-reader", initial.Password)
	if err != nil {
		t.Fatal(err)
	}
	login, err = accounts.ChangePassword(ctx, login.Token, initial.Password, "evidence-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	actor := tenancy.WithActor(ctx, login.Actor, team)
	if got, e := f.s.ListOperationsForRun(actor, "run-a"); e != nil || len(got) != 1 || got[0].ID != operations["run-a"].ID {
		t.Fatal("exact operations", got, e)
	}
	if got, e := f.s.ListReleasesForRun(actor, "run-a"); e != nil || len(got) != 1 || got[0].ID != releases["run-a"].ID {
		t.Fatal("exact releases", got, e)
	}
	for _, id := range []string{"run-b", "run-foreign", "missing"} {
		if _, e := f.s.ListOperationsForRun(actor, id); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("operation list scope", id, e)
		}
		if _, e := f.s.ListReleasesForRun(actor, id); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("release list scope", id, e)
		}
		if _, e := f.s.ReadOperationForRun(actor, id, operations["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("operation detail scope", id, e)
		}
		if _, e := f.s.ReadReleaseForRun(actor, id, releases["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
			t.Fatal("release detail scope", id, e)
		}
	}
	if _, e := f.s.ReadOperationForRun(actor, "run-a", operations["run-b"].ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("operation path substitution", e)
	}
	if _, e := f.s.ReadReleaseForRun(actor, "run-a", releases["run-b"].ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("release path substitution", e)
	}
	// A rollback retry from its readable source must not expose an unreadable child.
	input := ReleaseRollbackRequest{SourceID: releases["run-a"].ID, ExpectedRevision: 1, PlanID: "source-plan", RequestID: "lost-ack"}
	key, err := releaseRollbackRequestKey(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, "INSERT INTO k8s_release_rollback_requests(id,source_id,source_revision,source_plan_id,release_id,run_id,created_at) VALUES(?,?,?,?,?,?,?)", key, input.SourceID, input.ExpectedRevision, input.PlanID, releases["run-b"].ID, "run-b", 1); err != nil {
		t.Fatal(err)
	}
	if _, e := f.s.LookupReleaseRollback(actor, input); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("replayed rollback returned an unreadable run", e)
	}
	if _, e := f.s.FindReleaseRollbackRequest(actor, input.SourceID, input.RequestID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("lost acknowledgement returned an unreadable run", e)
	}
	if result, e := f.s.LookupReleaseRollback(adminCtx, input); e != nil || result.RunID != "run-b" || result.Release.ID != releases["run-b"].ID {
		t.Fatal("reader could not recover the receipt", e)
	}
	if e := f.s.AuthorizeOperationRecovery(actor, "run-a", operations["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("reader recovery", e)
	}
	if e := f.s.AuthorizeReleaseRecovery(actor, "run-a", releases["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("reader release recovery", e)
	}
	if e := f.s.AuthorizeOperationRecovery(adminCtx, "run-a", operations["run-a"].ID); e != nil {
		t.Fatal("administrator operation recovery", e)
	}
	if e := f.s.AuthorizeReleaseRecovery(adminCtx, "run-a", releases["run-a"].ID); e != nil {
		t.Fatal("administrator release recovery", e)
	}
	if _, e := repo.SaveRole(ctx, admin.Actor, team, role.ID, tenancy.RoleInput{Name: "Evidence only", ExpectedRevision: role.Revision, Pages: []string{"page.runs"}, RequiresSelectedBinding: true, Grants: []authz.Grant{{Action: "run.read", Scope: authz.Scope{Kind: authz.ScopeTeam}}, {Action: "run.retry", Scope: authz.Scope{Kind: authz.ScopeTeam}}}}); e != nil {
		t.Fatal(e)
	}
	if e := f.s.AuthorizeOperationRecovery(actor, "run-a", operations["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("run retry alone must not grant cluster executor access", e)
	}
	if e := f.s.AuthorizeReleaseRecovery(actor, "run-a", releases["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("run retry alone must not grant release executor access", e)
	}
	if err = accounts.Logout(ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, e := f.s.ReadReleaseForRun(actor, "run-a", releases["run-a"].ID); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logout release read", e)
	}
	if _, e := f.s.ListOperationsForRun(actor, "run-a"); !errors.Is(e, tenancy.ErrForbidden) {
		t.Fatal("logout operation list", e)
	}
}

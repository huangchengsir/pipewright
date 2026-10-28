package deploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

// Real SQLite/vault/deploy service, simulated SSH and worker finalization.
// A no-start intermediate attempt must not erase a prior real target receipt.
func TestR6SkippedAttemptPreservesActualDeploymentExpectation(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		name := "pending"
		if skipped {
			name = "skipped"
		}
		t.Run(name, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				rr, _, d, targets, ctx, node := retryDeployFixture(t, st, func(snapshot *runretry.Snapshot) {
					deployment := snapshot.Spec.Stages[0]
					deployment.Needs = []string{"middle"}
					snapshot.Spec.Stages = []pipeline.Stage{
						{ID: "upstream", AllowFailure: true, Jobs: []pipeline.Job{{ID: "upstream", Type: "script"}}},
						{ID: "middle", Needs: []string{"upstream"}, Jobs: []pipeline.Job{{ID: "middle", Type: "script"}}},
						deployment,
					}
				})
				ex, _ := runretry.ExecutionFrom(ctx)
				upstream, _ := ex.Node("upstream", "upstream")
				middle, _ := ex.Node("middle", "middle")
				finish := func(ctx context.Context, execution runretry.Execution, n runretry.NodeRef, status string) {
					t.Helper()
					if e := execution.Begin(ctx, n); e != nil {
						t.Fatal(e)
					}
					if e := execution.Finish(ctx, n, runretry.Outcome{Status: status, ExecutionMode: "real", ExternalOutcome: "none", Complete: status == "success", Reusable: status == "success"}); e != nil {
						t.Fatal(e)
					}
				}
				finish(ctx, ex, upstream, "failed")
				finish(ctx, ex, middle, "success")
				if e := ex.Begin(ctx, node); e != nil {
					t.Fatal(e)
				}
				cfg := map[string]string{"artifactType": "command", "restartCommand": "safe-test-command"}
				original, e := d.DeployForStage(ctx, "root", []string{"a"}, cfg, "")
				if e != nil || len(original) != 1 || original[0].Status != "success" {
					t.Fatal(original, e)
				}
				originalOperation := original[0].OperationID
				finishDeployFixture(t, st, rr, ctx, "root", node, original)

				child := func(source string, target runretry.NodeRef, key string) (string, context.Context, runretry.Execution) {
					t.Helper()
					plan, e := rr.Plan(context.Background(), source, []string{target.Key})
					if e != nil || !plan.CanSubmit {
						t.Fatal(plan, e)
					}
					if source != "root" {
						warned := false
						for _, warning := range plan.Warnings {
							warned = warned || warning.Code == "side_effect_reexecute" && warning.NodeKey == node.Key
						}
						if !warned {
							t.Fatal("older successful deployment disappeared from side-effect plan")
						}
					}
					request, e := rr.Submit(context.Background(), source, runretry.SubmitInput{Scope: "tester", RequestID: key, BaseAttemptID: plan.BaseAttemptID, BaseRevision: plan.BaseRevision, PlanFingerprint: plan.PlanFingerprint, TargetNodeIDs: plan.TargetNodeIDs})
					if e != nil {
						t.Fatal(e)
					}
					prepared, e := rr.Prepare(context.Background(), request.AttemptRunID)
					if e != nil {
						t.Fatal(e)
					}
					execution, _ := runretry.ExecutionFrom(prepared)
					prepared, e = runretry.WithNode(prepared, node)
					if e != nil {
						t.Fatal(e)
					}
					return request.AttemptRunID, prepared, execution
				}
				first, firstCtx, firstEx := child("root", upstream, "repair-upstream")
				finish(firstCtx, firstEx, upstream, "success")
				finish(firstCtx, firstEx, middle, "failed")
				if skipped {
					finish(firstCtx, firstEx, node, "skipped")
				}
				// The fixture owns no asynchronous worker. Retire only its simulated hold.
				if _, e := st.DB.Exec("UPDATE pipeline_runs SET status='failed',finished_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339), first); e != nil {
					t.Fatal(e)
				}
				if e := rr.Complete(firstCtx, first, "failed"); e != nil {
					t.Fatal(e)
				}
				if _, e := st.DB.Exec("UPDATE project_activity SET released_at=1 WHERE run_id=?", first); e != nil {
					t.Fatal(e)
				}

				second, secondCtx, secondEx := child(first, middle, "repair-middle")
				finish(secondCtx, secondEx, middle, "success")
				if e := secondEx.Begin(secondCtx, node); e != nil {
					t.Fatal(e)
				}
				// Simulate loss of the original verification proof, keeping its success
				// history/identity. A later attempt may not bypass this missing evidence.
				if _, e := st.DB.Exec("UPDATE run_retry_external_targets SET ciphertext=X'00' WHERE id=?", originalOperation); e != nil {
					t.Fatal(e)
				}
				before := targets.calls["a"]
				result, e := d.DeployForStage(secondCtx, second, []string{"a"}, cfg, "")
				if !errors.Is(e, runretry.ErrUnavailable) {
					t.Errorf("lost original proof did not reject before activation: %v %+v", e, result)
				}
				if targets.calls["a"] != before {
					t.Errorf("no-start ancestor erased old target expectation: calls %d -> %d", before, targets.calls["a"])
				}
				previous, source, e := runretry.PreviousOutcome(secondCtx, node)
				if e != nil || previous.Status != "success" || source != "root" || len(previous.Targets) != 1 || previous.Targets[0].OperationID != originalOperation {
					t.Errorf("actual business provenance lost: status=%s source=%s targets=%d error=%v", previous.Status, source, len(previous.Targets), e)
				}
				var state string
				if e := st.DB.QueryRow("SELECT state FROM run_retry_external_targets WHERE id=?", originalOperation).Scan(&state); e != nil || state != "success" {
					t.Fatal("original outcome history rewritten", state, e)
				}
			})
		})
	}
}

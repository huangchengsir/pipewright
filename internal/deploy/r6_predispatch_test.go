package deploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

type r6LookupUnavailable struct{ target.Service }

func (r6LookupUnavailable) Get(context.Context, string) (*target.Server, error) {
	return nil, errors.New("fixture lookup unavailable")
}

func TestR6PreDispatchAttemptKeepsPartialTargetProducer(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
		cfg := map[string]string{"artifactType": "command", "restartCommand": "safe-fixture-command"}
		rootResults, err := d.DeployForStage(ctx, "root", []string{"a", "b"}, cfg, "")
		if err != nil || len(rootResults) != 2 || rootResults[0].Status != "success" || rootResults[1].Status != "failed" {
			t.Fatal(rootResults, err)
		}
		finishDeployFixture(t, st, rr, ctx, "root", n, rootResults)
		child := func(source, key string) (string, context.Context, runretry.Execution) {
			t.Helper()
			p, e := rr.Plan(context.Background(), source, []string{n.Key})
			if e != nil || !p.CanSubmit {
				t.Fatal(p, e)
			}
			r, e := rr.Submit(context.Background(), source, runretry.SubmitInput{Scope: "tester", RequestID: key, BaseAttemptID: p.BaseAttemptID, BaseRevision: p.BaseRevision, PlanFingerprint: p.PlanFingerprint, TargetNodeIDs: p.TargetNodeIDs})
			if e != nil {
				t.Fatal(e)
			}
			prepared, e := rr.Prepare(context.Background(), r.AttemptRunID)
			if e != nil {
				t.Fatal(e)
			}
			ex, _ := runretry.ExecutionFrom(prepared)
			if e = ex.Begin(prepared, n); e != nil {
				t.Fatal(e)
			}
			prepared, e = runretry.WithNode(prepared, n)
			if e != nil {
				t.Fatal(e)
			}
			return r.AttemptRunID, prepared, ex
		}
		first, firstCtx, firstEx := child("root", "no-dispatch")
		d.targets = r6LookupUnavailable{Service: targets}
		results, err := d.DeployForStage(firstCtx, first, []string{"a", "b"}, cfg, "")
		if !errors.Is(err, ErrNotDispatched) || len(results) != 0 {
			t.Fatal("read-only failure missing typed boundary", results, err)
		}
		if err = firstEx.Finish(firstCtx, n, runretry.Outcome{Status: "failed", ExecutionMode: "real", ExternalOutcome: "none", DeploymentNotDispatched: true, ReasonCode: "input_unavailable"}); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec("UPDATE pipeline_runs SET status='failed',finished_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339), first); err != nil {
			t.Fatal(err)
		}
		if err = rr.Complete(firstCtx, first, "failed"); err != nil {
			t.Fatal(err)
		}
		// Only this synchronous fixture's simulated worker hold is retired.
		if _, err = st.DB.Exec("UPDATE project_activity SET released_at=1 WHERE run_id=?", first); err != nil {
			t.Fatal(err)
		}
		second, secondCtx, _ := child(first, "retry-partial")
		d.targets = targets
		previous, source, err := runretry.PreviousOutcome(secondCtx, n)
		if err != nil || source != "root" || previous.Status != "failed" || len(previous.Targets) != 2 {
			t.Fatal("actual partial producer lost", previous.Status, source, err)
		}
		results, err = d.DeployForStage(secondCtx, second, []string{"a", "b"}, cfg, "")
		if err != nil || len(results) != 2 || results[0].SourceAttemptID != "root" || results[0].OperationID != rootResults[0].OperationID || targets.calls["a"] != 1 || targets.calls["b"] != 2 {
			t.Fatal(results, targets.calls, err)
		}
		var original string
		if err = st.DB.QueryRow("SELECT state FROM run_retry_external_targets WHERE id=?", rootResults[1].OperationID).Scan(&original); err != nil || original != "failed" {
			t.Fatal("original partial history rewritten", original, err)
		}
	})
}

func TestR6UnprovenFailedDeploymentCannotEraseEarlierTargets(t *testing.T) {
	st := storetest.OpenForDialect(t, store.SQLite)
	rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
	cfg := map[string]string{"artifactType": "command", "restartCommand": "safe-fixture-command"}
	results, err := d.DeployForStage(ctx, "root", []string{"a", "b"}, cfg, "")
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	finishDeployFixture(t, st, rr, ctx, "root", n, results)
	child := func(source, key string) (string, context.Context, runretry.Execution) {
		t.Helper()
		plan, e := rr.Plan(context.Background(), source, []string{n.Key})
		if e != nil || !plan.CanSubmit {
			t.Fatal(plan, e)
		}
		r, e := rr.Submit(context.Background(), source, runretry.SubmitInput{Scope: "tester", RequestID: key, BaseAttemptID: plan.BaseAttemptID, BaseRevision: plan.BaseRevision, PlanFingerprint: plan.PlanFingerprint, TargetNodeIDs: plan.TargetNodeIDs})
		if e != nil {
			t.Fatal(e)
		}
		prepared, e := rr.Prepare(context.Background(), r.AttemptRunID)
		if e != nil {
			t.Fatal(e)
		}
		ex, _ := runretry.ExecutionFrom(prepared)
		if e = ex.Begin(prepared, n); e != nil {
			t.Fatal(e)
		}
		prepared, e = runretry.WithNode(prepared, n)
		if e != nil {
			t.Fatal(e)
		}
		return r.AttemptRunID, prepared, ex
	}
	first, firstCtx, firstEx := child("root", "unproven-failure")
	// A failed checkpoint with neither receipts nor the sealed no-dispatch proof
	// cannot prove that the logical deployment has never acted.
	if err = firstEx.Finish(firstCtx, n, runretry.Outcome{Status: "failed", ExecutionMode: "real", ExternalOutcome: "none"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", first); err != nil {
		t.Fatal(err)
	}
	if err = rr.Complete(firstCtx, first, "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("UPDATE project_activity SET released_at=1 WHERE run_id=?", first); err != nil {
		t.Fatal(err)
	}
	second, secondCtx, _ := child(first, "preserve-older-effects")
	if _, _, err = runretry.PreviousOutcome(secondCtx, n); !errors.Is(err, runretry.ErrUnavailable) {
		t.Errorf("unproven failure accepted as fresh deployment: %v", err)
	}
	if _, err = d.DeployForStage(secondCtx, second, []string{"a", "b"}, cfg, ""); !errors.Is(err, runretry.ErrUnavailable) {
		t.Errorf("missing provenance did not block: %v", err)
	}
	if targets.calls["a"] != 1 || targets.calls["b"] != 1 {
		t.Fatal("unproven failure replayed earlier target", targets.calls)
	}
}

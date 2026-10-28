package runretry

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResourcesRequireActualExitBeforeReadOnlyReconciliation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		r, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "owned-resources"))
		if err != nil {
			t.Fatal(err)
		}
		id := r.AttemptRunID
		prepared, err := f.s.Prepare(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := ExecutionFrom(prepared)
		cleanup, _ := ex.ControlNode("stage", "cleanup")
		if err = ex.Begin(prepared, cleanup); err != nil {
			t.Fatal(err)
		}
		prepared, err = WithNode(prepared, cleanup)
		if err != nil {
			t.Fatal(err)
		}
		ref := ResourceRef{Kind: "container", Name: "owned-name", Owner: "PRIVATE_OWNER_NONCE", RuntimeID: "runtime", ID: strings.Repeat("a", 64)}
		resource, err := TrackResource(prepared, ref)
		if err != nil {
			t.Fatal(err)
		}
		if err = UpdateResource(prepared, resource, ref, "active"); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if err = f.s.Complete(ctx, id, "failed"); err != nil {
			t.Fatal(err)
		}
		var probes atomic.Int32
		absent := false
		f.s.resourceVerifier = func(context.Context, ResourceRef) (bool, error) { probes.Add(1); return absent, nil }
		report, err := f.s.Reconcile(ctx, id)
		if err != nil || report.Drained || probes.Load() != 0 {
			t.Fatal("inspected before real reader exit", report, err, probes.Load())
		}
		if err = f.s.MarkReaderExit(ctx, id, id, "test-owner"); !errors.Is(err, ErrBlocked) {
			t.Fatal("manufactured exit proof", err)
		}
		if err = configstate.StartActivityReaders(id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		configstate.RecordActivityReadersExited(id, "test-owner")
		if err = f.s.MarkReaderExit(ctx, id, id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		report, err = f.s.Reconcile(ctx, id)
		if err != nil || report.Drained || probes.Load() != 1 {
			t.Fatal(report, err, probes.Load())
		}
		absent = true
		report, err = f.s.Reconcile(ctx, id)
		if err != nil || !report.Drained || probes.Load() != 2 {
			t.Fatal(report, err, probes.Load())
		}
		var active int
		_ = f.db.QueryRow("SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL", id).Scan(&active)
		if active != 0 {
			t.Fatal("exact exited activity retained", active)
		}
		configstate.ForgetActivityReaders(id, "test-owner")
		var cipher []byte
		_ = f.db.QueryRow("SELECT ciphertext FROM run_retry_resources WHERE id=?", resource).Scan(&cipher)
		if strings.Contains(string(cipher), "PRIVATE_OWNER_NONCE") {
			t.Fatal("resource identity leaked in plaintext")
		}
	})
}

func TestUnknownCreatorAndExternalTargetBlockDrainEvenWithoutBegunNode(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	if _, e := f.db.Exec("UPDATE run_retry_nodes SET owner_epoch='' WHERE attempt_id='root'"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec("INSERT INTO run_retry_external_targets(id,root_id,node_key,attempt_id,server_id,state,created_at,updated_at) VALUES('inconsistent','root',?,'root','server','running',0,0)", f.nodes["b"].Key); e != nil {
		t.Fatal(e)
	}
	drained, e := f.s.ResourcesDrained(ctx, "root")
	if e != nil || drained {
		t.Fatal("started==0 bypassed target ledger", drained, e)
	}
	c, e := f.s.Context(ctx, "root")
	if e != nil || !c.OutcomeUnknown {
		t.Fatal(c, e)
	}
}

func TestTargetReconcileUsesKnownPersistedExitAndNeverDispatches(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "target-reconcile"))
		if err != nil {
			t.Fatal(err)
		}
		id := request.AttemptRunID
		prepared, err := f.s.Prepare(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := ExecutionFrom(prepared)
		node := f.nodes["b"]
		if err = ex.Begin(prepared, node); err != nil {
			t.Fatal(err)
		}
		op, err := f.s.BeginTarget(prepared, id, node, "server", TargetProof{Kind: "command", ConfigHash: "private", ConnectionFingerprint: "binding"})
		if err != nil {
			t.Fatal(err)
		}
		proof := op.Proof
		proof.ExitKnown = true
		proof.ExitCode = 0
		if err = f.s.ProgressTarget(prepared, id, node.Key, op.ID, proof); err != nil {
			t.Fatal(err)
		}
		if err = ex.Finish(prepared, node, Outcome{Status: "failed", ExecutionMode: "real", ExternalOutcome: "unknown", Targets: []TargetReceipt{{ServerID: "server", Status: "failed", OperationID: op.ID, SourceAttemptID: id}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if err = f.s.Complete(ctx, id, "failed"); err != nil {
			t.Fatal(err)
		}
		probes := 0
		f.s.targetVerifier = func(_ context.Context, o TargetOperation) (string, TargetProof, error) {
			probes++
			if !o.Proof.ExitKnown {
				return "", o.Proof, ErrUnknown
			}
			return "success", o.Proof, nil
		}
		report, err := f.s.Reconcile(ctx, id)
		if err != nil || report.Drained || probes != 0 {
			t.Fatal(report, err, probes)
		}
		if err = configstate.StartActivityReaders(id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		configstate.RecordActivityReadersExited(id, "test-owner")
		if err = f.s.MarkReaderExit(ctx, id, id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		report, err = f.s.Reconcile(ctx, id)
		if err != nil || !report.Drained || probes != 1 || f.dispatched.Load() != 1 {
			t.Fatal(report, err, probes, f.dispatched.Load())
		}
		configstate.ForgetActivityReaders(id, "test-owner")
		p, err := f.s.Plan(ctx, id, []string{node.Key})
		if err != nil || !p.CanSubmit {
			t.Fatal(p, err)
		}
		saved, err := f.s.TargetOperation(ctx, id, node.Key, op.ID)
		if err != nil || saved.State != "success" || saved.AttemptID != id {
			t.Fatal(saved, err)
		}
		view, err := f.s.Context(ctx, id)
		if err != nil || view.OutcomeUnknown {
			t.Fatal(view, err)
		}
	})
}

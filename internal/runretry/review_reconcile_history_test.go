package runretry

import (
	"bytes"
	"context"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

func TestReviewReconcilePreservesCompletedControlEvidence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "history-control"))
		if err != nil {
			t.Fatal(err)
		}
		id := request.AttemptRunID
		prepared, err := f.s.Prepare(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := ExecutionFrom(prepared)
		barrier, _ := ex.ControlNode("stage", "barrier")
		if err = ex.Begin(prepared, barrier); err != nil {
			t.Fatal(err)
		}
		prepared, err = WithNode(prepared, barrier)
		if err != nil {
			t.Fatal(err)
		}
		ref := ResourceRef{Kind: "container", Name: "owned", Owner: "nonce", RuntimeID: "runtime", ID: strings.Repeat("a", 64)}
		resource, err := TrackResource(prepared, ref)
		if err != nil {
			t.Fatal(err)
		}
		if err = UpdateResource(prepared, resource, ref, "drained"); err != nil {
			t.Fatal(err)
		}
		if err = ex.Control(prepared, barrier, Outcome{Status: "success", ExecutionMode: "real", ExternalOutcome: "verified", Complete: true, Reusable: true}); err != nil {
			t.Fatal(err)
		}
		var before []byte
		if err = f.db.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", id, barrier.Key).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if err = f.s.Complete(ctx, id, "failed"); err != nil {
			t.Fatal(err)
		}
		if err = configstate.StartActivityReaders(id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		configstate.RecordActivityReadersExited(id, "test-owner")
		defer configstate.ForgetActivityReaders(id, "test-owner")
		if err = f.s.MarkReaderExit(ctx, id, id, "test-owner"); err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.Reconcile(ctx, id); err != nil {
			t.Fatal(err)
		}
		var status string
		var after []byte
		if err = f.db.QueryRow("SELECT status,ciphertext FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", id, barrier.Key).Scan(&status, &after); err != nil {
			t.Fatal(err)
		}
		if status != "success" || !bytes.Equal(before, after) {
			t.Fatal("reconciliation rewrote completed successful evidence", status)
		}
	})
}

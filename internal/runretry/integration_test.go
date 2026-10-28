package runretry

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestPreparationFailureRestoresEffectiveProducerAndRemainsRetryable(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "prepare-failure"))
		if err != nil {
			t.Fatal(err)
		}
		child := request.AttemptRunID
		if err = f.s.RestorePreparationFailure(ctx, child); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", child); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE project_activity SET released_at=? WHERE run_id=?", time.Now().UnixMilli(), child); err != nil {
			t.Fatal(err)
		}
		if err = f.s.Complete(ctx, child, "failed"); err != nil {
			t.Fatal(err)
		}
		if err = f.s.Settle(ctx, child); err != nil {
			t.Fatal(err)
		}
		view, err := f.s.Context(ctx, child)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range view.Nodes {
			if node.Node.Key == f.nodes["b"].Key {
				if node.Decision.Status != "failed" || node.Decision.SourceAttemptID != f.root || node.ExecutionStarted || node.ReasonCode != "preparation_failed" {
					t.Fatalf("lost original failure: %+v", node)
				}
			}
		}
		plan, err := f.s.Plan(ctx, child, []string{f.nodes["b"].Key})
		if err != nil || !plan.CanSubmit {
			t.Fatalf("plan=%+v err=%v", plan, err)
		}
	})
}

func TestCancelledBeforeExecutionRestoresSourceButRejectsAnyExternalLedger(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-unstarted", true: "external-without-node-owner"}[external], func(t *testing.T) {
			st := storetest.OpenForDialect(t, store.SQLite)
			f := newFixture(t, st)
			ctx := context.Background()
			r, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "cancel-unstarted"))
			if err != nil {
				t.Fatal(err)
			}
			id := r.AttemptRunID
			if _, err = f.db.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			if external {
				if _, err = f.db.Exec("INSERT INTO run_retry_external_targets(id,root_id,node_key,attempt_id,server_id,state,created_at,updated_at) VALUES('external','root',?,?,'server','running',0,0)", f.nodes["b"].Key, id); err != nil {
					t.Fatal(err)
				}
			}
			err = f.s.RestoreQueuedCancellation(ctx, id)
			if external {
				if !errors.Is(err, ErrUnknown) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.s.Complete(ctx, id, "failed"); err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec("UPDATE project_activity SET released_at=1 WHERE run_id=?", id); err != nil {
				t.Fatal(err)
			}
			view, err := f.s.Context(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, n := range view.Nodes {
				if n.Node.Key == f.nodes["b"].Key {
					found = n.Decision.Status == "failed" && n.Decision.SourceAttemptID == "root" && !n.ExecutionStarted && n.ReasonCode == "cancelled_before_start"
				}
			}
			if !found {
				t.Fatal(view.Nodes)
			}
			p, err := f.s.Plan(ctx, id, []string{f.nodes["b"].Key})
			if err != nil || !p.CanSubmit {
				t.Fatal(p, err)
			}
		})
	}
}
func TestTargetOperationScopeEncryptionAndUnknownDrain(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "target-operation"))
		if err != nil {
			t.Fatal(err)
		}
		child := request.AttemptRunID
		prepared, err := f.s.Prepare(ctx, child)
		if err != nil {
			t.Fatal(err)
		}
		ex, _ := ExecutionFrom(prepared)
		node := f.nodes["b"]
		if err = ex.Begin(prepared, node); err != nil {
			t.Fatal(err)
		}
		proof := TargetProof{Kind: "command", ConfigHash: "PRIVATE_CONFIG_DIGEST_NEVER_EXPOSE"}
		op, err := f.s.BeginTarget(prepared, child, node, "server-1", proof)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.BeginTarget(prepared, child, node, "server-1", proof); !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
		if _, err = f.s.TargetOperation(ctx, child, f.nodes["a"].Key, op.ID); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		if err = f.s.FinishTarget(ctx, child, node.Key, op.ID, "outcome_unknown", proof); err != nil {
			t.Fatal(err)
		}
		drained, err := f.s.ResourcesDrained(ctx, child)
		if err != nil || drained {
			t.Fatal(drained, err)
		}
		var ciphertext []byte
		if err = f.db.QueryRow("SELECT ciphertext FROM run_retry_external_targets WHERE id=?", op.ID).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(ciphertext, []byte(proof.ConfigHash)) {
			t.Fatal("private target proof stored clear")
		}
		proof.ExitKnown = true
		if err = f.s.FinishTarget(ctx, child, node.Key, op.ID, "success", proof); err != nil {
			t.Fatal(err)
		}
		actual, err := f.s.TargetOperation(ctx, child, node.Key, op.ID)
		if err != nil || !actual.Proof.ExitKnown || actual.State != "success" {
			t.Fatal(actual, err)
		}
	})
}
func TestLogicalEffectClaimIsOnceAcrossConcurrentCallers(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		var count atomic.Int32
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				claimed, err := f.s.ClaimEffect(ctx, f.root, "chain:success")
				if err != nil {
					failures <- err
					return
				}
				if claimed {
					count.Add(1)
				}
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if count.Load() != 1 {
			t.Fatalf("effects claimed=%d", count.Load())
		}
		if err := f.s.FinishEffect(ctx, f.root, "chain:success"); err != nil {
			t.Fatal(err)
		}
		claimed, err := f.s.ClaimEffect(ctx, f.root, "chain:success")
		if err != nil || claimed {
			t.Fatal(claimed, err)
		}
	})
}
func TestCheckpointAccountingAndQuotaPreserveKnownBusinessOutcome(t *testing.T) {
	st := storetest.Open(t)
	f := newFixture(t, st)
	var stored, actual int64
	if err := f.db.QueryRow("SELECT checkpoint_bytes FROM run_retry_lineages WHERE root_id=?", f.root).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT COALESCE(SUM(LENGTH(ciphertext)),0) FROM run_retry_nodes").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if stored <= 0 || stored != actual {
		t.Fatal(stored, actual)
	}
	ctx := context.Background()
	request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "quota"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := f.s.Prepare(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := ExecutionFrom(prepared)
	if _, err = f.db.Exec("UPDATE run_retry_lineages SET checkpoint_bytes=? WHERE root_id=?", 31<<20, f.root); err != nil {
		t.Fatal(err)
	}
	if err = ex.Begin(prepared, f.nodes["b"]); err != nil {
		t.Fatal(err)
	}
	err = ex.Finish(prepared, f.nodes["b"], Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "verified"})
	if !errors.Is(err, ErrCheckpoint) {
		t.Fatal(err)
	}
	var status, reason string
	var reusable bool
	if err = f.db.QueryRow("SELECT status,reason_code,reusable FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", request.AttemptRunID, f.nodes["b"].Key).Scan(&status, &reason, &reusable); err != nil {
		t.Fatal(err)
	}
	if status != "success" || reason != "checkpoint_budget_exceeded" || reusable {
		t.Fatal(status, reason, reusable)
	}
}

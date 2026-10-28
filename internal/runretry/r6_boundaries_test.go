package runretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestR6CheckpointRejectsSameRootSiblingReplay(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		in, err := f.ex.Inputs(ctx, f.nodes["b"])
		if err != nil || in.Env["TOKEN"] != "DYNAMIC_TOKEN_NEVER_PLAINTEXT" {
			t.Fatal("authorized producer edge rejected", err)
		}
		request, err := f.s.Submit(ctx, "root", submitFor(f.plan(t), "sibling-replay"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec("UPDATE run_retry_nodes SET step_id='producer-step',step_ordinal=9 WHERE attempt_id='root' AND node_key=?", f.nodes["a"].Key); err != nil {
			t.Fatal(err)
		}
		// Replay the valid A cipher and mutable source pointer as B without a key.
		var sourceCipher []byte
		if err = f.db.QueryRow("SELECT ciphertext FROM run_retry_nodes WHERE attempt_id='root' AND node_key=?", f.nodes["a"].Key).Scan(&sourceCipher); err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`UPDATE run_retry_nodes SET status='success',complete=1,reusable=1,source_node_key=?,ciphertext=? WHERE attempt_id='root' AND node_key=?`, f.nodes["a"].Key, sourceCipher, f.nodes["b"].Key); err != nil {
			t.Fatal(err)
		}
		_, err = f.ex.(*execution).Result(ctx, f.nodes["b"])
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("same-root sibling checkpoint substituted: %v", err)
		}
		prepared, err := f.s.Prepare(ctx, request.AttemptRunID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = PreviousOutcome(prepared, f.nodes["b"]); !errors.Is(err, ErrUnavailable) {
			t.Errorf("previous fallback accepted sibling source: %v", err)
		}
		view, err := f.s.Context(ctx, "root")
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range view.Nodes {
			if node.Node.Key == f.nodes["b"].Key && (node.SourceStepID == "producer-step" || node.Decision.SourceNodeKey == f.nodes["a"].Key) {
				t.Error("sibling source-log projection accepted corrupted pointer")
			}
		}
	})
}

func TestR6NoDispatchProofRequiresDeployAndZeroOwnedEffects(t *testing.T) {
	for _, mode := range []string{"none", "target", "drained-resource", "non-deploy", "later-target"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
			ctx := context.Background()
			snapshot := f.snap
			snapshot.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Jobs: []pipeline.Job{{ID: "deploy", Type: "deploy_ssh"}}}}}
			if mode == "non-deploy" {
				snapshot.Spec.Stages[0].Jobs[0].Type = "script"
			}
			if _, err := f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('proof','project','failed','old')"); err != nil {
				t.Fatal(err)
			}
			if err := f.s.SaveManifest(ctx, "proof", snapshot); err != nil {
				t.Fatal(err)
			}
			prepared, err := f.s.Prepare(ctx, "proof")
			if err != nil {
				t.Fatal(err)
			}
			ex, _ := ExecutionFrom(prepared)
			n, _ := ex.Node("deploy", "deploy")
			if err = ex.Begin(prepared, n); err != nil {
				t.Fatal(err)
			}
			prepared, err = WithNode(prepared, n)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "target" {
				if _, err = f.s.BeginTarget(prepared, "proof", n, "server", TargetProof{Kind: "command"}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "drained-resource" {
				ref := ResourceRef{Kind: "network", Name: "fixture", Owner: "fixture", RuntimeID: "fixture"}
				id, e := TrackResource(prepared, ref)
				if e != nil {
					t.Fatal(e)
				}
				if err = UpdateResource(prepared, id, ref, "drained"); err != nil {
					t.Fatal(err)
				}
			}
			err = ex.Finish(prepared, n, Outcome{Status: "failed", ExecutionMode: "real", ExternalOutcome: "none", DeploymentNotDispatched: true})
			if mode == "none" || mode == "later-target" {
				if err != nil {
					t.Fatal(err)
				}
				view, e := f.s.Context(ctx, "proof")
				if e != nil {
					t.Fatal(e)
				}
				wire, e := json.Marshal(view)
				if e != nil || strings.Contains(string(wire), "deploymentNotDispatched") {
					t.Fatal("private proof exposed", e)
				}
				if mode == "later-target" {
					// A subsequent independent ledger contradiction must not be
					// overridden merely because the old private flag decrypts.
					if _, err = f.db.Exec(`INSERT INTO run_retry_external_targets(id,root_id,node_key,attempt_id,server_id,state,ciphertext,created_at,updated_at) VALUES('late-op','proof',?,'proof','server','failed',X'00',0,0)`, n.Key); err != nil {
						t.Fatal(err)
					}
					plan, e := f.s.Plan(ctx, "proof", []string{n.Key})
					if e != nil || !plan.CanSubmit {
						t.Fatal(plan, e)
					}
					r, e := f.s.Submit(ctx, "proof", submitFor(plan, "contradicted-proof"))
					if e != nil {
						t.Fatal(e)
					}
					childCtx, e := f.s.Prepare(ctx, r.AttemptRunID)
					if e != nil {
						t.Fatal(e)
					}
					if _, _, e = PreviousOutcome(childCtx, n); !errors.Is(e, ErrUnknown) {
						t.Fatal("private flag bypassed independent ledger", e)
					}
				}
			} else if mode == "non-deploy" {
				if !errors.Is(err, ErrInvalid) {
					t.Fatal("generic job forged deployment proof", err)
				}
			} else if !errors.Is(err, ErrUnknown) {
				t.Fatal("owned action downgraded to not dispatched", mode, err)
			}
		})
	}
}

func TestR6LargeSelectiveRetryPreservesEveryProducer(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	const producers = 300
	var jobs []pipeline.Job
	var needs []string
	for i := 0; i < producers; i++ {
		id := fmt.Sprintf("producer-%03d", i)
		jobs = append(jobs, pipeline.Job{ID: id, Type: "script"})
		needs = append(needs, id)
	}
	jobs = append(jobs, pipeline.Job{ID: "failed", Type: "script", Needs: needs})
	snapshot := f.snap
	snapshot.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "large", Jobs: jobs}}}
	ctx := context.Background()
	if _, err := f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('large','project','failed','old')"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SaveManifest(ctx, "large", snapshot); err != nil {
		t.Fatal(err)
	}
	prepared, err := f.s.Prepare(ctx, "large")
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := ExecutionFrom(prepared)
	for _, job := range jobs {
		n, _ := ex.Node("large", job.ID)
		status := "success"
		if job.ID == "failed" {
			status = "failed"
		}
		if err = ex.Finish(ctx, n, Outcome{Status: status, ExecutionMode: "real", ExternalOutcome: "none", Complete: status == "success", Reusable: status == "success"}); err != nil {
			t.Fatal(err)
		}
	}
	n, _ := ex.Node("large", "failed")
	plan, err := f.s.Plan(ctx, "large", []string{n.Key})
	if err != nil || !plan.CanSubmit {
		t.Fatal(plan, err)
	}
	started := time.Now()
	request, err := f.s.Submit(ctx, "large", submitFor(plan, "large-selective"))
	t.Logf("Submit %d successful producers: %s", producers, time.Since(started))
	if err != nil {
		t.Fatal(err)
	}
	var refs, inherited, executed int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM run_retry_refs WHERE owner_attempt_id=? AND producer_attempt_id='large' AND object_key='node'", request.AttemptRunID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND origin='reuse' AND source_attempt_id='large' AND source_node_key=node_key", request.AttemptRunID).Scan(&inherited); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow("SELECT COUNT(*) FROM run_retry_nodes WHERE attempt_id=? AND origin='executed' AND node_key=?", request.AttemptRunID, n.Key).Scan(&executed); err != nil {
		t.Fatal(err)
	}
	if refs != producers || inherited != producers || executed != 1 || f.dispatched.Load() != 1 {
		t.Fatal("selective retry changed proofs or dispatch count", refs, inherited, executed, f.dispatched.Load())
	}
}

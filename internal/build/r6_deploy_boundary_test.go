package build

import (
	"context"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/target"
)

type r6Targets struct {
	r3Targets
	unavailable bool
}

func TestR6DeployInputPreparationRecordsNoDispatch(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Kind: "deploy", Jobs: []pipeline.Job{{ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "a", "artifactType": "command", "artifactSource": "absent-producer", "restartCommand": "safe-fixture-command"}}}}}}
	h := newRetryHarness(t, spec)
	targets := &r6Targets{}
	h.b.deployer = deploy.New(targets, run.New(h.store.DB), deploy.WithBatchDB(h.store.DB), deploy.WithRetryLedger(h.s))
	if h.execute(t, "root") == nil {
		t.Fatal("missing declared producer accepted")
	}
	ctx, err := h.s.Prepare(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := runretry.ExecutionFrom(ctx)
	n, _ := ex.Node("deploy", "deploy")
	o, err := ex.(interface {
		Result(context.Context, runretry.NodeRef) (runretry.Outcome, error)
	}).Result(ctx, n)
	if err != nil || !o.DeploymentNotDispatched || o.ExternalOutcome != "none" || o.Status != "failed" || targets.calls.Load() != 0 {
		t.Fatalf("input rejection lost its pre-dispatch proof: status=%s privateProof=%v calls=%d error=%v", o.Status, o.DeploymentNotDispatched, targets.calls.Load(), err)
	}
}

func (r *r6Targets) Get(ctx context.Context, id string) (*target.Server, error) {
	if r.unavailable {
		return nil, errors.New("fixture read-only target lookup unavailable")
	}
	return r.r3Targets.Get(ctx, id)
}

func TestR6DeployReadOnlyRejectionRemainsRetryable(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "build", Kind: "build", AllowFailure: true, Jobs: []pipeline.Job{retryJob("build")}},
		{ID: "deploy", Kind: "deploy", Needs: []string{"build"}, Jobs: []pipeline.Job{{ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "a", "artifactType": "command", "restartCommand": "safe-fixture-command"}}}},
	}}
	h := newRetryHarness(t, spec)
	targets := &r6Targets{}
	h.b.deployer = deploy.New(targets, run.New(h.store.DB), deploy.WithBatchDB(h.store.DB), deploy.WithRetryLedger(h.s))
	h.driver.fail["build"] = true
	if h.execute(t, "root") == nil || targets.calls.Load() != 1 {
		t.Fatal("missing original real deployment", targets.calls.Load())
	}
	buildNode := h.node(t, "root", "build", "build")
	deployNode := h.node(t, "root", "deploy", "deploy")
	h.driver.fail["build"] = false
	targets.unavailable = true
	child := h.submit(t, "root", []string{buildNode.Key}, nil)
	if h.execute(t, child) == nil {
		t.Fatal("expected read-only rejection")
	}
	var state string
	var owned int
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", child, deployNode.Key).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id=?", child).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || owned != 0 || targets.calls.Load() != 1 {
		t.Fatalf("pre-dispatch rejection fabricated uncertainty: status=%s operations=%d activations=%d", state, owned, targets.calls.Load())
	}
	targets.unavailable = false
	second := h.submit(t, child, []string{deployNode.Key}, nil)
	if err := h.execute(t, second); err != nil {
		t.Fatal(err)
	}
	if targets.calls.Load() != 2 || h.driver.counts["build"] != 2 {
		t.Fatal("completed downstream was silently inherited or producer reran", targets.calls.Load(), h.driver.counts)
	}
	var original int
	if err := h.store.DB.QueryRow("SELECT COUNT(*) FROM run_retry_external_targets WHERE attempt_id='root' AND state='success'").Scan(&original); err != nil || original != 1 {
		t.Fatal("original receipt changed", original, err)
	}
}

func TestR6ScheduledSkippedDeploymentRetainsOriginalEffect(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "build", Kind: "build", AllowFailure: true, Jobs: []pipeline.Job{retryJob("build")}},
		{ID: "middle", Kind: "build", Needs: []string{"build"}, Jobs: []pipeline.Job{retryJob("middle")}},
		{ID: "deploy", Kind: "deploy", Needs: []string{"middle"}, Jobs: []pipeline.Job{{ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverId": "a", "artifactType": "command", "restartCommand": "safe-fixture-command"}}}},
	}}
	h := newRetryHarness(t, spec)
	targets := &r6Targets{}
	h.b.deployer = deploy.New(targets, run.New(h.store.DB), deploy.WithBatchDB(h.store.DB), deploy.WithRetryLedger(h.s))
	h.driver.fail["build"] = true
	if h.execute(t, "root") == nil || targets.calls.Load() != 1 {
		t.Fatal("allowFailure downstream not executed", targets.calls.Load())
	}
	buildNode := h.node(t, "root", "build", "build")
	middleNode := h.node(t, "root", "middle", "middle")
	deployNode := h.node(t, "root", "deploy", "deploy")
	var operation string
	var originalProof []byte
	if err := h.store.DB.QueryRow("SELECT id,ciphertext FROM run_retry_external_targets WHERE attempt_id='root' AND node_key=? AND state='success'", deployNode.Key).Scan(&operation, &originalProof); err != nil {
		t.Fatal(err)
	}
	h.driver.fail["build"], h.driver.fail["middle"] = false, true
	first := h.submit(t, "root", []string{buildNode.Key}, nil)
	if h.execute(t, first) == nil {
		t.Fatal("middle failure not propagated")
	}
	var state string
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", first, deployNode.Key).Scan(&state); err != nil || (state != "pending" && state != "skipped") || targets.calls.Load() != 1 {
		t.Fatal("deployment ran despite failed dependency", state, targets.calls.Load(), err)
	}
	// Restore the upstream condition, but remove the old target's encrypted proof.
	// The later retry must still inspect that original action instead of treating
	// the skipped intermediate attempt as evidence the deployment never ran.
	h.driver.fail["middle"] = false
	if _, err := h.store.DB.Exec("UPDATE run_retry_external_targets SET ciphertext=X'00' WHERE id=?", operation); err != nil {
		t.Fatal(err)
	}
	second := h.submit(t, first, []string{middleNode.Key}, nil)
	if h.execute(t, second) == nil || targets.calls.Load() != 1 {
		t.Fatal("skipped ancestor erased the original effect", targets.calls.Load())
	}
	if err := h.store.DB.QueryRow("SELECT status FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", second, deployNode.Key).Scan(&state); err != nil || state != "failed" {
		t.Fatal("read-only verification became unknown", state, err)
	}
	if _, err := h.store.DB.Exec("UPDATE run_retry_external_targets SET ciphertext=? WHERE id=?", originalProof, operation); err != nil {
		t.Fatal(err)
	}
	third := h.submit(t, second, []string{deployNode.Key}, nil)
	if err := h.execute(t, third); err != nil {
		t.Fatal(err)
	}
	if targets.calls.Load() != 2 || h.driver.counts["build"] != 2 || h.driver.counts["middle"] != 3 {
		t.Fatal("wrong continuation/reuse counts", targets.calls.Load(), h.driver.counts)
	}
	var originalState string
	if err := h.store.DB.QueryRow("SELECT state FROM run_retry_external_targets WHERE id=?", operation).Scan(&originalState); err != nil || originalState != "success" {
		t.Fatal("original receipt rewritten", originalState, err)
	}
}

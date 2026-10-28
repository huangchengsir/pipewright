package runretry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/retention"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestR3InitiatorCreationTimeAndTriggerAreIndependent(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	var created string
	if e := f.db.QueryRow("SELECT created_at FROM pipeline_runs WHERE id='root'").Scan(&created); e != nil {
		t.Fatal(e)
	}
	actual, _ := time.Parse(time.RFC3339, created)
	page, e := f.s.Attempts(ctx, "root", 1, 10)
	if e != nil || !page.Items[0].CreatedAt.Equal(actual) {
		t.Fatal(page, e)
	}
	p := f.plan(t)
	input := submitFor(p, "initiator")
	input.Initiator = "authenticated-user"
	request, e := f.s.Submit(ctx, "root", input)
	if e != nil || request.Initiator != "authenticated-user" {
		t.Fatal(request, e)
	}
	execCtx, e := f.s.Prepare(ctx, request.AttemptRunID)
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := ExecutionFrom(execCtx)
	if ex.Snapshot().Trigger.Type != f.snap.Trigger.Type || ex.Snapshot().Trigger.Branch != f.snap.Trigger.Branch || ex.Snapshot().Trigger.Actor != f.snap.Trigger.Actor {
		t.Fatal("frozen trigger overwritten")
	}
	var wire SubmitInput
	if json.Unmarshal([]byte(`{"initiator":"forged"}`), &wire) != nil || wire.Initiator != "" {
		t.Fatal("client can forge initiator")
	}
}
func TestR3OrphanGCClaimsImageOutsideTransactionAndNoLateEffect(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	node := f.nodes["b"]
	prepared := WithExecution(ctx, f.ex)
	prepared, _ = WithNode(prepared, node)
	image := ImageRef{Ref: "pipewright/test:owned-0123456789abcdef", ImageID: "sha256:" + strings.Repeat("b", 64), RuntimeID: "runtime", ConfigSHA: "config"}
	if e := RememberImage(prepared, image); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec("DELETE FROM pipeline_runs WHERE id='root'"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	n, e := f.s.PruneOrphans(ctx, time.Now(), func(context.Context, ImageRef) error {
		calls++
		var claims int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM run_retry_effects WHERE state='gc_claimed'").Scan(&claims); e != nil || claims != 1 {
			t.Fatal(claims, e)
		}
		return nil
	})
	if e != nil || n != 1 || calls != 1 {
		t.Fatal(n, calls, e)
	}
	claimed, e := f.s.ClaimEffect(ctx, "root", "late-hook")
	if e != nil || claimed {
		t.Fatal("GC run treated as legacy", claimed, e)
	}
}
func TestR3FailedImageGCIsNotAutomaticallyRetried(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := WithExecution(context.Background(), f.ex)
	ctx, _ = WithNode(ctx, f.nodes["b"])
	if e := RememberImage(ctx, ImageRef{Ref: "pipewright/test:tag-0123456789abcdef", ImageID: "sha256:" + strings.Repeat("b", 64), RuntimeID: "runtime", ConfigSHA: "config"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec("DELETE FROM pipeline_runs WHERE id='root'"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	release := func(context.Context, ImageRef) error { calls++; return errors.New("in use or identity drift") }
	for range 2 {
		n, e := f.s.PruneOrphans(ctx, time.Now(), release)
		if e != nil || n != 0 {
			t.Fatal(n, e)
		}
	}
	if calls != 1 {
		t.Fatal("uncertain cleanup replayed", calls)
	}
}
func TestR3OutputPolicyAndFrozenEmptyDependencies(t *testing.T) {
	now := time.Now()
	o := Outcome{Complete: true, Reusable: true}
	ApplyOutputPolicy(pipeline.Job{Config: map[string]any{"checkpoint": map[string]any{"reusable": false, "ttlSeconds": 60}}}, &o, now)
	if o.Reusable || o.ValidUntil == nil || !o.ValidUntil.Equal(now.Add(time.Minute)) {
		t.Fatal(o)
	}
	s := Snapshot{Compiled: &FrozenGraph{Nodes: []NodeRef{{Key: "a", ExecutionStageID: "s", ExecutionJobID: "a"}, {Key: "b", ExecutionStageID: "s", ExecutionJobID: "b"}}}}
	deps, e := s.JobDependencies("s")
	if e != nil || len(deps["a"])+len(deps["b"]) != 0 {
		t.Fatal(deps, e)
	}
}
func TestR3GCWeakCandidatesRotateAndDeletedProjectDoesNotBlock(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	for i := range 18 {
		id := fmt.Sprintf("orphan-%02d", i)
		if _, e := f.db.Exec("INSERT INTO run_retry_lineages(root_id,project_id,head_attempt_id,revision,updated_at) VALUES(?,'project',?,1,0)", id, id); e != nil {
			t.Fatal(e)
		}
		if _, e := f.db.Exec("INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at) VALUES(?,?,'',1,'original','failed',0)", id, id); e != nil {
			t.Fatal(e)
		}
		if i < 16 {
			if _, e := f.db.Exec("INSERT INTO run_retry_resources(id,root_id,attempt_id,node_key,state,ciphertext,updated_at) VALUES(?,?,?,'node','outcome_unknown',X'00',0)", id, id, id); e != nil {
				t.Fatal(e)
			}
		}
	}
	now := time.Now()
	n, e := f.s.PruneOrphans(ctx, now, nil)
	if e != nil || n != 0 {
		t.Fatal(n, e)
	}
	n, e = f.s.PruneOrphans(ctx, now.Add(time.Second), nil)
	if e != nil || n != 2 {
		t.Fatal("old unknowns starved clear candidates", n, e)
	}
	if _, e = f.db.Exec("DELETE FROM projects WHERE id='project'"); e != nil {
		t.Fatal(e)
	}
	n, e = f.s.PruneOrphans(ctx, now.Add(2*time.Second), nil)
	if e != nil {
		t.Fatal("missing project blocked GC", n, e)
	}
	var unknown int
	if e = f.db.QueryRow("SELECT COUNT(*) FROM run_retry_resources WHERE state='outcome_unknown'").Scan(&unknown); e != nil || unknown != 16 {
		t.Fatal("unknown resources erased", unknown, e)
	}
}
func TestR3RetentionSubmitRaceNeverDeletesAcceptedProducer(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	p := f.plan(t)
	pruner := retention.NewService(f.db)
	if e := pruner.SetConfig(ctx, retention.Config{Enabled: true, MaxAgeDays: 1}); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var request *Request
	var submitErr, pruneErr error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; request, submitErr = f.s.Submit(ctx, "root", submitFor(p, "race")) }()
	go func() { defer wg.Done(); <-start; _, pruneErr = pruner.Prune(ctx, time.Now().Add(48*time.Hour)) }()
	close(start)
	wg.Wait()
	if pruneErr != nil {
		t.Fatal(pruneErr)
	}
	if submitErr == nil {
		var producer int
		if request == nil {
			t.Fatal("accepted without identity")
		}
		if e := f.db.QueryRow("SELECT COUNT(*) FROM pipeline_runs WHERE id='root'").Scan(&producer); e != nil || producer != 1 {
			t.Fatal("accepted producer removed", producer, e)
		}
	}
}
func TestR3ReservedImageIdentitySurvivesBusinessCheckpointQuota(t *testing.T) {
	f := newFixture(t, storetest.OpenForDialect(t, store.SQLite))
	ctx := context.Background()
	request, e := f.s.Submit(ctx, "root", submitFor(f.plan(t), "quota-identity"))
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := f.s.Prepare(ctx, request.AttemptRunID)
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := ExecutionFrom(prepared)
	n := f.nodes["b"]
	if e = ex.Begin(prepared, n); e != nil {
		t.Fatal(e)
	}
	prepared, _ = WithNode(prepared, n)
	if _, e = f.db.Exec("UPDATE run_retry_lineages SET checkpoint_bytes=? WHERE root_id='root'", 31<<20); e != nil {
		t.Fatal(e)
	}
	image := ImageRef{Ref: "pipewright/test:tag-0123456789abcdef", ImageID: "sha256:" + strings.Repeat("b", 64), RuntimeID: "runtime", ConfigSHA: "config"}
	if e = RememberImage(prepared, image); e != nil {
		t.Fatal(e)
	}
	e = ex.Finish(prepared, n, Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, Outputs: Inputs{Images: map[string]ImageRef{"image": image}}})
	if !errors.Is(e, ErrCheckpoint) {
		t.Fatal(e)
	}
	var status, reason string
	var identities, total int64
	if e = f.db.QueryRow("SELECT status,reason_code FROM run_retry_nodes WHERE attempt_id=? AND node_key=?", request.AttemptRunID, n.Key).Scan(&status, &reason); e != nil || status != "success" || reason != "checkpoint_budget_exceeded" {
		t.Fatal(status, reason, e)
	}
	if e = f.db.QueryRow("SELECT identity_bytes,identity_bytes+checkpoint_bytes FROM run_retry_lineages WHERE root_id='root'").Scan(&identities, &total); e != nil || identities <= 0 || identities > 1<<20 || total > 32<<20 {
		t.Fatal(identities, total, e)
	}
}

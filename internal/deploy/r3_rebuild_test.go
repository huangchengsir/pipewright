package deploy

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/artifactstore"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

type r3ArtifactRuns struct {
	run.Service
	artifact run.Artifact
}

func (r *r3ArtifactRuns) ListArtifacts(context.Context, string) ([]run.Artifact, error) {
	return []run.Artifact{r.artifact}, nil
}
func TestR3CompletedDeployWithAuthorizedRebuiltArtifactExecutesFresh(t *testing.T) {
	st := storetest.OpenForDialect(t, store.SQLite)
	rr, runs, d, targets, ctx, node := retryDeployFixture(t, st, func(s *runretry.Snapshot) {
		s.Spec.Stages = append([]pipeline.Stage{{ID: "producer", Name: "producer", Jobs: []pipeline.Job{{ID: "producer", Type: "script"}}}, {ID: "test", Name: "test", Needs: []string{"producer"}, AllowFailure: true, Jobs: []pipeline.Job{{ID: "test", Type: "script"}}}}, s.Spec.Stages...)
		s.Spec.Stages[2].Needs = []string{"test"}
	})
	artifacts, e := artifactstore.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	first, size, e := artifacts.Put(strings.NewReader("archive version one"))
	if e != nil {
		t.Fatal(e)
	}
	view := &r3ArtifactRuns{Service: runs, artifact: run.Artifact{ID: "artifact-v1", RunID: "root", Type: run.ArtifactJar, Name: "app", Reference: first, SizeBytes: size, Metadata: map[string]any{"stored": true, "format": "file", "filename": "app.jar"}}}
	d.runs = view
	d.artStore = artifacts
	targets.failures = 0
	ex, _ := runretry.ExecutionFrom(ctx)
	producer, _ := ex.Node("producer", "producer")
	testNode, _ := ex.Node("test", "test")
	if e = ex.Begin(ctx, producer); e != nil {
		t.Fatal(e)
	}
	if e = ex.Finish(ctx, producer, runretry.Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, WorkspaceIncomplete: true}); e != nil {
		t.Fatal(e)
	}
	if e = ex.Begin(ctx, testNode); e != nil {
		t.Fatal(e)
	}
	if e = ex.Finish(ctx, testNode, runretry.Outcome{Status: "failed", ExecutionMode: "real"}); e != nil {
		t.Fatal(e)
	}
	if e = ex.Begin(ctx, node); e != nil {
		t.Fatal(e)
	}
	cfg := map[string]string{"artifactType": "jar", "releaseBase": "/srv/fixture", "keepReleases": "100"}
	results, e := d.DeployForStage(ctx, "root", []string{"a"}, cfg, "")
	if e != nil || len(results) != 1 || results[0].Status != "success" {
		t.Fatal(results, e)
	}
	receipts := []runretry.TargetReceipt{{ServerID: "a", Status: "success", OperationID: results[0].OperationID, SourceAttemptID: "root"}}
	if e = ex.Finish(ctx, node, runretry.Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "verified", Targets: receipts}); e != nil {
		t.Fatal(e)
	}
	if _, e = st.DB.Exec("UPDATE pipeline_runs SET status='failed' WHERE id='root'"); e != nil {
		t.Fatal(e)
	}
	if e = rr.Complete(ctx, "root", "failed"); e != nil {
		t.Fatal(e)
	}
	p, e := rr.Plan(context.Background(), "root", []string{testNode.Key}, runretry.PlanOptions{RebuildNodeIDs: []string{producer.Key}})
	if e != nil || !p.CanSubmit {
		t.Fatal(p, e)
	}
	request, e := rr.Submit(context.Background(), "root", runretry.SubmitInput{Scope: "fixture", RequestID: "changed-input", BaseAttemptID: p.BaseAttemptID, BaseRevision: p.BaseRevision, PlanFingerprint: p.PlanFingerprint, TargetNodeIDs: p.TargetNodeIDs, RebuildNodeIDs: p.RebuildNodeIDs})
	if e != nil {
		t.Fatal(e)
	}
	next, e := rr.Prepare(context.Background(), request.AttemptRunID)
	if e != nil {
		t.Fatal(e)
	}
	childEx, _ := runretry.ExecutionFrom(next)
	for _, n := range []runretry.NodeRef{producer, testNode} {
		if e = childEx.Begin(next, n); e != nil {
			t.Fatal(e)
		}
		if e = childEx.Finish(next, n, runretry.Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true}); e != nil {
			t.Fatal(e)
		}
	}
	second, secondSize, e := artifacts.Put(strings.NewReader("archive version two"))
	if e != nil {
		t.Fatal(e)
	}
	view.artifact.ID = "artifact-v2"
	view.artifact.RunID = request.AttemptRunID
	view.artifact.Reference = second
	view.artifact.SizeBytes = secondSize
	if e = childEx.Begin(next, node); e != nil {
		t.Fatal(e)
	}
	next, e = runretry.WithNode(next, node)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := d.DeployForStage(next, request.AttemptRunID, []string{"a"}, cfg, "")
	if e != nil || len(fresh) != 1 || fresh[0].Status != "success" || fresh[0].OperationID == results[0].OperationID || targets.activations != 2 {
		t.Fatal(fresh, e, targets.activations)
	}
	old, e := rr.TargetOperation(next, request.AttemptRunID, node.Key, results[0].OperationID)
	if e != nil || old.Proof.ArtifactReference != first || old.AttemptID != "root" {
		t.Fatal("old target operation mutated", old, e)
	}
}

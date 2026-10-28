package run

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/vault"
	"testing"
)

type r3StrictExecution struct {
	runretry.Execution
	n runretry.NodeRef
}

func (e r3StrictExecution) Node(string, string) (runretry.NodeRef, bool) { return e.n, true }
func (e r3StrictExecution) Inputs(context.Context, runretry.NodeRef) (runretry.Inputs, error) {
	return runretry.Inputs{}, runretry.ErrUnavailable
}
func TestR3LostManifestKeyPreservesOwnReadViewButStrictConsumerFails(t *testing.T) {
	db := testDB(t)
	s := newService(db)
	project := seedProject(t, db)
	ctx := context.Background()
	r, e := s.Create(ctx, project, Trigger{})
	if e != nil {
		t.Fatal(e)
	}
	own, e := s.AddArtifact(ctx, Artifact{RunID: r.ID, Type: ArtifactArchive, Name: "own", Reference: "own-ref"})
	if e != nil {
		t.Fatal(e)
	}
	var oldKey, newKey [32]byte
	oldKey[0] = 1
	newKey[0] = 2
	ledger := runretry.New(db, vault.New(db, &oldKey))
	if e = ledger.SaveManifest(ctx, r.ID, runretry.Snapshot{ProjectID: project, Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Jobs: []pipeline.Job{{ID: "job", Type: "script"}}}}}}); e != nil {
		t.Fatal(e)
	}
	s.retryRuntime = runretry.New(db, vault.New(db, &newKey))
	arts, e := s.ListArtifacts(ctx, r.ID)
	if e != nil || len(arts) != 1 || arts[0].ID != own.ID {
		t.Fatal(arts, e)
	}
	n := runretry.NodeRef{Key: "logical", ExecutionStageID: "stage", ExecutionJobID: "job"}
	strict := runretry.WithExecution(ctx, r3StrictExecution{n: n})
	strict, e = runretry.WithNode(strict, n)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ListArtifacts(strict, r.ID); !errors.Is(e, runretry.ErrUnavailable) {
		t.Fatal("unverified strict input returned", e)
	}
}

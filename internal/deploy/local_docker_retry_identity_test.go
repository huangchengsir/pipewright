package deploy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Real SQLite/CAS/append-only deployment receipts, with modeled remote argv.
// Reused artifacts retain their independently persisted original producer.
func TestLocalDockerRecoveredArtifactKeepsProducerAndLabelsCurrentActivation(t *testing.T) {
	f := newLocalDockerFixture(t, "file")
	ctx := WithSourceStage(context.Background(), "current-logical-deploy-node")
	service := f.svc.(*service)
	actualRuns := service.runs
	parent := f.in.RunID
	artifacts, err := actualRuns.ListArtifacts(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	var original run.Artifact
	for _, artifact := range artifacts {
		if artifact.ID == f.in.ArtifactID {
			original = artifact
		}
	}
	if original.ID == "" || original.RunID != parent {
		t.Fatal("actual producer artifact missing")
	}
	child := "current-deployment-attempt"
	if _, err = f.db.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) SELECT ?,project_id,'running',created_at FROM pipeline_runs WHERE id=?", child, parent); err != nil {
		t.Fatal(err)
	}
	// This resolver projects the original immutable artifact to the consumer;
	// no new AddArtifact call is made for the deployment attempt.
	service.runs = &r3ArtifactRuns{Service: actualRuns, artifact: original}
	oldExec := f.targets.execFn
	var start []string
	f.targets.execFn = func(server string, args []string) (*target.ExecResult, error) {
		if hasPrefixTokens(args, "docker", "run") {
			start = append([]string(nil), args...)
			attempts, err := f.ledger.ListByRun(ctx, child)
			if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 || attempts[0].Targets[0].Outcome != deployattempt.ActivationStarted {
				t.Fatal("current activation was not persisted before run", err)
			}
			return &target.ExecResult{Stdout: f.cid + "\n"}, nil
		}
		return oldExec(server, args)
	}
	results, err := f.svc.DeployForStage(ctx, child, f.in.ServerIDs, f.in.Config, "")
	if err != nil || len(results) != 1 || results[0].Status != run.TargetSuccess {
		t.Fatal(results, err)
	}
	attempts, err := f.ledger.ListByRun(ctx, child)
	if err != nil || len(attempts) != 1 || len(attempts[0].Targets) != 1 {
		t.Fatal("current receipt missing", err)
	}
	proof := attempts[0].Targets[0]
	labels := map[string]string{}
	for i, arg := range start {
		if arg == "--label" && i+1 < len(start) {
			key, value, ok := strings.Cut(start[i+1], "=")
			if ok {
				labels[key] = value
			}
		}
	}
	if labels["pipewright.run"] != child {
		t.Errorf("activation mislabeled as producer: got=%s current=%s producer=%s", labels["pipewright.run"], child, parent)
	}
	if proof.Source.RunID != child || proof.Source.StageID != "current-logical-deploy-node" || proof.ArtifactID != original.ID || labels["pipewright.deployment-target"] != proof.ID || labels["pipewright.server"] != proof.ServerID || !deployattempt.ValidLocalExpected(proof.ExpectedImage) || proof.ExpectedImage.Digest != f.imageID || proof.Outcome != deployattempt.Succeeded {
		t.Fatal("exact activation/source/target identity changed")
	}
	if len(proof.Receipts) != 3 || proof.Receipts[2].ActualImage.ContainerID != f.cid || proof.Receipts[2].ActualImage.ConfigID != f.imageID {
		t.Fatal("full actual container/config receipt changed")
	}
	var producerRun string
	if err = f.db.QueryRow("SELECT run_id FROM run_artifacts WHERE id=?", original.ID).Scan(&producerRun); err != nil || producerRun != parent {
		t.Fatal("producer row was rewritten", producerRun, err)
	}
	after, err := actualRuns.ListArtifacts(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, artifact := range after {
		if artifact.ID == original.ID {
			found = true
			if !reflect.DeepEqual(artifact, original) {
				t.Fatal("producer metadata/history changed")
			}
		}
	}
	if !found {
		t.Fatal("original artifact lost")
	}
	var childArtifacts int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM run_artifacts WHERE run_id=?", child).Scan(&childArtifacts); err != nil || childArtifacts != 0 {
		t.Fatal("inherited artifact fabricated as new build", childArtifacts, err)
	}
}

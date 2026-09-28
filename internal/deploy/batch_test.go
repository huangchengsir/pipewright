package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

func TestDeployBatchPersistsIndependentResultsAndRetriesOnlyFailed(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	server := seedServer(t, targets, "test")
	runID, firstID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/one")
	second, err := runs.AddArtifact(context.Background(), run.Artifact{RunID: runID, Type: run.ArtifactJar, Name: "Backend", Reference: "jar/two"})
	if err != nil {
		t.Fatal(err)
	}
	failSecond := true
	targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		if failSecond && len(cmd) > 1 && cmd[0] == "mkdir" && strings.Contains(strings.Join(cmd, " "), "/srv/backend") {
			return &target.ExecResult{ExitCode: 2}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	input := BatchInput{RunID: runID, IdempotencyKey: "batch-one", Items: []BatchRequestItem{
		{ArtifactID: firstID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/frontend"}},
		{ArtifactID: second.ID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/backend"}},
	}}
	result, err := svc.DeployBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial_failed" || len(result.Items) != 2 || result.Items[0].Status != "success" || result.Items[1].Status != "failed" {
		t.Fatalf("unexpected first batch result: %+v", result)
	}
	if result.Items[0].Attempt != 1 || result.Items[1].Attempt != 1 {
		t.Fatalf("attempt counts: %+v", result.Items)
	}
	count := len(targets.calls)
	duplicate, err := svc.DeployBatch(context.Background(), input)
	if err != nil || duplicate.ID != result.ID || len(targets.calls) != count {
		t.Fatalf("idempotent submission reran commands: %v, %+v", err, duplicate)
	}
	if _, err := svc.DeployBatch(context.Background(), BatchInput{RunID: runID, IdempotencyKey: "batch-one", Items: input.Items[:1]}); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("reused key for other request: %v", err)
	}
	failSecond = false
	retried, err := svc.RetryBatch(context.Background(), runID, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "success" || retried.Items[0].Attempt != 1 || retried.Items[1].Attempt != 2 {
		t.Fatalf("retry should leave successful item untouched: %+v", retried)
	}
	legacy, err := runs.ListDeployTargets(context.Background(), runID)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("batch touched legacy target records: %+v, %v", legacy, err)
	}
	listed, err := svc.ListBatches(context.Background(), runID)
	if err != nil || len(listed) != 1 || listed[0].ID != result.ID {
		t.Fatalf("persisted list: %+v, %v", listed, err)
	}
}

func TestDeployBatchDifferentServersKeepIndependentResults(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	first := seedServer(t, targets, "first")
	second := seedServer(t, targets, "second")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/app")
	failSecond := true
	called := map[string]int{}
	targets.execFn = func(serverID string, cmd []string) (*target.ExecResult, error) {
		called[serverID]++
		if failSecond && serverID == second.ID && len(cmd) > 1 && cmd[0] == "mkdir" {
			return &target.ExecResult{ExitCode: 2}, nil
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	result, err := svc.DeployBatch(context.Background(), BatchInput{RunID: runID, IdempotencyKey: "different-servers", Items: []BatchRequestItem{
		{ArtifactID: artifactID, ServerIDs: []string{first.ID}, Config: map[string]string{"path": "/srv/first"}},
		{ArtifactID: artifactID, ServerIDs: []string{second.ID}, Config: map[string]string{"path": "/srv/second"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].ServerID != first.ID || result.Items[0].Status != "success" || result.Items[1].ServerID != second.ID || result.Items[1].Status != "failed" {
		t.Fatalf("cross-server result isolation: %+v", result)
	}
	firstCalls := called[first.ID]
	failSecond = false
	retried, err := svc.RetryBatch(context.Background(), runID, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "success" || retried.Items[0].Attempt != 1 || retried.Items[1].Attempt != 2 || called[first.ID] != firstCalls || called[second.ID] == 0 {
		t.Fatalf("cross-server retry isolation: %+v, calls=%+v", retried, called)
	}
}

func TestDeployBatchRejectsOverlappingPathsBeforeRemoteEffects(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	server := seedServer(t, targets, "test")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/one")
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	input := BatchInput{RunID: runID, IdempotencyKey: "overlap", Items: []BatchRequestItem{
		{ArtifactID: artifactID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/app"}},
		{ArtifactID: artifactID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/app/subdir"}},
	}}
	if _, err := svc.DeployBatch(context.Background(), input); !errors.Is(err, ErrBatchInvalid) {
		t.Fatalf("overlap accepted: %v", err)
	}
	if len(targets.calls) != 0 {
		t.Fatalf("preflight performed remote effects: %v", targets.calls)
	}
	listed, err := svc.ListBatches(context.Background(), runID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("invalid batch persisted: %+v, %v", listed, err)
	}
}

func TestDeployBatchRequiresExplicitPath(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	server := seedServer(t, targets, "test")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/one")
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	_, err := svc.DeployBatch(context.Background(), BatchInput{RunID: runID, IdempotencyKey: "no-path", Items: []BatchRequestItem{
		{ArtifactID: artifactID, ServerIDs: []string{server.ID}, Config: map[string]string{}},
	}})
	if !errors.Is(err, ErrBatchInvalid) || len(targets.calls) != 0 {
		t.Fatalf("missing path should fail before any remote effect: %v, %v", err, targets.calls)
	}
}

func TestDeployBatchHistoryCascadesWhenRunIsPruned(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	server := seedServer(t, targets, "test")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/one")
	targets.execFn = func(_ string, _ []string) (*target.ExecResult, error) { return &target.ExecResult{ExitCode: 0}, nil }
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	result, err := svc.DeployBatch(context.Background(), BatchInput{RunID: runID, IdempotencyKey: "prune", Items: []BatchRequestItem{
		{ArtifactID: artifactID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/app"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM pipeline_runs WHERE id=?`, runID); err != nil {
		t.Fatalf("run retention must not be blocked by batch history: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_batch_items WHERE batch_id=?`, result.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned batch items: %d, %v", count, err)
	}
}

func TestDeployBatchCanceledRequestCanContinuePendingItems(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	server := seedServer(t, targets, "test")
	runID, firstID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactDist, "dist/one")
	second, err := runs.AddArtifact(context.Background(), run.Artifact{RunID: runID, Type: run.ArtifactJar, Name: "Backend", Reference: "jar/two"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		if len(cmd) > 1 && cmd[0] == "mkdir" && strings.Contains(strings.Join(cmd, " "), "/srv/frontend") {
			cancel()
		}
		return &target.ExecResult{ExitCode: 0}, nil
	}
	svc := New(targets, runs, WithBatchDB(db)).(*service)
	result, err := svc.DeployBatch(ctx, BatchInput{RunID: runID, IdempotencyKey: "cancel", Items: []BatchRequestItem{
		{ArtifactID: firstID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/frontend"}},
		{ArtifactID: second.ID, ServerIDs: []string{server.ID}, Config: map[string]string{"path": "/srv/backend"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == "running" || result.Items[1].Status != "pending" {
		t.Fatalf("canceled request left batch running or lost pending item: %+v", result)
	}
	targets.execFn = func(_ string, _ []string) (*target.ExecResult, error) { return &target.ExecResult{ExitCode: 0}, nil }
	resumed, err := svc.ContinueBatch(context.Background(), runID, result.ID)
	if err != nil || resumed.Items[1].Status != "success" || resumed.Items[1].Attempt != 1 {
		t.Fatalf("continue pending item: %+v, %v", resumed, err)
	}
}

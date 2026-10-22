package deploy

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/target"
)

type failingAttemptWriter struct {
	deployattempt.Writer
	fail string
}

func (f failingAttemptWriter) Begin(ctx context.Context, in deployattempt.BeginInput) (*deployattempt.Attempt, error) {
	if f.fail == "begin" {
		return nil, errors.New("injected storage failure")
	}
	return f.Writer.Begin(ctx, in)
}
func (f failingAttemptWriter) StartTarget(ctx context.Context, id string, in deployattempt.TargetInput) (*deployattempt.Target, error) {
	if f.fail == "target" {
		return nil, errors.New("injected storage failure")
	}
	return f.Writer.StartTarget(ctx, id, in)
}

func (f failingAttemptWriter) AppendReceipt(ctx context.Context, id string, r deployattempt.Receipt) (*deployattempt.Receipt, error) {
	if r.Kind == f.fail {
		return nil, errors.New("injected storage failure")
	}
	return f.Writer.AppendReceipt(ctx, id, r)
}

type imageObserverFixture struct {
	mu     sync.Mutex
	events []string
}

func (o *imageObserverFixture) DeploymentStarted(context.Context, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "started")
}
func (o *imageObserverFixture) BeforeImageActivation(context.Context, string, string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "before")
}
func (o *imageObserverFixture) AfterImageActivation(_ context.Context, _, _, id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, "after:"+id)
}

func TestImageReceiptDeploymentPreservesIdentityAcrossRedeploy(t *testing.T) {
	for _, strategy := range []string{StrategyRolling, StrategyCanary, StrategyBlueGreen} {
		t.Run(strategy, func(t *testing.T) {
			db := testDB(t)
			runs := run.New(db)
			targets := &stubTarget{}
			srv := seedServer(t, targets, "web")
			runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "reg.invalid/app:stable")
			ledger := deployattempt.New(db)
			o := &imageObserverFixture{}
			id := strings.Repeat("1", 64)
			targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
				out := &target.ExecResult{}
				if len(cmd) > 1 && cmd[1] == "run" {
					out.Stdout = id
				}
				return out, nil
			}
			svc := New(targets, runs, WithAttemptLedger(ledger), WithImageObserver(o))
			in := DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}, Config: map[string]string{"containerName": "app"}, Strategy: strategy}
			if res, err := svc.Deploy(context.Background(), in); err != nil || res[0].Status != run.TargetSuccess {
				t.Fatal(res, err)
			}
			id = strings.Repeat("2", 64)
			if res, err := svc.Deploy(context.Background(), in); err != nil || res[0].Status != run.TargetSuccess {
				t.Fatal(res, err)
			}
			attempts, err := ledger.ListByRun(context.Background(), runID)
			if err != nil || len(attempts) != 2 {
				t.Fatal(attempts, err)
			}
			seen := map[string]bool{}
			for _, a := range attempts {
				if len(a.Targets) != 1 {
					t.Fatal(a)
				}
				tr := a.Targets[0]
				if tr.Outcome != deployattempt.Succeeded || len(tr.Receipts) != 3 {
					t.Fatal(tr)
				}
				seen[tr.Receipts[0].ActualImage.ContainerID] = true
				seen[tr.Receipts[1].ActualImage.ContainerID] = true
				if tr.ExpectedImage.DigestKind != deployattempt.DigestUnavailable || tr.IdentityTrust != "host_identity_unverified" {
					t.Fatal(tr)
				}
			}
			if !seen[strings.Repeat("1", 64)] || !seen[strings.Repeat("2", 64)] {
				t.Fatal(seen)
			}
			if len(targets.calls) != 8 {
				t.Fatal("ledger added remote commands", targets.calls)
			}
			if len(o.events) != 6 || o.events[0] != "started" || o.events[1] != "before" || o.events[2] != "after:"+strings.Repeat("1", 64) {
				t.Fatal(o.events)
			}
		})
	}
}

func TestImageLedgerFailuresNeverReplaySideEffects(t *testing.T) {
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		t.Run(kind, func(t *testing.T) {
			db := testDB(t)
			runs := run.New(db)
			targets := &stubTarget{}
			srv := seedServer(t, targets, "web")
			runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "reg.invalid/app:stable")
			ledger := deployattempt.New(db)
			targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
				out := &target.ExecResult{}
				if len(cmd) > 1 && cmd[1] == "run" {
					out.Stdout = strings.Repeat("1", 64)
				}
				return out, nil
			}
			svc := New(targets, runs, WithAttemptLedger(failingAttemptWriter{Writer: ledger, fail: kind}))
			res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}})
			if err != nil {
				t.Fatal(err)
			}
			runCount := 0
			for _, cmd := range targets.calls {
				if len(cmd) > 1 && cmd[1] == "run" {
					runCount++
				}
			}
			if kind == deployattempt.ActivationStarted {
				if runCount != 0 || hasCmd(targets.calls, "docker", "rm") || res[0].Status != run.TargetFailed {
					t.Fatal(res, targets.calls)
				}
			} else if runCount != 1 || res[0].Status != run.TargetSuccess {
				t.Fatal("replayed or changed existing result", res, targets.calls)
			}
			a, err := ledger.ListByRun(context.Background(), runID)
			if err != nil || len(a) != 1 || a[0].Targets[0].Outcome != deployattempt.OutcomeUnknown {
				t.Fatal(a, err)
			}
		})
	}
}

func TestImageLedgerAdmissionFailureDoesNotReachSSH(t *testing.T) {
	for _, fail := range []string{"begin", "target"} {
		t.Run(fail, func(t *testing.T) {
			db := testDB(t)
			runs := run.New(db)
			targets := &stubTarget{}
			srv := seedServer(t, targets, "web")
			runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
			svc := New(targets, runs, WithAttemptLedger(failingAttemptWriter{Writer: deployattempt.New(db), fail: fail}))
			res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}})
			if len(targets.calls) != 0 {
				t.Fatal("remote command executed before durable identity", targets.calls)
			}
			if fail == "begin" && err == nil {
				t.Fatal("admission error lost")
			}
			if fail == "target" && (err != nil || res[0].Status != run.TargetFailed) {
				t.Fatal(res, err)
			}
		})
	}
}

func TestImageExecutionPanicLeavesUnknownReceipt(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	srv := seedServer(t, targets, "web")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
	ledger := deployattempt.New(db)
	targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		if len(cmd) > 1 && cmd[1] == "run" {
			panic("injected transport panic")
		}
		return &target.ExecResult{}, nil
	}
	svc := New(targets, runs, WithAttemptLedger(ledger))
	if res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}}); err != nil || res[0].Status != run.TargetFailed {
		t.Fatal(res, err)
	}
	a, err := ledger.ListByRun(context.Background(), runID)
	if err != nil || a[0].Targets[0].Outcome != deployattempt.OutcomeUnknown {
		t.Fatal(a, err)
	}
}

func TestObservationImageTargetUsesDeploymentResolutionWithoutIO(t *testing.T) {
	a := run.Artifact{Type: run.ArtifactImage, Name: "Original App", Reference: "reg.invalid/ns/app:stable", Metadata: map[string]any{"pushed": true, "digestRepository": "reg.invalid/ns/app", "digest": "sha256:" + strings.Repeat("a", 64)}}
	cfg := map[string]string{"containerName": "Selected App"}
	name, image, err := ObservationImageTarget(a, cfg)
	if err != nil || name != "Selected_App" || image.Reference != "reg.invalid/ns/app@sha256:"+strings.Repeat("a", 64) || image.DigestKind != deployattempt.DigestRepositoryUnknown {
		t.Fatal(name, image, err)
	}
	if a.Reference != "reg.invalid/ns/app:stable" || cfg["containerName"] != "Selected App" {
		t.Fatal("local resolver mutated source")
	}
	a.Metadata["digestRepository"] = "reg.invalid/other"
	if _, _, err = ObservationImageTarget(a, cfg); err == nil {
		t.Fatal("resolver accepted repository/digest mismatch")
	}
}

func TestImageMissingContainerIDReceiptIsUnknownWithoutExtraInspect(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	srv := seedServer(t, targets, "web")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
	ledger := deployattempt.New(db)
	o := &imageObserverFixture{}
	svc := New(targets, runs, WithAttemptLedger(ledger), WithImageObserver(o))
	res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}})
	if err != nil || res[0].Status != run.TargetSuccess {
		t.Fatal(res, err)
	}
	a, err := ledger.ListByRun(context.Background(), runID)
	if err != nil || a[0].Targets[0].Outcome != deployattempt.OutcomeUnknown || len(targets.calls) != 4 || len(o.events) != 2 {
		t.Fatal(a, err, targets.calls, o.events)
	}
}

func TestImageInteractiveContinueRecordsOnlyExecutedTargets(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	first := seedServer(t, targets, "first")
	second := seedServer(t, targets, "second")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
	ledger := deployattempt.New(db)
	targets.execFn = func(serverID string, cmd []string) (*target.ExecResult, error) {
		out := &target.ExecResult{}
		if len(cmd) > 1 && cmd[1] == "run" {
			out.Stdout = strings.Repeat("1", 64)
			if serverID == second.ID {
				out.Stdout = strings.Repeat("2", 64)
			}
		}
		return out, nil
	}
	svc := New(targets, runs, WithAttemptLedger(ledger))
	res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{first.ID, second.ID}, Strategy: StrategyInteractive})
	if err != nil || res[1].Status != run.TargetPending {
		t.Fatal(res, err)
	}
	a, err := ledger.ListByRun(context.Background(), runID)
	if err != nil || len(a) != 1 || len(a[0].Targets) != 1 || a[0].Targets[0].ServerID != first.ID {
		t.Fatal(a, err)
	}
	if _, err = svc.ContinueDeploy(context.Background(), ContinueInput{RunID: runID, ArtifactID: artifactID}); err != nil {
		t.Fatal(err)
	}
	a, err = ledger.ListByRun(context.Background(), runID)
	if err != nil || len(a) != 2 {
		t.Fatal(a, err)
	}
	for _, x := range a {
		if len(x.Targets) != 1 || x.Targets[0].Outcome != deployattempt.Succeeded {
			t.Fatal(x)
		}
		if x.Source.Initiator == "continue" && x.Targets[0].ServerID != second.ID {
			t.Fatal(x)
		}
	}
}

func TestImageHealthFailurePreservesActivationAndRollbackReceipts(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	srv := seedServer(t, targets, "web")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
	ledger := deployattempt.New(db)
	targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		out := &target.ExecResult{}
		switch {
		case cmd[0] == "false":
			out.ExitCode = 1
		case len(cmd) > 1 && cmd[1] == "inspect":
			out.Stdout = "app:previous"
		case len(cmd) > 1 && cmd[1] == "run":
			out.Stdout = strings.Repeat("1", 64)
			if cmd[len(cmd)-1] == "app:previous" {
				out.Stdout = strings.Repeat("2", 64)
			}
		}
		return out, nil
	}
	svc := New(targets, runs, WithAttemptLedger(ledger))
	res, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}, HealthCheck: &HealthCheck{Type: HealthCheckCommand, Command: []string{"false"}, Retries: 1}})
	if err != nil || res[0].Status != run.TargetRolledBack {
		t.Fatal(res, err)
	}
	a, err := ledger.ListByRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	tr := a[0].Targets[0]
	if tr.Outcome != deployattempt.RolledBack || len(tr.Receipts) != 3 {
		t.Fatal(tr)
	}
	last := tr.Receipts[2]
	if last.ActualImage.ContainerID != strings.Repeat("1", 64) || last.RollbackContainerID != strings.Repeat("2", 64) || last.PreviousImageReference != "app:previous" || last.HealthGate != "failed" {
		t.Fatal(last)
	}
	count := 0
	for _, cmd := range targets.calls {
		if cmd[0] == "false" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("ledger replayed existing health command", targets.calls)
	}
}

func TestImageStageAndRetryHaveSeparateAttempts(t *testing.T) {
	db := testDB(t)
	runs := run.New(db)
	targets := &stubTarget{}
	srv := seedServer(t, targets, "web")
	runID, artifactID := seedSuccessRunWithArtifact(t, db, runs, run.ArtifactImage, "app:stable")
	ledger := deployattempt.New(db)
	fail := true
	targets.execFn = func(_ string, cmd []string) (*target.ExecResult, error) {
		out := &target.ExecResult{}
		if len(cmd) > 1 && cmd[1] == "pull" && fail {
			out.ExitCode = 1
		}
		if len(cmd) > 1 && cmd[1] == "run" {
			out.Stdout = strings.Repeat("1", 64)
		}
		return out, nil
	}
	svc := New(targets, runs, WithAttemptLedger(ledger))
	if _, err := svc.Deploy(context.Background(), DeployInput{RunID: runID, ArtifactID: artifactID, ServerIDs: []string{srv.ID}}); err != nil {
		t.Fatal(err)
	}
	fail = false
	if _, err := svc.RetryFailed(context.Background(), RetryInput{RunID: runID, ArtifactID: artifactID}); err != nil {
		t.Fatal(err)
	}
	ctx := WithSourceStage(context.Background(), "deploy-node")
	if _, err := svc.DeployForStage(ctx, runID, []string{srv.ID}, map[string]string{"artifactType": "image"}, ""); err != nil {
		t.Fatal(err)
	}
	a, err := ledger.ListByRun(ctx, runID)
	if err != nil || len(a) != 3 {
		t.Fatal(a, err)
	}
	initiators := map[string]bool{}
	for _, x := range a {
		initiators[x.Source.Initiator] = true
		if x.Source.Initiator == "stage" && x.Source.StageID != "deploy-node" {
			t.Fatal(x)
		}
	}
	if !initiators["deploy"] || !initiators["retry"] || !initiators["stage"] {
		t.Fatal(initiators)
	}
}

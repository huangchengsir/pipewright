package deploy

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type retryTargets struct {
	target.Service
	mu                              sync.Mutex
	calls                           map[string]int
	failures                        int
	failure                         error
	link, container, image, runtime string
	commands                        [][]string
	activations                     int
}

func TestRecoveryImageProgressBindsRealCompletionReceipt(t *testing.T) {
	st := storetest.OpenForDialect(t, store.SQLite)
	rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
	targets.container = strings.Repeat("b", 64)
	targets.image = "sha256:" + strings.Repeat("c", 64)
	targets.runtime = "daemon-one"
	srv, _ := targets.Get(ctx, "a")
	runtime, err := d.targetRuntime(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	p := runretry.TargetProof{Kind: "local_image", ConfigHash: "private-config", ArtifactID: "artifact", ArtifactReference: "bytes", ConnectionFingerprint: connectionBinding(srv), RuntimeID: runtime}
	op, err := rr.BeginTarget(ctx, "root", n, "a", p)
	if err != nil {
		t.Fatal(err)
	}
	scope := &recoveryReceipts{retry: rr, run: "root", node: n.Key, operations: map[string]*runretry.TargetOperation{"a": op}, proofs: map[string]runretry.TargetProof{}, unknown: map[string]bool{}}
	ctx = context.WithValue(ctx, recoveryReceiptsKey{}, scope)
	ledger := deployattempt.New(st.DB)
	d.attempts = ledger
	a, err := ledger.Begin(ctx, deployattempt.BeginInput{RunID: "root", StageID: "deploy", Initiator: "stage"})
	if err != nil {
		t.Fatal(err)
	}
	targetRow, err := ledger.StartTarget(ctx, a.ID, deployattempt.TargetInput{ArtifactID: "artifact", ServerID: "a", ContainerName: "app", ExpectedImage: deployattempt.ExpectedImage{DigestKind: deployattempt.DigestUnavailable}})
	if err != nil {
		t.Fatal(err)
	}
	actual := deployattempt.ActualImage{ContainerID: targets.container, ConfigID: targets.image, ContainerCreatedAt: "created", ContainerStartedAt: "started"}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		receipt, err := ledger.AppendReceipt(ctx, targetRow.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual})
		if err != nil {
			t.Fatal(err)
		}
		recordRecoveryImage(ctx, targetRow, receipt, actual, kind)
	}
	saved, err := rr.TargetOperation(ctx, "root", n.Key, op.ID)
	if err != nil || saved.Proof.DeploymentTargetID != targetRow.ID || saved.Proof.DeploymentReceiptID == "" || !saved.Proof.ExitKnown {
		t.Fatal(saved, err)
	}
	state, proof, err := d.VerifyTargetOperation(ctx, *saved)
	if err != nil || state != "success" || proof.ContainerID != targets.container {
		t.Fatal(state, proof, err)
	}
	if _, err = st.DB.Exec("UPDATE deployment_attempt_targets SET outcome='outcome_unknown' WHERE id=?", targetRow.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.VerifyTargetOperation(ctx, *saved); err == nil {
		t.Fatal("opaque success label replaced real receipt check")
	}
}

func (t *retryTargets) Get(_ context.Context, id string) (*target.Server, error) {
	return &target.Server{ID: id, Name: id, Host: "127.0.0.1", Port: 22, User: "test", CredentialID: "test-credential"}, nil
}
func (t *retryTargets) Exec(_ context.Context, id string, cmd []string) (*target.ExecResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commands = append(t.commands, append([]string(nil), cmd...))
	if len(cmd) > 0 && cmd[0] == "readlink" {
		return &target.ExecResult{Stdout: t.link}, nil
	}
	if len(cmd) > 2 && cmd[0] == "ln" {
		t.link = cmd[2]
		t.activations++
	}
	if len(cmd) > 1 && cmd[0] == "docker" && cmd[1] == "info" {
		return &target.ExecResult{Stdout: t.runtime}, nil
	}
	if len(cmd) > 2 && cmd[0] == "docker" && cmd[1] == "container" {
		return &target.ExecResult{Stdout: `{"ID":"` + t.container + `","Image":"` + t.image + `","Created":"created","Started":"started","Running":true}`}, nil
	}
	t.calls[id]++
	if id == "b" && t.calls[id] <= t.failures {
		if t.failure != nil {
			return nil, t.failure
		}
		return &target.ExecResult{ExitCode: 9}, nil
	}
	return &target.ExecResult{}, nil
}

func retryDeployFixture(t *testing.T, st *store.Store, modify ...func(*runretry.Snapshot)) (*runretry.Service, run.Service, *service, *retryTargets, context.Context, runretry.NodeRef) {
	t.Helper()
	ctx := context.Background()
	db := st.DB
	for _, q := range []string{`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','c','git_token','',X'00','m','old','old')`, `INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','p','https://example.invalid/p','main','c','old','old')`, `INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('root','p','running','old')`} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	runs := run.New(db)
	child, dispatch := run.RetryAdapters(runs)
	_ = dispatch
	var key [32]byte
	key[0] = 92
	rr := runretry.New(db, vault.New(db, &key), runretry.WithChildCreator(child), runretry.WithDispatch(func(context.Context, string) error { return nil }))
	snap := runretry.Snapshot{SchemaVersion: 1, ProjectID: "p", CommitSHA: strings.Repeat("a", 40), Trigger: runretry.Trigger{Type: "manual", Branch: "main"}, Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "D", Kind: "deploy", Jobs: []pipeline.Job{{ID: "job", Name: "Deploy", Type: "deploy_ssh"}}}}}}
	for _, fn := range modify {
		fn(&snap)
	}
	if e := rr.SaveManifest(ctx, "root", snap); e != nil {
		t.Fatal(e)
	}
	prepared, e := rr.Prepare(ctx, "root")
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := runretry.ExecutionFrom(prepared)
	n, _ := ex.Node("deploy", "job")
	if len(modify) == 0 {
		if e = ex.Begin(prepared, n); e != nil {
			t.Fatal(e)
		}
	}
	prepared, e = runretry.WithNode(prepared, n)
	if e != nil {
		t.Fatal(e)
	}
	targets := &retryTargets{calls: map[string]int{}, failures: 2}
	d := New(targets, runs, WithRetryLedger(rr), WithBatchDB(db)).(*service)
	return rr, runs, d, targets, prepared, n
}
func (t *retryTargets) Upload(_ context.Context, _ string, r io.Reader, _ string) error {
	_, e := io.Copy(io.Discard, r)
	return e
}
func finishDeployFixture(t *testing.T, st *store.Store, rr *runretry.Service, ctx context.Context, id string, n runretry.NodeRef, results []TargetResult) {
	t.Helper()
	var receipts []runretry.TargetReceipt
	status := "success"
	external := "verified"
	for _, r := range results {
		receipts = append(receipts, runretry.TargetReceipt{ServerID: r.ServerID, Status: r.Status, OperationID: r.OperationID, SourceAttemptID: r.SourceAttemptID})
		if r.Status != "success" {
			status = "failed"
		}
		if r.OutcomeUnknown {
			external = "unknown"
		}
	}
	ex, _ := runretry.ExecutionFrom(ctx)
	if e := ex.Finish(ctx, n, runretry.Outcome{Status: status, ExecutionMode: "real", ExternalOutcome: external, Targets: receipts, Complete: status == "success", Reusable: status == "success"}); e != nil {
		t.Fatal(e)
	}
	if _, e := st.DB.Exec("UPDATE pipeline_runs SET status=?,finished_at=? WHERE id=?", status, time.Now().UTC().Format(time.RFC3339), id); e != nil {
		t.Fatal(e)
	}
	if e := rr.Complete(ctx, id, status); e != nil {
		t.Fatal(e)
	}
	// The fixture does not run WorkerPool. Its deliberately simulated worker hold
	// is retired after the synchronous adapter call; real reader proof is separate.
	if _, e := st.DB.Exec("UPDATE project_activity SET released_at=1 WHERE run_id=?", id); e != nil {
		t.Fatal(e)
	}
}
func TestRecoveryTargetsPreserveProducerAcrossTwoAttempts(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
		cfg := map[string]string{"artifactType": "command", "restartCommand": "safe-test-command"}
		results, e := d.DeployForStage(ctx, "root", []string{"a", "b"}, cfg, "")
		if e != nil || len(results) != 2 || results[0].Status != "success" || results[1].Status != "failed" {
			t.Fatal(results, e)
		}
		finishDeployFixture(t, st, rr, ctx, "root", n, results)
		current := "root"
		for i := 0; i < 2; i++ {
			p, e := rr.Plan(context.Background(), current, []string{n.Key})
			if e != nil || !p.CanSubmit {
				t.Fatal(p, e)
			}
			r, e := rr.Submit(context.Background(), current, runretry.SubmitInput{Scope: "tester", RequestID: []string{"retry-1", "retry-2"}[i], BaseAttemptID: p.BaseAttemptID, BaseRevision: p.BaseRevision, PlanFingerprint: p.PlanFingerprint, TargetNodeIDs: p.TargetNodeIDs})
			if e != nil {
				t.Fatal(e)
			}
			current = r.AttemptRunID
			ctx, e = rr.Prepare(context.Background(), current)
			if e != nil {
				t.Fatal(e)
			}
			ex, _ := runretry.ExecutionFrom(ctx)
			if e = ex.Begin(ctx, n); e != nil {
				t.Fatal(e)
			}
			ctx, e = runretry.WithNode(ctx, n)
			if e != nil {
				t.Fatal(e)
			}
			results, e = d.DeployForStage(ctx, current, []string{"a", "b"}, cfg, "")
			if e != nil || len(results) != 2 || results[0].SourceAttemptID != "root" || results[0].OperationID == "" {
				t.Fatal(results, e)
			}
			finishDeployFixture(t, st, rr, ctx, current, n, results)
		}
		if targets.calls["a"] != 1 || targets.calls["b"] != 3 || results[1].Status != "success" {
			t.Fatal(targets.calls, results)
		}
		var a, b string
		if e = st.DB.QueryRow("SELECT status FROM deploy_targets WHERE run_id='root' AND server_id='a'").Scan(&a); e != nil {
			t.Fatal(e)
		}
		if e = st.DB.QueryRow("SELECT status FROM deploy_targets WHERE run_id='root' AND server_id='b'").Scan(&b); e != nil || a != "success" || b != "failed" {
			t.Fatal("parent mutated", a, b, e)
		}
	})
}

func TestRecoveryTargetUnknownAndKnownDialFailure(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "not-started"
		failure := target.NotStarted(target.ErrUnreachable)
		if unknown {
			name = "receipt-lost"
			failure = target.OutcomeUnknown(target.ErrUnreachable)
		}
		t.Run(name, func(t *testing.T) {
			st := storetest.OpenForDialect(t, store.SQLite)
			rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
			targets.failure = failure
			r, e := d.DeployForStage(ctx, "root", []string{"a", "b"}, map[string]string{"artifactType": "command", "restartCommand": "test"}, "")
			if unknown && !errors.Is(e, runretry.ErrUnknown) {
				t.Fatal(e)
			}
			if !unknown && e != nil {
				t.Fatal(e)
			}
			finishDeployFixture(t, st, rr, ctx, "root", n, r)
			view, e := rr.Context(context.Background(), "root")
			if e != nil || view.OutcomeUnknown != unknown {
				t.Fatal(view, e)
			}
			p, e := rr.Plan(context.Background(), "root", []string{n.Key})
			if e != nil || p.CanSubmit == unknown {
				t.Fatal(p, e)
			}
			if targets.calls["a"] != 1 || targets.calls["b"] != 1 {
				t.Fatal(targets.calls)
			}
		})
	}
}

func TestRecoveryTargetIdentityDriftIsReadOnly(t *testing.T) {
	targets := &retryTargets{calls: map[string]int{}, link: "/srv/app/releases/original", container: strings.Repeat("b", 64), image: "sha256:" + strings.Repeat("c", 64), runtime: "runtime-one"}
	d := &service{targets: targets}
	ctx := context.Background()
	file := runretry.TargetProof{Kind: "dist", ExitKnown: true, CurrentPath: "/srv/app/current", ReleasePath: targets.link}
	if e := d.verifySuccessfulTarget(ctx, "a", file); e != nil {
		t.Fatal(e)
	}
	targets.link = "/srv/app/releases/foreign"
	if e := d.verifySuccessfulTarget(ctx, "a", file); !errors.Is(e, runretry.ErrUnavailable) {
		t.Fatal(e)
	}
	runtime, e := d.targetRuntime(ctx, "a")
	if e != nil {
		t.Fatal(e)
	}
	image := runretry.TargetProof{Kind: "local_image", ExitKnown: true, RuntimeID: runtime, ContainerName: "app", ContainerID: targets.container, ImageID: targets.image, ContainerCreatedAt: "created", ContainerStartedAt: "started"}
	if e = d.verifySuccessfulTarget(ctx, "a", image); e != nil {
		t.Fatal(e)
	}
	targets.container = strings.Repeat("d", 64)
	if e = d.verifySuccessfulTarget(ctx, "a", image); !errors.Is(e, runretry.ErrUnavailable) {
		t.Fatal(e)
	}
	for _, cmd := range targets.commands {
		if cmd[0] != "readlink" && !(len(cmd) > 1 && cmd[0] == "docker" && (cmd[1] == "info" || cmd[1] == "container")) {
			t.Fatal("verification mutated target", cmd)
		}
	}
}

func TestRecoveryTargetReceiptWriteFailureStaysUnknown(t *testing.T) {
	st := storetest.OpenForDialect(t, store.SQLite)
	rr, _, d, targets, ctx, n := retryDeployFixture(t, st)
	targets.failures = 0
	if _, e := st.DB.Exec(`CREATE TRIGGER fail_target_finish BEFORE UPDATE ON run_retry_external_targets BEGIN SELECT RAISE(FAIL,'injected receipt persistence failure'); END`); e != nil {
		t.Fatal(e)
	}
	results, e := d.DeployForStage(ctx, "root", []string{"a"}, map[string]string{"artifactType": "command", "restartCommand": "test"}, "")
	if !errors.Is(e, runretry.ErrUnknown) || len(results) != 1 || !results[0].OutcomeUnknown {
		t.Fatal(results, e)
	}
	view, e := rr.Context(context.Background(), "root")
	if e != nil || !view.OutcomeUnknown {
		t.Fatal(view, e)
	}
	drained, e := rr.ResourcesDrained(context.Background(), "root")
	if e != nil || drained {
		t.Fatal(drained, e)
	}
	op, e := rr.TargetOperation(context.Background(), "root", n.Key, results[0].OperationID)
	if e != nil || op.State != "running" {
		t.Fatal(op, e)
	}
}

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/productrelease"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
	"github.com/huangchengsir/pipewright/internal/workspace"
)

func productReleaseSpec() pipeline.Spec {
	return pipeline.Spec{Stages: []pipeline.Stage{
		{ID: "source", Name: "Source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "Load source", Type: "git_source"}}},
		{ID: "build", Name: "Build", Kind: pipeline.KindBuild, Jobs: []pipeline.Job{
			{ID: "image", Name: "Build image", Type: "build_image"},
			{ID: "push", Name: "Push image", Type: "push_image", Needs: []string{"image"}},
		}},
		{ID: "deploy", Name: "Deployment", Gate: true, Kind: pipeline.KindDeploy, Jobs: []pipeline.Job{
			{ID: "execute", Name: "Deploy image", Type: "deploy_ssh", Config: map[string]any{"artifactType": "image"}},
		}},
	}}
}

type productReleaseFixtureRunner struct{ snapshots chan runretry.Snapshot }

func (r productReleaseFixtureRunner) Run(ctx context.Context, _ *run.Run, _ run.StepSink) error {
	execution, ok := runretry.ExecutionFrom(ctx)
	if !ok || !run.IsProductReleaseExecution(ctx) {
		return errors.New("frozen runtime unavailable")
	}
	r.snapshots <- execution.Snapshot()
	return nil
}
func TestProductReleaseUsesEncryptedPreparedRunAndOriginalQueueIdentity(t *testing.T) {
	ctx := productrelease.WithLegacySession(t.Context())
	db := storetest.OpenDB(t)
	key := [32]byte{1, 3, 5}
	v := vault.New(db, &key)
	source, err := v.Create(vault.CreateInput{Name: "Repository fixture", Type: vault.TypeGitToken, Secret: "test-fixture-token"})
	if err != nil {
		t.Fatal(err)
	}
	ssh, err := v.Create(vault.CreateInput{Name: "Target fixture", Type: vault.TypeSSHPassword, Secret: "test-only", Username: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-11-20T00:00:00Z"
	id := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, "App", "https://example.invalid/project", "main", source.ID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	user := store.Identifier(store.DialectOf(db), "user")
	if _, err = db.Exec(`INSERT INTO servers(id,name,host,port,`+user+`,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, "server", "Target", "192.0.2.11", 22, "fixture", ssh.ID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	projects := project.New(db, v, nil)
	ws := workspace.New(db, projects)
	org, err := ws.CreateBusinessProject(ctx, "Workspace test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ws.CreateProduct(ctx, org.ID, "Product test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.AssignApplication(ctx, id, p.ID, 0); err != nil {
		t.Fatal(err)
	}
	pipelines := pipeline.New(db)
	if _, err = pipelines.Save(ctx, id, productReleaseSpec()); err != nil {
		t.Fatal(err)
	}
	ps := pipeline.NewSettingsService(db, v)
	defaults, err := ps.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ps.Save(ctx, id, pipeline.SettingsInput{Build: defaults.Build, Environments: []pipeline.Environment{{Name: "staging", TargetServerIDs: []string{"server"}}}}); err != nil {
		t.Fatal(err)
	}
	recovery := runretry.New(db, v, runretry.WithSnapshotProvider(func(context.Context, string) (runretry.Snapshot, error) {
		t.Error("runtime read the live source")
		return runretry.Snapshot{}, errors.New("live capture rejected")
	}))
	reader := &accessSourceReader{}
	servers := target.New(db, v, nil)
	compiler := &runAccessCompiler{db: db, projects: projects, pipelines: pipelines, settings: ps, source: reader, vault: v, targets: servers, recovery: recovery}
	runs := run.New(db, run.WithExecutionAuthorizer(compiler))
	// Only the fake runner observes the frozen manifest. No Docker or SSH runs.
	fixtureRunner := productReleaseFixtureRunner{snapshots: make(chan runretry.Snapshot, 1)}
	pool := run.NewWorkerPool(runs, run.WithRetryRuntime(recovery), run.WithRunner(fixtureRunner))
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pool.Stop(stop)
	})
	executor := &productReleaseController{db: db, workspace: ws, compiler: compiler, runs: runs, targets: servers.(target.LimitedExecutor), attempts: deployattempt.New(db), ready: true}
	svc := productrelease.New(db, executor, executor)
	opts, err := svc.Options(ctx, p.ID, 1)
	if err != nil || !opts.CanRelease || len(opts.Applications) != 1 || len(opts.Applications[0].Environments) != 1 {
		t.Fatalf("options %#v: %v", opts, err)
	}
	plan, err := svc.Preview(ctx, p.ID, []productrelease.PreviewItem{{ApplicationID: id, Environment: "staging"}})
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || plan.Items[0].CommitSHA != strings.Repeat("a", 40) {
		t.Fatal(plan, reader.calls)
	}
	if _, err = svc.Confirm(ctx, uuid.NewString(), plan); !errors.Is(err, productrelease.ErrConflict) {
		t.Fatal("reuse with a new request identity accepted", err)
	}
	durable, err := svc.Confirm(ctx, plan.Items[0].SnapshotRef, plan)
	if err != nil || durable.State != "pending" {
		t.Fatal(durable, err)
	}
	// The same sweep removes old unused ciphertext and preserves every member
	// of a confirmed plan, even if confirmation precedes a long approval.
	if _, err = db.Exec(`UPDATE product_release_previews SET created_at=1 WHERE id=?`, plan.Items[0].SnapshotRef); err != nil {
		t.Fatal(err)
	}
	orphan := uuid.NewString()
	principal, err := executor.Principal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO product_release_previews(id,tenant_id,actor_id,product_id,plan_id,created_at,ciphertext) VALUES(?,?,?,?,?,1,`+store.BinaryPlaceholder(store.DialectOf(db))+`)`,
		orphan, principal.TenantID, principal.ActorID, p.ID, orphan, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err = configstate.WithWriteTx(ctx, db, func(q configstate.DBTX) error { return executor.cleanupPreviews(ctx, q, principal, 2) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM product_release_previews WHERE id IN (?,?)`, orphan, plan.Items[0].SnapshotRef).Scan(&n); err != nil || n != 1 {
		t.Fatal("preview cleanup did not preserve the confirmed plan", n, err)
	}

	// The compiler source can be unavailable after confirmation. Dispatch
	// still restores the original sealed inputs and never re-reads branch HEAD.
	reader.err = errors.New("branch cannot be read")
	secondService := productrelease.New(db, executor, executor)
	b, err := secondService.Advance(ctx, durable.ID, durable.Revision)
	if err != nil || b.State != "running" || b.Items[0].RunID == "" {
		t.Fatal(b, err)
	}
	rn, err := runs.Get(ctx, b.Items[0].RunID)
	if err != nil || rn.Trigger.Commit != plan.Items[0].CommitSHA || rn.DispatchState != "accepted" {
		t.Fatal(rn, err)
	}
	snapshot, err := recovery.FrozenSnapshotForRelease(ctx, rn.ID)
	if err != nil || !snapshot.Spec.Stages[2].Gate || reader.calls != 1 {
		t.Fatal(snapshot, err, reader.calls)
	}
	const pushSource = "{\"declarationIndex\":0,\"jobId\":\"push\",\"stageId\":\"build\"}"
	if source := snapshot.Spec.Stages[2].Jobs[0].Config["artifactSource"]; source != pushSource {
		t.Fatalf("release snapshot selected the wrong image artifact: %v", source)
	}
	digest, err := compiler.Digest(ctx, rn.ID)
	if err != nil || digest != plan.Items[0].ConfigDigest {
		t.Fatal(digest, err)
	}
	replay, err := svc.Confirm(ctx, plan.Items[0].SnapshotRef, plan)
	if err != nil || replay.ID != b.ID || replay.Items[0].RunID != rn.ID {
		t.Fatal(replay, err)
	}
	if _, err = svc.Get(t.Context(), b.ID); err == nil {
		t.Fatal("read without authenticated owner")
	}
	pool.Start()
	select {
	case snapshot := <-fixtureRunner.snapshots:
		if !snapshot.Spec.Stages[2].Gate || snapshot.CommitSHA != plan.Items[0].CommitSHA {
			t.Fatal("worker did not receive exact plan")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("frozen run did not reach fixture runner")
	}
	deadline := time.Now().Add(5 * time.Second)
	for rn.Status != run.StatusSuccess && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		rn, err = runs.Get(ctx, rn.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if rn.Status != run.StatusSuccess {
		t.Fatal(rn.Status)
	}
	// Production checkout shortens the display SHA before the batch reconciles.
	if err = runs.SetCommit(ctx, rn.ID, plan.Items[0].CommitSHA); err != nil {
		t.Fatal(err)
	}
	rn, err = runs.Get(ctx, rn.ID)
	if err != nil || rn.Trigger.Commit != plan.Items[0].CommitSHA[:7] {
		t.Fatal("expected checkout to store the abbreviated display SHA", rn, err)
	}
	b, err = secondService.Reconcile(ctx, b.ID, b.Revision)
	if err != nil || b.State != "needs_reconciliation" || b.Items[0].RunID != rn.ID || b.Items[0].State != "unknown" {
		t.Fatal("checkout should reconcile but cannot replace deployment proof", b, err)
	}
	if err = runs.SetCommit(ctx, rn.ID, plan.Items[0].CommitSHA[:7]+strings.Repeat("b", 33)); err != nil {
		t.Fatal(err)
	}
	if _, err = executor.Observe(ctx, plan.Items[0], rn.ID); !errors.Is(err, productrelease.ErrConflict) {
		t.Fatal("a conflicting checkout with the same display prefix was accepted", err)
	}
}

func TestProductReleaseProofRequiresExactActivationAndTargets(t *testing.T) {
	sha := strings.Repeat("a", 40)
	digest := "sha256:" + strings.Repeat("b", 64)
	want := productReleaseSnapshot{Plan: productrelease.ItemPlan{ApplicationID: "app", Environment: "stage", CommitSHA: sha, TargetIDs: []string{"server"}}, StageID: "deploy", Revision: 4, Targets: map[string]string{"server": "expected-server-version"}}
	source := deployattempt.Source{RunID: "run", ProjectID: "app", Environment: "stage", StageID: "deploy", Initiator: "stage", ConfigRevision: 4, ResolvedFullSHA: sha}
	repository := "[2001:db8::1]:5000/org/app"
	identity := deployattempt.ExpectedImage{Repository: repository, Reference: repository + "@" + digest, Digest: digest, DigestKind: deployattempt.DigestRepositoryUnknown}
	r := deployattempt.Receipt{TargetID: "target", Kind: deployattempt.Succeeded, HealthGate: "passed", ActualImage: deployattempt.ActualImage{
		ContainerID: strings.Repeat("c", 64), ConfigID: "sha256:" + strings.Repeat("d", 64), RepoDigests: []string{repository + "@" + digest},
		Platform: deployattempt.Platform{OS: "linux", Architecture: "amd64"},
	}}
	a := deployattempt.Attempt{ID: "attempt", Source: source, Targets: []deployattempt.Target{{
		ID: "target", AttemptID: "attempt", Source: source, Outcome: deployattempt.Succeeded,
		TargetInput: deployattempt.TargetInput{ServerID: "server", ExpectedImage: identity, ConnectionFingerprint: "expected-server-version"},
		Receipts:    []deployattempt.Receipt{r},
	}}}
	manual := a
	manual.Source.Initiator = "deploy"
	if !verifiedProductDeployment(want, "run", []deployattempt.Attempt{manual, a}) {
		t.Fatal("verified receipt rejected by unrelated deployment")
	}
	for _, mutate := range []func(*deployattempt.Attempt){
		func(a *deployattempt.Attempt) { a.Source.ResolvedFullSHA = "different" },
		func(a *deployattempt.Attempt) { a.Targets[0].ConnectionFingerprint = "different" },
		func(a *deployattempt.Attempt) { a.Targets[0].Receipts[0].ActualImage.RepoDigests = nil },
		func(a *deployattempt.Attempt) { a.Targets = append(a.Targets, a.Targets[0]) },
	} {
		candidate := a
		candidate.Targets = append([]deployattempt.Target(nil), a.Targets...)
		candidate.Targets[0].Receipts = append([]deployattempt.Receipt(nil), a.Targets[0].Receipts...)
		mutate(&candidate)
		if verifiedProductDeployment(want, "run", []deployattempt.Attempt{candidate}) {
			t.Fatal("mismatching receipt counted as success")
		}
	}
}

func TestProductReleaseOptionsRecognizeBranchAndRejectMissingImageDependency(t *testing.T) {
	spec := productReleaseSpec()
	spec.Stages[2].When = pipeline.When{Branches: []string{"release/*"}}
	if !canPreviewBranch(spec, "main", []string{"server"}) {
		t.Fatal("release branch was not offered")
	}
	if _, err := releaseStage(spec, "main", []string{"server"}); !errors.Is(err, productrelease.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := releaseStage(spec, "release/1", []string{"server"}); err != nil {
		t.Fatal(err)
	}
	spec.Stages[1].Jobs[1].Needs = nil
	if _, err := releaseStage(spec, "release/1", []string{"server"}); !errors.Is(err, productrelease.ErrUnsupported) {
		t.Fatal(err)
	}
}

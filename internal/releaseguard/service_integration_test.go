package releaseguard

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deploy"
	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

type integrationClock struct{ n atomic.Int64 }

func (c *integrationClock) now() time.Time      { return time.Unix(0, c.n.Load()).UTC() }
func (c *integrationClock) add(d time.Duration) { c.n.Add(int64(d)) }

type integrationObserver struct {
	clock     *integrationClock
	executor  target.LimitedExecutor
	reads     atomic.Int64
	exited    atomic.Bool
	createdAt string
	startedAt string
	entered   chan struct{}
	release   chan struct{}
}

func (o *integrationObserver) Capture(c context.Context, id string) (target.ConnectionSnapshot, error) {
	return o.executor.Capture(c, id)
}
func (o *integrationObserver) Inspect(_ context.Context, _ target.ConnectionSnapshot, id string) RuntimeSnapshot {
	o.reads.Add(1)
	if o.entered != nil {
		select {
		case o.entered <- struct{}{}:
		default:
		}
		<-o.release
	}
	return RuntimeSnapshot{Outcome: "ok", ContainerID: id, CreatedAt: o.createdAt, StartedAt: o.startedAt, ConfigID: "sha256:" + strings.Repeat("b", 64), DeclaredImage: "reg.invalid/app@sha256:" + strings.Repeat("a", 64), RepoDigests: []string{"reg.invalid/app@sha256:" + strings.Repeat("a", 64)}, Platform: deployattempt.Platform{OS: "linux", Architecture: "amd64"}, Running: !o.exited.Load(), Health: "", ImageAvailable: true, CollectedAt: o.clock.now(), Reason: "fixture_runtime"}
}
func (o *integrationObserver) Baseline(c context.Context, s target.ConnectionSnapshot, id string) RuntimeSnapshot {
	return o.Inspect(c, s, id)
}
func (o *integrationObserver) Logs(context.Context, target.ConnectionSnapshot, string, time.Time, time.Time) (string, bool, string) {
	o.reads.Add(1)
	return "", false, "fixture_logs_unavailable"
}

type serviceIntegrationFixture struct {
	store                           *store.Store
	service                         *Service
	ledger                          *deployattempt.Store
	observer                        *integrationObserver
	clock                           *integrationClock
	runs                            run.Service
	targets                         target.Service
	attemptID, targetID, artifactID string
}

func newServiceIntegrationFixture(t *testing.T) serviceIntegrationFixture {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('c','credential','ssh_password','',X'00','masked','` + stamp + `','` + stamp + `')`,
		`INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('p','project','https://example.invalid/app','main','c','` + stamp + `','` + stamp + `')`,
		`INSERT INTO project_config_versions(project_id,revision) VALUES('p',1)`,
		`INSERT INTO pipeline_runs(id,project_id,status,resolved_environment,created_at) VALUES('r','p','success','prod','` + stamp + `')`,
		`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES('s','server','127.0.0.1',22,'fixture','c','` + stamp + `','` + stamp + `')`,
	} {
		if _, e := st.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	runs := run.New(st.DB)
	a, err := runs.AddArtifact(ctx, run.Artifact{RunID: "r", Type: run.ArtifactImage, Name: "app", Reference: "reg.invalid/app:stable", Metadata: map[string]any{"pushed": true, "digestRepository": "reg.invalid/app", "digest": "sha256:" + strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	targets := target.New(st.DB, nil, nil)
	cap, err := targets.(target.LimitedExecutor).Capture(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	ledger := deployattempt.New(st.DB)
	attempt, err := ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy", ResolvedFullSHA: strings.Repeat("d", 40)})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := ledger.StartTarget(ctx, attempt.ID, deployattempt.TargetInput{ArtifactID: a.ID, ArtifactName: "app", ServerID: "s", ServerName: "server", ContainerName: "app", ConnectionFingerprint: cap.Fingerprint(), ExpectedImage: deployattempt.ExpectedImage{Reference: "reg.invalid/app@sha256:" + strings.Repeat("a", 64), Repository: "reg.invalid/app", Digest: "sha256:" + strings.Repeat("a", 64), DigestKind: deployattempt.DigestRepositoryUnknown}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		if _, err = ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: strings.Repeat("1", 64)}}); err != nil {
			t.Fatal(err)
		}
	}
	clock := &integrationClock{}
	clock.n.Store(time.Now().UTC().UnixNano())
	observer := &integrationObserver{clock: clock, executor: targets.(target.LimitedExecutor), createdAt: clock.now().Add(-time.Hour).Format(time.RFC3339Nano), startedAt: clock.now().Add(-time.Minute).Format(time.RFC3339Nano)}
	s, err := New(Options{DB: st.DB, Ledger: ledger, Runs: runs, Targets: targets, Observer: observer, Now: clock.now, ResolveImageTarget: deploy.ObservationImageTarget})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if observer.release != nil {
			select {
			case <-observer.release:
			default:
				close(observer.release)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := s.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return serviceIntegrationFixture{st, s, ledger, observer, clock, runs, targets, attempt.ID, tr.ID, a.ID}
}

func (f serviceIntegrationFixture) prepare(t *testing.T) *Plan {
	t.Helper()
	p, e := f.service.Prepare(context.Background(), "p", PrepareInput{Mode: "from_now", AttemptID: f.attemptID, DurationSeconds: 300})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func (f serviceIntegrationFixture) start(t *testing.T, key string) *Report {
	t.Helper()
	p := f.prepare(t)
	r, e := f.service.Start(context.Background(), "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: key})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f serviceIntegrationFixture) wait(t *testing.T, id string, condition func(*Report) bool) *Report {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, e := f.service.Snapshot(context.Background(), "p", id)
		if e != nil {
			t.Fatal(e)
		}
		if condition(r) {
			return r
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(r)
			t.Fatalf("report did not converge: %s", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServiceIntegrationPlansIdempotencyScopeAndCapacity(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	ctx := context.Background()
	p := f.prepare(t)
	if f.observer.reads.Load() != 0 {
		t.Fatal("Prepare reached remote observer")
	}
	input := StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "once"}
	bad := input
	bad.PlanHash = "changed"
	if _, e := f.service.Start(ctx, "p", bad); e != ErrConflict {
		t.Fatal(e)
	}
	if _, e := f.service.Start(ctx, "other", input); e != ErrNotFound {
		t.Fatal(e)
	}
	r, e := f.service.Start(ctx, "p", input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.service.Start(ctx, "p", input)
	if e != nil || again.ID != r.ID {
		t.Fatal(again, e)
	}
	f.start(t, "two")
	p3 := f.prepare(t)
	if _, e = f.service.Start(ctx, "p", StartInput{PlanID: p3.ID, PlanHash: p3.Hash, IdempotencyKey: "three"}); e != ErrCapacity {
		t.Fatal(e)
	}
	f.clock.add(16 * time.Minute)
	expired := f.prepare(t)
	f.clock.add(16 * time.Minute)
	if _, e = f.service.Start(ctx, "p", StartInput{PlanID: expired.ID, PlanHash: expired.Hash, IdempotencyKey: "expired"}); e != ErrConflict {
		t.Fatal(e)
	}
}

func TestServiceIntegrationStartupFailureEvidenceAndPrivateSnapshot(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	f.observer.exited.Store(true)
	r := f.start(t, "startup-exit")
	r = f.wait(t, r.ID, func(r *Report) bool { return r.VerificationResult != nil && *r.VerificationResult == "failed" })
	page, e := f.service.Evidence(context.Background(), "p", r.ID, 0, 100)
	if e != nil || len(page.Items) == 0 {
		t.Fatal(page, e)
	}
	ids := map[string]bool{}
	for _, item := range page.Items {
		ids[item.ID] = true
	}
	for _, c := range r.Targets[0].Conditions {
		for _, id := range c.EvidenceIDs {
			if !ids[id] {
				t.Fatal("condition cites unavailable evidence", id)
			}
		}
	}
	b, _ := json.Marshal(r)
	for _, private := range []string{"credentialVersion", "bindings", "CredentialID", "Host"} {
		if strings.Contains(string(b), private) {
			t.Fatal("private capture exposed", private, string(b))
		}
	}
}

func TestServiceIntegrationStopDiscardsLateSample(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	f.observer.entered = make(chan struct{}, 1)
	f.observer.release = make(chan struct{})
	f.observer.exited.Store(true)
	r := f.start(t, "late")
	select {
	case <-f.observer.entered:
	case <-time.After(time.Second):
		t.Fatal("observer did not start")
	}
	if _, e := f.service.Stop(context.Background(), "p", r.ID); e != nil {
		t.Fatal(e)
	}
	close(f.observer.release)
	r = f.wait(t, r.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
	page, e := f.service.Evidence(context.Background(), "p", r.ID, 0, 100)
	if e != nil || len(page.Items) != 0 || r.VerificationResult == nil || *r.VerificationResult != "insufficient" {
		t.Fatal(page, r, e)
	}
}

func TestServiceIntegrationRecoveryRetentionAndExpiry(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	ctx := context.Background()
	p, e := f.service.Prepare(ctx, "p", PrepareInput{Mode: "attached", RunID: "r", ArtifactID: f.artifactID, ServerIDs: []string{"s"}, DeployConfig: map[string]string{"containerName": "app"}, DurationSeconds: 300})
	if e != nil {
		t.Fatal(e)
	}
	_, r, e := f.service.Attached(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "restart-binding"}, "r", f.artifactID, []string{"s"}, map[string]string{"containerName": "app"})
	if e != nil {
		t.Fatal(e)
	}
	recovered, e := New(Options{DB: f.store.DB, Ledger: f.ledger, Runs: f.runs, Targets: f.targets, Observer: f.observer, Now: f.clock.now, ResolveImageTarget: deploy.ObservationImageTarget})
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.Close(ctx)
	r, e = recovered.Snapshot(ctx, "p", r.ID)
	if e != nil || r.ExecutionStatus != "interrupted" || f.observer.reads.Load() != 0 {
		t.Fatal(r, e)
	}
	active := f.start(t, "retained")
	f.wait(t, active.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples > 0 })
	if _, e = f.store.DB.Exec(`DELETE FROM run_artifacts WHERE run_id='r'`); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.DB.Exec(`DELETE FROM pipeline_runs WHERE id='r'`); e != nil {
		t.Fatal(e)
	}
	if snap, e := f.service.Snapshot(ctx, "p", active.ID); e != nil || snap.Targets[0].ContainerID == "" {
		t.Fatal(snap, e)
	}
	if _, e = f.service.Stop(ctx, "p", active.ID); e != nil {
		t.Fatal(e)
	}
	f.wait(t, active.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
	f.clock.add(8 * 24 * time.Hour)
	if e = f.service.Purge(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = f.store.DB.QueryRow(`SELECT COUNT(*) FROM release_guard_evidence WHERE guard_id=?`, active.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("raw facts retained after expiry", n, e)
	}
	var body string
	var persisted document
	if e = f.store.DB.QueryRow(`SELECT record_json FROM release_guards WHERE id=?`, active.ID).Scan(&body); e != nil || json.Unmarshal([]byte(body), &persisted) != nil {
		t.Fatal(e)
	}
	if persisted.Report.Targets[0].Actual != nil || len(persisted.Private.Bindings) > 0 {
		t.Fatal("expired raw capture/runtime facts remain in persisted snapshot")
	}
	history, e := f.service.Snapshot(ctx, "p", active.ID)
	if e != nil || !history.EvidenceExpired || history.VerificationResult == nil || history.Targets[0].Actual != nil {
		t.Fatal(history, e)
	}
}

func TestServiceIntegrationProjectDeletionBarrier(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	ctx := context.Background()
	r := f.start(t, "deleting")
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples > 0 })
	p := f.prepare(t)
	if e := f.service.BeginProjectDeletion(ctx, "p"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.Start(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "blocked"}); e == nil {
		t.Fatal("deleting project accepted worker")
	}
	f.service.CancelProjectDeletion("p")
	if _, e := f.service.Start(ctx, "p", StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "unblocked"}); e != nil {
		t.Fatal(e)
	}
}

func TestServiceIntegrationUnknownAndRollbackNeverPass(t *testing.T) {
	for _, outcome := range []string{deployattempt.OutcomeUnknown, deployattempt.RolledBack} {
		t.Run(outcome, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			if _, e := f.ledger.AppendReceipt(context.Background(), f.targetID, deployattempt.Receipt{Kind: outcome}); e != nil {
				t.Fatal(e)
			}
			r := f.start(t, "unverified")
			r = f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
			if r.VerificationResult == nil || *r.VerificationResult == "passed" {
				t.Fatal(r)
			}
		})
	}
}

func TestServiceIntegrationReplacementRetainsFailures(t *testing.T) {
	for _, replacement := range []string{"rollback", "new_attempt"} {
		for _, failed := range []bool{false, true} {
			t.Run(replacement+"_"+map[bool]string{false: "normal_replacement", true: "failed_then_replacement"}[failed], func(t *testing.T) {
				f := newServiceIntegrationFixture(t)
				f.observer.exited.Store(failed)
				r := f.start(t, "replacement")
				f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Actual != nil })
				if replacement == "rollback" {
					if _, e := f.ledger.AppendReceipt(context.Background(), f.targetID, deployattempt.Receipt{Kind: deployattempt.RolledBack}); e != nil {
						t.Fatal(e)
					}
				} else {
					ctx := context.Background()
					a, e := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy"})
					if e != nil {
						t.Fatal(e)
					}
					old, e := f.ledger.GetTarget(ctx, f.targetID)
					if e != nil {
						t.Fatal(e)
					}
					tr, e := f.ledger.StartTarget(ctx, a.ID, old.TargetInput)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = f.ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: deployattempt.ActivationStarted}); e != nil {
						t.Fatal(e)
					}
				}
				f.clock.add(30 * time.Second)
				r = f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
				want := "insufficient"
				if failed {
					want = "failed"
				}
				if r.VerificationResult == nil || *r.VerificationResult != want {
					t.Fatal(r)
				}
			})
		}
	}
}

func TestServiceIntegrationVirtualCompleteWindowPasses(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	r := f.start(t, "virtual-window")
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples == 1 })
	for i := 2; i <= 11; i++ {
		f.clock.add(30 * time.Second)
		want := i
		f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples >= want })
	}
	r = f.wait(t, r.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
	if r.VerificationResult == nil || *r.VerificationResult != "passed" || !r.Targets[0].Coverage.Complete || r.Targets[0].Coverage.SuccessfulSamples != 11 || r.Targets[0].Terminated {
		t.Fatal(r)
	}
}

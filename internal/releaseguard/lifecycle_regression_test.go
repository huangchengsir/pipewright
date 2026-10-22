package releaseguard

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/target"
)

type lifecycleRead struct {
	entered, cancelled, release chan struct{}
}

func newLifecycleRead() *lifecycleRead {
	return &lifecycleRead{make(chan struct{}), make(chan struct{}), make(chan struct{})}
}

func (r *lifecycleRead) wait(ctx context.Context) {
	close(r.entered)
	<-ctx.Done()
	close(r.cancelled)
	// Cancellation and actual observer exit are distinct lifecycle events.
	<-r.release
}

func (r *lifecycleRead) unblock() {
	select {
	case <-r.release:
	default:
		close(r.release)
	}
}

type lifecycleObserver struct {
	Observer
	baselines []*lifecycleRead
	baselineN atomic.Int64
	runtime   *lifecycleRead
}

func (o *lifecycleObserver) Baseline(ctx context.Context, cap target.ConnectionSnapshot, name string) RuntimeSnapshot {
	i := int(o.baselineN.Add(1)) - 1
	if i < len(o.baselines) {
		o.baselines[i].wait(ctx)
	}
	return RuntimeSnapshot{Outcome: "ok", Reason: "late_private_baseline", CollectedAt: time.Now().UTC()}
}

func (o *lifecycleObserver) Inspect(ctx context.Context, cap target.ConnectionSnapshot, id string) RuntimeSnapshot {
	if o.runtime != nil {
		o.runtime.wait(ctx)
	}
	return RuntimeSnapshot{Outcome: "ok", Reason: "late_runtime", CollectedAt: time.Now().UTC()}
}

func lifecycleAwait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not reach the requested lifecycle boundary")
	}
}

func attachedLifecycleFixture(t *testing.T, o *lifecycleObserver) (serviceIntegrationFixture, context.Context, *Report, string) {
	t.Helper()
	f := newServiceIntegrationFixture(t)
	o.Observer = f.observer
	f.service.observer = o
	ctx := context.Background()
	if _, err := f.store.DB.Exec(`INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) SELECT 's2','server2',host,port,user,credential_id,created_at,updated_at FROM servers WHERE id='s'`); err != nil {
		t.Fatal(err)
	}
	plan, err := f.service.Prepare(ctx, "p", PrepareInput{Mode: "attached", RunID: "r", ArtifactID: f.artifactID, ServerIDs: []string{"s", "s2"}, DurationSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	deployCtx, report, err := f.service.Attached(ctx, "p", StartInput{PlanID: plan.ID, PlanHash: plan.Hash, IdempotencyKey: "attached-lifecycle"}, "r", f.artifactID, []string{"s", "s2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.service.DeploymentStarted(deployCtx, f.attemptID)
	template, err := f.ledger.GetTarget(ctx, f.targetID)
	if err != nil {
		t.Fatal(err)
	}
	input := template.TargetInput
	input.ServerID, input.ServerName = "s2", "server2"
	cap, err := f.targets.(target.LimitedExecutor).Capture(ctx, "s2")
	if err != nil {
		t.Fatal(err)
	}
	input.ConnectionFingerprint = cap.Fingerprint()
	second, err := f.ledger.StartTarget(ctx, f.attemptID, input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, read := range o.baselines {
			read.unblock()
		}
		if o.runtime != nil {
			o.runtime.unblock()
		}
	})
	return f, deployCtx, report, second.ID
}

func TestAttachedBaselineStopTracksEveryHostUntilObserverExit(t *testing.T) {
	o := &lifecycleObserver{baselines: []*lifecycleRead{newLifecycleRead(), newLifecycleRead()}}
	f, deployCtx, report, secondID := attachedLifecycleFixture(t, o)
	done := make(chan struct{}, 2)
	for i, id := range []string{f.targetID, secondID} {
		go func(id string) { f.service.BeforeImageActivation(deployCtx, f.attemptID, id); done <- struct{}{} }(id)
		lifecycleAwait(t, o.baselines[i].entered)
	}
	r, err := f.service.Stop(context.Background(), "p", report.ID)
	if err != nil || r.ExecutionStatus != "stopping" {
		t.Fatalf("stop must acknowledge pending observer exit: report=%+v error=%v", r, err)
	}
	for _, read := range o.baselines {
		lifecycleAwait(t, read.cancelled)
	}
	if err := f.service.Delete(context.Background(), "p", report.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("deletion claimed completion while baseline was in flight: %v", err)
	}
	o.baselines[0].unblock()
	lifecycleAwait(t, done)
	r, err = f.service.Snapshot(context.Background(), "p", report.ID)
	if err != nil || r.ExecutionStatus != "stopping" {
		t.Fatalf("one remaining host must retain stopping barrier: %+v %v", r, err)
	}
	o.baselines[1].unblock()
	lifecycleAwait(t, done)
	f.wait(t, report.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
	if deployCtx.Err() != nil {
		t.Fatalf("guard stop cancelled the independently authorized deployment: %v", deployCtx.Err())
	}
	evidence, err := f.service.Evidence(context.Background(), "p", report.ID, 0, 100)
	if err != nil || len(evidence.Items) != 0 {
		t.Fatalf("cancelled baselines persisted late private evidence: %+v %v", evidence, err)
	}
}

func TestAttachedBaselineStopCancelsSemaphoreWaitWithoutDispatch(t *testing.T) {
	o := &lifecycleObserver{}
	f, deployCtx, report, _ := attachedLifecycleFixture(t, o)
	for i := 0; i < cap(f.service.sshSem); i++ {
		f.service.sshSem <- struct{}{}
	}
	defer func() {
		for len(f.service.sshSem) > 0 {
			<-f.service.sshSem
		}
	}()
	done := make(chan struct{})
	go func() { f.service.BeforeImageActivation(deployCtx, f.attemptID, f.targetID); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.service.mu.Lock()
		waiting := f.service.hasReadsLocked(report.ID)
		f.service.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("baseline semaphore wait was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := f.service.Stop(context.Background(), "p", report.ID); err != nil {
		t.Fatal(err)
	}
	lifecycleAwait(t, done)
	f.wait(t, report.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
	if got := o.baselineN.Load(); got != 0 {
		t.Fatalf("cancelled semaphore wait dispatched %d observer reads", got)
	}
	if deployCtx.Err() != nil {
		t.Fatal("guard stop cancelled deployment context")
	}
}

func TestAttachedBaselineAndWorkerShareTerminalBarrier(t *testing.T) {
	o := &lifecycleObserver{baselines: []*lifecycleRead{newLifecycleRead()}, runtime: newLifecycleRead()}
	f, deployCtx, report, secondID := attachedLifecycleFixture(t, o)
	f.service.AfterImageActivation(deployCtx, f.attemptID, f.targetID, strings.Repeat("1", 64))
	lifecycleAwait(t, o.runtime.entered)
	done := make(chan struct{})
	go func() { f.service.BeforeImageActivation(deployCtx, f.attemptID, secondID); close(done) }()
	lifecycleAwait(t, o.baselines[0].entered)
	if _, err := f.service.Stop(context.Background(), "p", report.ID); err != nil {
		t.Fatal(err)
	}
	lifecycleAwait(t, o.runtime.cancelled)
	lifecycleAwait(t, o.baselines[0].cancelled)
	o.runtime.unblock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.service.mu.Lock()
		_, running := f.service.workers[report.ID]
		f.service.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled worker did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	r, err := f.service.Snapshot(context.Background(), "p", report.ID)
	if err != nil || r.ExecutionStatus != "stopping" {
		t.Fatalf("worker exit incorrectly bypassed another host baseline: %+v %v", r, err)
	}
	o.baselines[0].unblock()
	lifecycleAwait(t, done)
	f.wait(t, report.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
}

func TestAttachedBaselineCloseAndProjectDeletionWaitForExit(t *testing.T) {
	for _, action := range []string{"close", "project_delete"} {
		t.Run(action, func(t *testing.T) {
			o := &lifecycleObserver{baselines: []*lifecycleRead{newLifecycleRead()}}
			f, deployCtx, report, _ := attachedLifecycleFixture(t, o)
			done := make(chan struct{})
			go func() { f.service.BeforeImageActivation(deployCtx, f.attemptID, f.targetID); close(done) }()
			lifecycleAwait(t, o.baselines[0].entered)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			var err error
			if action == "close" {
				err = f.service.Close(ctx)
			} else {
				err = f.service.BeginProjectDeletion(ctx, "p")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s claimed completion before observation exit: %v", action, err)
			}
			lifecycleAwait(t, o.baselines[0].cancelled)
			r, err := f.service.Snapshot(context.Background(), "p", report.ID)
			if err != nil || terminal(r.ExecutionStatus) {
				t.Fatalf("report claimed terminal while baseline had not exited: %+v %v", r, err)
			}
			if deployCtx.Err() != nil {
				t.Fatal("observation cancellation propagated to deployment")
			}
			o.baselines[0].unblock()
			lifecycleAwait(t, done)
			if action == "close" {
				err = f.service.Close(context.Background())
			} else {
				err = f.service.BeginProjectDeletion(context.Background(), "p")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExpiredReportStopAndIdempotentStartUseResponseProjection(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	p := f.prepare(t)
	in := StartInput{PlanID: p.ID, PlanHash: p.Hash, IdempotencyKey: "expired-response"}
	r, err := f.service.Start(context.Background(), "p", in)
	if err != nil {
		t.Fatal(err)
	}
	f.wait(t, r.ID, func(r *Report) bool { return r.Targets[0].Actual != nil })
	if _, err := f.service.Stop(context.Background(), "p", r.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(t, r.ID, func(r *Report) bool { return r.ExecutionStatus == "stopped" })
	f.clock.add(8 * 24 * time.Hour)
	for _, action := range []string{"stop", "idempotent_start"} {
		var result *Report
		if action == "stop" {
			result, err = f.service.Stop(context.Background(), "p", r.ID)
		} else {
			result, err = f.service.Start(context.Background(), "p", in)
		}
		if err != nil || result == nil || !result.EvidenceExpired || result.Targets[0].Actual != nil || len(result.Timeline) != 0 || len(result.Investigation.Candidates) != 0 || result.Investigation.BaselineStatus != "expired" {
			t.Fatalf("%s returned expired private facts: %+v %v", action, result, err)
		}
	}
	// Projection is a read-time boundary, not a hidden physical retention write.
	d, err := f.service.load(context.Background(), "p", r.ID)
	if err != nil || d.Report.EvidenceExpired || d.Report.Targets[0].Actual == nil {
		t.Fatalf("response projection unexpectedly rewrote persisted historical document: %+v %v", d, err)
	}
}

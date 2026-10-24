package releaseguard

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
	"github.com/huangchengsir/pipewright/internal/target"
)

type unavailableDeploymentObserver struct {
	Observer
	unavailable atomic.Bool
}

func (o *unavailableDeploymentObserver) Inspect(ctx context.Context, cap target.ConnectionSnapshot, id string) RuntimeSnapshot {
	if !o.unavailable.Load() {
		return o.Observer.Inspect(ctx, cap, id)
	}
	return unavailableSnapshot("fixture_node_unavailable")
}

func activateAttachedLocal(t *testing.T, f serviceIntegrationFixture, ctx context.Context, tr *deployattempt.Target) deployattempt.ActualImage {
	t.Helper()
	f.service.BeforeImageActivation(ctx, tr.AttemptID, tr.ID)
	a := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64), ConfigID: tr.ExpectedImage.Digest, Platform: tr.ExpectedImage.Platform}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated} {
		if _, err := f.ledger.AppendReceipt(context.Background(), tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: a}); err != nil {
			t.Fatal(err)
		}
	}
	f.service.AfterImageActivation(ctx, tr.AttemptID, tr.ID, a.ContainerID)
	return a
}

func advanceAttachedWindow(t *testing.T, f serviceIntegrationFixture, id string) *Report {
	t.Helper()
	f.wait(t, id, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples == 1 })
	for i := 2; i <= 11; i++ {
		f.clock.add(30 * time.Second)
		want := i
		f.wait(t, id, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples >= want })
	}
	return f.wait(t, id, func(r *Report) bool { return terminal(r.ExecutionStatus) })
}

func TestAttachedDeploymentHealthFailureCannotPassRuntimeWindow(t *testing.T) {
	for _, finishCallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "worker reads immutable failure", true: "deployment completion reads immutable failure"}[finishCallback], func(t *testing.T) {
			f, ctx, report, tr := localAttachedFixture(t)
			observer := &unavailableDeploymentObserver{Observer: localOnlyObserver{f.observer}}
			f.service.observer = observer
			actual := activateAttachedLocal(t, f, ctx, tr)
			f.wait(t, report.ID, func(r *Report) bool { return r.Targets[0].Coverage.SuccessfulSamples == 1 })
			// This is the real SQLite receipt sequence produced when the
			// deployment's HTTP/command gate exits nonzero after activation.
			failedReceipt, err := f.ledger.AppendReceipt(context.Background(), tr.ID, deployattempt.Receipt{Kind: deployattempt.DeployFailed, ActualImage: actual, HealthGate: "failed", Reason: "local_docker_health_failed"})
			if err != nil {
				t.Fatal(err)
			}
			if finishCallback {
				f.service.FinishAttached(ctx, report.ID)
			}
			f.clock.add(30 * time.Second)
			r := f.wait(t, report.ID, func(r *Report) bool { return r.VerificationResult != nil && *r.VerificationResult == "failed" })
			page, err := f.service.Evidence(context.Background(), "p", report.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var evidenceID string
			for _, e := range page.Items {
				if e.Kind != "deployment_health_gate" {
					continue
				}
				data, _ := json.Marshal(e.Value)
				var fact deploymentHealthGateFact
				if json.Unmarshal(data, &fact) != nil || fact.ReceiptID != failedReceipt.ID || fact.Sequence != failedReceipt.Sequence || fact.Kind != deployattempt.DeployFailed || fact.HealthGate != "failed" {
					t.Fatalf("untraceable deployment evidence: %s", data)
				}
				evidenceID = e.ID
			}
			if evidenceID == "" {
				t.Fatal("missing persisted health gate evidence")
			}
			found := false
			for _, c := range r.Targets[0].Conditions {
				if c.Name == "deployment_health_gate" && c.Result == "failed" && len(c.EvidenceIDs) == 1 && c.EvidenceIDs[0] == evidenceID {
					found = true
				}
			}
			if !found {
				t.Fatal("deployment failure not tied to guard evidence", r)
			}
			// A subsequent unavailable node cannot downgrade the known failure.
			observer.unavailable.Store(true)
			f.clock.add(301 * time.Second)
			r = f.wait(t, report.ID, func(r *Report) bool { return terminal(r.ExecutionStatus) })
			if r.VerificationResult == nil || *r.VerificationResult != "failed" {
				t.Fatal("known deployment failure downgraded", r)
			}
		})
	}
}

func TestAttachedDeploymentCompletionRequiredForPass(t *testing.T) {
	for _, finished := range []bool{false, true} {
		t.Run(map[bool]string{false: "runtime passes while deployment is pending", true: "successful deployment is confirmed"}[finished], func(t *testing.T) {
			f, ctx, report, tr := localAttachedFixture(t)
			f.service.observer = localOnlyObserver{f.observer}
			actual := activateAttachedLocal(t, f, ctx, tr)
			if finished {
				if _, err := f.ledger.AppendReceipt(context.Background(), tr.ID, deployattempt.Receipt{Kind: deployattempt.Succeeded, ActualImage: actual, HealthGate: "passed"}); err != nil {
					t.Fatal(err)
				}
				f.service.FinishAttached(ctx, report.ID)
			}
			r := advanceAttachedWindow(t, f, report.ID)
			want := "insufficient"
			if finished {
				want = "passed"
			}
			if r.VerificationResult == nil || *r.VerificationResult != want || !r.Targets[0].Coverage.Complete {
				t.Fatalf("result=%v want=%s complete=%v", r.VerificationResult, want, r.Targets[0].Coverage.Complete)
			}
		})
	}
}

func TestDeploymentHealthFailureSurvivesEvidenceCapacityFailure(t *testing.T) {
	f, id, _ := capacityFixture(t)
	ctx := context.Background()
	if _, err := f.ledger.AppendReceipt(ctx, f.targetID, deployattempt.Receipt{Kind: deployattempt.DeployFailed, HealthGate: "failed", Reason: "local_docker_health_failed"}); err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	d, err := f.service.load(ctx, "p", id)
	if err == nil {
		d.Private.EvidenceBytes = maxEvidenceBytes
		err = f.service.save(ctx, d, "fixture_capacity", nil)
	}
	f.service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.service.sample(ctx, "p", id, f.targetID)
	r, err := f.service.Snapshot(ctx, "p", id)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExecutionStatus != "tool_error" || r.VerificationResult == nil || *r.VerificationResult != "failed" {
		t.Fatal("storage failure erased recorded deployment failure", r)
	}
	for _, c := range r.Targets[0].Conditions {
		if c.Name == "deployment_health_gate" && len(c.EvidenceIDs) != 0 {
			t.Fatal("cites an uncommitted evidence row", c)
		}
	}
}

func TestFromNowPendingDeploymentCannotPassCompleteRuntimeWindow(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	f.service.observer = localOnlyObserver{f.observer}
	ctx := context.Background()
	a, err := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.ledger.GetTarget(ctx, f.targetID)
	if err != nil {
		t.Fatal(err)
	}
	in := old.TargetInput
	in.ExpectedImage = localExpected("sha256:" + strings.Repeat("b", 64))
	tr, err := f.ledger.StartTarget(ctx, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	actual := deployattempt.ActualImage{ContainerID: strings.Repeat("2", 64), ConfigID: in.ExpectedImage.Digest, Platform: in.ExpectedImage.Platform}
	for _, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated} {
		if _, err = f.ledger.AppendReceipt(ctx, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: actual}); err != nil {
			t.Fatal(err)
		}
	}
	f.attemptID, f.targetID = a.ID, tr.ID
	r := f.start(t, "from-now-deployment-still-pending")
	r = advanceAttachedWindow(t, f, r.ID)
	if r.VerificationResult == nil || *r.VerificationResult != "insufficient" || !r.Targets[0].Coverage.Complete {
		t.Fatal("pending deployment passed based only on runtime observations", r)
	}
}

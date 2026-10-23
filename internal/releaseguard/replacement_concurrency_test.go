package releaseguard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/deployattempt"
)

func TestServiceIntegrationReplacementUsesActivationReceiptOrder(t *testing.T) {
	for _, olderActivatesLast := range []bool{true, false} {
		name := "newer_target_activates_last"
		if olderActivatesLast {
			name = "older_target_slow_pull_activates_last"
		}
		t.Run(name, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			// Target creation precedes image pulling. The two attempts are created
			// in A/B order, but activation can complete in either order.
			base := f.clock.now().Add(time.Hour).Truncate(time.Millisecond)
			a := newReplacementTarget(t, f, base)
			b := newReplacementTarget(t, f, base.Add(time.Second))
			first, last := a, b
			if olderActivatesLast {
				first, last = b, a
			}
			completeReplacementActivation(t, f, first, base.Add(2*time.Second), "2")
			lastActivation := base.Add(5 * time.Second)
			completeReplacementActivation(t, f, last, lastActivation, "3")

			assertReplacementAt(t, f, first, &lastActivation)
			assertReplacementAt(t, f, last, nil)
			if got := f.observer.reads.Load(); got != 0 {
				t.Fatalf("lineage lookup made %d remote observer calls", got)
			}
		})
	}
}

func TestServiceIntegrationReplacementRejectsOverlappingActivationIntervals(t *testing.T) {
	for _, olderCompletesLast := range []bool{true, false} {
		name := "newer_target_completes_last"
		if olderCompletesLast {
			name = "older_target_completes_last"
		}
		t.Run(name, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			base := f.clock.now().Add(time.Hour).Truncate(time.Millisecond)
			a := newReplacementTarget(t, f, base)
			b := newReplacementTarget(t, f, base.Add(time.Second))
			appendReplacementReceipt(t, f, a.ID, deployattempt.Receipt{Kind: deployattempt.ActivationStarted}, base.Add(2*time.Second))
			bStarted := base.Add(3 * time.Second)
			appendReplacementReceipt(t, f, b.ID, deployattempt.Receipt{Kind: deployattempt.ActivationStarted}, bStarted)
			first, last := a, b
			if olderCompletesLast {
				first, last = b, a
			}
			for i, tr := range []*deployattempt.Target{first, last} {
				at := base.Add(time.Duration(4+i*2) * time.Second)
				image := deployattempt.ActualImage{ContainerID: strings.Repeat("4", 64)}
				appendReplacementReceipt(t, f, tr.ID, deployattempt.Receipt{Kind: deployattempt.Activated, ActualImage: image}, at)
				appendReplacementReceipt(t, f, tr.ID, deployattempt.Receipt{Kind: deployattempt.Succeeded, ActualImage: image}, at.Add(time.Second))
			}
			// Interleaved rm/run intervals cannot prove which activation owns the
			// workload. A last Activated receipt alone must not bless either one.
			aActivated := base.Add(4 * time.Second)
			if olderCompletesLast {
				aActivated = base.Add(6 * time.Second)
			}
			assertReplacementAt(t, f, a, &bStarted)
			assertReplacementAt(t, f, b, &aActivated)
			if got := f.observer.reads.Load(); got != 0 {
				t.Fatalf("lineage lookup made %d remote observer calls", got)
			}
		})
	}
}

func TestServiceIntegrationReplacementRejectsEarlierStillOpenActivation(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	base := f.clock.now().Add(time.Hour).Truncate(time.Millisecond)
	a := newReplacementTarget(t, f, base)
	b := newReplacementTarget(t, f, base.Add(time.Second))
	aStarted := base.Add(2 * time.Second)
	appendReplacementReceipt(t, f, a.ID, deployattempt.Receipt{Kind: deployattempt.ActivationStarted}, aStarted)
	bStarted := base.Add(3 * time.Second)
	completeReplacementActivation(t, f, b, bStarted, "5")

	// A may still remove or replace the workload even after B's docker run
	// returns. B's completed activation cannot close A's unresolved interval.
	assertReplacementAt(t, f, b, &aStarted)
	assertReplacementAt(t, f, a, &bStarted)
	if got := f.observer.reads.Load(); got != 0 {
		t.Fatalf("lineage lookup made %d remote observer calls", got)
	}
}

func TestServiceIntegrationReplacementPreservesSubMillisecondReceiptOrder(t *testing.T) {
	f := newServiceIntegrationFixture(t)
	base := f.clock.now().Add(time.Hour).Truncate(time.Millisecond)
	// B is registered first but waits while A completes its activation. All
	// six receipts share one indexed millisecond; their JSON retains precision.
	b := newReplacementTarget(t, f, base)
	a := newReplacementTarget(t, f, base.Add(time.Second))
	activation := base.Add(2 * time.Second)
	for i, tr := range []*deployattempt.Target{a, b} {
		for j, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
			at := activation.Add(time.Duration(1+i*3+j) * 100 * time.Microsecond)
			appendReplacementReceipt(t, f, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: strings.Repeat("6", 64)}}, at)
		}
	}
	bStarted := activation.Add(400 * time.Microsecond)
	assertReplacementAt(t, f, b, nil)
	assertReplacementAt(t, f, a, &bStarted)
	if got := f.observer.reads.Load(); got != 0 {
		t.Fatalf("lineage lookup made %d remote observer calls", got)
	}
}

func TestServiceIntegrationReplacementFailedActivationIntervals(t *testing.T) {
	for _, tc := range []struct {
		name         string
		started, end time.Duration
		current      time.Duration
		wantReplaced bool
	}{
		{"earlier_activation_fails_after_current_activation", 2 * time.Second, 6 * time.Second, 3 * time.Second, true},
		{"failed_activation_finished_before_current_activation", 2 * time.Second, 3 * time.Second, 4 * time.Second, false},
		{"pull_failure_has_no_activation_interval", 0, 6 * time.Second, 3 * time.Second, false},
		{"completed_failure_same_millisecond_is_prior", 100 * time.Microsecond, 200 * time.Microsecond, 300 * time.Microsecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceIntegrationFixture(t)
			base := f.clock.now().Add(time.Hour).Truncate(time.Millisecond)
			earlier := newReplacementTarget(t, f, base)
			current := newReplacementTarget(t, f, base.Add(time.Microsecond))
			if tc.started != 0 {
				appendReplacementReceipt(t, f, earlier.ID, deployattempt.Receipt{Kind: deployattempt.ActivationStarted}, base.Add(tc.started))
			}
			appendReplacementReceipt(t, f, earlier.ID, deployattempt.Receipt{Kind: deployattempt.DeployFailed, Reason: "docker_run_failed_without_previous_image"}, base.Add(tc.end))
			for i, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
				appendReplacementReceipt(t, f, current.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: strings.Repeat("6", 64)}}, base.Add(tc.current+time.Duration(i)*time.Microsecond))
			}
			var want *time.Time
			if tc.wantReplaced {
				at := base.Add(tc.started)
				want = &at
			}
			assertReplacementAt(t, f, current, want)
			if got := f.observer.reads.Load(); got != 0 {
				t.Fatalf("failed activation lineage lookup made %d observer calls", got)
			}
		})
	}
}

func newReplacementTarget(t *testing.T, f serviceIntegrationFixture, at time.Time) *deployattempt.Target {
	t.Helper()
	ctx := context.Background()
	template, err := f.ledger.GetTarget(ctx, f.targetID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := f.ledger.Begin(ctx, deployattempt.BeginInput{RunID: "r", Initiator: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := f.ledger.StartTarget(ctx, attempt.ID, template.TargetInput)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the indexed timestamp and immutable snapshot consistent. This
	// controls ordering without sleeping or changing the ledger's real clock.
	tr.CreatedAt = at
	body, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB.ExecContext(ctx, `UPDATE deployment_attempt_targets SET created_at=?,target_json=? WHERE id=?`, at.UnixMilli(), string(body), tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set target fixture time: rows=%d err=%v", n, err)
	}
	return tr
}

func completeReplacementActivation(t *testing.T, f serviceIntegrationFixture, tr *deployattempt.Target, at time.Time, containerDigit string) {
	t.Helper()
	for i, kind := range []string{deployattempt.ActivationStarted, deployattempt.Activated, deployattempt.Succeeded} {
		appendReplacementReceipt(t, f, tr.ID, deployattempt.Receipt{Kind: kind, ActualImage: deployattempt.ActualImage{ContainerID: strings.Repeat(containerDigit, 64)}}, at.Add(time.Duration(i)*time.Second))
	}
}

func appendReplacementReceipt(t *testing.T, f serviceIntegrationFixture, targetID string, in deployattempt.Receipt, at time.Time) {
	t.Helper()
	ctx := context.Background()
	receipt, err := f.ledger.AppendReceipt(ctx, targetID, in)
	if err != nil {
		t.Fatal(err)
	}
	receipt.At = at
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB.ExecContext(ctx, `UPDATE deployment_attempt_receipts SET created_at=?,receipt_json=? WHERE id=?`, at.UnixMilli(), string(body), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set receipt fixture time: rows=%d err=%v", n, err)
	}
}

func assertReplacementAt(t *testing.T, f serviceIntegrationFixture, tr *deployattempt.Target, want *time.Time) {
	t.Helper()
	got, reason, err := f.service.replacementAt(context.Background(), PlanTarget{ID: tr.ID, WorkloadKey: tr.WorkloadKey}, tr.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if got != nil {
			t.Errorf("latest activated target was superseded by an earlier completed activation: replacementAt=%s", got.Format(time.RFC3339Nano))
		}
		return
	}
	if got == nil || !got.Equal(*want) || reason != "superseded_by_deployment" {
		t.Errorf("replacement must follow activation receipt lineage: replacementAt=%v reason=%q want=%s", got, reason, want.Format(time.RFC3339Nano))
	}
}

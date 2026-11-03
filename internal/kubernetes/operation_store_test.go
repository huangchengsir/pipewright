package kubernetes

import (
	"context"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func beforeReceipt() Receipt {
	return Receipt{UID: "deployment-uid", ResourceVersion: "opaque/0001", Generation: 2, ObservedGeneration: 2, Image: "registry.invalid/app/backend:old", TemplateHash: strings.Repeat("1", 64), ExpectedTemplateHash: strings.Repeat("2", 64), DesiredReplicas: 2, TotalReplicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}
}
func acceptedReceipt(o Operation) Receipt {
	r := beforeReceipt()
	r.ResourceVersion = "opaque/0002"
	r.Generation = 3
	r.ObservedGeneration = 3
	r.Image = o.Intent.Image
	r.Marker = o.ID
	r.TemplateHash = strings.Repeat("2", 64)
	return r
}
func cleanupRuntime(t *testing.T, f *fixture, o Operation) {
	t.Helper()
	ctx := context.Background()
	l, e := f.s.GetRuntimeLease(ctx, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.RecordCleanup(ctx, o.ID, l.Revision, CleanupEvidence{ContainerName: l.ContainerName, ContainerID: l.ContainerID, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, LabelsMatched: true, WriterExited: true, ContainerAbsent: true})
	if e != nil {
		t.Fatal(e)
	}
}
func TestDispatchNeedsApprovalAndOnlyOneAdmission(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		o := f.operation(t, "run-1")
		f.runtime(t, o)
		if _, e := f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "deployment-uid"); e != nil {
			t.Fatal(e)
		}
		o, e := f.s.GetOperation(ctx, o.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt()); e != ErrDenied {
			t.Fatal("server approval bypass", e)
		}
		o, e = f.s.ApproveOperation(ctx, o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
		if e != nil {
			t.Fatal(e)
		}
		o, e = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt())
		if e != nil {
			t.Fatal(e)
		}
		if !o.DispatchFenced || o.Business != OutcomeUnknown {
			t.Fatal("fence did not record ambiguity before send")
		}
		if _, e = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt()); e != ErrConflict {
			t.Fatal("second dispatch admitted", e)
		}
		if e = f.s.ReleaseHold(ctx, o.ID, o.OwnerEpoch); e != ErrHeld {
			t.Fatal("unknown released hold", e)
		}
		r := acceptedReceipt(o)
		o, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Submitted, r)
		if e != nil {
			t.Fatal(e)
		}
		wrong := r
		wrong.Marker = "other-operation"
		if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Ready, wrong); e != ErrConflict {
			t.Fatal("other operation success accepted", e)
		}
		incomplete := r
		incomplete.AvailableReplicas = 1
		if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Ready, incomplete); e != ErrConflict {
			t.Fatal("unready marked ready", e)
		}
		o, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Ready, r)
		if e != nil {
			t.Fatal(e)
		}
		if o.Successful() {
			t.Fatal("cleanup pending considered success")
		}
		cleanupRuntime(t, f, o)
		o, e = f.s.GetOperation(ctx, o.ID)
		if e != nil || !o.Successful() {
			t.Fatal("verified success missing", e)
		}
		if e = f.s.ReleaseHold(ctx, o.ID, o.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
	})
}
func TestFencingReconcileAndRetireKeepUnknown(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	o := f.operation(t, "run-1")
	f.runtime(t, o)
	_, e := f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "deployment-uid")
	if e != nil {
		t.Fatal(e)
	}
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, e = f.s.ApproveOperation(ctx, o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt())
	if e != nil {
		t.Fatal(e)
	}
	r := beforeReceipt()
	if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Failed, r); e != ErrConflict {
		t.Fatal("old GET disproved dispatch", e)
	}
	o, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Ready, acceptedReceipt(o)); e != ErrOwner {
		t.Fatal("late owner wrote", e)
	}
	if _, e = f.s.Retire(ctx, o.ID, o.Revision, r); e != ErrDenied {
		t.Fatal("retire without cleanup", e)
	}
	cleanupRuntime(t, f, o)
	o, _ = f.s.GetOperation(ctx, o.ID)
	unexplained := acceptedReceipt(o)
	unexplained.TemplateHash = strings.Repeat("f", 64)
	if _, e = f.s.RecordReconciliation(ctx, o.ID, o.Revision, Ready, unexplained); e != ErrConflict {
		t.Fatal("unexplained admission accepted", e)
	}
	o, e = f.s.Retire(ctx, o.ID, o.Revision, r)
	if e != nil {
		t.Fatal(e)
	}
	if !o.Retired || o.Business != OutcomeUnknown {
		t.Fatal("retire rewrote original unknown")
	}
	var holds int
	_ = f.db.DB.QueryRow(`SELECT COUNT(*) FROM k8s_workload_holds WHERE operation_id=?`, o.ID).Scan(&holds)
	if holds != 0 {
		t.Fatal("retire kept hold")
	}
	if _, e = f.s.RecordReconciliation(ctx, o.ID, o.Revision, Ready, acceptedReceipt(o)); e != ErrConflict {
		t.Fatal("retired history changed")
	}
}
func TestReconcileCanAdoptVerifiedOriginalWithoutRunMutation(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	o := f.operation(t, "run-1")
	f.runtime(t, o)
	_, _ = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "deployment-uid")
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, _ = f.s.ApproveOperation(ctx, o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
	o, e := f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt())
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	cleanupRuntime(t, f, o)
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, e = f.s.RecordReconciliation(ctx, o.ID, o.Revision, Ready, acceptedReceipt(o))
	if e != nil || !o.Successful() {
		t.Fatal("read-only proof not adopted", e)
	}
	var runs int
	_ = f.db.DB.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&runs)
	if runs != 0 {
		t.Fatal("domain created or changed source run")
	}
}
func TestCleanupIdentityAndPersistentSlots(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	o := f.operation(t, "run-1")
	f.runtime(t, o)
	l, _ := f.s.GetRuntimeLease(ctx, o.ID)
	evidence := CleanupEvidence{ContainerName: l.ContainerName, ContainerID: l.ContainerID, EngineID: l.EngineID, OwnerEpoch: l.OwnerEpoch, LabelsMatched: true, WriterExited: true, ContainerAbsent: true}
	bad := evidence
	bad.EngineID = "replacement-engine"
	if _, e := f.s.RecordCleanup(ctx, o.ID, l.Revision, bad); e != ErrOwner {
		t.Fatal("wrong engine cleanup", e)
	}
	bad = evidence
	bad.WriterExited = false
	if _, e := f.s.RecordCleanup(ctx, o.ID, l.Revision, bad); e != ErrRuntimeUnverified {
		t.Fatal("unknown writer cleanup", e)
	}
	if _, e := f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test"); e != ErrConflict {
		t.Fatal("duplicate create intent", e)
	}
	b := f.operation(t, "run-2")
	f.runtime(t, b)
	c := f.operation(t, "run-3")
	if _, e := f.s.RegisterRuntime(ctx, c.ID, c.OwnerEpoch, "engine-test"); e != ErrInUse {
		t.Fatal("global slot bound", e)
	}
	if _, e := f.s.RecordCleanup(ctx, o.ID, l.Revision, evidence); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.RegisterRuntime(ctx, c.ID, c.OwnerEpoch, "engine-test"); e != nil {
		t.Fatal("clean slot not reusable", e)
	}
}
func TestSubmittedTemplateDriftCannotBecomeSuccess(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	o := f.operation(t, "run-1")
	f.runtime(t, o)
	_, _ = f.s.AcquireHold(ctx, o.ID, o.OwnerEpoch, "deployment-uid")
	o, _ = f.s.GetOperation(ctx, o.ID)
	o, _ = f.s.ApproveOperation(ctx, o.ID, o.OwnerEpoch, o.Revision, o.IntentHash)
	o, e := f.s.PrepareDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, beforeReceipt())
	if e != nil {
		t.Fatal(e)
	}
	r := acceptedReceipt(o)
	o, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Submitted, r)
	if e != nil {
		t.Fatal(e)
	}
	r.TemplateHash = strings.Repeat("f", 64)
	if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Ready, r); e != ErrConflict {
		t.Fatal("foreign template ready", e)
	}
	if _, e = f.s.RecordObservation(ctx, o.ID, o.OwnerEpoch, o.Revision, Superseded, r); e != nil {
		t.Fatal(e)
	}
}

func TestFailureBeforeDispatchHasNoInventedReceipt(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	o := f.operation(t, "run-1")
	ctx := context.Background()
	o, e := f.s.FailBeforeDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, "secret_runtime_unverified")
	if e != nil {
		t.Fatal(e)
	}
	if o.Business != Failed || o.Receipt != nil || o.DispatchFenced || o.ReasonCode != "secret_runtime_unverified" {
		t.Fatal("failure invented facts")
	}
	if _, e = f.s.RegisterRuntime(ctx, o.ID, o.OwnerEpoch, "engine-test"); e != ErrConflict {
		t.Fatal("failed operation restarted", e)
	}
	persisted, e := f.s.GetOperation(ctx, o.ID)
	if e != nil || persisted.ReasonCode != o.ReasonCode {
		t.Fatal("failure reason not persisted", e)
	}
}

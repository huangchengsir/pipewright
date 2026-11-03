package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/retention"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func TestReviewReconcilePreservesPredispatchFailure(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	o := f.operation(t, "review-run")
	var e error
	o, e = f.s.FailBeforeDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, "preflight_failed")
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.ApplyReconciliation(ctx, o.ID, o.Revision, beforeReceipt())
	if e != nil {
		t.Fatal(e)
	}
	if o.Business != Failed || o.DispatchFenced || o.Receipt != nil {
		t.Fatalf("unexpected observed state: %+v", o)
	}
	_, e = f.s.Retire(ctx, o.ID, o.Revision, beforeReceipt())
	if e != ErrDenied {
		t.Fatalf("retire=%v", e)
	}
	if _, e = f.s.RecordReconciliation(ctx, o.ID, o.Revision, OutcomeUnknown, beforeReceipt()); e != ErrConflict {
		t.Fatal("no-dispatch proof erased", e)
	}
}

func TestReviewRetentionProtectsBlockedKubernetesCleanup(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	now := time.Now().UTC()
	_, e := f.db.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,trigger_type,created_at) VALUES('review-run','project-1','failed','manual',?)`, now.Add(-72*time.Hour).Format(time.RFC3339))
	if e != nil {
		t.Fatal(e)
	}
	o := f.operation(t, "review-run")
	f.runtime(t, o)
	l, e := f.s.GetRuntimeLease(ctx, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.RecordCleanupBlocked(ctx, o.ID, l.Revision)
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.GetOperation(ctx, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.FailBeforeDispatch(ctx, o.ID, o.OwnerEpoch, o.Revision, "preflight_failed")
	if e != nil {
		t.Fatal(e)
	}
	o, e = f.s.FenceOwner(ctx, o.ID, o.OwnerEpoch, o.Revision)
	if e != nil {
		t.Fatal(e)
	}
	svc := retention.NewService(f.db.DB)
	if e = svc.SetConfig(ctx, retention.Config{Enabled: true, MaxAgeDays: 1}); e != nil {
		t.Fatal(e)
	}
	n, e := svc.Prune(ctx, now)
	if e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatalf("deleted=%d", n)
	}
	_, e = f.s.GetOperation(ctx, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	if e = f.db.DB.QueryRow("SELECT COUNT(*) FROM pipeline_runs WHERE id=?", o.RunID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	cleanupRuntime(t, f, o)
	if e = f.s.ReleaseHold(ctx, o.ID, o.OwnerEpoch); e != nil {
		t.Fatal(e)
	}
	n, e = svc.Prune(ctx, now)
	if e != nil || n != 1 {
		t.Fatal("settled run cannot be pruned", n, e)
	}
}

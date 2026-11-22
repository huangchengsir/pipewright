package productrelease

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

var denied = errors.New("denied")

type authority struct {
	revoked atomic.Bool
	p       Principal
}

func (a *authority) Principal(context.Context) (Principal, error) { return a.p, nil }
func (a *authority) Authorize(context.Context, string, []ItemPlan, bool) error {
	if a.revoked.Load() {
		return denied
	}
	return nil
}

type executor struct {
	mu         sync.Mutex
	starts     int
	r          Receipt
	found      bool
	err        error
	validation error
	gate       chan struct{}
	started    chan struct{}
}

func (e *executor) ValidateFrozen(context.Context, ItemPlan) error { return e.validation }
func (e *executor) StartFrozen(_ context.Context, p ItemPlan, i Identity) (Receipt, error) {
	e.mu.Lock()
	e.starts++
	e.mu.Unlock()
	if e.started != nil {
		e.started <- struct{}{}
	}
	if e.gate != nil {
		<-e.gate
	}
	return e.r, e.err
}
func (e *executor) Lookup(context.Context, ItemPlan, Identity) (Receipt, bool, error) {
	return e.r, e.found, e.err
}
func (e *executor) Observe(context.Context, ItemPlan, string) (Receipt, error) { return e.r, e.err }
func fixture(t *testing.T) (*Service, *authority, *executor, Plan) {
	t.Helper()
	db := storetest.OpenDB(t)
	a := &authority{p: Principal{TenantID: "team", ActorID: "actor"}}
	e := &executor{r: Receipt{RunID: uuid.NewString(), State: "running"}, found: true}
	s := New(db, a, e)
	p := Plan{ProductID: uuid.NewString(), Items: []ItemPlan{{ApplicationID: uuid.NewString(), CommitSHA: strings.Repeat("a", 40), Environment: "prod", TargetIDs: []string{uuid.NewString()}, ConfigDigest: strings.Repeat("b", 64), SnapshotRef: uuid.NewString()}, {ApplicationID: uuid.NewString(), CommitSHA: strings.Repeat("c", 40), Environment: "prod", TargetIDs: []string{uuid.NewString()}, ConfigDigest: strings.Repeat("d", 64), SnapshotRef: uuid.NewString()}}}
	return s, a, e, p
}
func TestConfirmDurableIdempotency(t *testing.T) {
	s, _, e, p := fixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	b, err := s.Confirm(ctx, key, p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Confirm(ctx, key, p)
	if err != nil || again.ID != b.ID || e.starts != 0 {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	p.Items[0].CommitSHA = strings.Repeat("e", 40)
	if _, err = s.Confirm(ctx, key, p); !errors.Is(err, ErrConflict) {
		t.Fatalf("different input: %v", err)
	}
}
func TestUnknownNeverStartsTwice(t *testing.T) {
	s, a, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	e.err = errors.New("response lost")
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil || b.Items[0].State != "unknown" {
		t.Fatalf("advance %+v %v", b, err)
	}
	e.err = nil
	e.found = false
	s = New(s.db, a, e)
	b, err = s.Reconcile(ctx, b.ID, b.Revision)
	if err != nil || b.Items[0].State != "unknown" {
		t.Fatalf("missing lookup %+v %v", b, err)
	}
	if _, err = s.Advance(ctx, b.ID, b.Revision); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if e.starts != 1 {
		t.Fatal(e.starts)
	}
}
func TestDeploymentEvidenceNotPipelineSuccess(t *testing.T) {
	s, _, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	e.r.State = "succeeded"
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil || b.State != "needs_reconciliation" {
		t.Fatalf("unverified %+v %v", b, err)
	}
	e.r.VerifiedDeployment = true
	e.r.PlanDigest = ItemDigest(p.Items[1])
	b, err = s.Reconcile(ctx, b.ID, b.Revision)
	if err != nil || b.State != "needs_reconciliation" {
		t.Fatalf("wrong plan %+v %v", b, err)
	}
	e.r.PlanDigest = ItemDigest(p.Items[0])
	b, err = s.Reconcile(ctx, b.ID, b.Revision)
	if err != nil || b.Items[0].State != "succeeded" || b.Items[1].State != "pending" || b.State != "pending" {
		t.Fatalf("verified %+v %v", b, err)
	}
}
func TestFailureStopsFollowingItems(t *testing.T) {
	s, _, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	e.r.State = "failed"
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil || b.State != "blocked" {
		t.Fatal(err)
	}
	if _, err = s.Advance(ctx, b.ID, b.Revision); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if e.starts != 1 || b.Items[1].State != "pending" {
		t.Fatal("later item dispatched")
	}
}
func TestRevocationAndFrozenDriftBlockAdmission(t *testing.T) {
	s, a, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.revoked.Store(true)
	if _, err = s.Advance(ctx, b.ID, b.Revision); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	a.revoked.Store(false)
	e.validation = ErrConflict
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil || b.Items[0].State != "blocked" || b.Items[0].Reason != "frozen_plan_changed" {
		t.Fatalf("stale plan not blocked: %+v %v", b, err)
	}
	if e.starts != 0 {
		t.Fatal("dispatched revoked or drifted plan")
	}
}
func TestConcurrentClaimOnlyOneDispatch(t *testing.T) {
	s, _, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	e.gate = make(chan struct{})
	e.started = make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() { _, err := s.Advance(ctx, b.ID, b.Revision); done <- err }()
	select {
	case <-e.started:
	case <-time.After(5 * time.Second):
		close(e.gate)
		t.Fatal("first dispatch did not start")
	}
	_, err = s.Advance(ctx, b.ID, b.Revision)
	close(e.gate)
	first := <-done
	if first != nil {
		t.Fatal(first)
	}
	if err == nil {
		t.Fatal("concurrent admission succeeded")
	}
	if e.starts != 1 {
		t.Fatal(e.starts)
	}
}
func TestScopeAndAbsentExecutorFailClosed(t *testing.T) {
	s, a, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.p.ActorID = "other"
	if _, err = s.Get(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	a.p.ActorID = "actor"
	a.p.TenantID = "other"
	if _, err = s.Get(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = New(s.db, a, nil).Confirm(ctx, uuid.NewString(), p); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if e.starts != 0 {
		t.Fatal(e.starts)
	}
}
func TestReceiptCannotChangeRunIdentity(t *testing.T) {
	s, _, e, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	original := b.Items[0].RunID
	e.r.RunID = uuid.NewString()
	e.r.State = "succeeded"
	e.r.VerifiedDeployment = true
	e.r.PlanDigest = ItemDigest(p.Items[0])
	b, err = s.Reconcile(ctx, b.ID, b.Revision)
	if err != nil || b.Items[0].RunID != original || b.Items[0].State != "unknown" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestDefinitivePreDispatchRejectionStopsBatch(t *testing.T) {
	s, _, worker, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	worker.err = ErrDispatchRejected
	b, err = s.Advance(ctx, b.ID, b.Revision)
	if err != nil || b.State != "blocked" || b.Items[0].Reason != "admission_rejected" || b.Items[0].RunID != "" || b.Items[1].State != "pending" {
		t.Fatalf("pre-admission rejection not persisted: %+v %v", b, err)
	}
}

func TestEarlyReconcileDoesNotReplacePendingDispatchAcknowledgement(t *testing.T) {
	s, _, worker, p := fixture(t)
	ctx := context.Background()
	b, err := s.Confirm(ctx, uuid.NewString(), p)
	if err != nil {
		t.Fatal(err)
	}
	worker.gate = make(chan struct{})
	defer close(worker.gate)
	worker.started = make(chan struct{}, 1)
	worker.found = false
	result := make(chan error, 1)
	go func() {
		_, err := s.Advance(ctx, b.ID, b.Revision)
		result <- err
	}()
	select {
	case <-worker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("original dispatch did not claim")
	}
	claim, err := s.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := s.Reconcile(ctx, claim.ID, claim.Revision)
	if err != nil || checked.Revision != claim.Revision || checked.Items[0].State != "dispatching" {
		t.Fatalf("early reconciliation changed the claim: %+v %v", checked, err)
	}
	worker.gate <- struct{}{}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	saved, err := s.Get(ctx, b.ID)
	if err != nil || saved.Items[0].State != "running" || saved.Items[0].RunID != worker.r.RunID {
		t.Fatalf("original acknowledgement was lost: %+v %v", saved, err)
	}
}
func TestProductReleaseHistoryCanReachOlderUnsettledBatches(t *testing.T) {
	s, _, _, plan := fixture(t)
	ctx := context.Background()
	ids := map[string]bool{}
	for range batchPageSize + 2 {
		batch, err := s.Confirm(ctx, uuid.NewString(), plan)
		if err != nil {
			t.Fatal(err)
		}
		ids[batch.ID] = true
	}
	recent, err := s.List(ctx, plan.ProductID, 1)
	if err != nil || !recent.HasMore || len(recent.Items) != batchPageSize {
		t.Fatalf("newest batch page incomplete: %+v %v", recent, err)
	}
	older, err := s.List(ctx, plan.ProductID, 2)
	if err != nil || older.HasMore || len(older.Items) != 2 {
		t.Fatalf("older batch page unavailable: %+v %v", older, err)
	}
	for _, entry := range append(recent.Items, older.Items...) {
		if !ids[entry.ID] || entry.State != "pending" {
			t.Fatalf("unexpected history entry: %+v", entry)
		}
		delete(ids, entry.ID)
	}
	if len(ids) != 0 {
		t.Fatalf("history lost %d batches", len(ids))
	}
}

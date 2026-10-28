package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/vault"
)

type retryLifecycleProbe struct {
	svc                          *service
	retry, captured              bool
	prepareErr                   error
	mu                           sync.Mutex
	errors                       []error
	binds                        []string
	prepares, completes, settles atomic.Int32
	completed                    chan struct{}
	settled                      chan struct{}
}

func TestQueuedRecoveryCancelRestoresParentWithoutWorkerStart(t *testing.T) {
	st := storetest.OpenForDialect(t, store.SQLite)
	ctx := context.Background()
	s := newService(st.DB)
	pid := seedProject(t, st.DB)
	root, err := s.Create(ctx, pid, Trigger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.Exec("UPDATE pipeline_runs SET status='failed' WHERE id=?", root.ID); err != nil {
		t.Fatal(err)
	}
	if err = configstate.WithWriteTxContext(ctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
		return configstate.ReleaseActivityTx(tx, q, root.ID, s.activityOwner)
	}); err != nil {
		t.Fatal(err)
	}
	configstate.ForgetActivityReaders(root.ID, s.activityOwner)
	var key [32]byte
	key[0] = 77
	creator, _ := RetryAdapters(s)
	rr := runretry.New(st.DB, vault.New(st.DB, &key), runretry.WithChildCreator(creator), runretry.WithDispatch(func(context.Context, string) error { return nil }))
	snap := runretry.Snapshot{ProjectID: pid, CommitSHA: strings.Repeat("a", 40), Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Jobs: []pipeline.Job{{ID: "failed", Type: "script"}}}}}}
	if err = rr.SaveManifest(ctx, root.ID, snap); err != nil {
		t.Fatal(err)
	}
	prepared, err := rr.Prepare(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := runretry.ExecutionFrom(prepared)
	node, _ := ex.Node("stage", "failed")
	if err = ex.Begin(prepared, node); err != nil {
		t.Fatal(err)
	}
	if err = ex.Finish(prepared, node, runretry.Outcome{Status: "failed", ExecutionMode: "real", ExternalOutcome: "none"}); err != nil {
		t.Fatal(err)
	}
	plan, err := rr.Plan(ctx, root.ID, []string{node.Key})
	if err != nil || !plan.CanSubmit {
		t.Fatal(plan, err)
	}
	request, err := rr.Submit(ctx, root.ID, runretry.SubmitInput{Scope: "tester", RequestID: "cancel-before-worker", BaseAttemptID: plan.BaseAttemptID, BaseRevision: plan.BaseRevision, PlanFingerprint: plan.PlanFingerprint, TargetNodeIDs: plan.TargetNodeIDs})
	if err != nil {
		t.Fatal(err)
	}
	_ = NewWorkerPool(s, WithRetryRuntime(rr)) // deliberately never Start: no worker can run.
	cancelled, err := s.Cancel(ctx, request.AttemptRunID)
	if err != nil || cancelled.Status != "failed" || cancelled.StartedAt != nil {
		t.Fatal(cancelled, err)
	}
	view, err := rr.Context(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range view.Nodes {
		if n.Node.Key == node.Key {
			found = n.Decision.Status == "failed" && n.Decision.SourceAttemptID == root.ID && n.ReasonCode == "cancelled_before_start" && !n.ExecutionStarted
		}
	}
	if !found {
		t.Fatal(view.Nodes)
	}
	next, err := rr.Plan(ctx, request.AttemptRunID, []string{node.Key})
	if err != nil || !next.CanSubmit {
		t.Fatal(next, err)
	}
}

type retryProbeKey struct{}

func (p *retryLifecycleProbe) Prepare(ctx context.Context, id string) (context.Context, error) {
	p.prepares.Add(1)
	var phase string
	if err := p.svc.db.QueryRowContext(ctx, "SELECT phase FROM project_activity WHERE activity_id=? AND released_at IS NULL", id).Scan(&phase); err != nil {
		return ctx, err
	}
	if phase != "executing" {
		return ctx, fmt.Errorf("capture before claim: %s", phase)
	}
	return context.WithValue(ctx, retryProbeKey{}, true), p.prepareErr
}
func (p *retryLifecycleProbe) Complete(ctx context.Context, id, status string) error {
	p.completes.Add(1)
	r, err := p.svc.Get(ctx, id)
	if err == nil && (!IsTerminal(r.Status) || r.Status != status) {
		err = errors.New("complete before terminal")
	}
	p.record(err)
	if p.completed != nil {
		close(p.completed)
	}
	return err
}
func (p *retryLifecycleProbe) Settle(ctx context.Context, id string) error {
	p.settles.Add(1)
	var n int
	err := p.svc.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_activity WHERE activity_id=? AND released_at IS NULL", id).Scan(&n)
	if err == nil && n != 0 {
		err = errors.New("settle before reader drain")
	}
	p.record(err)
	if p.settled != nil {
		close(p.settled)
	}
	return err
}
func (p *retryLifecycleProbe) record(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors = append(p.errors, err)
}
func (p *retryLifecycleProbe) IsRetry(context.Context, string) (bool, error) { return p.retry, nil }
func (p *retryLifecycleProbe) HasManifest(context.Context, string) (bool, error) {
	return p.captured, nil
}
func (p *retryLifecycleProbe) BindStep(_ context.Context, _, key, id string, ordinal int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" || ordinal < 0 {
		return errors.New("invalid step binding")
	}
	p.binds = append(p.binds, key)
	return nil
}
func (p *retryLifecycleProbe) check(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.errors) > 0 {
		t.Fatal(p.errors)
	}
}

func TestRetryRuntimeCaptureFailureKeepsOriginalBehavior(t *testing.T) {
	for _, tc := range []struct {
		name            string
		retry, captured bool
		want            string
		executions      int32
	}{
		{"original", false, false, StatusSuccess, 1},
		{"recovery", true, true, StatusFailed, 0},
		{"existing_manifest", false, true, StatusFailed, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				s := newService(st.DB)
				pid := seedProject(t, st.DB)
				probe := &retryLifecycleProbe{svc: s, retry: tc.retry, captured: tc.captured, prepareErr: errors.New("private configuration detail")}
				var executed atomic.Int32
				pool := NewWorkerPool(s, WithRetryRuntime(probe), WithRunner(evidenceRunner(func(context.Context, *Run, StepSink) error { executed.Add(1); return nil })))
				pool.Start()
				t.Cleanup(func() { pool.Stop(ctx) })
				r, err := s.Create(ctx, pid, Trigger{})
				if err != nil {
					t.Fatal(err)
				}
				waitForStatus(t, s, r.ID, tc.want)
				pool.Stop(ctx)
				if got := executed.Load(); got != tc.executions {
					t.Fatalf("executions=%d want %d", got, tc.executions)
				}
				if probe.prepares.Load() != 1 {
					t.Fatal("missing prepare")
				}
				if tc.captured && probe.completes.Load() != 1 {
					t.Fatal("failed recovery was not finalized")
				}
				probe.check(t)
				var n int
				if err := st.DB.QueryRow("SELECT COUNT(*) FROM run_logs WHERE run_id=? AND text LIKE ?", r.ID, "%private configuration detail%").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatal("raw capture error leaked into run log")
				}
			})
		})
	}
}

func TestRetryRuntimeWaitsForTerminalHooksToDrain(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := newService(st.DB)
		pid := seedProject(t, st.DB)
		entered, exit := make(chan struct{}), make(chan struct{})
		probe := &retryLifecycleProbe{svc: s, captured: true, completed: make(chan struct{}), settled: make(chan struct{})}
		pool := NewWorkerPool(s, WithRetryRuntime(probe), WithRunner(evidenceRunner(func(ctx context.Context, _ *Run, _ StepSink) error {
			if ctx.Value(retryProbeKey{}) != true {
				return errors.New("prepared context lost")
			}
			return nil
		})), WithNotifyHook(func(context.Context, string, string) { close(entered); <-exit }))
		pool.Start()
		released := false
		t.Cleanup(func() {
			if !released {
				close(exit)
			}
			pool.Stop(ctx)
		})
		r, err := s.Create(ctx, pid, Trigger{})
		if err != nil {
			t.Fatal(err)
		}
		awaitSignal(t, entered)
		awaitSignal(t, probe.completed)
		if probe.settles.Load() != 0 {
			t.Fatal("terminal status was treated as resource exit")
		}
		expectActivity(t, st, r.ID, "finalizing", false)
		close(exit)
		released = true
		awaitSignal(t, probe.settled)
		pool.Stop(ctx)
		if probe.settles.Load() != 1 || probe.completes.Load() != 1 {
			t.Fatal("lifecycle callbacks duplicated or lost")
		}
		probe.check(t)
	})
}

func TestRetryRuntimeFinalizesPanicWithoutExecutingNextWork(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := newService(st.DB)
		pid := seedProject(t, st.DB)
		probe := &retryLifecycleProbe{svc: s, captured: true}
		pool := NewWorkerPool(s, WithRetryRuntime(probe), WithRunner(evidenceRunner(func(context.Context, *Run, StepSink) error { panic("fixture panic") })))
		pool.Start()
		t.Cleanup(func() { pool.Stop(ctx) })
		r, err := s.Create(ctx, pid, Trigger{})
		if err != nil {
			t.Fatal(err)
		}
		waitForStatus(t, s, r.ID, StatusFailed)
		pool.Stop(ctx)
		if probe.completes.Load() != 1 || probe.settles.Load() != 1 {
			t.Fatal("panic bypassed recovery finalization")
		}
		probe.check(t)
	})
}

func TestInheritedRetryStepHasNoFreshTimestamps(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		s := newService(st.DB)
		pid := seedProject(t, st.DB)
		probe := &retryLifecycleProbe{svc: s, captured: true, retry: true}
		s.retryRuntime = probe
		r, err := s.Create(ctx, pid, Trigger{})
		if err != nil {
			t.Fatal(err)
		}
		sink := &dbStepSink{svc: s, runID: r.ID}
		err = sink.Plan(ctx, []StepDecl{{Name: "successful source", Stage: "build", LogicalNodeKey: "stable-key", SeedStatus: StepSuccess}})
		if err != nil {
			t.Fatal(err)
		}
		if sink.StepRunning(ctx, 0) == nil || sink.StepDone(ctx, 0, StepSuccess) == nil {
			t.Fatal("inherited work acquired fresh timings")
		}
		got, err := s.Get(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Steps) != 1 || got.Steps[0].Status != StepSuccess || got.Steps[0].StartedAt != nil || got.Steps[0].FinishedAt != nil {
			t.Fatalf("inherited step=%+v", got.Steps)
		}
		if len(probe.binds) != 1 || probe.binds[0] != "stable-key" {
			t.Fatal("logical key binding lost")
		}
	})
}

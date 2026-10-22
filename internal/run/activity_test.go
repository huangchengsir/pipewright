package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("reader barrier timed out")
	}
}

func expectActivity(t *testing.T, st *store.Store, id, phase string, released bool) {
	t.Helper()
	var got string
	var count int
	if err := st.DB.QueryRow(`SELECT phase FROM project_activity WHERE activity_id=?`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE activity_id=? AND released_at IS NULL`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got != phase || (count == 0) != released {
		t.Fatalf("activity phase=%s active=%d; want %s released=%v", got, count, phase, released)
	}
}

func TestRunAdmissionAndQueuedCancelActivity(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		initializerCalled := false
		s := newService(st.DB, WithConfigInitializer(func(txctx context.Context, projectID string) error {
			initializerCalled = true
			if configstate.Queryer(txctx, st.DB) == st.DB {
				t.Fatal("initializer has no transaction")
			}
			var n int
			if err := configstate.Queryer(txctx, st.DB).QueryRowContext(txctx, `SELECT COUNT(*) FROM pipeline_runs WHERE project_id=?`, pid).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Fatal("run visible before initializer")
			}
			_, err := configstate.Queryer(txctx, st.DB).ExecContext(txctx, `UPDATE projects SET default_branch='release' WHERE id=?`, pid)
			return err
		}))
		r, err := s.Create(ctx, pid, Trigger{Type: TriggerManual})
		if err != nil {
			t.Fatal(err)
		}
		if !initializerCalled || r.Trigger.Branch != "release" {
			t.Fatalf("initializer/default branch %v/%s", initializerCalled, r.Trigger.Branch)
		}
		expectActivity(t, st, r.ID, "queued", false)
		if err := configstate.CheckIdle(ctx, st.DB, pid); !errors.Is(err, configstate.ErrBusy) {
			t.Fatalf("queued idle=%v", err)
		}
		if _, err := s.Cancel(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		expectActivity(t, st, r.ID, "released", true)
		if err := configstate.CheckIdle(ctx, st.DB, pid); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFailedInitializationRollsBackRunAndDefaults(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		injected := errors.New("initializer failed")
		s := newService(st.DB, WithConfigInitializer(func(txctx context.Context, _ string) error {
			_, err := configstate.Queryer(txctx, st.DB).ExecContext(txctx, `UPDATE projects SET default_branch='wrong' WHERE id=?`, pid)
			if err != nil {
				return err
			}
			return injected
		}))
		if _, err := s.Create(ctx, pid, Trigger{}); !errors.Is(err, injected) {
			t.Fatal(err)
		}
		var n int
		var branch string
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("run leaked %d/%v", n, err)
		}
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM project_activity`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("activity leaked %d/%v", n, err)
		}
		if err := st.DB.QueryRow(`SELECT default_branch FROM projects WHERE id=?`, pid).Scan(&branch); err != nil || branch != "main" {
			t.Fatalf("default leaked %s/%v", branch, err)
		}
	})
}

func TestTerminalHookRetainsActivityUntilReaderExit(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		s := newService(st.DB)
		entered, exit := make(chan struct{}), make(chan struct{})
		pool := NewWorkerPool(s, WithRunner(evidenceRunner(func(context.Context, *Run, StepSink) error { return nil })), WithNotifyHook(func(context.Context, string, string) { close(entered); <-exit }))
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
		if r, err := s.Get(ctx, r.ID); err != nil || r.Status != StatusSuccess {
			t.Fatalf("terminal %v/%v", r, err)
		}
		expectActivity(t, st, r.ID, "finalizing", false)
		if err := configstate.MutateProject(ctx, st.DB, pid, func(context.Context, configstate.DBTX) error { return nil }); !errors.Is(err, configstate.ErrBusy) {
			t.Fatalf("terminal hook permitted config write: %v", err)
		}
		// A terminal retention pass must not destroy the reader's release key.
		if _, err := st.DB.Exec(`DELETE FROM pipeline_runs WHERE id=?`, r.ID); err != nil {
			t.Fatal(err)
		}
		close(exit)
		released = true
		pool.Stop(ctx)
		expectActivity(t, st, r.ID, "released", true)
		if err := configstate.CheckIdle(ctx, st.DB, pid); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCancellationRetainsExecutingReaderAndRestartDoesNotExpire(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		s := newService(st.DB)
		entered, cancelled, exit := make(chan struct{}), make(chan struct{}), make(chan struct{})
		pool := NewWorkerPool(s, WithRunner(evidenceRunner(func(ctx context.Context, _ *Run, _ StepSink) error {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-exit
			return ctx.Err()
		})))
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
		if _, err := s.Cancel(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		awaitSignal(t, cancelled)
		expectActivity(t, st, r.ID, "executing", false)
		// Even a terminal orphan sweep is not proof that a config reader exited.
		if _, err := newService(st.DB).FailOrphanedRuns(ctx); err != nil {
			t.Fatal(err)
		}
		if err := configstate.CheckIdle(ctx, st.DB, pid); !errors.Is(err, configstate.ErrBusy) {
			t.Fatalf("orphan sweep released hold: %v", err)
		}
		close(exit)
		released = true
		pool.Stop(ctx)
		expectActivity(t, st, r.ID, "released", true)
	})
}

func TestUnarmedExecutingClaimRetainsLocalExitEvidence(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		s := newService(st.DB)
		r, err := s.Create(ctx, pid, Trigger{})
		if err != nil {
			t.Fatal(err)
		}
		readers := s.newActivityReaders(r.ID)
		// Deterministic ambiguous-COMMIT boundary: the database claim is durable,
		// but the caller never receives success and therefore never arms a reader.
		if err := s.claimRunActivity(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		readers.done()
		expectActivity(t, st, r.ID, "executing", false)
		maintenance := configstate.NewMaintenance(st.DB, &configstate.MaintenanceOptions{AuthorizeAdmin: func(context.Context, configstate.DBTX, string) error { return nil }})
		snapshot, err := maintenance.List(ctx, "admin")
		if err != nil {
			t.Fatal(err)
		}
		var item configstate.MaintenanceItem
		for _, i := range snapshot.Items {
			if i.ActivityID == r.ID {
				item = i
			}
		}
		if !item.Recoverable || item.EvidenceID == "" {
			t.Fatal("unarmed exit lost local proof for durable executing hold")
		}
		if _, err := maintenance.Recover(ctx, "admin", configstate.MaintenanceRecoverRequest{ActivityID: item.ActivityID, ExpectedDigest: item.Digest, EvidenceID: item.EvidenceID}); err != nil {
			t.Fatal(err)
		}
		expectActivity(t, st, r.ID, "released", true)
		if err := configstate.CheckIdle(ctx, st.DB, pid); err != nil {
			t.Fatal(err)
		}
	})
}

func TestUnarmedDuplicateCannotProveActiveWorkerExited(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		pid := seedProject(t, st.DB)
		s := newService(st.DB)
		r, err := s.Create(ctx, pid, Trigger{})
		if err != nil {
			t.Fatal(err)
		}
		primary := s.newActivityReaders(r.ID)
		if err := s.claimRunActivity(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		primary.arm()
		duplicate := s.newActivityReaders(r.ID)
		duplicate.done()
		expectActivity(t, st, r.ID, "executing", false)
		maintenance := configstate.NewMaintenance(st.DB, &configstate.MaintenanceOptions{AuthorizeAdmin: func(context.Context, configstate.DBTX, string) error { return nil }})
		snapshot, err := maintenance.List(ctx, "admin")
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range snapshot.Items {
			if i.ActivityID == r.ID && (i.Recoverable || i.EvidenceID != "") {
				t.Fatal("duplicate exit manufactured proof while worker remains")
			}
		}
		primary.done()
		expectActivity(t, st, r.ID, "released", true)
	})
}

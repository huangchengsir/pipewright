package completedrefs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"sync/atomic"
	"testing"
)

func TestRetryUsesFreshContextAndExactDatabaseIdentity(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, a *store.Store) {
		b := storetest.OpenForDialect(t, a.Dialect)
		for _, st := range []*store.Store{a, b} {
			if _, e := st.DB.Exec("INSERT INTO selfupdate_leases(id,kind,created_at) VALUES('same-reference','owner-a',7)"); e != nil {
				t.Fatal(e)
			}
		}
		var r registry
		attempts := 0
		key := identity{a.DB, "maintenance", "same-reference", "local-owner"}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		remove := func(ctx context.Context) error {
			attempts++
			_, e := a.DB.ExecContext(ctx, "DELETE FROM selfupdate_leases WHERE id=? AND kind=? AND created_at=?", "same-reference", "owner-a", 7)
			return e
		}
		if e := r.complete(canceled, key, remove); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		r.flush(context.Background(), b.DB)
		if attempts != 0 {
			t.Fatal("completion retried on a different database")
		}
		var fresh registry
		fresh.flush(context.Background(), a.DB)
		var n int
		if e := a.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_leases").Scan(&n); e != nil || n != 1 {
			t.Fatal("fresh process invented completion", n, e)
		}
		r.flush(context.Background(), a.DB)
		if attempts != 1 {
			t.Fatal("fresh context did not retry known returned reference", attempts)
		}
		if e := a.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_leases").Scan(&n); e != nil || n != 0 {
			t.Fatal(n, e)
		}
		if e := b.DB.QueryRow("SELECT COUNT(*) FROM selfupdate_leases").Scan(&n); e != nil || n != 1 {
			t.Fatal("foreign database row was deleted", n, e)
		}
	})
}
func TestPendingCompletionCannotReplaceItsOriginalRemovalEvidence(t *testing.T) {
	st := storetest.Open(t)
	var r registry
	key := identity{st.DB, "test", "id", "owner"}
	attempts := 0
	first := func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("injected bookkeeping failure")
		}
		return nil
	}
	if e := r.complete(context.Background(), key, first); e == nil {
		t.Fatal("failure hidden")
	}
	replacementCalled := false
	if e := r.complete(context.Background(), key, func(context.Context) error { replacementCalled = true; return nil }); e != nil {
		t.Fatal(e)
	}
	if replacementCalled || attempts != 2 {
		t.Fatal("pending exact-owner evidence was replaced", attempts)
	}
	r.mu.Lock()
	left := len(r.pending)
	r.mu.Unlock()
	if left != 0 {
		t.Fatal("successful bookkeeping remained pending")
	}
}

func TestConcurrentRetryAndStaleSnapshotRemoveExactlyOnce(t *testing.T) {
	db := new(sql.DB) // Identity only; the callback below models one SQL completion.
	var r registry
	key := identity{db, "test", "exact-id", "exact-owner"}
	entered, allow, finished, secondStarted, secondFinished := make(chan struct{}), make(chan struct{}), make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var calls atomic.Int32
	go func() {
		finished <- r.complete(context.Background(), key, func(context.Context) error {
			if calls.Add(1) == 1 {
				close(entered)
			}
			<-allow
			return nil
		})
	}()
	<-entered
	r.mu.Lock()
	snapshot := r.pending[key]
	r.mu.Unlock()
	go func() { close(secondStarted); secondFinished <- r.retry(context.Background(), key, snapshot) }()
	<-secondStarted
	close(allow)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := <-secondFinished; e != nil {
		t.Fatal(e)
	}
	// Even a snapshot retained before successful cleanup must never replay removal.
	if e := r.retry(context.Background(), key, snapshot); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent/stale bookkeeping removal replayed", calls.Load())
	}
	r.mu.Lock()
	left := len(r.pending)
	r.mu.Unlock()
	if left != 0 {
		t.Fatal("successful reference remained queued")
	}
}

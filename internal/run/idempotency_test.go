package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
)

func idempotentTrigger() Trigger {
	sum := sha256.Sum256([]byte("canonical request"))
	return Trigger{Type: TriggerManual, Branch: "main", Params: map[string]string{"MODE": "test"}, IdempotencyScope: "token-test", IdempotencyKey: "request-1", IdempotencyFingerprint: hex.EncodeToString(sum[:])}
}

func TestIdempotencyConcurrentServicesAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	projectID := seedProject(t, st.DB)
	var enqueued atomic.Int32
	services := []*service{newService(st.DB), newService(st.DB)}
	for _, s := range services {
		s.setEnqueue(func(string) error { enqueued.Add(1); return nil })
	}
	ids := make(chan string, 32)
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := services[i%2].Create(ctx, projectID, idempotentTrigger())
			if err != nil {
				errs <- err
			} else {
				ids <- r.ID
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("duplicate run", got, id)
		}
	}
	if enqueued.Load() != 1 {
		t.Fatal("enqueued", enqueued.Load())
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("run count", count, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	restarted := newService(st.DB)
	restarted.setEnqueue(func(string) error { t.Error("restart enqueued duplicate"); return nil })
	r, err := restarted.Create(ctx, projectID, idempotentTrigger())
	if err != nil || r.ID != id || r.DispatchState != "accepted" {
		t.Fatal("restart replay", r, err)
	}
	changed := idempotentTrigger()
	changed.IdempotencyFingerprint = strings.Repeat("a", 64)
	if _, err := restarted.Create(ctx, projectID, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("conflict", err)
	}
	if _, err := st.DB.Exec(`DELETE FROM pipeline_runs WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Create(ctx, projectID, idempotentTrigger()); !errors.Is(err, ErrIdempotencyUnavailable) {
		t.Fatal("retention recreated run", err)
	}
}

func TestIdempotencyUnknownAndFailedEnqueue(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "executed_unknown"}[started], func(t *testing.T) {
			db := testDB(t)
			ctx := context.Background()
			p := seedProject(t, db)
			s := newService(db)
			calls := 0
			enqueueErr := ErrQueueFull
			state := "rejected"
			if started {
				enqueueErr = errors.New("acknowledgement lost")
				state = "unknown"
			}
			s.setEnqueue(func(id string) error {
				calls++
				if started {
					if _, err := db.Exec(`UPDATE pipeline_runs SET status='success' WHERE id=?`, id); err != nil {
						t.Fatal(err)
					}
				}
				return enqueueErr
			})
			first, err := s.Create(ctx, p, idempotentTrigger())
			if !errors.Is(err, enqueueErr) || first == nil || first.DispatchState != state {
				t.Fatal(first, err)
			}
			replay, err := newService(db).Create(ctx, p, idempotentTrigger())
			if err != nil || replay.ID != first.ID || replay.DispatchState != state || calls != 1 {
				t.Fatal("recreated", replay, err, calls)
			}
			if started && replay.Status != StatusSuccess {
				t.Fatal("lost executed run")
			}
			if !started && (replay.Status != StatusFailed || replay.FinishedAt == nil) {
				t.Fatal("rejected enqueue remained queued")
			}
			queried, err := s.Get(ctx, first.ID)
			if err != nil || queried.DispatchState != state {
				t.Fatal("query lost durable dispatch state", queried, err)
			}
		})
	}
}

func TestIdempotencyLookupDoesNotDispatchOrResolve(t *testing.T) {
	db := testDB(t)
	s := newService(db)
	p := seedProject(t, db)
	ctx := context.Background()
	trigger := idempotentTrigger()
	if original, found, err := s.LookupIdempotentRun(ctx, trigger); err != nil || found || original != nil {
		t.Fatal("unknown request lookup", original, found, err)
	}
	first, err := s.Create(ctx, p, trigger)
	if !errors.Is(err, ErrPoolStopped) || first == nil || first.Status != StatusFailed || first.DispatchState != "rejected" {
		t.Fatal("stopped pool did not become terminal", first, err)
	}
	s.setEnqueue(func(string) error { t.Error("lookup re-enqueued work"); return nil })
	original, found, err := s.LookupIdempotentRun(ctx, trigger)
	if err != nil || !found || original.ID != first.ID || original.DispatchState != "rejected" {
		t.Fatal("durable lookup", original, found, err)
	}
}

func TestIdempotencyPreservesEnvironmentAndLegacyCreate(t *testing.T) {
	db := testDB(t)
	p := seedProject(t, db)
	ctx := context.Background()
	s := newService(db, WithEnvResolver(idempotencyEnv{}))
	s.setEnqueue(func(string) error { return nil })
	in := idempotentTrigger()
	in.Branch = ""
	r, err := s.Create(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Trigger.Branch != "main" || r.Trigger.ResolvedEnvironment != "production" || len(r.Trigger.ResolvedTargetServerIDs) != 1 || r.Trigger.Params["MODE"] != "test" {
		t.Fatal("lost trigger context", r.Trigger)
	}
	in.IdempotencyScope = ""
	in.IdempotencyKey = ""
	in.IdempotencyFingerprint = ""
	legacy1, err := s.Create(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	legacy2, err := s.Create(ctx, p, in)
	if err != nil || legacy1.ID == legacy2.ID {
		t.Fatal("legacy became idempotent", err)
	}
	s.setEnqueue(func(string) error { return ErrQueueFull })
	if _, err := s.Create(ctx, p, in); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&count)
	if count != 3 {
		t.Fatal("legacy rollback changed", count)
	}
	bad := idempotentTrigger()
	bad.IdempotencyKey = "bad request"
	if _, err := s.Create(ctx, p, bad); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("invalid idempotency accepted", err)
	}
}

type idempotencyEnv struct{}

func (idempotencyEnv) ResolveEnv(context.Context, string, string) (string, []string) {
	return "production", []string{"target"}
}

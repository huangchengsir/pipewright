package runretry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestSubmitConcurrentPreflightKeepsConflictAndOriginalIdentity(t *testing.T) {
	for _, sameRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(sameRequest), func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				var armed atomic.Bool
				entered, release := make(chan struct{}), make(chan struct{})
				verifier := func(ctx context.Context, _ ImageRef) error {
					if !armed.CompareAndSwap(true, false) {
						return nil
					}
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				f := newFixture(t, st, WithImageVerifier(verifier))
				if _, e := f.db.Exec("UPDATE run_retry_nodes SET status='pending' WHERE attempt_id=? AND node_key=?", f.root, f.nodes["a"].Key); e != nil {
					t.Fatal(e)
				}
				image := ImageRef{Ref: "pipewright/test:image", ImageID: "sha256:" + strings.Repeat("a", 64), RuntimeID: "test-runtime", ConfigSHA: strings.Repeat("b", 64)}
				if e := f.ex.Finish(context.Background(), f.nodes["a"], Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, Outputs: Inputs{Images: map[string]ImageRef{"image": image}}}); e != nil {
					t.Fatal(e)
				}
				plan := f.plan(t)
				first, second := submitFor(plan, "first"), submitFor(plan, "second")
				if sameRequest {
					second = first
				}
				type result struct {
					request *Request
					err     error
				}
				finished := make(chan result, 1)
				armed.Store(true)
				go func() { request, e := f.s.Submit(context.Background(), f.root, first); finished <- result{request, e} }()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("first preflight did not enter")
				}
				accepted, e := f.s.Submit(context.Background(), f.root, second)
				close(release)
				if e != nil {
					t.Fatal(e)
				}
				var got result
				select {
				case got = <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("first submit did not finish")
				}
				if sameRequest {
					if got.err != nil || got.request == nil || got.request.AttemptRunID != accepted.AttemptRunID {
						t.Fatal("response-lost duplicate did not return original", got.request, got.err)
					}
				} else if !errors.Is(got.err, ErrConflict) {
					t.Fatalf("old base classified as %v, want conflict", got.err)
				}
				if f.dispatched.Load() != 1 {
					t.Fatal("competing submit duplicated dispatch", f.dispatched.Load())
				}
			})
		})
	}
}

func TestGCUnreadableIdentityRotatesWithoutRemovingUnknownImage(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		for i := range 18 {
			id := fmt.Sprintf("identity-orphan-%02d", i)
			if _, e := f.db.Exec("INSERT INTO run_retry_lineages(root_id,project_id,head_attempt_id,revision,updated_at) VALUES(?,'project',?,1,0)", id, id); e != nil {
				t.Fatal(e)
			}
			if _, e := f.db.Exec("INSERT INTO run_retry_attempts(run_id,root_id,parent_id,revision,execution_kind,status,created_at) VALUES(?,?,'',1,'original','failed',0)", id, id); e != nil {
				t.Fatal(e)
			}
			if i < 16 {
				state := "retained_image"
				if i%2 == 1 {
					state = "identity_evidence_unavailable"
				}
				if _, e := f.db.Exec("INSERT INTO run_retry_effects(root_id,effect_key,attempt_id,state,ciphertext,created_at,updated_at) VALUES(?,'image_gc:unreadable',?,?,X'00',0,0)", id, id, state); e != nil {
					t.Fatal(e)
				}
			}
		}
		calls := 0
		release := func(context.Context, ImageRef) error { calls++; return nil }
		now := time.Now()
		n, e := f.s.PruneOrphans(context.Background(), now, release)
		if n != 0 || !errors.Is(e, ErrUnavailable) {
			t.Fatal(n, e)
		}
		var rotated int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM run_retry_lineages WHERE root_id LIKE 'identity-orphan-%' AND updated_at>0").Scan(&rotated); e != nil || rotated != 16 {
			t.Fatal("unreadable evidence rolled back rotation", rotated, e)
		}
		n, e = f.s.PruneOrphans(context.Background(), now.Add(time.Second), release)
		if n != 2 || !errors.Is(e, ErrUnavailable) {
			t.Fatal("unreadable identities starved clear later candidates", n, e)
		}
		var unknown int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM run_retry_effects WHERE effect_key='image_gc:unreadable'").Scan(&unknown); e != nil || unknown != 16 || calls != 0 {
			t.Fatal("unknown image proof lost or physical removal attempted", unknown, calls, e)
		}
	})
}

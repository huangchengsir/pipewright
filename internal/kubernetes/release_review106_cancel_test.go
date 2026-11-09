package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReview106CancelReleaseFencesContinuationWithoutRollback(t *testing.T) {
	for _, scenario := range []string{"waiting", "requested", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, p := releaseStoreFixture(t, st)
				runID := "pause-cancel"
				if _, e := st.DB.Exec("INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'project-1','running','now')", runID); e != nil {
					t.Fatal(e)
				}
				r, e := f.s.CreateRelease(ctx, runID, "node", "owner", p)
				if e != nil {
					t.Fatal(e)
				}
				r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
					t.Fatal(e)
				}
				r, e = f.s.SuspendRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID, Cursor: 1})
				if e != nil {
					t.Fatal(e)
				}
				var request ReleaseContinuation
				if scenario == "requested" {
					request, e = f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID)
					if e != nil {
						t.Fatal(e)
					}
				}
				if scenario == "unknown" {
					if _, e = st.DB.Exec("UPDATE k8s_releases SET state='outcome_unknown' WHERE id=?", r.ID); e != nil {
						t.Fatal(e)
					}
				}
				if _, e = f.s.CancelReleaseRun(ctx, runID); !errors.Is(e, ErrConflict) {
					t.Fatal("nonterminal run granted cancellation fence", e)
				}
				if _, e = st.DB.Exec("UPDATE pipeline_runs SET status='failed',finished_at='now' WHERE id=?", runID); e != nil {
					t.Fatal(e)
				}
				safe, e := f.s.CancelReleaseRun(ctx, runID)
				if e != nil || safe != (scenario != "unknown") {
					t.Fatal("cancel known/unknown proof", safe, e)
				}
				fresh, e := f.s.GetRelease(ctx, r.ID)
				if e != nil || !fresh.OwnerFenced || fresh.State == "paused" || fresh.State == "resume_requested" {
					t.Fatal("paused frontier survived", e)
				}
				if _, e = f.s.RequestReleaseContinue(ctx, fresh.ID, fresh.Revision, fresh.PlanID); !errors.Is(e, ErrConflict) {
					t.Fatal("cancelled source resumed", e)
				}
				if request.ID != "" {
					if _, e = f.s.ClaimReleaseContinuation(ctx, request.ID, "other-owner"); e == nil {
						t.Fatal("cancelled outbox claimed")
					}
				}
				var gates, actions int
				st.DB.QueryRow("SELECT COUNT(*) FROM k8s_resource_gates WHERE owner_id=?", r.ID).Scan(&gates)
				st.DB.QueryRow("SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=?", r.ID).Scan(&actions)
				if actions != 0 || (gates > 0) != (scenario == "unknown") {
					t.Fatal("cancel wrote business action or dropped unknown hold", actions, gates)
				}
			})
		})
	}
}

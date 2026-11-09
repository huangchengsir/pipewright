package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReleaseContinuationRecoversBothPreRunnerCrashWindows(t *testing.T) {
	for _, activated := range []bool{false, true} {
		storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
			t.Run(map[bool]string{false: "claimed-before-activate", true: "activated-before-runner"}[activated], func(t *testing.T) {
				ctx := context.Background()
				f, p := releaseStoreFixture(t, st)
				suffix := "first"
				if activated {
					suffix = "second"
				}
				runID := "crash-" + suffix
				if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,'running','2026-11-07T00:00:00Z')`, runID, "project-1"); e != nil {
					t.Fatal(e)
				}
				r, e := f.s.CreateRelease(ctx, runID, "node", "departed-"+suffix, p)
				if e != nil {
					t.Fatal(e)
				}
				r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				r, e = f.s.SuspendRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID, Cursor: 1, Stages: map[string]string{"done": "success", "release": "waiting_action"}, Nodes: map[string]string{"node": "waiting_action"}})
				if e != nil {
					t.Fatal(e)
				}
				request, e := f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				request, e = f.s.ClaimReleaseContinuation(ctx, request.ID, "new-owner")
				if e != nil {
					t.Fatal(e)
				}
				if e = f.s.ActivateReleaseContinuation(ctx, request.ID, "new-owner"); !errors.Is(e, ErrOwner) {
					t.Fatal("activation before worker claim", e)
				}
				if e = configstate.WithWriteTxContext(ctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
					if _, err := q.ExecContext(tx, `INSERT INTO project_activity(activity_id,project_id,run_id,owner_epoch,kind,phase,created_at,heartbeat_at) VALUES(?,?,?,?,'run','executing',1,1)`, runID, "project-1", runID, "departed-reader"); err != nil {
						return err
					}
					if err := f.s.BindReleaseContinuationReader(tx, runID, "new-owner", "departed-reader"); err != nil {
						return err
					}
					_, err := q.ExecContext(tx, `UPDATE pipeline_runs SET status='running' WHERE id=?`, runID)
					return err
				}); e != nil {
					t.Fatal(e)
				}
				departed := "new-owner"
				if activated {
					if e = f.s.ActivateReleaseContinuation(ctx, request.ID, "new-owner"); e != nil {
						t.Fatal(e)
					}
					departed = "new-owner"
				}
				controller := &ReleaseController{store: f.s}
				if e = controller.RecoverDeparted(ctx, []string{r.OwnerEpoch}); e != nil {
					t.Fatal("old owner recovery rejected", e)
				}
				var before string
				if e = st.DB.QueryRow(`SELECT state FROM k8s_release_continuations WHERE id=?`, request.ID).Scan(&before); e != nil || before == "pending" {
					t.Fatal("unproved active dispatcher was recovered", before, e)
				}
				if e = controller.RecoverDeparted(ctx, []string{departed}); e != nil {
					t.Fatal(e)
				}
				fresh, e := f.s.GetRelease(ctx, r.ID)
				if e != nil || fresh.PlanID != r.PlanID || fresh.State != "resume_requested" || !fresh.OwnerFenced {
					t.Fatal("frozen continue request lost", fresh, e)
				}
				var status string
				if e = st.DB.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, runID).Scan(&status); e != nil || status != "waiting_action" {
					t.Fatal("claimed continuation turned terminal", status, e)
				}
				var activeReaders int
				if e = st.DB.QueryRow(`SELECT COUNT(*) FROM project_activity WHERE run_id=? AND released_at IS NULL`, runID).Scan(&activeReaders); e != nil || activeReaders != 0 {
					t.Fatal("departed process reader remained held", activeReaders, e)
				}
				next, e := f.s.ClaimReleaseContinuation(ctx, request.ID, "after-restart")
				if e != nil || next.Checkpoint.Stages["done"] != "success" || next.Checkpoint.PlanID != r.PlanID {
					t.Fatal("frozen frontier lost", next, e)
				}
				actions, e := f.s.ListReleaseActions(ctx, r.ID)
				if e != nil || len(actions) != 0 {
					t.Fatal("recovery invented dispatch", e)
				}
			})
		})
	}
}

func TestReleaseContinueCannotReviveCanceledRun(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('canceled-pause','project-1','running','now')`); e != nil {
			t.Fatal(e)
		}
		r, e := f.s.CreateRelease(ctx, "canceled-pause", "node", "owner", p)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.SuspendRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID, Cursor: 1})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec(`UPDATE pipeline_runs SET status='canceled',finished_at='ended' WHERE id=?`, r.RunID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID); !errors.Is(e, ErrConflict) {
			t.Fatal("canceled run revived", e)
		}
		fresh, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || fresh.State != "paused" || fresh.Revision != r.Revision {
			t.Fatal("rejected request modified paused truth", fresh, e)
		}
	})
}

func TestReleaseContinuationUnknownCrashBecomesBlockedFactWithoutRequeue(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('unknown-continue','project-1','running','now')`); e != nil {
			t.Fatal(e)
		}
		r, e := f.s.CreateRelease(ctx, "unknown-continue", "node", "first-owner", p)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.SuspendRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID, Cursor: 1})
		if e != nil {
			t.Fatal(e)
		}
		request, e := f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		request, e = f.s.ClaimReleaseContinuation(ctx, request.ID, "departed")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec(`UPDATE pipeline_runs SET status='running' WHERE id=?`, r.RunID); e != nil {
			t.Fatal(e)
		}
		if e = f.s.ActivateReleaseContinuation(ctx, request.ID, "departed"); e != nil {
			t.Fatal(e)
		}
		r, e = f.s.GetRelease(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		registerControllerTestWriter(t, f, r)
		object := releaseObjectCopy(p.Resources[0].Object)
		releaseMap(object["metadata"])["uid"] = "uid"
		releaseMap(object["metadata"])["resourceVersion"] = "17"
		body, _ := json.Marshal(object)
		action, e := f.s.PrepareReleaseAction(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseActionIntent{Version: 2, ParentID: r.PlanID, StepKey: "lost-after-continue", Resource: p.Resources[0].ID, Verb: "patch", Body: body, ExpectedUID: "uid", ExpectedRV: "17", AuthorizationFingerprint: p.Authorization.Fingerprint})
		if e != nil {
			t.Fatal(e)
		}
		cleaner := &probeControllerAPI{cleanup: true, releaseControllerAPI: &releaseControllerAPI{f: f, record: r}}
		if _, e = cleaner.Close(ctx); e != nil {
			t.Fatal(e)
		}
		if e = f.s.RecoverReleaseContinuation(ctx, request.ID, "departed"); !errors.Is(e, ErrOutcomeUnknown) {
			t.Fatal("unknown request was auto resumed", e)
		}
		fresh, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || fresh.State != "outcome_unknown" || !fresh.OwnerFenced {
			t.Fatal("unknown recovery fact not committed", fresh.State, e)
		}
		var state, runState string
		if e = st.DB.QueryRow(`SELECT state FROM k8s_release_continuations WHERE id=?`, request.ID).Scan(&state); e != nil || state != "blocked_unknown" {
			t.Fatal(state, e)
		}
		if e = st.DB.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&runState); e != nil || runState != "failed" {
			t.Fatal(runState, e)
		}
		if pending, e := f.s.PendingReleaseContinuations(ctx); e != nil || len(pending) != 0 {
			t.Fatal("blocked request queued again", len(pending), e)
		}
		fact, e := f.s.GetReleaseAction(ctx, action.ID)
		if e != nil || fact.State != "outcome_unknown" {
			t.Fatal("ambiguous action rewritten", e)
		}
	})
}

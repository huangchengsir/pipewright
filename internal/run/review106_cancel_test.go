package run

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReview106PausedCancelCommitsFrontierAndCallbackAtomically(t *testing.T) {
	for _, scenario := range []string{"safe", "unknown", "callback-error"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				pid := seedProject(t, st.DB)
				called := false
				svc := New(st.DB, WithReleaseCancellation(func(tx context.Context, id string) (bool, error) {
					called = true
					var status string
					if e := configstate.Queryer(tx, st.DB).QueryRowContext(tx, "SELECT status FROM pipeline_runs WHERE id=?", id).Scan(&status); e != nil {
						return false, e
					}
					if status != "failed" {
						t.Fatal("callback before terminal CAS")
					}
					if configstate.Queryer(tx, st.DB) == st.DB {
						t.Fatal("callback outside transaction")
					}
					if scenario == "callback-error" {
						return false, errors.New("controlled failure")
					}
					return scenario == "safe", nil
				})).(*service)
				r, e := svc.Create(ctx, pid, Trigger{Type: TriggerManual})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = st.DB.Exec("UPDATE pipeline_runs SET status='waiting_action' WHERE id=?", r.ID); e != nil {
					t.Fatal(e)
				}
				sink := &dbStepSink{svc: svc, runID: r.ID}
				if e = sink.Plan(ctx, []StepDecl{{Name: "done"}, {Name: "pause"}, {Name: "later"}}); e != nil {
					t.Fatal(e)
				}
				sink.StepRunning(ctx, 0)
				sink.StepDone(ctx, 0, StepSuccess)
				sink.StepRunning(ctx, 1)
				sink.StepDone(ctx, 1, StepWaitingAction)
				got, e := svc.Cancel(ctx, r.ID)
				if !called {
					t.Fatal("release cancellation omitted")
				}
				fresh, _ := New(st.DB).Get(ctx, r.ID)
				if scenario == "callback-error" {
					if e == nil || fresh.Status != StatusWaitingAction || fresh.FinishedAt != nil {
						t.Fatal("partial transaction committed", e)
					}
					return
				}
				if e != nil || got.Status != StatusFailed || got.FinishedAt == nil || fresh.Status != StatusFailed {
					t.Fatal("paused cancellation not durable", e)
				}
				rows, e := st.DB.Query("SELECT status,finished_at FROM run_steps WHERE run_id=? ORDER BY ordinal", r.ID)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				want := []string{StepSuccess, StepFailed, StepSkipped}
				i := 0
				for rows.Next() {
					var status string
					var ended any
					rows.Scan(&status, &ended)
					if status != want[i] || ended == nil {
						t.Fatal("frontier changed incorrectly", status)
					}
					i++
				}
				if _, e = svc.Cancel(ctx, r.ID); !errors.Is(e, ErrNotCancelable) {
					t.Fatal("terminal cancellation accepted", e)
				}
			})
		})
	}
}

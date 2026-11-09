package run

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
)

type batchPauseRunner struct {
	db    *sql.DB
	calls atomic.Int32
}

func (r *batchPauseRunner) Run(ctx context.Context, rn *Run, sink StepSink) error {
	r.calls.Add(1)
	if err := sink.Plan(ctx, []StepDecl{{Name: "release", Stage: "deploy"}}); err != nil {
		return err
	}
	if err := sink.StepRunning(ctx, 0); err != nil {
		return err
	}
	if rn.Trigger.Branch == "quick" {
		return sink.StepDone(ctx, 0, StepSuccess)
	}
	if err := sink.StepDone(ctx, 0, StepWaitingAction); err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE pipeline_runs SET status='waiting_action' WHERE id=? AND status='running'`, rn.ID); err != nil {
		return err
	}
	return ErrSuspended
}
func TestWorkerSuspensionHasNoTerminalTimingOrNotificationAndFreesSlot(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	runner := &batchPauseRunner{db: db}
	notifications := make(chan string, 4)
	pool := NewWorkerPool(svc, WithRunner(runner), WithConcurrency(1), WithMaxConcurrent(1), WithNotifyHook(func(ctx context.Context, id, status string) { notifications <- id }))
	pool.Start()
	project := seedProject(t, db)
	paused, err := svc.Create(context.Background(), project, Trigger{Type: TriggerManual, Branch: "batch"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForStatus(t, svc, paused.ID, StatusWaitingAction)
	if waiting.FinishedAt != nil {
		t.Fatal("paused run has terminal timestamp")
	}
	quick, err := svc.Create(context.Background(), project, Trigger{Type: TriggerManual, Branch: "quick"})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, svc, quick.ID, StatusSuccess)
	pool.Stop(context.Background())
	close(notifications)
	for id := range notifications {
		if id == paused.ID {
			t.Fatal("paused run emitted terminal notification")
		}
	}
	var status string
	var ended any
	if err = db.QueryRow(`SELECT status,finished_at FROM run_steps WHERE run_id=?`, paused.ID).Scan(&status, &ended); err != nil || status != StepWaitingAction || ended != nil {
		t.Fatal("paused step reconciled as terminal", status, ended, err)
	}
}
func TestContinuationPlanReusesStepIdentityAndCannotReplayCompletedNode(t *testing.T) {
	db := testDB(t)
	svc := New(db).(*service)
	project := seedProject(t, db)
	rn, err := svc.Create(context.Background(), project, Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	sink := &dbStepSink{svc: svc, runID: rn.ID}
	ctx := context.Background()
	decls := []StepDecl{{Name: "source", Stage: "source"}, {Name: "release", Stage: "deploy"}}
	if err = sink.Plan(ctx, decls); err != nil {
		t.Fatal(err)
	}
	ids := append([]string(nil), sink.stepIDs...)
	sink.StepRunning(ctx, 0)
	sink.StepDone(ctx, 0, StepSuccess)
	sink.StepRunning(ctx, 1)
	sink.StepDone(ctx, 1, StepWaitingAction)
	resumed := &dbStepSink{svc: svc, runID: rn.ID}
	decls[0].SeedStatus = StepSuccess
	if err = resumed.Plan(ctx, decls); err != nil {
		t.Fatal(err)
	}
	if resumed.stepIDs[0] != ids[0] || resumed.stepIDs[1] != ids[1] {
		t.Fatal("continuation created new step IDs")
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM run_steps WHERE run_id=?`, rn.ID).Scan(&count)
	if count != 2 {
		t.Fatal("duplicated step rows", count)
	}
	decls[0].SeedStatus = ""
	if err = (&dbStepSink{svc: svc, runID: rn.ID}).Plan(ctx, decls); err == nil {
		t.Fatal("completed source could be replayed")
	}
}

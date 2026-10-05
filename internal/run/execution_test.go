package run

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/huangchengsir/pipewright/internal/storetest"
)

type evidenceRunner func(context.Context, *Run, StepSink) error

func (f evidenceRunner) Run(ctx context.Context, r *Run, sink StepSink) error { return f(ctx, r, sink) }

func TestExecutionEvidenceConcurrent(t *testing.T) {
	var e ExecutionEvidence
	if e.Mode() != ExecutionPending {
		t.Fatal(e.Mode())
	}
	e.RecordExecution(ExecutionLegacyUnknown)
	if e.Mode() != ExecutionPending {
		t.Fatal("runtime must not create historical evidence")
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); e.RecordExecution(ExecutionReal) }()
		go func() { defer wg.Done(); e.RecordExecution(ExecutionStub) }()
	}
	wg.Wait()
	if e.Mode() != ExecutionMixed {
		t.Fatal(e.Mode())
	}
}

func TestWorkerExecutionEvidence(t *testing.T) {
	storetest.SkipIfMySQL(t) // Ordering and failure injection use SQLite triggers.
	for _, tc := range []struct {
		name, want, status          string
		modes                       []string
		fail, panicRun, rejectWrite bool
	}{
		{name: "noop", want: ExecutionPending, status: StatusSuccess},
		{name: "real", want: ExecutionReal, status: StatusSuccess, modes: []string{ExecutionReal}},
		{name: "stub", want: ExecutionStub, status: StatusSuccess, modes: []string{ExecutionStub}},
		{name: "mixed", want: ExecutionMixed, status: StatusSuccess, modes: []string{ExecutionReal, ExecutionStub}},
		{name: "failure", want: ExecutionReal, status: StatusFailed, modes: []string{ExecutionReal}, fail: true},
		{name: "panic", want: ExecutionStub, status: StatusFailed, modes: []string{ExecutionStub}, panicRun: true},
		{name: "write_failure", want: ExecutionPending, status: StatusSuccess, modes: []string{ExecutionReal}, rejectWrite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			id := seedProject(t, db)
			if tc.rejectWrite {
				if _, err := db.Exec(`CREATE TRIGGER reject_evidence BEFORE UPDATE OF execution_mode ON pipeline_runs BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			// Terminal persistence itself verifies evidence was committed first.
			if _, err := db.Exec(`CREATE TRIGGER check_terminal BEFORE UPDATE OF status ON pipeline_runs WHEN NEW.status IN ('success','failed') AND NEW.execution_mode != '` + tc.want + `' BEGIN SELECT RAISE(ABORT, 'evidence ordering'); END`); err != nil {
				t.Fatal(err)
			}
			svc := New(db)
			pool := NewWorkerPool(svc, WithRunner(evidenceRunner(func(ctx context.Context, r *Run, sink StepSink) error {
				mode, err := LoadExecutionMode(ctx, db, r.ID)
				if err != nil || mode != ExecutionPending {
					return errors.New("evidence written before execution finished")
				}
				for _, mode := range tc.modes {
					RecordExecution(sink, mode)
				}
				if tc.panicRun {
					panic("test panic")
				}
				if tc.fail {
					return errors.New("test execution failure")
				}
				return nil
			})))
			pool.Start()
			defer pool.Stop(context.Background())
			r, err := svc.Create(context.Background(), id, Trigger{Type: TriggerManual})
			if err != nil {
				t.Fatal(err)
			}
			waitForStatus(t, svc, r.ID, tc.status)
			mode, err := LoadExecutionMode(context.Background(), db, r.ID)
			if err != nil || mode != tc.want {
				t.Fatalf("mode = %s, %v; want %s", mode, err, tc.want)
			}
		})
	}
}

func TestExecutionPersistenceOnce(t *testing.T) {
	storetest.SkipIfMySQL(t) // Write counting uses a SQLite trigger.
	db := testDB(t)
	svc := New(db)
	r, err := svc.Create(context.Background(), seedProject(t, db), Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE evidence_writes (n INTEGER)`,
		`INSERT INTO evidence_writes VALUES (0)`,
		`CREATE TRIGGER count_evidence AFTER UPDATE OF execution_mode ON pipeline_runs BEGIN UPDATE evidence_writes SET n = n + 1; END`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	sink := &dbStepSink{svc: svc.(*service), runID: r.ID}
	sink.RecordExecution(ExecutionReal)
	sink.persistExecution()
	sink.persistExecution()
	var n int
	if err := db.QueryRow(`SELECT n FROM evidence_writes`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("writes = %d, %v", n, err)
	}
}

func TestDefaultStubRunnerEvidence(t *testing.T) {
	db := testDB(t)
	svc := New(db)
	pool := NewWorkerPool(svc)
	pool.Start()
	defer pool.Stop(context.Background())
	r, err := svc.Create(context.Background(), seedProject(t, db), Trigger{Type: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, svc, r.ID, StatusSuccess)
	mode, err := LoadExecutionMode(context.Background(), db, r.ID)
	if err != nil || mode != ExecutionStub {
		t.Fatalf("default runner mode = %s, %v", mode, err)
	}
}

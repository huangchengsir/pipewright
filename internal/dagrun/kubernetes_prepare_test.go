package dagrun

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"reflect"
	"testing"
)

func TestStagePreparationPrecedesGateAndExecution(t *testing.T) {
	var calls []string
	loader := &fakeLoader{cfg: cfgWith(pipeline.Stage{ID: "s", Gate: true, Jobs: []pipeline.Job{{ID: "j", Type: "script"}}})}
	runner := New(loader, WithStagePreparation(func(ctx context.Context, _ *run.Run, _ pipeline.Stage) (StagePreparation, error) {
		calls = append(calls, "prepare-exact-intent")
		return StagePreparation{RequireGate: true, Approve: func(context.Context) error { calls = append(calls, "persist-approved-intent"); return nil }, Close: func(context.Context) error { calls = append(calls, "finalize"); return nil }}, nil
	}), WithGate(func(context.Context, *run.Run, pipeline.Stage) (bool, error) {
		calls = append(calls, "human-approval")
		return true, nil
	}), WithStageExecutor(func(context.Context, *run.Run, pipeline.Stage, StageReporter) error {
		calls = append(calls, "execute")
		return nil
	}))
	if e := runner.Run(context.Background(), &run.Run{}, newFakeSink()); e != nil {
		t.Fatal(e)
	}
	want := []string{"prepare-exact-intent", "human-approval", "persist-approved-intent", "execute", "finalize"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal(calls)
	}
}
func TestPreparedApprovalPersistenceFailureCannotExecute(t *testing.T) {
	failed := errors.New("durable approval unavailable")
	called := false
	closed := false
	loader := &fakeLoader{cfg: cfgWith(pipeline.Stage{ID: "s", Gate: true, Jobs: []pipeline.Job{{ID: "j", Type: "script"}}})}
	runner := New(loader, WithStagePreparation(func(context.Context, *run.Run, pipeline.Stage) (StagePreparation, error) {
		return StagePreparation{RequireGate: true, Approve: func(context.Context) error { return failed }, Close: func(context.Context) error { closed = true; return nil }}, nil
	}), WithGate(func(context.Context, *run.Run, pipeline.Stage) (bool, error) { return true, nil }), WithStageExecutor(func(context.Context, *run.Run, pipeline.Stage, StageReporter) error { called = true; return nil }))
	if e := runner.Run(context.Background(), &run.Run{}, newFakeSink()); e == nil || called || !closed {
		t.Fatal(e, called, closed)
	}
}

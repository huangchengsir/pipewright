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

func TestGitSourceAndStagePreparationCoexist(t *testing.T) {
	for _, mode := range []string{"approved", "rejected", "missing gate", "missing handler", "approval persistence failure", "source failure"} {
		t.Run(mode, func(t *testing.T) {
			source, _ := gitContextStages()
			stage := pipeline.Stage{ID: "deploy", Kind: pipeline.KindDeploy, Gate: mode != "missing gate", Needs: []string{"src"}, Jobs: []pipeline.Job{{ID: "kube", Type: pipeline.KubernetesJobType}}}
			var calls []string
			opts := []Option{WithGitSourceContext(), WithStagePreparation(func(ctx context.Context, _ *run.Run, st pipeline.Stage) (StagePreparation, error) {
				if st.ID != "deploy" {
					return StagePreparation{}, nil
				}
				bound, present := run.SourceFromContext(ctx)
				if !present || bound.CommitSHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
					return StagePreparation{}, errors.New("preparation lost source binding")
				}
				calls = append(calls, "prepare")
				return StagePreparation{Context: context.WithValue(ctx, preparationSourceTestKey{}, true), RequireGate: true,
					Approve: func(context.Context) error {
						calls = append(calls, "persist approval")
						if mode == "approval persistence failure" {
							return errors.New("durability failure")
						}
						return nil
					},
					Close: func(context.Context) error { calls = append(calls, "close"); return nil }}, nil
			}), WithStageExecutor(func(ctx context.Context, _ *run.Run, st pipeline.Stage, _ StageReporter) error {
				if st.ID == "src" {
					calls = append(calls, "source")
					if mode == "source failure" {
						return errors.New("source binding failure")
					}
					return run.BindSourceContext(ctx, run.SourceContext{CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
				}
				if ctx.Value(preparationSourceTestKey{}) != true {
					t.Error("prepared context was lost")
				}
				if source, present := run.SourceFromContext(ctx); !present || source.CommitSHA == "" {
					t.Error("execution lost source context")
				}
				calls = append(calls, "execute")
				return nil
			})}
			if mode != "missing handler" {
				opts = append(opts, WithGate(func(ctx context.Context, _ *run.Run, _ pipeline.Stage) (bool, error) {
					if ctx.Value(preparationSourceTestKey{}) != true {
						t.Error("gate lost prepared context")
					}
					calls = append(calls, "gate")
					return mode != "rejected", nil
				}))
			}
			sink := &sourceCompatibilitySink{fakeSink: newFakeSink(), t: t}
			err := New(&fakeLoader{cfg: cfgWith(source, stage)}, opts...).Run(context.Background(), &run.Run{}, sink)
			wants := map[string][]string{
				"approved":                     {"source", "prepare", "gate", "persist approval", "execute", "close"},
				"rejected":                     {"source", "prepare", "gate", "close"},
				"missing gate":                 {"source", "prepare", "close"},
				"missing handler":              {"source", "prepare", "close"},
				"approval persistence failure": {"source", "prepare", "gate", "persist approval", "close"},
				"source failure":               {"source"},
			}
			if !reflect.DeepEqual(calls, wants[mode]) || (err == nil) != (mode == "approved") || sink.notices != 0 {
				t.Fatalf("calls=%v err=%v notices=%d", calls, err, sink.notices)
			}
		})
	}
}

type preparationSourceTestKey struct{}

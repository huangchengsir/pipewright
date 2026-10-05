package dagrun

import (
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
)

type evidenceSink struct {
	*fakeSink
	run.ExecutionEvidence
}

func TestDAGExecutionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		stages     []pipeline.Stage
		exec       StageExecutor
	}{
		{name: "empty", want: run.ExecutionPending},
		{name: "source", want: run.ExecutionPending, stages: []pipeline.Stage{{ID: "s", Jobs: []pipeline.Job{{ID: "j", Type: "git_source"}}}}},
		{name: "push_marker", want: run.ExecutionPending, stages: []pipeline.Stage{{ID: "s", Jobs: []pipeline.Job{{ID: "j", Type: "push_image"}}}}},
		{name: "stub", want: run.ExecutionStub, stages: []pipeline.Stage{{ID: "s", Jobs: []pipeline.Job{{ID: "j", Type: "script"}}}}},
		{name: "condition_skipped", want: run.ExecutionPending, stages: []pipeline.Stage{{ID: "s", When: pipeline.When{Branches: []string{"other"}}, Jobs: []pipeline.Job{{ID: "j", Type: "script"}}}}},
		{name: "mixed_parallel", want: run.ExecutionMixed, stages: []pipeline.Stage{{ID: "a", Needs: []string{"src"}, Jobs: []pipeline.Job{{ID: "ja"}}}, {ID: "b", Needs: []string{"src"}, Jobs: []pipeline.Job{{ID: "jb"}}}, {ID: "src"}}, exec: func(_ context.Context, _ *run.Run, stage pipeline.Stage, rep StageReporter) error {
			if len(stage.Jobs) > 0 {
				mode := run.ExecutionReal
				if stage.ID == "b" {
					mode = run.ExecutionStub
				}
				run.RecordExecution(rep.JobReporter(stage.Jobs[0].ID), mode)
			}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &evidenceSink{fakeSink: newFakeSink()}
			runner := New(&fakeLoader{cfg: cfgWith(tc.stages...)}, WithStageExecutor(tc.exec))
			if err := runner.Run(context.Background(), &run.Run{ProjectID: "p", Trigger: run.Trigger{Branch: "main"}}, sink); err != nil {
				t.Fatal(err)
			}
			if got := sink.Mode(); got != tc.want {
				t.Fatalf("mode = %s, want %s", got, tc.want)
			}
		})
	}
}

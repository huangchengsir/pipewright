package dagrun

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"sync"
	"testing"
)

type r3FrozenExecution struct {
	runretry.Execution
	snapshot runretry.Snapshot
}

func (e r3FrozenExecution) Snapshot() runretry.Snapshot { return e.snapshot }
func (e r3FrozenExecution) Node(stage, job string) (runretry.NodeRef, bool) {
	return runretry.NodeRef{Key: stage + "/" + job, ExecutionStageID: stage, ExecutionJobID: job}, true
}
func (e r3FrozenExecution) ControlNode(string, string) (runretry.NodeRef, bool) {
	return runretry.NodeRef{}, false
}
func (e r3FrozenExecution) Decision(runretry.NodeRef) runretry.Decision {
	return runretry.Decision{Action: "execute"}
}
func TestR3CapturedEmptyStageDependenciesNeverUseCurrentLinearFallback(t *testing.T) {
	stages := []pipeline.Stage{{ID: "a", Name: "a", Jobs: []pipeline.Job{{ID: "a", Name: "a"}}}, {ID: "b", Name: "b", Jobs: []pipeline.Job{{ID: "b", Name: "b"}}}}
	snap := runretry.Snapshot{Spec: pipeline.Spec{Stages: stages}, Compiled: &runretry.FrozenGraph{Version: 1, Stages: stages, Dependencies: map[string][]string{"a": {}, "b": {}}}}
	var mu sync.Mutex
	calls := map[string]int{}
	runner := New(&fakeLoader{}, WithStageExecutor(func(_ context.Context, _ *run.Run, stage pipeline.Stage, _ StageReporter) error {
		mu.Lock()
		calls[stage.ID]++
		mu.Unlock()
		if stage.ID == "a" {
			return errors.New("fixture failed")
		}
		return nil
	}))
	err := runner.Run(runretry.WithExecution(context.Background(), r3FrozenExecution{snapshot: snap}), &run.Run{ID: "frozen"}, newFakeSink())
	if err == nil || calls["a"] != 1 || calls["b"] != 1 {
		t.Fatal("captured parallel branch was reinterpreted", calls, err)
	}
}

package main

import (
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"testing"
)

func TestFrozenRollbackSnapshotKeepsExactMatrixIdentityAndExcludesOtherEffects(t *testing.T) {
	spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "build", Name: "build", Kind: "build", Jobs: []pipeline.Job{{ID: "script", Type: "script"}}}, {ID: "deploy", Name: "original deployment", Kind: "deploy", Needs: []string{"build"}, Matrix: map[string][]string{"region": {"one", "two"}}, When: pipeline.When{Events: []string{"schedule"}}, Post: []pipeline.PostStep{{}}, Services: []pipeline.ServiceSpec{{}}, Jobs: []pipeline.Job{{ID: "kube", Type: "deploy_kubernetes", Config: map[string]any{"privateConfigRef": "original-node-bound-ref"}}}}}}
	graph, e := runretry.Compile(spec)
	if e != nil {
		t.Fatal(e)
	}
	var selected runretry.NodeRef
	for _, n := range graph.Nodes {
		if n.JobID == "kube" && n.Axes["region"] == "two" {
			selected = n
		}
	}
	source := kubernetes.ReleaseRecord{ID: "original-release", NodeKey: selected.Key}
	snapshot := runretry.Snapshot{Spec: spec, Compiled: &runretry.FrozenGraph{Version: 1, Nodes: graph.Nodes}, Trigger: runretry.Trigger{Commit: "moving-target", Type: "schedule", Params: map[string]string{"frozen": "value"}}}
	frozen, node, e := frozenRollbackSnapshot(snapshot, source)
	if e != nil {
		t.Fatal(e)
	}
	if node.Key != selected.Key || node.ExecutionStageID != selected.ExecutionStageID || len(frozen.Spec.Stages) != 1 {
		t.Fatal("rollback identity changed")
	}
	stage := frozen.Spec.Stages[0]
	if !stage.Gate || len(stage.Needs) > 0 || len(stage.Post) > 0 || len(stage.Services) > 0 || !stage.When.IsEmpty() || len(stage.Matrix["region"]) != 1 || stage.Matrix["region"][0] != "two" || len(stage.Jobs) != 1 {
		t.Fatal("unrelated effect or missing fresh approval")
	}
	if frozen.Trigger.Commit != "" || frozen.Trigger.Type != "kubernetes_rollback" || frozen.Source.Kind != "release_rollback" || frozen.Source.Ref != source.ID || stage.Jobs[0].Config["privateConfigRef"] != "original-node-bound-ref" || frozen.Trigger.Params["frozen"] != "value" {
		t.Fatal("rollback source or protected config was recaptured")
	}
}

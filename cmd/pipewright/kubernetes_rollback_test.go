package main

import (
	"context"
	"errors"

	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/tenancy"
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

func TestKubernetesRollbackSealsOnlyItsOwnRunAndTarget(t *testing.T) {
	f := newCompilerFixture(t, nil)
	coordinator := kubernetesRollbackCoordinator{db: f.db}
	request := kubernetes.ReleaseAuthorizationRequest{
		ProjectID: f.project, Environment: "test", ConnectionID: "connection", CredentialID: "kube-credential",
		Executor: kubernetes.Executor{Kind: "local"},
	}
	if err := configstate.WithWriteTxContext(f.admin, f.db, func(tx context.Context, q configstate.DBTX) error {
		if err := tenancy.RegisterEnvironment(tx, f.db, f.project, request.Environment); err != nil {
			return err
		}
		if err := tenancy.RecordOwnership(tx, f.db, "kubernetes", request.ConnectionID, ""); err != nil {
			return err
		}
		return tenancy.RecordOwnership(tx, f.db, "kubernetes_credential", request.CredentialID, "")
	}); err != nil {
		t.Fatal(err)
	}
	create := func(ctx context.Context, id string, target kubernetes.ReleaseAuthorizationRequest) (runretry.Snapshot, error) {
		snapshot := runretry.Snapshot{
			SchemaVersion: runretry.SchemaVersion, ScriptEntrypointMode: runretry.ScriptEntrypointClearV1,
			ProjectID: f.project, Trigger: runretry.Trigger{Environment: request.Environment},
			Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "Deploy", Kind: "deploy", Gate: true,
				Jobs: []pipeline.Job{{ID: "kube", Type: "deploy_kubernetes"}}}}},
			RunnerRefs: map[string]string{"server/unrelated": "original", "notification/unrelated": "original"},
		}
		err := configstate.WithWriteTxContext(ctx, f.db, func(tx context.Context, q configstate.DBTX) error {
			if _, err := q.ExecContext(tx, "INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,?,?,?)", id, f.project, "queued", "2026-01-01T00:00:00Z"); err != nil {
				return err
			}
			if err := coordinator.approveRun(tx, id, &snapshot, target); err != nil {
				return err
			}
			return f.compiler.recovery.SaveManifest(tx, id, snapshot)
		})
		return snapshot, err
	}
	mismatched := request
	mismatched.Environment = "production"
	if _, err := create(f.admin, "env-denied", mismatched); !errors.Is(err, tenancy.ErrConflict) {
		t.Fatal("different environment accepted", err)
	}
	if _, err := create(f.noCredential, "grant-denied", request); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("caller without rollback permissions accepted", err)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM access_ownership WHERE resource_kind='run'").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected rollback left ownership", count, err)
	}
	if err := f.db.QueryRow("SELECT COUNT(*) FROM pipeline_runs").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected rollback left runs", count, err)
	}

	const child = "rollback-run"
	snapshot, err := create(f.admin, child, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.RunnerRefs) != 0 {
		t.Fatal("rollback inherited unrelated execution bindings")
	}
	ctx := context.Background()
	job, err := f.compiler.continuationContext(ctx, child)
	if err != nil {
		t.Fatal("dispatcher cannot obtain the saved identity", err)
	}
	if _, err = f.compiler.continuationContext(ctx, "grant-denied"); err == nil {
		t.Fatal("dispatcher admitted an unapproved run")
	}
	if err = tenancy.Check(f.admin, f.db, "run.read", "run", child); err != nil {
		t.Fatal("rollback is invisible to caller", err)
	}
	if err = tenancy.CheckRunBoundary(job, f.db, child); err != nil {
		t.Fatal("dispatcher cannot verify approved plan", err)
	}
	if action, err := tenancy.RunAdmissionAction(job, f.db, child); action != "run.retry" || err != nil {
		t.Fatal("wrong admission action", action, err)
	}
	if err = tenancy.CheckRunPermission(job, f.db, "run.retry", child, ""); err != nil {
		t.Fatal("rollback cannot enter the queue", err)
	}
	if err = tenancy.CheckRunPermission(job, f.db, "run.start", child, ""); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("rollback acquired new-run authority", err)
	}
	if err = tenancy.CheckRunPermission(job, f.db, "kubernetes.use", "other-connection", ""); !errors.Is(err, tenancy.ErrForbidden) {
		t.Fatal("rollback acquired another connection", err)
	}
}

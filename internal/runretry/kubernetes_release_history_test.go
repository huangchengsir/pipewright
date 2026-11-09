package runretry

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestKubernetesReleaseTypedHistoryUsesSealedSourceAndNeverV1Operation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		run := "history-v2"
		if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'project','running','now')`, run); e != nil {
			t.Fatal(e)
		}
		snapshot := f.snap
		snapshot.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "deploy", Name: "release", Kind: "deploy", Jobs: []pipeline.Job{{ID: "kube", Type: "deploy_kubernetes"}}}}}
		if e := f.s.SaveManifest(ctx, run, snapshot); e != nil {
			t.Fatal(e)
		}
		work, e := f.s.Prepare(ctx, run)
		if e != nil {
			t.Fatal(e)
		}
		ex, _ := ExecutionFrom(work)
		node, _ := ex.Node("deploy", "kube")
		for _, statement := range []string{`INSERT INTO k8s_credentials(id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at) VALUES('history-cred','fixture',1,1,'{}',X'00',0,0)`, `INSERT INTO k8s_connections(id,credential_id,revision,enabled,connection_json,created_at,updated_at) VALUES('history-conn','history-cred',1,1,'{}',0,0)`} {
			if _, e = st.DB.Exec(statement); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = st.DB.Exec(`INSERT INTO k8s_releases(id,run_id,node_key,project_id,connection_id,credential_id,mode,owner_epoch,revision,state,cleanup_state,owner_fenced,plan_cipher,plan_id,created_at,updated_at) VALUES('history-release',?,?,'project','history-conn','history-cred','manifest','owner',2,'ready','verified',1,X'00','opaque-cipher-id',0,0)`, run, node.Key); e != nil {
			t.Fatal(e)
		}
		outcome := Outcome{Status: "success", ExecutionMode: "real", Complete: true, Reusable: true, ExternalOutcome: "verified", KubernetesRelease: &KubernetesReleaseReceipt{ReleaseID: "history-release", SourceAttemptID: run, NodeKey: node.Key, PlanID: "opaque-cipher-id", Revision: 2, State: "ready", Cleanup: kubernetes.CleanupVerified}}
		if e = ex.Begin(work, node); e != nil {
			t.Fatal(e)
		}
		if e = ex.Finish(work, node, outcome); e != nil {
			t.Fatal(e)
		}
		l, e := f.s.load(ctx, run)
		if e != nil {
			t.Fatal(e)
		}
		records, e := f.s.records(ctx, run)
		if e != nil {
			t.Fatal(e)
		}
		ref, e := f.s.kubernetesReference(ctx, l, run, node, records[node.Key])
		if e != nil || ref == nil || ref.Version != 2 || ref.ReleaseID != "history-release" || ref.OperationID != "" || ref.SourceAttemptID != run {
			t.Fatal("mixed or lost release reference", ref, e)
		}
		history, e := f.s.KubernetesHistory(ctx, "project")
		if e != nil || len(history) != 1 || history[0].ReleaseID != ref.ReleaseID || history[0].OperationID != "" {
			t.Fatal("typed environment source lost", history, e)
		}
		if _, e = st.DB.Exec(`UPDATE k8s_releases SET plan_id='foreign-plan' WHERE id='history-release'`); e != nil {
			t.Fatal(e)
		}
		if ref, e = f.s.kubernetesReference(ctx, l, run, node, records[node.Key]); e != nil || ref != nil {
			t.Fatal("mutable unrelated plan adopted", ref, e)
		}
	})
}

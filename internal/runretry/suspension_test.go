package runretry

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestContinuationRecoversKnownActionWindowWithoutReplayingCompletedNodes(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		snap := f.snap
		snap.Spec = pipeline.Spec{Stages: []pipeline.Stage{{ID: "done", Name: "original frozen", Kind: "custom", Jobs: []pipeline.Job{{ID: "effect", Type: "script"}}}, {ID: "deploy", Kind: "deploy", Jobs: []pipeline.Job{{ID: "kube", Type: "deploy_kubernetes"}}}}}
		if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('continued','project','running','now')`); e != nil {
			t.Fatal(e)
		}
		if e := f.s.SaveManifest(ctx, "continued", snap); e != nil {
			t.Fatal(e)
		}
		initial, e := f.s.Prepare(ctx, "continued")
		if e != nil {
			t.Fatal(e)
		}
		ex, _ := ExecutionFrom(initial)
		kube, _ := ex.Node("deploy", "kube")
		done, _ := ex.Node("done", "effect")
		init, _ := ex.ControlNode("deploy", "init")
		l, e := f.s.load(ctx, "continued")
		if e != nil {
			t.Fatal(e)
		}
		for _, query := range []string{
			`INSERT INTO k8s_credentials(id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at) VALUES('continuation-cred','fixture',1,1,'{}',X'00',0,0)`,
			`INSERT INTO k8s_connections(id,credential_id,revision,enabled,connection_json,created_at,updated_at) VALUES('continuation-conn','continuation-cred',1,1,'{}',0,0)`,
		} {
			if _, e = st.DB.Exec(query); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = st.DB.Exec(`INSERT INTO k8s_releases(id,run_id,node_key,project_id,connection_id,credential_id,mode,owner_epoch,revision,state,cleanup_state,owner_fenced,plan_cipher,plan_id,created_at,updated_at) VALUES('continued-release','continued',?,'project','continuation-conn','continuation-cred','progressive','owner',1,'running','verified',0,X'00','frozen-plan',0,0)`, kube.Key); e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec(`INSERT INTO k8s_release_continuations(id,release_id,run_id,release_revision,action,state,checkpoint_cipher,created_at,updated_at) VALUES('continued-request','continued-release','continued',1,'continue','running',X'00',0,0)`); e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec(`UPDATE run_retry_nodes SET status='running',owner_epoch='departed' WHERE attempt_id='continued' AND node_key IN (?,?)`, kube.Key, init.Key); e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec(`UPDATE run_retry_nodes SET status='success',owner_epoch='departed',complete=1,reusable=1 WHERE attempt_id='continued' AND node_key=?`, done.Key); e != nil {
			t.Fatal(e)
		}
		if e = f.s.ReconcileStartup(ctx); e != nil {
			t.Fatal("startup audit", e)
		}
		resumed, e := f.s.Prepare(ctx, "continued")
		if e != nil {
			t.Fatal(e)
		}
		next, _ := ExecutionFrom(resumed)
		if next.Decision(kube).Action != "execute" || next.Decision(done).Action != "reuse" || next.Snapshot().Spec.Stages[0].Name != "original frozen" {
			t.Fatal("frozen completed frontier was not retained")
		}
		if e = next.Begin(resumed, kube); e != nil {
			t.Fatal("known release node could not resume", e)
		}
		if e = next.Begin(resumed, done); !errors.Is(e, ErrBlocked) {
			t.Fatal("completed side effect replay permitted", e)
		}
		if _, e = st.DB.Exec(`UPDATE run_retry_nodes SET status='running' WHERE attempt_id='continued' AND node_key=?`, done.Key); e != nil {
			t.Fatal(e)
		}
		if e = f.s.prepareContinuationNodes(ctx, "continued", l); !errors.Is(e, ErrUnknown) {
			t.Fatal("unrelated running effect became replayable", e)
		}
		var status string
		if e = st.DB.QueryRow(`SELECT status FROM run_retry_nodes WHERE attempt_id='continued' AND node_key=?`, kube.Key).Scan(&status); e != nil || status != "running" {
			t.Fatal("rejected recovery changed frontier", status, e)
		}
	})
}

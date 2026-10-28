package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/huangchengsir/pipewright/internal/dagrun"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/project"
	"github.com/huangchengsir/pipewright/internal/run"
	"github.com/huangchengsir/pipewright/internal/runner"
	"github.com/huangchengsir/pipewright/internal/runretry"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"github.com/huangchengsir/pipewright/internal/vault"
)

func seedRetryBinding(t *testing.T, st *store.Store, id, credential, host string) *target.Server {
	t.Helper()
	_, err := st.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES(?,?,'ssh_password','',X'00','masked','2026-10-28T00:00:00Z','2026-10-28T00:00:00Z')", credential, credential)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec("INSERT INTO servers(id,name,host,port,user,credential_id,created_at,updated_at) VALUES(?,?,?,22,'fixture',?,'2026-10-28T00:00:00Z','2026-10-28T00:00:00Z')", id, id, host, credential)
	if err != nil {
		t.Fatal(err)
	}
	return &target.Server{ID: id, Host: host, Port: 22, User: "fixture", CredentialID: credential}
}
func TestNodeRetryPreflightOnlyChecksAffectedTargetsAndAllowsCredentialRotation(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		a := seedRetryBinding(t, st, "server-a", "credential-a", "192.0.2.10")
		other := seedRetryBinding(t, st, "server-b", "credential-b", "192.0.2.11")
		b := &nodeRetryBindings{db: st.DB, real: true}
		snap := runretry.Snapshot{Settings: map[string]any{}, RunnerRefs: map[string]string{
			"server/" + a.ID: bindingIdentity(a), "server/" + other.ID: bindingIdentity(other), "credential/" + a.CredentialID: "ssh_password",
		}, Compiled: &runretry.FrozenGraph{Stages: []pipeline.Stage{{ID: "stage", Jobs: []pipeline.Job{{ID: "deploy", Type: "deploy_ssh", Config: map[string]any{"serverIds": []any{a.ID}}}}}}}}
		plan := &runretry.Plan{Nodes: []runretry.PlannedNode{{Node: runretry.NodeRef{ExecutionStageID: "stage", ExecutionJobID: "deploy", Type: "deploy_ssh"}, Decision: runretry.Decision{Action: "execute"}}}}
		if _, err := st.DB.Exec("UPDATE servers SET host='192.0.2.99' WHERE id=?", other.ID); err != nil {
			t.Fatal(err)
		}
		reasons, err := b.preflight(ctx, snap, plan)
		if err != nil || len(reasons) != 0 {
			t.Fatal(reasons, err)
		}
		if _, err = st.DB.Exec("UPDATE credentials SET ciphertext=X'01',updated_at='rotated' WHERE id=?", a.CredentialID); err != nil {
			t.Fatal(err)
		}
		reasons, err = b.preflight(ctx, snap, plan)
		if err != nil || len(reasons) != 0 {
			t.Fatal(reasons, err)
		}
		if _, err = st.DB.Exec("UPDATE servers SET host='192.0.2.100' WHERE id=?", a.ID); err != nil {
			t.Fatal(err)
		}
		reasons, err = b.preflight(ctx, snap, plan)
		if err != nil || len(reasons) != 1 || reasons[0].Code != "target_binding_changed" {
			t.Fatal(reasons, err)
		}
	})
}

type retryCaptureLoader struct {
	cfg   *pipeline.Config
	calls int
}

func (l *retryCaptureLoader) Get(context.Context, string, string) (*pipeline.Config, dagrun.SpecSource, error) {
	l.calls++
	return l.cfg, dagrun.SpecSource{FromRepo: true, Ref: "feature", File: ".pipewright.yml"}, nil
}
func TestNodeRetryCapturesActualSourceAndReusesFrozenManifest(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if _, err := st.DB.Exec("INSERT INTO credentials(id,name,type,scope,ciphertext,masked_value,created_at,updated_at) VALUES('git','fixture','git_token','',X'00','m','2026-10-28T00:00:00Z','2026-10-28T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec("INSERT INTO projects(id,name,repo_url,default_branch,credential_id,created_at,updated_at) VALUES('project','fixture','https://example.invalid/repo','feature','git','2026-10-28T00:00:00Z','2026-10-28T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	key[0] = 19
	v := vault.New(st.DB, &key)
	settings := pipeline.NewSettingsService(st.DB, v)
	if _, err := settings.Get(ctx, "project"); err != nil {
		t.Fatal(err)
	}
	runs := run.New(st.DB)
	current, err := runs.Create(ctx, "project", run.Trigger{Type: run.TriggerManual, Params: map[string]string{"VERSION": "frozen"}})
	if err != nil {
		t.Fatal(err)
	}
	loader := &retryCaptureLoader{cfg: &pipeline.Config{Spec: pipeline.Spec{Stages: []pipeline.Stage{{ID: "stage", Jobs: []pipeline.Job{{ID: "task", Type: "script", Config: map[string]any{"commands": "PRIVATE_ORIGINAL_COMMAND"}}}}}}}}
	b := &nodeRetryBindings{db: st.DB, runs: runs, projects: project.New(st.DB, v, nil), settings: settings, targets: target.New(st.DB, v, nil), runners: runner.New(st.DB, nil), loader: loader, real: true}
	snapshot, err := b.capture(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source.Kind != "repo" || snapshot.Source.Ref != "feature" || snapshot.Trigger.Params["VERSION"] != "frozen" || snapshot.RunnerRefs["credential/git"] != "git_token" {
		t.Fatalf("source binding=%+v", snapshot.Source)
	}
	recovery := newNodeRetryService(st.DB, v, b)
	if err = recovery.SaveManifest(ctx, current.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	loader.cfg.Spec.Stages[0].Jobs[0].Config["commands"] = "EDITED"
	prepared, err := recovery.Prepare(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := runretry.ExecutionFrom(prepared)
	if !ok {
		t.Fatal("no recovery context")
	}
	if ex.Snapshot().Spec.Stages[0].Jobs[0].Config["commands"] != "PRIVATE_ORIGINAL_COMMAND" || loader.calls != 1 {
		t.Fatal("manifest reloaded current configuration")
	}
	var cipher []byte
	if err = st.DB.QueryRow("SELECT ciphertext FROM run_retry_manifests WHERE root_id=?", current.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte("PRIVATE_ORIGINAL_COMMAND")) {
		t.Fatal("frozen script stored clear")
	}
}

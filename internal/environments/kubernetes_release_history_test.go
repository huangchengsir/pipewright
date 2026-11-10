package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
	"time"
)

func TestKubernetesV2HistoryUsesSealedTypedFactsAndStableChronology(t *testing.T) {
	svc, db := testService(t)
	project := seedProject(t, db)
	ctx := context.Background()
	first := seedRun(t, db, project, "prod", "one")
	second := seedRun(t, db, project, "prod", "two")
	key := [32]byte{73, 19}
	v := vault.New(db, &key)
	k := kubernetes.New(db, v)
	svc.WithKubernetes(k, nil)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO k8s_credentials(id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at) VALUES('kcred-history','fixture',1,1,'{}',X'00',1,1)`)
	exec(`INSERT INTO k8s_connections(id,credential_id,revision,enabled,connection_json,created_at,updated_at) VALUES('history-connection','kcred-history',1,1,'{}',1,1)`)
	seal := func(purpose, id string, data any) []byte {
		t.Helper()
		body, e := json.Marshal(data)
		if e != nil {
			t.Fatal(e)
		}
		envelope, _ := json.Marshal(map[string]any{"purpose": purpose, "id": id, "revision": 1, "data": body})
		cipher, e := v.SealSecret(envelope)
		if e != nil {
			t.Fatal(e)
		}
		return cipher
	}
	resource := kubernetes.ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "settings"}
	add := func(id, run string, at int64) {
		plan := kubernetes.ReleasePlan{Version: 2, Mode: "manifest", Authorization: kubernetes.ReleaseAuthorization{Request: kubernetes.ReleaseAuthorizationRequest{ProjectID: project, Environment: "prod"}}, Resources: []kubernetes.ReleaseResource{{ID: resource, Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "settings", "namespace": "app"}, "data": map[string]any{"private": "sealed-history-value"}}}}, TimeoutSeconds: 300, ObserveSeconds: 30}
		cipher := seal("pipewright.k8s.release-plan.v2", id, plan)
		h := sha256.Sum256(cipher)
		exec(`INSERT INTO k8s_releases(id,run_id,node_key,project_id,connection_id,credential_id,mode,owner_epoch,revision,state,cleanup_state,owner_fenced,plan_cipher,plan_id,created_at,updated_at) VALUES(?,?,'kube',?,'history-connection','kcred-history','manifest','owner',1,'ready','verified',1,?,?,?,?)`, id, run, project, cipher, hex.EncodeToString(h[:]), at-60000, at-60000)
		actionID := "action-" + id
		intent := kubernetes.ReleaseActionIntent{Version: 2, ParentID: id, StepKey: "resource-0", Resource: resource, Verb: "create", Body: []byte("sealed-history-value")}
		action := seal("pipewright.k8s.release-action.v2", actionID, intent)
		digest := sha256.Sum256(action)
		exec(`INSERT INTO k8s_release_actions(id,release_id,step_key,resource_key,owner_epoch,revision,state,intent_cipher,intent_id,created_at,updated_at) VALUES(?,?,'resource-0','key','owner',1,'verified',?,?,?,?)`, actionID, id, action, hex.EncodeToString(digest[:]), at, at)
	}
	now := time.Now().UTC()
	add("krel-first", first, now.Add(-2*time.Hour).UnixMilli())
	add("krel-second", second, now.Add(-time.Hour).UnixMilli())
	history, e := svc.EnvironmentHistory(ctx, project, "prod", 50)
	if e != nil {
		t.Fatal(e)
	}
	if history.Active == nil || history.Active.RunID != second || len(history.Deployments) != 2 {
		t.Fatal("v2 deployment facts missing or out of order")
	}
	target := history.Deployments[0].Kubernetes[0]
	if target.Version != 2 || target.ReleaseID != "krel-second" || target.OperationID != "" || target.Mode != "manifest" || len(target.Resources) != 1 || target.Image != "" {
		t.Fatal("v2 history fabricated legacy operation or image", target)
	}
	blob, _ := json.Marshal(history)
	if strings.Contains(string(blob), "sealed-history-value") {
		t.Fatal("history exported private plan")
	}
	exec(`UPDATE k8s_releases SET updated_at=?,revision=2 WHERE id='krel-first'`, now.UnixMilli())
	after, e := svc.EnvironmentHistory(ctx, project, "prod", 50)
	if e != nil || after.Active.RunID != second || history.Deployments[1].DeployedAt != after.Deployments[1].DeployedAt {
		t.Fatal("read-only reconciliation reordered history", e)
	}
	if _, e = svc.ResolveRollback(ctx, project, "prod"); !errors.Is(e, ErrKubernetesRollback) {
		t.Fatal("Kubernetes release allowed an SSH rollback", e)
	}
}

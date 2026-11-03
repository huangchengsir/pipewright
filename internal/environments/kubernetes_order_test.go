package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"strings"
	"testing"
	"time"
)

type reviewSealer struct{}

func (reviewSealer) SealSecret(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
func (reviewSealer) OpenSecret(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
func TestKubernetesReconcilePreservesEnvironmentOrder(t *testing.T) {
	svc, db := testService(t)
	project := seedProject(t, db)
	ctx := context.Background()
	a := seedRun(t, db, project, "prod", "commit-a")
	b := seedRun(t, db, project, "prod", "commit-b")
	k := kubernetes.New(db, reviewSealer{})
	svc.WithKubernetes(k, nil)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO k8s_credentials(id,name,revision,enabled,metadata_json,secret_cipher,created_at,updated_at) VALUES('kcred-review','review',1,1,'{}',X'00',1,1)`)
	exec(`INSERT INTO k8s_connections(id,credential_id,revision,enabled,connection_json,created_at,updated_at) VALUES('connection-review','kcred-review',1,1,'{}',1,1)`)
	image := "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
	receipt := kubernetes.Receipt{UID: "uid-one", ResourceVersion: "rv1", Generation: 1, ObservedGeneration: 1, Image: image, TemplateHash: strings.Repeat("a", 64), DesiredReplicas: 1, TotalReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}
	add := func(id, run string, when int64) {
		in := kubernetes.Intent{Image: image, Authorization: kubernetes.Authorization{Target: kubernetes.Target{ProjectID: project, Environment: "prod"}}}
		raw, _ := json.Marshal(in)
		h := sha256.Sum256(raw)
		seal := func(purpose string, data any) []byte {
			d, _ := json.Marshal(data)
			v, _ := json.Marshal(map[string]any{"purpose": purpose, "id": id, "revision": 1, "data": d})
			return v
		}
		exec(`INSERT INTO k8s_operations(id,run_id,project_id,node_id,attempt,connection_id,credential_id,owner_epoch,revision,business_state,cleanup_state,owner_fenced,intent_cipher,receipt_cipher,intent_hash,created_at,updated_at) VALUES(?,?,?,?,1,'connection-review','kcred-review','owner',1,'unchanged','verified',1,?,?,?,?,?)`, id, run, project, "node", seal("pipewright.k8s.intent.v1", in), seal("pipewright.k8s.receipt.v1", func() kubernetes.Receipt { r := receipt; r.DeploymentAt = when; return r }()), hex.EncodeToString(h[:]), when, when)
	}
	now := time.Now().UTC()
	add("op-a", a, now.Add(-2*time.Hour).UnixMilli())
	add("op-b", b, now.Add(-time.Hour).UnixMilli())
	exec("UPDATE pipeline_runs SET finished_at=? WHERE id=?", now.Add(-10*time.Minute).Format(time.RFC3339), a)
	exec("UPDATE pipeline_runs SET finished_at=? WHERE id=?", now.Add(-30*time.Minute).Format(time.RFC3339), b)
	// Approval start and whole-run finish are not deployment events.
	exec("UPDATE k8s_operations SET created_at=? WHERE id=?", now.Add(-3*time.Hour).UnixMilli(), "op-b")
	before, e := svc.EnvironmentHistory(ctx, project, "prod", 50)
	if e != nil {
		t.Fatal(e)
	}
	if before.Active.RunID != b {
		t.Fatal("fixture newer run not active")
	}
	if _, e = k.ApplyReconciliation(ctx, "op-a", 1, receipt); e != nil {
		t.Fatal(e)
	}
	after, e := svc.EnvironmentHistory(ctx, project, "prod", 50)
	if e != nil {
		t.Fatal(e)
	}
	if after.Active.RunID != b || after.Deployments[1].DeployedAt != before.Deployments[1].DeployedAt {
		t.Fatal("read-only reconciliation reordered deployment history")
	}

}

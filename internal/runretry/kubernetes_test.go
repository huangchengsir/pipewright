package runretry

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

func addKubernetesLedgerFixture(t *testing.T, f *fixture) kubernetes.Operation {
	t.Helper()
	for _, q := range []string{`INSERT INTO k8s_credentials VALUES('kc','fixture',1,1,'{}',X'00',1,1)`, `INSERT INTO k8s_connections VALUES('conn','kc',1,1,'{}',1,1)`} {
		if _, e := f.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	o := kubernetes.Operation{ID: "operation", OperationInput: kubernetes.OperationInput{RunID: f.root, NodeID: f.nodes["a"].Key, OwnerEpoch: "old-owner"}, Revision: 4, Business: kubernetes.Ready, Cleanup: kubernetes.CleanupVerified, OwnerFenced: true, IntentHash: strings.Repeat("a", 64)}
	_, e := f.db.Exec(`INSERT INTO k8s_operations(id,run_id,project_id,node_id,attempt,connection_id,credential_id,owner_epoch,revision,business_state,cleanup_state,owner_fenced,intent_cipher,intent_hash,created_at,updated_at) VALUES(?,?,'project',?,1,'conn','kc','old-owner',4,'ready','verified',1,X'00',?,1,1)`, o.ID, o.RunID, o.NodeID, o.IntentHash)
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func TestKubernetesChecksRunOutsideTransactionAndRejectStaleRevision(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	o := addKubernetesLedgerFixture(t, f)
	calls := 0
	f.s.kubernetesVerifier = func(ctx context.Context, id string) (kubernetes.Operation, error) {
		calls++
		if configstate.Queryer(ctx, f.db) != f.db {
			t.Fatal("network verifier inside transaction")
		}
		return o, nil
	}
	ctx, e := f.s.kubernetesCheckContext(context.Background(), f.root)
	if e != nil || calls != 1 {
		t.Fatal(e, calls)
	}
	if _, ok := f.s.checkedKubernetes(ctx, o.ID); !ok {
		t.Fatal("verified proof lost")
	}
	if _, e = f.db.Exec("UPDATE k8s_operations SET revision=5 WHERE id=?", o.ID); e != nil {
		t.Fatal(e)
	}
	if _, ok := f.s.checkedKubernetes(ctx, o.ID); ok {
		t.Fatal("stale proof reused")
	}
}
func TestKubernetesReceiptCannotInventSuccessOrHideCleanup(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	o := addKubernetesLedgerFixture(t, f)
	n := f.nodes["a"]
	n.Type = "deploy_kubernetes"
	out := Outcome{Status: "success", ExecutionMode: "real", ExternalOutcome: "verified", Kubernetes: &KubernetesReceipt{OperationID: o.ID, SourceAttemptID: f.root, IntentHash: o.IntentHash, Revision: o.Revision, Business: o.Business, Cleanup: o.Cleanup}}
	if e := f.s.validateKubernetesReceipt(context.Background(), f.root, n, out); e != nil {
		t.Fatal(e)
	}
	out.Kubernetes.OperationID = "invented"
	if e := f.s.validateKubernetesReceipt(context.Background(), f.root, n, out); e == nil {
		t.Fatal("invented receipt accepted")
	}
	out.Kubernetes.OperationID = o.ID
	_, _ = f.db.Exec("UPDATE k8s_operations SET cleanup_state='blocked' WHERE id=?", o.ID)
	out.Kubernetes.Cleanup = kubernetes.CleanupBlocked
	if e := f.s.validateKubernetesReceipt(context.Background(), f.root, n, out); e == nil {
		t.Fatal("unclean success")
	}
	if code, e := f.s.kubernetesBoundary(context.Background(), f.root); e != nil || code == "" {
		t.Fatal("cleanup not blocking", code, e)
	}
}

func TestKubernetesContextProjectsSealedAdoptionSourceWithoutRewritingHistory(t *testing.T) {
	f := newFixture(t, storetest.Open(t))
	ctx := context.Background()
	request, err := f.s.Submit(ctx, f.root, submitFor(f.plan(t), "adopt-context"))
	if err != nil {
		t.Fatal(err)
	}
	op := addKubernetesLedgerFixture(t, f)
	// Keep the fixture DAG intact while marking its published node as K8s.
	l, err := f.s.load(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range l.snapshot.Compiled.Nodes {
		if l.snapshot.Compiled.Nodes[i].Key == op.NodeID {
			l.snapshot.Compiled.Nodes[i].Type = "deploy_kubernetes"
		}
	}
	manifest, err := f.s.seal("manifest", l.root, "", l.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("UPDATE run_retry_manifests SET ciphertext=? WHERE root_id=?", manifest, l.root); err != nil {
		t.Fatal(err)
	}
	outcome := Outcome{Status: "success", ExecutionMode: "real", ExternalOutcome: "verified", Complete: true, Reusable: true, Kubernetes: &KubernetesReceipt{OperationID: op.ID, SourceAttemptID: op.RunID, IntentHash: op.IntentHash, Revision: op.Revision, Business: op.Business, Cleanup: op.Cleanup}}
	save := func() {
		t.Helper()
		cipher, e := f.s.seal("checkpoint", l.root, request.AttemptRunID+":"+op.NodeID, outcome)
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.db.Exec("UPDATE run_retry_nodes SET status='success',origin='execute',source_attempt_id=?,source_node_key=?,complete=1,reusable=1,ciphertext=?,reason_code='kubernetes_adopted_verified' WHERE attempt_id=? AND node_key=?", request.AttemptRunID, op.NodeID, cipher, request.AttemptRunID, op.NodeID)
		if e != nil {
			t.Fatal(e)
		}
	}
	save()
	contextDTO, err := f.s.Context(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	var found *KubernetesReference
	for _, node := range contextDTO.Nodes {
		if node.Node.Key == op.NodeID {
			found = node.Kubernetes
			if node.Decision.SourceAttemptID != request.AttemptRunID {
				t.Fatal("execute source changed")
			}
		}
	}
	if found == nil || found.SourceAttemptID != f.root || found.OperationID != op.ID || found.NodeKey != op.NodeID {
		t.Fatalf("missing exact adoption source: %+v", found)
	}
	b, err := json.Marshal(found)
	if err != nil || strings.Contains(string(b), "intentHash") || strings.Contains(string(b), "credential") {
		t.Fatal("unsafe projection", string(b), err)
	}
	var rootStatus string
	if err = f.db.QueryRow("SELECT status FROM pipeline_runs WHERE id=?", f.root).Scan(&rootStatus); err != nil || rootStatus != "failed" {
		t.Fatal("source history rewritten", rootStatus, err)
	}
	outcome.Kubernetes.SourceAttemptID = "unrelated"
	save()
	contextDTO, err = f.s.Context(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range contextDTO.Nodes {
		if node.Node.Key == op.NodeID && node.Kubernetes != nil {
			t.Fatal("unverified source projected")
		}
	}
	outcome.Kubernetes.SourceAttemptID = f.root
	outcome.Kubernetes.IntentHash = strings.Repeat("b", 64)
	save()
	contextDTO, err = f.s.Context(ctx, request.AttemptRunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range contextDTO.Nodes {
		if node.Node.Key == op.NodeID && node.Kubernetes != nil {
			t.Fatal("mismatched intent projected")
		}
	}
}

package configdraft

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"strings"
	"testing"
)

func TestReview107CopiedSecretMCPDraftReviewApplySnapshot(t *testing.T) {
	f, k := kubeDraftFixture(t)
	ctx := context.Background()
	const secret = "review107-mcp-private-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	op := kubeAdd("manifest")
	op.Kubernetes.Config["source"] = map[string]any{"kind": "inline", "content": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: copied\n  annotations:\n    kubectl.kubernetes.io/last-applied-configuration: '{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"data\":{\"password\":\"" + encoded + "\"}}'\nstringData: {password: " + secret + "}\n"}
	x := f.snapshot(t)
	d, e := f.svc.Create(ctx, f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{op}})
	if e != nil {
		t.Fatal(e)
	}
	var operations string
	if e = f.db.QueryRow("SELECT operations_json FROM mcp_config_drafts WHERE id=?", d.DraftID).Scan(&operations); e != nil {
		t.Fatal(e)
	}
	human, e := f.svc.Review(ctx, "fixture-admin", d.DraftID)
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []any{d, operations, human} {
		b, _ := json.Marshal(value)
		if strings.Contains(string(b), secret) || strings.Contains(string(b), encoded) {
			t.Fatal("MCP/public/human projection leaked copied Secret")
		}
	}
	before := f.stored(t)
	if rejected, applyErr := f.svc.Apply(ctx, f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: uuid.NewString(), RequestID: uuid.NewString()}); (applyErr == nil && rejected.State == "applied") || f.stored(t) != before {
		t.Fatal("unapproved Secret config applied")
	}
	a := f.approve(t, d)
	if _, e = f.svc.Apply(ctx, f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	var exported string
	if e = f.db.QueryRow("SELECT spec_yaml FROM pipeline_configs WHERE project_id=?", f.project).Scan(&exported); e != nil {
		t.Fatal(e)
	}
	snap, e := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "kubernetes", BeginSnapshot: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []any{f.stored(t), exported, snap} {
		b, _ := json.Marshal(value)
		if strings.Contains(string(b), secret) || strings.Contains(string(b), encoded) {
			t.Fatal("applied pipeline or snapshot leaked copied Secret")
		}
	}
	var spec pipeline.Spec
	if json.Unmarshal([]byte(f.stored(t)), &spec) != nil {
		t.Fatal("stored spec")
	}
	resolved, e := pipeline.ResolveReleaseConfigs(ctx, f.project, spec, k)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(resolved)
	if !strings.Contains(string(b), secret) || !strings.Contains(string(b), encoded) {
		t.Fatal("trusted runtime lost Secret or original copied annotation")
	}
}

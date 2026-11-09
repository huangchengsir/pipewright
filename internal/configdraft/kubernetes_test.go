package configdraft

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
)

func kubeDraftFixture(t *testing.T) (*fixture, *kubernetes.Service) {
	t.Helper()
	f := setup(t)
	key := [32]byte{11, 29}
	k := kubernetes.New(f.db, vault.New(f.db, &key))
	f.svc.opts.Kubernetes = k
	f.svc.cursorKey = []byte("stable-test-configdraft-key-32bytes")
	f.raw["stages"] = append(f.raw["stages"].([]any), map[string]any{"id": "deploy", "name": "Deploy", "kind": "deploy", "needs": []string{"build"}, "jobs": []any{}})
	b, _ := json.Marshal(f.raw)
	if _, e := f.db.Exec(`UPDATE pipeline_configs SET spec_json=? WHERE project_id=?`, string(b), f.project); e != nil {
		t.Fatal(e)
	}
	return f, k
}
func kubeDraftConfig(mode string) map[string]any {
	cfg := map[string]any{"version": 2, "mode": mode, "connectionId": "conn", "credentialId": "kcred_fixture", "context": "test", "namespace": "app", "executor": map[string]any{"kind": "local"}, "variables": []any{map[string]any{"name": "PRIVATE", "type": "string", "source": "fixed", "value": "new-private-binding"}}}
	if mode == "manifest" {
		cfg["source"] = map[string]any{"kind": "inline", "content": "apiVersion: v1\nkind: Pod\nmetadata: {name: app}\nspec:\n  containers:\n  - name: app\n    image: registry.invalid/app\n    command: [sh, -c]\n    args: ['human-review-command $${HOSTNAME}']\n    env: [{name: TOKEN, value: new-private-env}]\n"}
		cfg["checks"] = []any{map[string]any{"kind": "http", "name": "probe", "target": "http://app/private-new-token", "expected": "private-expected"}}
	}
	if mode == "helm" {
		cfg["source"] = map[string]any{"kind": "git", "path": "charts/app"}
		cfg["helm"] = map[string]any{"release": "app", "values": map[string]any{"kind": "inline", "content": "password: helm-private-value\n"}, "hooks": "approved-jobs"}
	}
	if mode == "image" || mode == "progressive" {
		cfg["workload"] = map[string]any{"kind": "Deployment", "name": "app"}
		cfg["images"] = []any{map[string]any{"container": "app", "source": map[string]any{"kind": "external", "reference": "registry.invalid/app@sha256:" + strings.Repeat("a", 64)}}}
	}
	if mode == "progressive" {
		cfg["progressive"] = map[string]any{"service": "app", "candidate": "app-canary", "trackKey": "track", "trackValue": "canary", "batches": []int{1, 2}}
	}
	return cfg
}
func kubeAdd(mode string) Operation {
	return Operation{Type: "kubernetes.add", StageID: "deploy", JobID: "kube", Value: str("Kubernetes"), Kubernetes: &KubernetesFields{Config: kubeDraftConfig(mode)}}
}
func TestKubernetesDraftFourModesSealedReviewedApplied(t *testing.T) {
	for _, mode := range []string{"manifest", "image", "helm", "progressive"} {
		t.Run(mode, func(t *testing.T) {
			f, k := kubeDraftFixture(t)
			ctx := context.Background()
			x := f.snapshot(t)
			in := CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{kubeAdd(mode)}}
			original := f.stored(t)
			d, e := f.svc.Create(ctx, f.token, in)
			if e != nil {
				t.Fatal(e)
			}
			if f.stored(t) != original || !d.Validation.ApplyAllowed || len(d.Diff) != 1 {
				t.Fatal("draft changed live configuration or failed validation")
			}
			var raw string
			if e = f.db.QueryRow(`SELECT operations_json FROM mcp_config_drafts WHERE id=?`, d.DraftID).Scan(&raw); e != nil {
				t.Fatal(e)
			}
			blob, _ := json.Marshal(d)
			for _, secret := range []string{"new-private-binding", "human-review-command", "new-private-env", "helm-private-value", "private-new-token", "private-expected"} {
				if strings.Contains(raw, secret) || strings.Contains(string(blob), secret) {
					t.Fatal("MCP/persistent draft leaked private input", secret)
				}
			}
			review, e := f.svc.Review(ctx, "fixture-admin", d.DraftID)
			if e != nil || len(review.KubernetesReview) != 1 {
				t.Fatal("human review missing", e)
			}
			if mode == "manifest" && !strings.Contains(review.KubernetesReview[0].After, "human-review-command") {
				t.Fatal("human cannot inspect executable command")
			}
			if strings.Contains(review.KubernetesReview[0].After, "new-private-env") || strings.Contains(review.KubernetesReview[0].After, "new-private-binding") {
				t.Fatal("human review exposed protected scalar")
			}
			again, e := f.svc.Create(ctx, f.token, in)
			if e != nil || again.DraftID != d.DraftID {
				t.Fatal("request identity lost", e)
			}
			if rejected, applyErr := f.svc.Apply(ctx, f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: uuid.NewString(), RequestID: uuid.NewString()}); (applyErr == nil && rejected.State == "applied") || f.stored(t) != original {
				t.Fatal("unapproved configuration applied")
			}
			a := f.approve(t, d)
			apply := ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}
			result, e := f.svc.Apply(ctx, f.token, apply)
			if e != nil || result.State != "applied" {
				t.Fatal("apply failed", e, result)
			}
			var spec pipeline.Spec
			if json.Unmarshal([]byte(f.stored(t)), &spec) != nil {
				t.Fatal("invalid stored config")
			}
			resolved, e := pipeline.ResolveReleaseConfigs(ctx, f.project, spec, k)
			if e != nil {
				t.Fatal(e)
			}
			cfg := resolved.Stages[3].Jobs[0].Config
			b, _ := json.Marshal(cfg)
			if !strings.Contains(string(b), "new-private-binding") {
				t.Fatal("private config not available to trusted runtime")
			}
			replay, e := f.svc.Apply(ctx, f.token, apply)
			if e != nil || replay.OperationID != result.OperationID {
				t.Fatal("apply replay duplicated", e)
			}
			snap, e := f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "kubernetes", BeginSnapshot: true})
			if e != nil || len(snap.Items) != 1 {
				t.Fatal("Kubernetes readback missing", e)
			}
			b, _ = json.Marshal(snap)
			if strings.Contains(string(b), "human-review-command") || strings.Contains(string(b), "new-private-binding") {
				t.Fatal("snapshot exported private input")
			}
		})
	}
}
func TestKubernetesDraftScopedUpdatePreservesSourceAndRejectsCrossNode(t *testing.T) {
	f, k := kubeDraftFixture(t)
	ctx := context.Background()
	d := f.create(t, kubeAdd("manifest"))
	a := f.approve(t, d)
	if _, e := f.svc.Apply(ctx, f.token, ApplyRequest{DraftID: d.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	next := f.create(t, Operation{Type: "kubernetes.update", StageID: "deploy", JobID: "kube", Kubernetes: &KubernetesFields{Config: map[string]any{"timeoutSeconds": 420}}})
	a = f.approve(t, next)
	if _, e := f.svc.Apply(ctx, f.token, ApplyRequest{DraftID: next.DraftID, ApprovalID: a.ID, RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	var spec pipeline.Spec
	_ = json.Unmarshal([]byte(f.stored(t)), &spec)
	cfg := spec.Stages[3].Jobs[0].Config
	full, e := k.ResolveReleaseConfig(ctx, f.project, "kube", cfg)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(full)
	for _, kept := range []string{"human-review-command", "new-private-env", "private-new-token", "private-expected"} {
		if !strings.Contains(string(b), kept) {
			t.Fatal("partial update lost private field", kept)
		}
	}
	if _, e = k.ReleaseDraftConfig(ctx, f.project, "another-node", cfg["privateRef"].(string)); e == nil {
		t.Fatal("configRef crossed node")
	}
	x := f.snapshot(t)
	bad := Operation{Type: "kubernetes.update", StageID: "build", JobID: "kube", Kubernetes: &KubernetesFields{Config: map[string]any{"timeoutSeconds": 440}}}
	if _, e = f.svc.Create(ctx, f.token, CreateRequest{ProjectID: f.project, BaseRevision: x.BaseRevision, PolicyVersion: x.PolicyVersion, RequestID: uuid.NewString(), Operations: []Operation{bad}}); e == nil {
		t.Fatal("wrong stage mutated node")
	}
	// Old grants remain unable to read, add or edit Kubernetes after software upgrade.
	raw, _ := json.Marshal([]auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Apply: true, Sections: []string{"pipeline"}, Operations: []string{"job.rename"}}})
	_, _ = f.db.Exec(`UPDATE mcp_tokens SET config_grants_json=? WHERE id=?`, string(raw), f.token)
	if _, e = f.svc.Snapshot(ctx, f.token, SnapshotRequest{ProjectID: f.project, Section: "kubernetes", BeginSnapshot: true}); !errors.Is(e, ErrForbidden) {
		t.Fatal("old grant expanded", e)
	}
}

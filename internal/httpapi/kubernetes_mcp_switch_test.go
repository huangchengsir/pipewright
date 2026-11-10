package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/auth"
	"github.com/huangchengsir/pipewright/internal/configdraft"
	"github.com/huangchengsir/pipewright/internal/kubernetes"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/vault"
	"strings"
	"testing"
)

func TestKubernetesMCPSameNodeModeSwitchBrowserRoundTrip(t *testing.T) {
	f, drafts, _, sess, token := configHTTPFixture(t)
	ctx := context.Background()
	key := [32]byte{19, 23}
	k := kubernetes.New(f.db, vault.New(f.db, &key))
	pipelines := pipeline.New(f.db, pipeline.WithReleaseConfigProtector(k))
	admin := New(testWebFSAuth(), configSessionAuth{store: auth.NewSessionStore(f.db)}, WithPipelines(pipelines), WithConfigManagement(f.db, drafts, true))
	var raw string
	if e := f.db.QueryRow(`SELECT spec_json FROM pipeline_configs WHERE project_id=?`, f.project).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var original map[string]any
	if json.Unmarshal([]byte(raw), &original) != nil {
		t.Fatal("fixture spec invalid")
	}
	original["stages"] = append(original["stages"].([]any), map[string]any{"id": "deploy", "name": "Deploy", "kind": "deploy", "needs": []string{"test"}, "jobs": []any{}})
	b, _ := json.Marshal(original)
	if _, e := f.db.Exec(`UPDATE pipeline_configs SET spec_json=? WHERE project_id=?`, string(b), f.project); e != nil {
		t.Fatal(e)
	}
	legacy, old, e := f.tokens.Create(ctx, auth.MCPTokenInput{Name: "legacy", ProjectIDs: []string{f.project}, ConfigGrants: []auth.MCPConfigGrant{{ProjectID: f.project, Read: true, Propose: true, Apply: true, Sections: []string{"pipeline"}, Operations: []string{"job.rename"}}}})
	if e != nil {
		t.Fatal(e)
	}
	if len(old.ConfigGrants[0].StageIDs) != 0 || len(old.ConfigGrants[0].JobIDs) != 0 {
		t.Fatal("old selectors broadened")
	}
	expectMCPError(t, f.call(t, legacy, "project_config_get", map[string]any{"projectId": f.project, "section": "kubernetes", "beginSnapshot": true}), "permission_denied")
	privateValues := []string{"mode-private-fixed", "mode-private-env", "mode-private-values", "mode-private-probe", "mode-human-command"}
	assertMCPPrivate := func(v any) {
		t.Helper()
		body, _ := json.Marshal(v)
		for _, secret := range privateValues {
			if strings.Contains(string(body), secret) {
				t.Fatal("MCP exposed private fixture input")
			}
		}
	}
	configFor := func(mode string, clearing bool) map[string]any {
		c := map[string]any{"version": 2, "mode": mode, "connectionId": "conn", "credentialId": "kcred_fixture", "context": "test", "namespace": "app", "executor": map[string]any{"kind": "local"}, "images": []any{}, "variables": []any{}, "checks": []any{}}
		if clearing {
			for _, field := range []string{"source", "workload", "helm", "progressive", "coreDNS"} {
				c[field] = nil
			}
		}
		switch mode {
		case "manifest":
			c["source"] = map[string]any{"kind": "inline", "content": "apiVersion: v1\nkind: Pod\nmetadata: {name: app}\nspec:\n  containers:\n  - name: app\n    image: registry.invalid/app\n    args: ['mode-human-command $${HOSTNAME}']\n    env: [{name: PRIVATE, value: mode-private-env}]\n"}
			c["variables"] = []any{map[string]any{"name": "PRIVATE", "type": "string", "source": "fixed", "value": "mode-private-fixed"}}
			c["checks"] = []any{map[string]any{"kind": "http", "name": "health", "target": "http://app/healthz", "expected": "mode-private-probe"}}
		case "helm":
			c["source"] = map[string]any{"kind": "git", "path": "chart"}
			c["helm"] = map[string]any{"release": "app", "values": map[string]any{"kind": "inline", "content": "password: mode-private-values\n"}}
		default:
			c["workload"] = map[string]any{"kind": "Deployment", "name": "app"}
			c["images"] = []any{map[string]any{"container": "app", "source": map[string]any{"kind": "external", "reference": "registry.invalid/app@sha256:" + strings.Repeat("a", 64)}}}
			if mode == "progressive" {
				c["progressive"] = map[string]any{"service": "app", "candidate": "app-canary", "trackKey": "track", "trackValue": "canary", "batches": []int{1, 2}}
			}
		}
		return c
	}
	snapshot := func() map[string]any {
		t.Helper()
		out := mcpOutput(t, f.call(t, token, "project_config_get", map[string]any{"projectId": f.project, "section": "kubernetes", "beginSnapshot": true}))
		assertMCPPrivate(out)
		return out
	}
	create := func(c map[string]any, add bool) map[string]any {
		t.Helper()
		snap := snapshot()
		op := map[string]any{"type": "kubernetes.update", "stageId": "deploy", "jobId": "kube", "kubernetes": map[string]any{"config": c}}
		if add {
			op["type"] = "kubernetes.add"
			op["value"] = "Kubernetes"
		}
		req := map[string]any{"projectId": f.project, "baseRevision": snap["baseRevision"], "policyVersion": snap["policyVersion"], "requestId": uuid.NewString(), "operations": []any{op}}
		legacyReq := map[string]any{"projectId": f.project, "baseRevision": snap["baseRevision"], "policyVersion": snap["policyVersion"], "requestId": uuid.NewString(), "operations": []any{map[string]any{"type": "kubernetes.update", "stageId": "deploy", "jobId": "kube", "kubernetes": map[string]any{"config": map[string]any{"timeoutSeconds": 420}}}}}
		if add {
			legacyReq["operations"] = []any{map[string]any{"type": "kubernetes.add", "stageId": "deploy", "jobId": "kube", "value": "No implicit grant", "kubernetes": map[string]any{"config": configFor("manifest", false)}}}
		}
		expectMCPError(t, f.call(t, legacy, "config_draft_create", legacyReq), "permission_denied")
		out := mcpOutput(t, f.call(t, token, "config_draft_create", req))
		assertMCPPrivate(out)
		assertMCPPrivate(mcpOutput(t, f.call(t, token, "config_draft_get", map[string]any{"draftId": out["draftId"]})))
		return out
	}
	apply := func(draft map[string]any, mode string) {
		t.Helper()
		path := "/api/mcp/config-drafts/" + draft["draftId"].(string)
		review := configAdminRequest(admin, sess, "GET", path+"/review", "", "", false)
		var d configdraft.Draft
		if review.Code != 200 || json.Unmarshal(review.Body.Bytes(), &d) != nil || !d.Complete || len(d.KubernetesReview) != 1 {
			t.Fatal("complete human review unavailable", review.Code)
		}
		if mode == "manifest" && !strings.Contains(d.KubernetesReview[0].After, "mode-human-command") {
			t.Fatal("human command review missing")
		}
		for _, secret := range privateValues[:4] {
			if strings.Contains(review.Body.String(), secret) {
				t.Fatal("human review exposed protected scalar")
			}
		}
		body, _ := json.Marshal(configdraft.ApprovalRequest{ReviewDigest: d.ReviewDigest, BaseRevision: d.BaseRevision, PolicyVersion: d.PolicyVersion})
		if w := configAdminRequest(admin, sess, "POST", path+"/approve", string(body), "", false); w.Code != 403 {
			t.Fatal("approval CSRF bypass")
		}
		approved := configAdminRequest(admin, sess, "POST", path+"/approve", string(body), "", true)
		var a configdraft.Approval
		if approved.Code != 200 || json.Unmarshal(approved.Body.Bytes(), &a) != nil || a.ID == "" {
			t.Fatal("browser approval failed", approved.Code)
		}
		req := map[string]any{"draftId": d.DraftID, "approvalId": a.ID, "requestId": uuid.NewString()}
		result := mcpOutput(t, f.call(t, token, "config_apply", req))
		if result["state"] != "applied" {
			t.Fatal("same-node update did not apply", result["state"])
		}
		replay := mcpOutput(t, f.call(t, token, "config_apply", req))
		if replay["operationId"] != result["operationId"] {
			t.Fatal("same-node apply identity lost")
		}
	}
	readBack := func(mode string) (map[string]any, map[string]any) {
		t.Helper()
		response := configAdminRequest(admin, sess, "GET", "/api/projects/"+f.project+"/pipeline", "", "", false)
		var dto pipelineDTO
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &dto) != nil {
			t.Fatal("browser pipeline read failed", response.Code)
		}
		job := dto.Stages[2].Jobs[0]
		if job.ID != "kube" || job.Config["mode"] != mode {
			t.Fatal("node identity/mode changed")
		}
		assertMCPPrivate(job.Config)
		full, e := k.ResolveReleaseConfig(ctx, f.project, "kube", job.Config)
		if e != nil {
			t.Fatal("private roundtrip failed", e)
		}
		parsed, e := pipeline.ParseKubernetesReleaseConfig(full)
		if e != nil || parsed.Mode != mode {
			t.Fatal("resolved mode invalid", e)
		}
		switch mode {
		case "manifest":
			if parsed.Source == nil || parsed.Workload != nil || parsed.Helm != nil || parsed.Progressive != nil {
				t.Fatal("obsolete mode fields retained")
			}
		case "image":
			if parsed.Source != nil || parsed.Workload == nil || parsed.Helm != nil || parsed.Progressive != nil {
				t.Fatal("obsolete mode fields retained")
			}
		case "helm":
			if parsed.Source == nil || parsed.Workload != nil || parsed.Helm == nil || parsed.Progressive != nil {
				t.Fatal("obsolete mode fields retained")
			}
		case "progressive":
			if parsed.Source != nil || parsed.Workload == nil || parsed.Helm != nil || parsed.Progressive == nil {
				t.Fatal("obsolete mode fields retained")
			}
		}
		// The browser editor writes its protected view back unchanged with CAS.
		written := configAdminRequest(admin, sess, "PUT", "/api/projects/"+f.project+"/pipeline", response.Body.String(), response.Header().Get("X-Config-Revision"), true)
		if written.Code != 200 {
			t.Fatal("browser write-only roundtrip failed", written.Code)
		}
		snapshot()
		return job.Config, full
	}
	for index, mode := range []string{"manifest", "image", "helm", "progressive", "manifest", "progressive", "helm", "image", "manifest"} {
		t.Logf("same-node transition %d: %s", index, mode)
		draft := create(configFor(mode, index > 0), index == 0)
		if index == 0 {
			result := mcpOutput(t, f.call(t, token, "config_apply", map[string]any{"draftId": draft["draftId"], "approvalId": uuid.NewString(), "requestId": uuid.NewString()}))
			if result["state"] == "applied" {
				t.Fatal("unapproved mode applied")
			}
		}
		apply(draft, mode)
		_, before := readBack(mode)
		update := create(map[string]any{"timeoutSeconds": 420 + index}, false)
		apply(update, mode)
		_, after := readBack(mode)
		delete(before, "timeoutSeconds")
		delete(after, "timeoutSeconds")
		previous, _ := json.Marshal(before)
		next, _ := json.Marshal(after)
		if string(previous) != string(next) {
			t.Fatal("partial update lost protected mode content")
		}

		if t.Failed() {
			return
		}
	}
	var n int
	if e := f.db.QueryRow(`SELECT COUNT(*) FROM pipeline_runs`).Scan(&n); e != nil || n != 0 {
		t.Fatal("configuration changed execution state")
	}
}

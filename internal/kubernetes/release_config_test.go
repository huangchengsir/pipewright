package kubernetes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func privateConfigFixture() map[string]any {
	return map[string]any{"version": 2, "mode": "manifest", "connectionId": "conn", "credentialId": "kcred_fixture", "context": "test", "namespace": "app", "executor": map[string]any{"kind": "local"}, "source": map[string]any{"kind": "inline", "content": "apiVersion: v1\nkind: Secret\nmetadata: {name: secret}\nstringData:\n  password: private-password\n  remove: delete-me\n"}, "variables": []any{map[string]any{"name": "COUNT", "source": "fixed", "type": "number", "value": 17}}}
}
func TestReleaseConfigWriteOnlyKeepReplaceDeleteAndIdentity(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		safe, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", privateConfigFixture())
		if e != nil {
			t.Fatal(e)
		}
		public, _ := json.Marshal(safe)
		if strings.Contains(string(public), "private-password") || strings.Contains(string(public), "delete-me") {
			t.Fatal("secret returned in public config")
		}
		resolved, e := f.s.ResolveReleaseConfig(ctx, "project-1", "node", safe)
		if e != nil || !strings.Contains(releaseString(releaseMap(resolved["source"])["content"]), "private-password") {
			t.Fatal("private source not recoverable", e)
		}
		if _, e = f.s.ResolveReleaseConfig(ctx, "project-1", "another-node", safe); e == nil {
			t.Fatal("private reference crossed logical node")
		}
		copy, e := cloneReleaseConfig(safe)
		if e != nil {
			t.Fatal(e)
		}
		source := releaseMap(copy["source"])
		source["content"] = strings.ReplaceAll(releaseString(source["content"]), "password:", "moved:")
		if _, e = f.s.ProtectReleaseConfig(ctx, "project-1", "node", copy); e == nil {
			t.Fatal("keep marker moved to another key")
		}
		copy, _ = cloneReleaseConfig(safe)
		source = releaseMap(copy["source"])
		var lines []string
		for _, line := range strings.Split(releaseString(source["content"]), "\n") {
			if strings.Contains(line, "remove:") {
				continue
			}
			if strings.Contains(line, "password:") {
				line = "    password: replacement-password"
			}
			lines = append(lines, line)
		}
		source["content"] = strings.Join(lines, "\n")
		updated, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", copy)
		if e != nil {
			t.Fatal(e)
		}
		resolved, e = f.s.ResolveReleaseConfig(ctx, "project-1", "node", updated)
		if e != nil {
			t.Fatal(e)
		}
		text := releaseString(releaseMap(resolved["source"])["content"])
		if !strings.Contains(text, "replacement-password") || strings.Contains(text, "delete-me") {
			t.Fatal("write-only replace/delete not applied")
		}
		var cipher []byte
		if e = st.DB.QueryRow(`SELECT ciphertext FROM k8s_config_payloads WHERE id=?`, updated["privateRef"]).Scan(&cipher); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(cipher), "replacement-password") {
			t.Fatal("private payload persisted plaintext")
		}
	})
}
func TestPipelineReleaseSecretsNeverEnterSpecJSONOrYAML(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		svc := pipeline.New(st.DB, pipeline.WithReleaseConfigProtector(f.s))
		spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "source", Name: "Source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "Git", Type: "git_source"}}}, {ID: "deploy", Name: "Deploy", Kind: pipeline.KindDeploy, Needs: []string{"source"}, Jobs: []pipeline.Job{{ID: "node", Name: "Kubernetes", Type: pipeline.KubernetesJobType, Config: privateConfigFixture()}}}}}
		saved, e := svc.Save(context.Background(), "project-1", spec)
		if e != nil {
			t.Fatal(e)
		}
		var raw, yaml string
		if e = st.DB.QueryRow(`SELECT spec_json,spec_yaml FROM pipeline_configs WHERE project_id=?`, "project-1").Scan(&raw, &yaml); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(raw, "private-password") || strings.Contains(yaml, "private-password") {
			t.Fatal("pipeline persistence leaked private YAML")
		}
		resolved, e := pipeline.ResolveReleaseConfigs(context.Background(), "project-1", saved.Spec, f.s)
		if e != nil || !strings.Contains(releaseString(releaseMap(resolved.Stages[1].Jobs[0].Config["source"])["content"]), "private-password") {
			t.Fatal("trusted runtime could not resolve frozen payload", e)
		}
	})
}

func TestReleaseConfigScriptsAndEnvironmentUsePrivateRoundTrip(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		ctx := context.Background()
		input := privateConfigFixture()
		releaseMap(input["source"])["content"] = "apiVersion: v1\nkind: Pod\nmetadata: {name: script}\nspec:\n  containers:\n  - name: app\n    image: registry.invalid/app\n    command: [sh, -c]\n    args: ['run-private-script $${HOSTNAME}']\n    env: [{name: TOKEN, value: private-env-value}]\n"
		safe, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", input)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(safe)
		if strings.Contains(string(b), "run-private-script") || strings.Contains(string(b), "private-env-value") {
			t.Fatal("public config contains private script/env")
		}
		saved, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", safe)
		if e != nil {
			t.Fatal("unchanged protected values cannot roundtrip", e)
		}
		resolved, e := f.s.ResolveReleaseConfig(ctx, "project-1", "node", saved)
		if e != nil {
			t.Fatal(e)
		}
		text := releaseString(releaseMap(resolved["source"])["content"])
		if !strings.Contains(text, "run-private-script $${HOSTNAME}") || !strings.Contains(text, "private-env-value") {
			t.Fatal("private source was overwritten by placeholders")
		}
	})
}

func TestReleaseHumanReviewDoesNotUnsealCustomNestedPrivateData(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f := newFixture(t, st)
		cfg := privateConfigFixture()
		releaseMap(cfg["source"])["content"] = "apiVersion: example.invalid/v1\nkind: PrivateData\nmetadata: {name: config}\nspec:\n  data:\n    containers:\n    - args: [private-custom-value]\n"
		safe, e := f.s.ProtectReleaseConfig(context.Background(), "project-1", "node", cfg)
		if e != nil {
			t.Fatal(e)
		}
		human, e := f.s.ReleaseReviewConfig(context.Background(), "project-1", "node", safe["privateRef"].(string))
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(human)
		if strings.Contains(string(b), "private-custom-value") {
			t.Fatal("custom secret data mistaken for executable command")
		}
	})
}

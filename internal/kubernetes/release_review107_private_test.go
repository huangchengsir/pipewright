package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/pipeline"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

const review107Secret = "review107-private-secret-canary"
const review107CopyKey = "kubectl.kubernetes.io/last-applied-configuration"

func review107SecretConfig() map[string]any {
	cfg := privateConfigFixture()
	annotation := `{"apiVersion":"v1","kind":"Secret","stringData":{"password":"` + review107Secret + `"},"data":{"copied":"` + base64.StdEncoding.EncodeToString([]byte(review107Secret)) + `"}}`
	releaseMap(cfg["source"])["content"] = "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db-auth\n  annotations:\n    " + review107CopyKey + ": '" + annotation + "'\nstringData:\n  password: " + review107Secret + "\n"
	return cfg
}
func TestReview107SecretCopyPipelinePersistenceAndReview(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		svc := pipeline.New(st.DB, pipeline.WithReleaseConfigProtector(f.s))
		cfg := review107SecretConfig()
		spec := pipeline.Spec{Stages: []pipeline.Stage{{ID: "source", Name: "Source", Kind: pipeline.KindSource, Jobs: []pipeline.Job{{ID: "git", Name: "Git", Type: "git_source"}}}, {ID: "deploy", Name: "Deploy", Kind: pipeline.KindDeploy, Needs: []string{"source"}, Jobs: []pipeline.Job{{ID: "node", Name: "Kubernetes", Type: pipeline.KubernetesJobType, Config: cfg}}}}}
		saved, e := svc.Save(ctx, "project-1", spec)
		if e != nil {
			t.Fatal(e)
		}
		got, e := svc.Get(ctx, "project-1")
		if e != nil {
			t.Fatal(e)
		}
		var raw, exported string
		if e = st.DB.QueryRow("SELECT spec_json,spec_yaml FROM pipeline_configs WHERE project_id='project-1'").Scan(&raw, &exported); e != nil {
			t.Fatal(e)
		}
		human, e := f.s.ReleaseReviewConfig(ctx, "project-1", "node", got.Spec.Stages[1].Jobs[0].Config["privateRef"].(string))
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range []any{saved.Spec, got.Spec, raw, exported, human} {
			blob, _ := json.Marshal(v)
			if strings.Contains(string(blob), review107Secret) || strings.Contains(string(blob), base64.StdEncoding.EncodeToString([]byte(review107Secret))) {
				t.Fatal("classified Secret copy escaped into ordinary persistence or review")
			}
		}
		resolved, e := pipeline.ResolveReleaseConfigs(ctx, "project-1", got.Spec, f.s)
		if e != nil {
			t.Fatal(e)
		}
		files, _ := InlineReleaseFile(releaseString(releaseMap(resolved.Stages[1].Jobs[0].Config["source"])["content"]))
		frozen, e := FreezeReleaseManifest(files, nil, releaseTestDiscovery(t), "app")
		if e != nil {
			t.Fatal(e)
		}
		annotation := releaseString(releaseMap(releaseMap(frozen.Resources[0].Object["metadata"])["annotations"])[review107CopyKey])
		if !strings.Contains(annotation, review107Secret) || releaseMap(frozen.Resources[0].Object["data"])["password"] != base64.StdEncoding.EncodeToString([]byte(review107Secret)) {
			t.Fatal("runtime source or copied annotation content lost")
		}
	})
}

func review107YAML(t *testing.T, cfg map[string]any) (*yaml.Node, *yaml.Node) {
	t.Helper()
	var doc yaml.Node
	if e := yaml.Unmarshal([]byte(releaseString(releaseMap(cfg["source"])["content"])), &doc); e != nil {
		t.Fatal(e)
	}
	value := releaseSecretAppliedValue(doc.Content[0])
	if value == nil {
		t.Fatal("fixture copied annotation missing")
	}
	return &doc, value
}
func review107Encode(t *testing.T, cfg map[string]any, doc *yaml.Node) {
	t.Helper()
	var out bytes.Buffer
	e := yaml.NewEncoder(&out)
	if err := e.Encode(doc); err != nil {
		t.Fatal(err)
	}
	e.Close()
	releaseMap(cfg["source"])["content"] = out.String()
}
func TestReview107SecretCopyKeepReplaceDeleteAndLegacyResave(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f := newFixture(t, st)
		input := review107SecretConfig()
		_, original := review107YAML(t, input)
		originalText := original.Value
		safe, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", input)
		if e != nil {
			t.Fatal(e)
		}
		for _, change := range []string{"keep", "replace", "delete", "field", "structural-field-collision", "node", "project"} {
			t.Run(change, func(t *testing.T) {
				cfg, _ := cloneReleaseConfig(safe)
				doc, value := review107YAML(t, cfg)
				project, node := "project-1", "node"
				switch change {
				case "replace":
					value.Value = "replacement-private-annotation"
				case "delete":
					ann := releaseYAMLField(releaseYAMLField(doc.Content[0], "metadata"), "annotations")
					ann.Content = nil
				case "field":
					ann := releaseYAMLField(releaseYAMLField(doc.Content[0], "metadata"), "annotations")
					ann.Content[0].Value = "other-field"
				case "structural-field-collision":
					ann := releaseYAMLField(releaseYAMLField(doc.Content[0], "metadata"), "annotations")
					token := *value
					ann.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "kubectl.kubernetes.io"}, {Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "last-applied-configuration"}, &token}}}
				case "node":
					node = "another-node"
				case "project":
					project = "another-project"
				}
				review107Encode(t, cfg, doc)
				updated, err := f.s.ProtectReleaseConfig(ctx, project, node, cfg)
				if change == "field" || change == "structural-field-collision" || change == "node" || change == "project" {
					if err == nil {
						t.Fatal("private annotation crossed owner/path", change)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := f.s.ResolveReleaseConfig(ctx, project, node, updated)
				if err != nil {
					t.Fatal(err)
				}
				var full yaml.Node
				yaml.Unmarshal([]byte(releaseString(releaseMap(resolved["source"])["content"])), &full)
				actual := releaseSecretAppliedValue(full.Content[0])
				switch change {
				case "keep":
					if actual == nil || actual.Value != originalText {
						t.Fatal("opaque annotation changed")
					}
				case "replace":
					if actual == nil || actual.Value != "replacement-private-annotation" {
						t.Fatal("explicit replacement lost")
					}
				case "delete":
					if actual != nil {
						t.Fatal("explicit deletion lost")
					}
				}
			})
		}
		// Model an unreleased106 payload: full runtime bytes encrypted, but annotation
		// was mistakenly copied into the protected public clone and omitted from Keeps.
		id := safe["privateRef"].(string)
		legacy, e := f.s.privateReleaseConfig(ctx, st.DB, id, "project-1", "node")
		if e != nil {
			t.Fatal(e)
		}
		doc, value := review107YAML(t, legacy.Public)
		delete(legacy.Keeps, value.Value)
		value.Value = originalText
		review107Encode(t, legacy.Public, doc)
		raw, _ := json.Marshal(legacy)
		cipher, e := f.s.seal(releaseConfigPurpose, id, 1, raw)
		clear(raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = st.DB.Exec("UPDATE k8s_config_payloads SET ciphertext=? WHERE id=?", cipher, id); e != nil {
			t.Fatal(e)
		}
		human, e := f.s.ReleaseReviewConfig(ctx, "project-1", "node", id)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(human)
		if strings.Contains(string(b), review107Secret) {
			t.Fatal("retained legacy human review leaked newly classified copy")
		}
		upgraded, e := f.s.ProtectReleaseConfig(ctx, "project-1", "node", legacy.Public)
		if e != nil {
			t.Fatal(e)
		}
		b, _ = json.Marshal(upgraded)
		if strings.Contains(string(b), review107Secret) || upgraded["privateRef"] == id {
			t.Fatal("unchanged old public fastpath bypassed reclassification")
		}
		resolved, e := f.s.ResolveReleaseConfig(ctx, "project-1", "node", upgraded)
		if e != nil {
			t.Fatal(e)
		}
		_, kept := review107YAML(t, resolved)
		if kept.Value != originalText {
			t.Fatal("legacy upgrade dropped original annotation")
		}
		var after []byte
		if e = st.DB.QueryRow("SELECT ciphertext FROM k8s_config_payloads WHERE id=?", id).Scan(&after); e != nil || !bytes.Equal(cipher, after) {
			t.Fatal("old encrypted payload/hash was rewritten", e)
		}
	})
}

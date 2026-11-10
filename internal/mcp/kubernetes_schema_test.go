package mcp

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"testing"
)

func TestKubernetesOptionalPointerSchemaClearsAndRequiredObjectsStayTyped(t *testing.T) {
	raw, err := json.Marshal(kubernetesConfigSchema())
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err = json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"workload", "source", "helm", "progressive", "coreDNS", "observeSeconds"} {
		if err := resolved.Validate(map[string]any{field: nil}); err != nil {
			t.Errorf("optional pointer %s cannot be explicitly cleared", field)
		}
	}
	for _, input := range []map[string]any{
		{"source": map[string]any{"kind": "inline", "content": "apiVersion: v1", "artifact": nil}},
		{"helm": map[string]any{"release": "app", "values": nil}},
		{"images": []any{map[string]any{"container": "app", "source": map[string]any{"kind": "external", "reference": "registry.invalid/app:one", "artifact": nil}}}},
		{"checks": []any{map[string]any{"kind": "http", "name": "health", "target": "http://app", "resource": nil}}},
	} {
		if err := resolved.Validate(input); err != nil {
			t.Error("optional nested pointer rejected")
		}
	}
	for _, field := range []string{"version", "mode", "connectionId", "credentialId", "context", "namespace", "executor", "timeoutSeconds", "images", "variables", "checks"} {
		if err := resolved.Validate(map[string]any{field: nil}); err == nil {
			t.Errorf("nonnullable field %s accepted null", field)
		}
	}
	for _, input := range []map[string]any{
		{"executor": map[string]any{"kind": nil}},
		{"workload": map[string]any{"kind": "Deployment", "name": nil}},
		{"source": map[string]any{"kind": nil}},
		{"helm": map[string]any{"release": nil}},
		{"images": []any{map[string]any{"container": "app", "source": nil}}},
		{"progressive": map[string]any{"service": nil}},
	} {
		if err := resolved.Validate(input); err == nil {
			t.Error("required object/scalar accepted null")
		}
	}
}

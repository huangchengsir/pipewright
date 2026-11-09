package kubeprotocol

import (
	"encoding/json"
	"testing"
)

func TestBindTypedListRejectsAmbiguousOrConflictingIdentity(t *testing.T) {
	for _, body := range []string{
		`{"items":[]}`, `{"apiVersion":"v1","items":[]}`, `{"apiVersion":"v1","kind":"PodList","Kind":"SecretList","items":[]}`,
		`{"apiVersion":"v1","kind":"PodList","items":[{"kind":null}]}`, `{"apiVersion":"v1","kind":"PodList","items":[{"kind":{}}]}`,
		`{"apiVersion":"v1","kind":"PodList","items":[{"ApiVersion":"foreign/v1"}]}`, `{"apiVersion":"v1","kind":"PodList","items":{}}`,
		`{"apiVersion":"v1","kind":"PodList","items":[null]}`, `{"apiVersion":"v1","kind":"PodList"}`,
	} {
		var list map[string]any
		if json.Unmarshal([]byte(body), &list) != nil {
			t.Fatal("invalid test")
		}
		if BindTypedList(list, "v1", "Pod") == nil {
			t.Fatal("invalid typed collection accepted")
		}
	}
}
func TestBindTypedListUsesAuthorizedCollectionGVKOnly(t *testing.T) {
	for _, tc := range []struct{ version, kind string }{{"v1", "Pod"}, {"apps/v1", "ReplicaSet"}, {"discovery.k8s.io/v1", "EndpointSlice"}, {"batch/v1", "Job"}, {"autoscaling/v2", "HorizontalPodAutoscaler"}, {"v1", "Secret"}} {
		t.Run(tc.kind, func(t *testing.T) {
			item := map[string]any{"metadata": map[string]any{"name": "owned", "namespace": "app", "uid": "stable"}}
			list := map[string]any{"apiVersion": tc.version, "kind": tc.kind + "List", "items": []any{item}}
			if e := BindTypedList(list, tc.version, tc.kind); e != nil || item["apiVersion"] != tc.version || item["kind"] != tc.kind {
				t.Fatal("bound raw TypeMeta missing")
			}
			if item["metadata"].(map[string]any)["uid"] != "stable" {
				t.Fatal("identity metadata changed")
			}
		})
	}
}

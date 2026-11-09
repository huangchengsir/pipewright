package kubeagent

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRawTypedRelatedListsRecoverOnlyBoundGVKAndOwnedChain(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app", Actions: []string{"get", "related-replicasets", "related-pods"}}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/app/deployments/app":
			w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app","uid":"root"}}`))
		case "/apis/apps/v1/namespaces/app/replicasets":
			w.Write([]byte(`{"apiVersion":"apps/v1","kind":"ReplicaSetList","items":[{"metadata":{"name":"owned-rs","namespace":"app","uid":"rs","ownerReferences":[{"uid":"root","controller":true}]}},{"metadata":{"name":"foreign-rs","namespace":"app","uid":"foreign","ownerReferences":[{"uid":"elsewhere","controller":true}]}}]}`))
		case "/api/v1/namespaces/app/pods":
			w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"owned-pod","namespace":"app","uid":"pod","ownerReferences":[{"uid":"rs","controller":true}]}},{"metadata":{"name":"foreign-pod","namespace":"app","uid":"other","ownerReferences":[{"uid":"foreign","controller":true}]}}]}`))
		default:
			t.Error("unexpected API path")
		}
	}), []kubeprotocol.Resource{scope})
	for _, item := range []struct{ path, kind, version, name string }{{resourcePath(scope, false), "Deployment", "apps/v1", "app"}, {"/apis/apps/v1/namespaces/app/replicasets", "ReplicaSet", "apps/v1", "owned-rs"}, {"/api/v1/namespaces/app/pods", "Pod", "v1", "owned-pod"}} {
		r, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: item.path})
		if e != nil {
			t.Fatal(e)
		}
		if item.kind == "Deployment" {
			continue
		}
		var list struct {
			Items []map[string]any `json:"items"`
		}
		json.Unmarshal(r.Body, &list)
		if len(list.Items) != 1 || list.Items[0]["kind"] != item.kind || list.Items[0]["apiVersion"] != item.version || stringValue(mapValue(list.Items[0]["metadata"])["name"]) != item.name {
			t.Fatal("real raw list shape lost authorized owner chain")
		}
	}
}
func TestRawTypedListRejectsExplicitGVKConflictsIncludingLaterPages(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespace: "app", Name: "app", Actions: []string{"get", "related-pods"}}
	for _, tc := range []struct {
		name, body string
		second     bool
	}{
		{"wrong_list_kind", `{"apiVersion":"v1","kind":"SecretList","items":[]}`, false},
		{"wrong_list_version", `{"apiVersion":"foreign.io/v1","kind":"PodList","items":[]}`, false},
		{"wrong_item_kind", `{"apiVersion":"v1","kind":"PodList","items":[{"apiVersion":"v1","kind":"Secret","metadata":{"name":"owned","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}}]}`, false},
		{"wrong_item_version", `{"apiVersion":"v1","kind":"PodList","items":[{"apiVersion":"foreign.io/v1","kind":"Pod","metadata":{"name":"owned","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}}]}`, false},
		{"later_page_kind", `{"apiVersion":"v1","kind":"SecretList","items":[{"metadata":{"name":"owned","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}}]}`, true},
		{"later_page_version", `{"apiVersion":"foreign.io/v1","kind":"PodList","items":[{"metadata":{"name":"owned","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.second && r.URL.Query().Get("continue") == "" {
					w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"continue":"issued"},"items":[]}`))
					return
				}
				w.Write([]byte(tc.body))
			}), []kubeprotocol.Resource{scope})
			a.cache[resourcePath(scope, false)] = map[string]any{"metadata": map[string]any{"uid": "root"}}
			if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/pods"}); e == nil {
				t.Fatal("explicit list/item GVK conflict accepted")
			}
			if tc.second && calls.Load() != 2 {
				t.Fatal("later page was not checked")
			}
		})
	}
}
func TestRawTypedPaginationDoesNotLoseOwnedItemsOrExpandNamespace(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespace: "app", Name: "app", Actions: []string{"get", "related-pods"}}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("continue") == "" {
			w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"continue":"issued"},"items":[{"metadata":{"name":"foreign","namespace":"app","ownerReferences":[{"uid":"foreign","controller":true}]}}]}`))
			return
		}
		w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"mine","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}},{"metadata":{"name":"outside","namespace":"other","ownerReferences":[{"uid":"root","controller":true}]}}]}`))
	}), []kubeprotocol.Resource{scope})
	a.cache[resourcePath(scope, false)] = map[string]any{"metadata": map[string]any{"uid": "root"}}
	r, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/pods", Query: "limit=200"})
	if e != nil || !strings.Contains(string(r.Body), "mine") || strings.Contains(string(r.Body), "outside") || strings.Contains(string(r.Body), "foreign") {
		t.Fatal("real raw pagination lost namespace/owner fence")
	}
}

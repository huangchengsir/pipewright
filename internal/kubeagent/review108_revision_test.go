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

func TestReview108ExplicitDaemonRevisionGateway(t *testing.T) {
	for _, grant := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_grant", true: "explicit"}[grant], func(t *testing.T) {
			scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet", Namespace: "app", Name: "agent", Actions: []string{"get", "related-pods"}}
			if grant {
				scope.Actions = append(scope.Actions, "related-controllerrevisions")
			}
			var calls atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == resourcePath(scope, false) {
					w.Write([]byte(`{"apiVersion":"apps/v1","kind":"DaemonSet","metadata":{"name":"agent","namespace":"app","uid":"root"}}`))
					return
				}
				if r.URL.Path != "/apis/apps/v1/namespaces/app/controllerrevisions" {
					t.Error("unapproved upstream request")
					w.WriteHeader(500)
					return
				}
				if r.URL.Query().Get("continue") == "" {
					w.Write([]byte(`{"apiVersion":"apps/v1","kind":"ControllerRevisionList","metadata":{"continue":"next"},"items":[{"metadata":{"name":"foreign","namespace":"app","ownerReferences":[{"apiVersion":"apps/v1","kind":"DaemonSet","name":"agent","uid":"other","controller":true}]},"data":{"private":"foreign-data"}}]}`))
					return
				}
				w.Write([]byte(`{"apiVersion":"apps/v1","kind":"ControllerRevisionList","items":[{"metadata":{"name":"agent-hash","namespace":"app","uid":"revision","ownerReferences":[{"apiVersion":"apps/v1","kind":"DaemonSet","name":"agent","uid":"root","controller":true}]},"data":{"spec":{"template":{"metadata":{"labels":{"app":"agent"}},"spec":{"containers":[{"name":"agent","image":"agent:v1"}]},"$patch":"replace"}}}},{"metadata":{"name":"forged-kind","namespace":"app","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"agent","uid":"root","controller":true}]},"data":{"private":"forged-data"}},{"metadata":{"name":"outside","namespace":"other","ownerReferences":[{"apiVersion":"apps/v1","kind":"DaemonSet","name":"agent","uid":"root","controller":true}]},"data":{"private":"outside-data"}}]}`))
			}), []kubeprotocol.Resource{scope})
			ctx := context.Background()
			if _, e := a.Do(ctx, kubeprotocol.Request{Method: "GET", Path: resourcePath(scope, false)}); e != nil {
				t.Fatal(e)
			}
			result, e := a.Do(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/apps/v1/namespaces/app/controllerrevisions", Query: "limit=200"})
			if !grant {
				if e == nil || calls.Load() != 1 {
					t.Fatal("old grant expanded")
				}
				return
			}
			if e != nil {
				t.Fatal("explicit revision read denied", e)
			}
			var list map[string]any
			json.Unmarshal(result.Body, &list)
			if len(listValue(list["items"])) != 1 || !strings.Contains(string(result.Body), "agent-hash") || strings.Contains(string(result.Body), "foreign-data") || strings.Contains(string(result.Body), "forged-data") || strings.Contains(string(result.Body), "outside-data") {
				t.Fatal("revision owner/GVK/namespace filter failed")
			}
			before := calls.Load()
			for _, req := range []kubeprotocol.Request{{Method: "GET", Path: "/api/v1/nodes"}, {Method: "GET", Path: "/apis/apps/v1/namespaces/other/controllerrevisions"}, {Method: "GET", Path: "/apis/apps/v1/namespaces/app/controllerrevisions/agent-hash"}, {Method: "POST", Path: "/apis/apps/v1/namespaces/app/controllerrevisions", Body: []byte(`{}`)}, {Method: "PATCH", Path: "/apis/apps/v1/namespaces/app/controllerrevisions/agent-hash", Body: []byte(`{}`)}} {
				if _, e := a.Do(ctx, req); e == nil {
					t.Fatal("related read expanded")
				}
			}
			if calls.Load() != before {
				t.Fatal("denied request reached API")
			}
			if !has(a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "ping"}).Capabilities, "daemonset-controllerrevision-read-v1") {
				t.Fatal("new proof capability absent")
			}
		})
	}
}

func TestReview108RevisionAnchorAndTypeMetaFences(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet", Namespace: "app", Name: "agent", Actions: []string{"get", "related-controllerrevisions"}}
	for _, tc := range []string{"anchor-kind", "anchor-name", "anchor-ns", "anchor-uid", "list-kind", "item-api", "duplicate-owner"} {
		t.Run(tc, func(t *testing.T) {
			anchor := map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "metadata": map[string]any{"name": "agent", "namespace": "app", "uid": "root"}}
			item := map[string]any{"metadata": map[string]any{"name": "rev", "namespace": "app", "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "agent", "uid": "root", "controller": true}}}, "data": map[string]any{"private": "must-not-leave"}}
			list := map[string]any{"apiVersion": "apps/v1", "kind": "ControllerRevisionList", "items": []any{item}}
			switch tc {
			case "anchor-kind":
				anchor["kind"] = "Deployment"
			case "anchor-name":
				mapValue(anchor["metadata"])["name"] = "another"
			case "anchor-ns":
				mapValue(anchor["metadata"])["namespace"] = "other"
			case "anchor-uid":
				mapValue(anchor["metadata"])["uid"] = ""
			case "list-kind":
				list["kind"] = "SecretList"
			case "item-api":
				item["apiVersion"] = "foreign/v1"
			case "duplicate-owner":
				owners := listValue(mapValue(item["metadata"])["ownerReferences"])
				mapValue(item["metadata"])["ownerReferences"] = append(owners, owners[0])
			}
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(list) }), []kubeprotocol.Resource{scope})
			a.cache[resourcePath(scope, false)] = anchor
			result, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/apis/apps/v1/namespaces/app/controllerrevisions"})
			if e == nil && strings.Contains(string(result.Body), "must-not-leave") {
				t.Fatal("invalid revision proof leaked")
			}
		})
	}
}

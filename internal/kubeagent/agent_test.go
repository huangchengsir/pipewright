package kubeagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

func agentFixture(t *testing.T, handler http.Handler, scopes []kubeprotocol.Resource) (*Agent, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	cfg := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "test", "clusters": []any{map[string]any{"name": "cluster", "cluster": map[string]any{"server": server.URL, "certificate-authority-data": base64.StdEncoding.EncodeToString(cert)}}}, "users": []any{map[string]any{"name": "identity", "user": map[string]any{"token": "fixture-private-token"}}}, "contexts": []any{map[string]any{"name": "test", "context": map[string]any{"cluster": "cluster", "user": "identity"}}}}
	body, _ := json.Marshal(cfg)
	agent := New()
	if e := agent.Initialize(kubeprotocol.Init{Kubeconfig: body, Resources: scopes, Deadline: time.Now().Add(time.Minute).UnixMilli()}); e != nil {
		t.Fatal(e)
	}
	return agent, server
}
func configScope() kubeprotocol.Resource {
	return kubeprotocol.Resource{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config", Actions: []string{"get", "create", "patch"}}
}
func TestAgentHasNoUpstreamWriteUntilSingleBoundPermit(t *testing.T) {
	var writes atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-private-token" {
			t.Error("credential not injected privately")
		}
		writes.Add(1)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"u","resourceVersion":"1"}}`))
	}), []kubeprotocol.Resource{configScope()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := a.Do(ctx, kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/configmaps", Body: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`)})
		done <- e
	}()
	var pending *kubeprotocol.PendingWrite
	for pending == nil {
		select {
		case <-ctx.Done():
			t.Fatal("no pending permission request")
		default:
		}
		pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
		if pending == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if writes.Load() != 0 {
		t.Fatal("helper wrote before controller fence")
	}
	bad := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "action", Digest: pending.Digest, Allow: true, Expires: time.Now().Add(time.Hour).UnixMilli()}})
	if bad.Code == "ok" {
		t.Fatal("unbounded permit accepted")
	}
	permit := &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "action", Digest: pending.Digest, Allow: true, Expires: time.Now().Add(5 * time.Second).UnixMilli()}
	if r := a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}); r.Code != "ok" {
		t.Fatal(r.Code)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if writes.Load() != 1 {
		t.Fatal("write replayed", writes.Load())
	}
	if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}).Code == "ok" {
		t.Fatal("permit reused")
	}
}
func TestAgentRejectsSubresourcesForeignScopesAndRedirects(t *testing.T) {
	var requests atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://example.invalid/secret", http.StatusFound)
	}), []kubeprotocol.Resource{configScope()})
	for _, path := range []string{"/api/v1/namespaces/other/configmaps/config", "/api/v1/namespaces/app/pods/p/exec", "/api/v1/namespaces/app/secrets", "https://other.invalid/", "/api/v1/namespaces/app/configmaps/config%2flog"} {
		if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path}); e == nil {
			t.Fatal("foreign request accepted", path)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("denied request touched upstream")
	}
	if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/configmaps/config"}); e == nil {
		t.Fatal("redirect accepted")
	}
	if requests.Load() != 1 {
		t.Fatal("redirect followed")
	}
}
func TestAgentRelatedListProjectionRequiresOwnerAndExactGVR(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app", Actions: []string{"get", "related-pods"}}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{},"items":[{"kind":"Pod","metadata":{"name":"mine","namespace":"app","uid":"p","ownerReferences":[{"uid":"rs","controller":true}]}},{"kind":"Pod","metadata":{"name":"foreign","namespace":"app","uid":"other","ownerReferences":[{"uid":"foreign","controller":true}]},"spec":{"secret":"must-not-cross-gateway"}}]}`))
	}), []kubeprotocol.Resource{scope})
	a.cache[resourcePath(scope, false)] = map[string]any{"metadata": map[string]any{"uid": "root"}}
	a.cache["/apis/apps/v1/namespaces/app/replicasets/rs"] = map[string]any{"kind": "ReplicaSet", "metadata": map[string]any{"uid": "rs", "namespace": "app", "ownerReferences": []any{map[string]any{"uid": "root", "controller": true}}}}
	result, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/pods"})
	if e != nil || strings.Contains(string(result.Body), "must-not-cross-gateway") || !strings.Contains(string(result.Body), "mine") {
		t.Fatal("owner projection failed", e)
	}
	if _, e = a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/apis/foreign.io/v1/namespaces/app/pods"}); e == nil {
		t.Fatal("related read expanded across GVR")
	}
	scope.Actions = []string{"get"}
	a.resources = []kubeprotocol.Resource{scope}
	if _, e = a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/pods"}); e == nil {
		t.Fatal("ordinary target get expanded into Pod list")
	}
}
func TestAgentUploadOrderDigestAndTotalBudget(t *testing.T) {
	a := New()
	body := []byte("private input")
	f := kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: "input", Count: 1, Size: len(body), Digest: privateDigest(body), Data: body}
	if r := a.Handle(context.Background(), f); r.Code != "ok" {
		t.Fatal(r.Code)
	}
	if r := a.Handle(context.Background(), f); r.Code == "ok" {
		t.Fatal("completed upload replay accepted")
	}
	f.StreamID = "wrong"
	f.Digest = strings.Repeat("0", 64)
	if r := a.Handle(context.Background(), f); r.Code == "ok" {
		t.Fatal("digest mismatch accepted")
	}
	f.StreamID = "huge"
	f.Count = 128
	f.Size = kubeprotocol.MaxInputBytes
	f.Digest = "not-yet"
	if r := a.Handle(context.Background(), f); r.Code != "input_limit" {
		t.Fatal("aggregate upload budget ignored", r.Code)
	}
}

func TestAgentPrivatePaginationFindsAuthorizedPodsBeyondFirstPage(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespace: "app", Name: "app", Actions: []string{"get", "related-pods"}}
	var pages atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"continue":"server-issued-token"},"items":[{"kind":"Pod","metadata":{"name":"foreign","namespace":"app","ownerReferences":[{"uid":"foreign","controller":true}]}}]}`))
		} else if r.URL.Query().Get("continue") == "server-issued-token" {
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{},"items":[{"kind":"Pod","metadata":{"name":"current","namespace":"app","ownerReferences":[{"uid":"root","controller":true}]}}]}`))
		} else {
			t.Error("unissued pagination token")
		}
	}), []kubeprotocol.Resource{scope})
	a.cache[resourcePath(scope, false)] = map[string]any{"metadata": map[string]any{"uid": "root"}}
	a.cache["/apis/apps/v1/namespaces/app/replicasets/rs"] = map[string]any{"kind": "ReplicaSet", "metadata": map[string]any{"uid": "rs", "namespace": "app", "ownerReferences": []any{map[string]any{"uid": "root", "controller": true}}}}
	result, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/pods", Query: "limit=200"})
	if e != nil || pages.Load() != 2 || !strings.Contains(string(result.Body), "current") || strings.Contains(string(result.Body), "foreign") {
		t.Fatal("bounded private pagination failed", pages.Load(), e)
	}
}

func TestAgentCreateBindsExactBodyIdentityBeforeDryRunOrPermit(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(201) }), []kubeprotocol.Resource{configScope()})
	for _, query := range []string{"", "dryRun=All"} {
		for _, body := range []string{`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"other","namespace":"app"}}`, `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"config","namespace":"app"}}`, `{"apiVersion":"foreign.io/v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"other"}}`, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","name":"other","namespace":"app"}}`, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","Name":"other","namespace":"app"}}`, `{"apiVersion":"v1","ApiVersion":"foreign.io/v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`} {
			if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/configmaps", Query: query, Body: []byte(body)}); e == nil {
				t.Fatal("create escaped exact resource identity", body, query)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("foreign create reached API before identity check", calls.Load())
	}
	if len(a.PendingIDs()) != 0 {
		t.Fatal("foreign create reached permit queue")
	}
}

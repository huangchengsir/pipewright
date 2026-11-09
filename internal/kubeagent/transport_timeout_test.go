package kubeagent

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStandardClientGoTransportTimeoutReachesOnlyAuthorizedReads(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-private-token" || r.URL.Query().Get("timeout") == "" {
			t.Error("bounded request lost private authorization or transport timeout")
		}
		_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
	}), []kubeprotocol.Resource{configScope()})
	for _, path := range []string{"/version", "/api", "/apis", "/api/v1"} {
		for _, value := range []string{"32s", "500ms", "1m0s"} {
			r, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path, Query: "timeout=" + value})
			if e != nil || r.Status != 200 {
				t.Errorf("standard client-go timeout denied on approved discovery %s", path)
			}
		}
	}
	if calls.Load() != 12 {
		t.Fatal("approved transport reads did not reach upstream exactly once", calls.Load())
	}
	before := calls.Load()
	for _, path := range []string{"/api/v1/namespaces/other/configmaps/config", "/api/v1/namespaces/app/secrets/private", "/api/v1/namespaces/app/pods/p/exec", "/apis/other.invalid/v1"} {
		if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path, Query: "timeout=32s"}); e == nil {
			t.Fatal("transport timeout expanded a path grant", path)
		}
	}
	if calls.Load() != before {
		t.Fatal("denied paths reached upstream")
	}
	a.deadline = time.Now().Add(-time.Second)
	if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/version", Query: "timeout=32s"}); e == nil || calls.Load() != before {
		t.Fatal("transport timeout extended the lease deadline")
	}
}
func TestTransportTimeoutRejectsAmbiguousUnboundedOrUnknownQueriesBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }), []kubeprotocol.Resource{configScope()})
	for _, query := range []string{"timeout=", "timeout=0s", "timeout=-1s", "timeout=1m1s", "timeout=999999999999999999h", "timeout=32", "timeout=32000ms", "timeout=+32s", "timeout=32s&timeout=32s", "timeout=32s&unknown=value", "timeout=32s&force=true", "Timeout=32s", "timeout=%zz", "timeout=" + strings.Repeat("1", 64) + "s"} {
		if _, e := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/version", Query: query}); e == nil {
			t.Error("invalid transport query accepted", query)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unverified query reached upstream", calls.Load())
	}
}
func TestHelmPlanningOverlayIgnoresOnlyValidatedTransportTimeout(t *testing.T) {
	var writes atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/apis" {
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"APIGroupList","groups":[]}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"NotFound"}`))
	}), []kubeprotocol.Resource{{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition", Name: "widgets.example.invalid", Actions: []string{"get", "create"}}, {Group: "example.invalid", Version: "v1", Resource: "widgets", Kind: "Widget", Namespace: "app", Name: "approved", Actions: []string{"get", "create"}}})
	ctx := context.Background()
	catalog, e := a.chartDiscovery(ctx, planningChart(t))
	if e != nil {
		t.Fatal(e)
	}
	a.planningDiscovery = catalog
	for _, path := range []string{"/apis", "/apis/example.invalid", "/apis/example.invalid/v1"} {
		r, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: path, Query: "timeout=32s"})
		if e != nil || r.Status != 200 || !strings.Contains(string(r.Body), "example.invalid") {
			t.Errorf("authorized planning overlay lost standard client-go timeout %s", path)
		}
	}
	actual, e := a.Do(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/example.invalid/v1", Query: "timeout=32s"})
	if e != nil || actual.Status != 404 {
		t.Fatal("planning overlay replaced actual API discovery", e, actual.Status)
	}
	for _, query := range []string{"timeout=32s&fieldSelector=metadata.name%3Dapproved", "fieldSelector=metadata.name%3Dapproved"} {
		r, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis", Query: query})
		if e != nil || r.Status != 200 || strings.Contains(string(r.Body), "example.invalid") {
			t.Fatal("nontransport query received fabricated planning metadata", e)
		}
	}
	for _, query := range []string{"timeout=0s", "timeout=1m1s", "timeout=32s&timeout=32s", "timeout=32s&unknown=value"} {
		if _, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis", Query: query}); e == nil {
			t.Error("unverified transport received planning overlay", query)
		}
	}
	named, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/example.invalid/v1/namespaces/app/widgets/approved", Query: "timeout=32s"})
	if e != nil || named.Status != 404 {
		t.Fatal("planner invented a custom resource", e, named.Status)
	}
	if writes.Load() != 0 || len(a.PendingIDs()) != 0 {
		t.Fatal("transport planning dispatched writes")
	}
}

func TestTransportTimeoutPreservesOriginalPendingDigestAndWriteFence(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("timeout") != "32s" {
			t.Error("wire deadline changed")
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"u","resourceVersion":"1"}}`))
	}), []kubeprotocol.Resource{configScope()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	in := kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/configmaps", Query: "timeout=32s", Body: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"}}`)}
	done := make(chan error, 1)
	go func() { _, e := a.Do(ctx, in); done <- e }()
	var pending *kubeprotocol.PendingWrite
	for pending == nil {
		if ctx.Err() != nil {
			t.Fatal("pending fence missing")
		}
		pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
		if pending == nil {
			time.Sleep(time.Millisecond)
		}
	}
	raw, _ := json.Marshal(in)
	if calls.Load() != 0 || pending.Request.Query != in.Query || pending.Digest != privateDigest(raw) {
		t.Fatal("transport normalization bypassed fence or changed original request digest")
	}
	wrong := kubeprotocol.Permit{RequestID: pending.ID, ActionID: "action", Digest: privateDigest([]byte("other")), Expires: time.Now().Add(time.Second).UnixMilli(), Allow: true}
	if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &wrong}).Code == "ok" {
		t.Fatal("foreign request permit accepted")
	}
	permit := wrong
	permit.Digest = pending.Digest
	if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit}).Code != "ok" {
		t.Fatal("exact permitted write rejected")
	}
	if e := <-done; e != nil || calls.Load() != 1 {
		t.Fatal("bounded transport write result incorrect", e, calls.Load())
	}
	if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &permit}).Code == "ok" {
		t.Fatal("transport permit replayed")
	}
}

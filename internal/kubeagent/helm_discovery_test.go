package kubeagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func planningChart(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for name, text := range map[string]string{"chart/Chart.yaml": "apiVersion: v2\nname: chart\nversion: 1.0.0\n", "chart/crds/widgets.yaml": "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: widgets.example.invalid}\nspec:\n  group: example.invalid\n  scope: Namespaced\n  names: {plural: widgets, singular: widget, kind: Widget}\n  versions:\n  - name: v1\n    served: true\n    storage: true\n    schema: {openAPIV3Schema: {type: object}}\n"} {
		if e := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(text)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e := w.Write([]byte(text)); e != nil {
			t.Fatal(e)
		}
	}
	w.Close()
	z.Close()
	return b.Bytes()
}
func TestHelmPlanningDiscoveryIsGrantedNotFoundAndCLIOnly(t *testing.T) {
	var writes atomic.Int32
	existing := atomic.Bool{}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/apis" {
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`))
			return
		}
		if strings.Contains(r.URL.Path, "customresourcedefinitions/") && existing.Load() {
			_, _ = w.Write([]byte(`{"kind":"CustomResourceDefinition","metadata":{"name":"widgets.example.invalid","uid":"actual"}}`))
			return
		}
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"kind":"Status","reason":"NotFound"}`))
	}), []kubeprotocol.Resource{{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition", Name: "widgets.example.invalid", Actions: []string{"get", "create"}}, {Group: "example.invalid", Version: "v1", Resource: "widgets", Kind: "Widget", Namespace: "app", Name: "approved", Actions: []string{"get", "create"}}})
	ctx := context.Background()
	catalog, e := a.chartDiscovery(ctx, planningChart(t))
	if e != nil || len(catalog["example.invalid/v1"]) != 1 {
		t.Fatal("new granted CRD unavailable to planner", e)
	}
	a.planningDiscovery = catalog
	for _, path := range []string{"/apis", "/apis/example.invalid", "/apis/example.invalid/v1"} {
		result, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: path})
		if e != nil || result.Status != 200 || !strings.Contains(string(result.Body), "example.invalid") {
			t.Fatal("planning metadata missing", path, e, string(result.Body))
		}
	}
	actual, e := a.Do(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/example.invalid/v1"})
	if e != nil || actual.Status != 404 {
		t.Fatal("planning catalog fabricated runtime discovery", e, actual.Status)
	}
	object, e := a.planningRequest(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/example.invalid/v1/namespaces/app/widgets/approved"})
	if e != nil || object.Status != 404 {
		t.Fatal("planner fabricated an object", e, object.Status)
	}
	if writes.Load() != 0 || len(a.PendingIDs()) != 0 {
		t.Fatal("planning issued mutation")
	}
	existing.Store(true)
	catalog, e = a.chartDiscovery(ctx, planningChart(t))
	if e != nil || len(catalog) != 0 {
		t.Fatal("existing CRD schema was upgraded by planner", e)
	}
	a.resources = a.resources[:1]
	existing.Store(false)
	catalog, e = a.chartDiscovery(ctx, planningChart(t))
	if e != nil || len(catalog) != 0 {
		t.Fatal("ungranted custom resource received discovery", e)
	}
	if _, e = json.Marshal(catalog); e != nil {
		t.Fatal(e)
	}
}

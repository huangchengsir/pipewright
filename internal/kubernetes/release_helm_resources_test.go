package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func helmChartFixture(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for name, body := range files {
		if e := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e := w.Write(body); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestHelmCRDArchiveReadsOnlyPackagedDefinitions(t *testing.T) {
	dependency := helmChartFixture(t, map[string][]byte{"sub/Chart.yaml": []byte("apiVersion: v2"), "sub/crds/child.yaml": []byte("child")})
	chart := helmChartFixture(t, map[string][]byte{"Chart.yaml": []byte("apiVersion: v2"), "chart/templates/not-crd.yaml": []byte("template"), "chart/crds/one.yaml": []byte("one"), "chart/charts/sub.tgz": dependency})
	files, e := ReadReleaseChartCRDs(chart)
	if e != nil || len(files) != 2 {
		t.Fatal("CRDs or packaged dependency missing", len(files), e)
	}
	bad := helmChartFixture(t, map[string][]byte{"chart/../../escape": []byte("bad")})
	if _, e = ReadReleaseChartCRDs(bad); e == nil {
		t.Fatal("chart traversal accepted")
	}
}

type helmResourcesAPI struct{ releaseControllerAPI }

func (a *helmResourcesAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/api/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true,"verbs":["get","create","patch"]}]}`), nil
	}
	if path == "/apis/apiextensions.k8s.io/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"apiextensions.k8s.io/v1","resources":[{"name":"customresourcedefinitions","kind":"CustomResourceDefinition","namespaced":false,"verbs":["get","create","patch"]}]}`), nil
	}
	return a.releaseControllerAPI.Read(ctx, path, query)
}
func TestHelmCRDPlanSeparatesNewCreationFromExistingReadOnlyDefinition(t *testing.T) {
	for _, existing := range []bool{false, true} {
		storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
			f, p := releaseStoreFixture(t, st)
			ctx := context.Background()
			id := ReleaseResourceID{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition", Name: "widgets.example.invalid"}
			cmID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
			anchor := ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app"}
			scopes := []ReleaseAccess{{ID: id, Actions: []string{"get", "create"}}, {ID: cmID, Actions: []string{"get", "create", "patch"}}, {ID: anchor, Actions: []string{"get", "create", "update", "patch", "helm-storage"}}}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Enabled: true, Resources: scopes}); e != nil {
				t.Fatal(e)
			}
			request := p.Authorization.Request
			request.Resources = scopes
			auth, e := f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			definition := releaseTestObject(`{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"widgets.example.invalid"},"spec":{"group":"example.invalid","scope":"Namespaced","names":{"kind":"Widget","plural":"widgets"},"versions":[{"name":"v1","served":true,"storage":true,"schema":{"openAPIV3Schema":{"type":"object"}}}]}}`)
			body, _ := json.Marshal(definition)
			chart := helmChartFixture(t, map[string][]byte{"chart/Chart.yaml": []byte("apiVersion: v2\nname: chart\nversion: 1.0.0"), "chart/crds/widget.yaml": body})
			api := &helmResourcesAPI{releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{}}}
			if existing {
				actual := releaseObjectCopy(definition)
				releaseMap(actual["metadata"])["uid"] = "existing-crd"
				releaseMap(actual["metadata"])["resourceVersion"] = "17"
				actual["status"] = map[string]any{"conditions": []any{map[string]any{"type": "NamesAccepted", "status": "True"}, map[string]any{"type": "Established", "status": "True"}}}
				api.objects["/apis/apiextensions.k8s.io/v1/customresourcedefinitions/"+id.Name] = actual
			}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			files, _ := InlineReleaseFile(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"secret":"private"}}`)
			helm := &ReleaseHelmPlan{Name: "app", Namespace: "app", Action: "install", Revision: 1, StorageAnchor: anchor, Release: map[string]any{}}
			r, e := c.prepareManifest(ctx, "helm-crd-run", "node", "owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0, helm, chart, nil)
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Plan.Resources) != 2 || len(r.Plan.Helm.Rendered) != 1 {
				t.Fatal("CRD managed as ordinary Helm manifest")
			}
			resource := r.Plan.Resources[0]
			if !resource.HelmCRD || resource.VerifyOnly != existing {
				t.Fatal("existing CRD upgraded or new CRD skipped")
			}
			for _, scope := range r.Plan.Authorization.Request.Resources {
				if scope.ID == id && existing && (len(scope.Actions) != 1 || scope.Actions[0] != "get") {
					t.Fatal("existing CRD acquired mutation authority")
				}
			}
		})
	}
}

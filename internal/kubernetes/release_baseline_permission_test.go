package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

type baselinePermissionAPI struct {
	*releaseControllerAPI
	reads int
}

func (a *baselinePermissionAPI) Read(ctx context.Context, p, q string) ([]byte, error) {
	if p == "/api/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true}]}`), nil
	}
	if strings.Contains(p, "/configmaps/") {
		a.reads++
		return nil, ErrNotFound
	}
	return a.releaseControllerAPI.Read(ctx, p, q)
}
func TestPrepareManifestAuthorizesAllBaselineReads(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		wrong, missingGet, multi bool
	}{
		{name: "allowed-create"}, {name: "wrong-namespace", wrong: true}, {name: "missing-get", missingGet: true}, {name: "allowed-first-denied-second", wrong: true, multi: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, p := releaseStoreFixture(t, storetest.Open(t))
			id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "first"}
			actions := []string{"get", "create", "patch"}
			if tc.missingGet {
				actions = []string{"create", "patch"}
			}
			scope := ReleaseAccess{ID: id, Actions: actions}
			_, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true})
			if e != nil {
				t.Fatal(e)
			}
			req := p.Authorization.Request
			req.Resources = []ReleaseAccess{scope}
			auth, e := f.s.AuthorizeRelease(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			objects := []map[string]any{}
			namespace := "app"
			if tc.wrong && !tc.multi {
				namespace = "other"
			}
			objects = append(objects, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "first", "namespace": namespace}, "data": map[string]any{"key": "value"}})
			if tc.multi {
				objects = append(objects, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "second", "namespace": "other"}, "data": map[string]any{"key": "value"}})
			}
			var docs []string
			for _, o := range objects {
				b, _ := json.Marshal(o)
				docs = append(docs, string(b))
			}
			files, e := InlineReleaseFile(strings.Join(docs, "\n---\n"))
			if e != nil {
				t.Fatal(e)
			}
			api := &baselinePermissionAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			out, e := c.PrepareManifest(ctx, "baseline-run", "node", "owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
			if tc.wrong || tc.missingGet {
				phase, reason, _ := ReleasePreparationDetails(e)
				if !errors.Is(e, ErrDenied) || phase != "authorization" || reason != "kubernetes_not_authorized" || api.reads != 0 || out.ID != "" {
					t.Fatalf("must reject before any baseline: reads=%d phase=%s reason=%s err=%v", api.reads, phase, reason, e)
				}
			} else if e != nil || out.ID == "" || api.reads == 0 {
				t.Fatalf("valid create rejected reads=%d err=%v", api.reads, e)
			}
		})
	}
}

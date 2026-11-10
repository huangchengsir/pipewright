package kubernetes

import (
	"context"
	"testing"
)

type rawRelatedAPI struct {
	releaseAPI
	objects map[string][]byte
}

func (a rawRelatedAPI) Read(_ context.Context, path, query string) ([]byte, error) {
	raw, ok := a.objects[path]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), raw...), nil
}
func TestRawTypedListHostBindsExactCollectionBeforeOwnerAndTemplateFilter(t *testing.T) {
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app"}
	anchor := []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app","uid":"root"},"spec":{"template":{"metadata":{"labels":{"app":"app"}}}}}`)
	rs := []byte(`{"apiVersion":"apps/v1","kind":"ReplicaSetList","items":[{"metadata":{"name":"owned","namespace":"app","uid":"rs","ownerReferences":[{"uid":"root","controller":true}]},"spec":{"template":{"metadata":{"labels":{"app":"app","pod-template-hash":"current"}}}}}]}`)
	pods := []byte(`{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"owned","namespace":"app","uid":"pod","ownerReferences":[{"uid":"rs","controller":true}]}},{"metadata":{"name":"foreign","namespace":"app","uid":"other","ownerReferences":[{"uid":"foreign","controller":true}]}}]}`)
	api := rawRelatedAPI{objects: map[string][]byte{"/apis/apps/v1/namespaces/app/deployments/app": anchor, "/apis/apps/v1/namespaces/app/replicasets": rs, "/api/v1/namespaces/app/pods": pods}}
	c := NewReleaseController(nil, nil)
	out, e := c.related(context.Background(), api, ReleaseResource{ID: id})
	if e != nil || len(out) != 2 {
		t.Fatal("raw typed lists lost owned ReplicaSet/Pod chain", e)
	}
	for _, body := range []string{`{"apiVersion":"v1","kind":"SecretList","items":[]}`, `{"apiVersion":"foreign.io/v1","kind":"PodList","items":[]}`, `{"apiVersion":"v1","kind":"PodList","items":[{"kind":"Secret","metadata":{"name":"owned","namespace":"app","ownerReferences":[{"uid":"rs","controller":true}]}}]}`} {
		api.objects["/api/v1/namespaces/app/pods"] = []byte(body)
		if _, e := c.related(context.Background(), api, ReleaseResource{ID: id}); e == nil {
			t.Fatal("host accepted conflicting collection/item GVK")
		}
	}
}

func TestRawTypedEndpointSliceHostPreservesServiceUIDFence(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "svc"}
	api := rawRelatedAPI{objects: map[string][]byte{
		"/api/v1/namespaces/app/services/svc":                     []byte(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"svc","namespace":"app","uid":"service-root"}}`),
		"/apis/discovery.k8s.io/v1/namespaces/app/endpointslices": []byte(`{"apiVersion":"discovery.k8s.io/v1","kind":"EndpointSliceList","items":[{"metadata":{"name":"mine","namespace":"app","ownerReferences":[{"uid":"service-root","controller":true}]}},{"metadata":{"name":"foreign","namespace":"app","ownerReferences":[{"uid":"other","controller":true}]}},{"metadata":{"name":"outside","namespace":"other","ownerReferences":[{"uid":"service-root","controller":true}]}}]}`),
	}}
	out, e := NewReleaseController(nil, nil).related(context.Background(), api, ReleaseResource{ID: id})
	if e != nil || len(out) != 1 || out[0]["kind"] != "EndpointSlice" || releaseMap(out[0]["metadata"])["name"] != "mine" {
		t.Fatal("raw EndpointSlice UID fence failed", e)
	}
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type delayedCRDRegistrationAPI struct {
	releaseControllerAPI
	discoveryReads, schemaReads int
}

func (a *delayedCRDRegistrationAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/apis/example.invalid/v1" {
		a.discoveryReads++
		if a.discoveryReads == 1 {
			return nil, ErrNotFound
		}
		return []byte(`{"kind":"APIResourceList","groupVersion":"example.invalid/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["get","create","patch"]}]}`), nil
	}
	if strings.HasPrefix(path, "/openapi/v3/") {
		a.schemaReads++
		if a.schemaReads == 1 {
			return nil, ErrNotFound
		}
		return []byte(`{"components":{"schemas":{"Widget":{"x-kubernetes-group-version-kind":[{"group":"example.invalid","version":"v1","kind":"Widget"}]}}}}`), nil
	}
	return a.releaseControllerAPI.Read(ctx, path, query)
}
func TestCustomPrerequisiteWaitsForDiscoveryAndSchemaWithoutLosingUIDAnchor(t *testing.T) {
	id := ReleaseResourceID{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition", Name: "widgets.example.invalid"}
	actual := releaseTestObject(`{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"widgets.example.invalid","uid":"fixed-crd","resourceVersion":"17"},"spec":{},"status":{"conditions":[{"type":"NamesAccepted","status":"True"},{"type":"Established","status":"True"}]}}`)
	definition := releaseResourceFromObject(id, releaseCanonicalObject(actual), "crd")
	definition.VerifyOnly = true
	definition.ExpectedUID = "fixed-crd"
	resource := ReleaseResource{ID: ReleaseResourceID{Group: "example.invalid", Version: "v1", Resource: "widgets", Kind: "Widget", Namespace: "app", Name: "widget"}, RequiredCRD: &id}
	api := &delayedCRDRegistrationAPI{releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{"/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + id.Name: actual}}}
	c := &ReleaseController{poll: time.Millisecond}
	parent := ReleaseRecord{Plan: ReleasePlan{Resources: []ReleaseResource{definition}, Baseline: []ReleaseResource{releaseResourceFromObject(id, actual, "crd")}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := c.verifyCustomPrerequisites(ctx, api, parent, resource); e != nil {
		t.Fatal(e)
	}
	if api.discoveryReads < 3 || api.schemaReads < 2 {
		t.Fatal("registration race was not exercised")
	}
	clone := releaseObjectCopy(actual)
	releaseMap(clone["metadata"])["uid"] = "replacement"
	api.objects["/apis/apiextensions.k8s.io/v1/customresourcedefinitions/"+id.Name] = clone
	if e := c.verifyCustomPrerequisites(ctx, api, parent, resource); !errors.Is(e, ErrSuperseded) {
		body, _ := json.Marshal(parent)
		t.Fatal("replacement CRD accepted", string(body), e)
	}
}

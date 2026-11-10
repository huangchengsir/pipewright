package kubernetes

import (
	"encoding/json"
	"testing"
)

func TestProposedCRDDoesNotGuessPluralAndRequiresCurrentGeneration(t *testing.T) {
	source := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: people.example.io}
spec:
  group: example.io
  scope: Namespaced
  names: {plural: people, singular: person, kind: Person}
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec: {type: object, properties: {name: {type: string}}}
          status: {type: object, x-kubernetes-preserve-unknown-fields: true}
---
apiVersion: example.io/v1
kind: Person
metadata: {name: user}
spec: {name: test}
`
	files, e := InlineReleaseFile(source)
	if e != nil {
		t.Fatal(e)
	}
	d := NewReleaseDiscovery()
	if e = d.Add([]byte(`{"kind":"APIResourceList","groupVersion":"apiextensions.k8s.io/v1","resources":[{"name":"customresourcedefinitions","kind":"CustomResourceDefinition","namespaced":false}]}`)); e != nil {
		t.Fatal(e)
	}
	if e = d.addProposedCRDs(files, nil, true); e != nil {
		t.Fatal(e)
	}
	manifest, e := FreezeReleaseManifest(files, nil, d, "app")
	if e != nil || len(manifest.Resources) != 2 {
		t.Fatal("CRD-first freeze failed", e)
	}
	custom := manifest.Resources[1]
	if custom.ID.Resource != "people" || custom.RequiredCRD == nil || custom.RequiredCRD.Name != "people.example.io" || custom.Health != "custom" {
		t.Fatal("plural/scope guessed or adapter falsely native", custom.ID, custom.Health)
	}
	custom.Predicates = []ReleasePredicate{{Predicate: "condition:Ready", Expected: "True"}}
	actual := releaseObjectCopy(custom.Object)
	meta := releaseMap(actual["metadata"])
	meta["uid"] = "custom-uid"
	meta["generation"] = 2
	actual["status"] = map[string]any{"observedGeneration": 1, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
	if h := EvaluateReleaseHealth(custom, actual, nil); h.State != "pending" || h.Reason != "custom_generation_not_observed" {
		t.Fatal("stale custom Ready accepted", h)
	}
	releaseMap(actual["status"])["observedGeneration"] = 2
	if h := EvaluateReleaseHealth(custom, actual, nil); h.State != "ready" {
		t.Fatal("current custom condition rejected", h)
	}
	custom.Predicates = nil
	if h := EvaluateReleaseHealth(custom, actual, nil); h.State != "unverified" {
		t.Fatal("unknown custom resource green without predicate", h)
	}
}
func TestJobCompleteRequiresOwnedTemplateAndContainerExitProof(t *testing.T) {
	object := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"task","namespace":"app","uid":"job-uid"},"spec":{"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"work","image":"repo@sha256:abc"}]}}},"status":{"conditions":[{"type":"Complete","status":"True"}]}}`)
	body, _ := json.Marshal(releaseCanonicalObject(object))
	resource := ReleaseResource{ID: ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "task"}, Object: releaseCanonicalObject(object), Digest: releaseByteDigest(body), Health: "workload"}
	if h := EvaluateReleaseHealth(resource, object, nil); h.State != "pending" {
		t.Fatal("Job Complete fabricated pod evidence", h)
	}
	pod := releaseTestObject(`{"kind":"Pod","metadata":{"name":"task-pod","namespace":"app","uid":"pod-uid","ownerReferences":[{"uid":"job-uid","controller":true}]},"spec":{"restartPolicy":"Never","containers":[{"name":"work","image":"repo@sha256:abc"}]},"status":{"phase":"Succeeded","containerStatuses":[{"name":"work","image":"repo@sha256:abc","state":{"terminated":{"exitCode":0}}}]}}`)
	if h := EvaluateReleaseHealth(resource, object, []map[string]any{pod}); h.State != "ready" || len(h.Containers) != 1 || h.Containers[0].Pod != "task-pod" {
		t.Fatal("completed container proof missing", h)
	}
	releaseMap(releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["state"])["terminated"] = map[string]any{"exitCode": 1}
	if h := EvaluateReleaseHealth(resource, object, []map[string]any{pod}); h.State == "ready" {
		t.Fatal("nonzero task container accepted", h)
	}
}

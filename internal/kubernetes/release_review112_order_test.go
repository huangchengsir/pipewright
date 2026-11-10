package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestReview112EnvInsertionOrdering(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "append-control", true: "prepend-valid-env"}[before], func(t *testing.T) {
			f, auth, _, _, live := ownedMutationFixture(t, true)
			object := releaseCanonicalObject(live)
			container := releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(object["spec"])["template"])["spec"])["containers"])[0])
			keep := releaseItems(container["env"])[0]
			fresh := map[string]any{"name": "EARLY", "value": "new"}
			if before {
				container["env"] = []any{fresh, keep}
			} else {
				container["env"] = []any{keep, fresh}
			}
			desired := releaseResourceFromObject(ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "config"}, object, "workload")
			path, _ := resourceAPI(desired.ID).Path("app", "config")
			api := &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{desired}, Baseline: []ReleaseResource{releaseResourceFromObject(desired.ID, live, "workload")}, TimeoutSeconds: 300}
			err := c.dryRunReleaseResources(context.Background(), "blind111-owner", &plan)
			t.Logf("prepend=%t preparationAdmissionMismatch=%t error=%v writes=%d", before, errors.Is(err, errReleaseAdmissionMismatch), err, api.writes)
			if err != nil || api.writes != 0 {
				t.Fatalf("valid ordered declaration was rejected or written during preparation: %v", err)
			}
		})
	}
}

func TestReview112UnknownNamedArrayOrdering(t *testing.T) {
	old := releaseTestObject(`{"spec":{"steps":[{"name":"migrate","command":"migrate"},{"name":"serve","command":"serve"}]}}`)
	want := releaseTestObject(`{"spec":{"steps":[{"name":"serve","command":"serve"},{"name":"migrate","command":"migrate"}]}}`)
	target, _, _, err := releaseThreeWay(old, old, want, false)
	if err != nil {
		t.Fatal(err)
	}
	targetRaw, _ := json.Marshal(target)
	t.Logf("compilerKeptOldOrder=%t compiledMatchesDesired=%t target=%s", stringMustJSON(target) == stringMustJSON(old), releaseApprovedFieldsMatch(ReleaseResourceID{Group: "example.invalid", Version: "v1", Resource: "workflows", Kind: "Workflow"}, want, target), targetRaw)
	if stringMustJSON(target) != stringMustJSON(want) || releaseApprovedFieldsMatch(ReleaseResourceID{Group: "example.invalid", Version: "v1", Resource: "workflows", Kind: "Workflow"}, want, old) {
		t.Fatal("atomic order change omitted or old order accepted")
	}
}

func TestReview112AtomicCRDOrderHealth(t *testing.T) {
	id := ReleaseResourceID{Group: "example.invalid", Version: "v1", Resource: "workflows", Kind: "Workflow", Namespace: "app", Name: "workflow"}
	expected := releaseTestObject(`{"apiVersion":"example.invalid/v1","kind":"Workflow","metadata":{"name":"workflow","namespace":"app"},"spec":{"steps":[{"name":"serve","command":"serve"},{"name":"migrate","command":"migrate"}]}}`)
	live := releaseTestObject(`{"apiVersion":"example.invalid/v1","kind":"Workflow","metadata":{"name":"workflow","namespace":"app","uid":"workflow-uid","generation":2},"spec":{"steps":[{"name":"migrate","command":"migrate"},{"name":"serve","command":"serve"}]},"status":{"observedGeneration":2,"conditions":[{"type":"Ready","status":"True","observedGeneration":2}]}}`)
	resource := releaseResourceFromObject(id, expected, "custom")
	resource.Expected = releaseObjectCopy(expected)
	resource.ExpectedUID = "workflow-uid"
	resource.Predicates = []ReleasePredicate{{Predicate: "condition:Ready", Expected: "True"}}
	health := EvaluateReleaseHealth(resource, live, nil)
	t.Logf("differentAtomicStepsOrder health=%s reason=%s", health.State, health.Reason)
	if health.State == "ready" {
		t.Fatal("atomic declaration order drift falsely ready")
	}
}

func TestReview112NativeServiceDefaultedPorts(t *testing.T) {
	old := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"app"},"spec":{"ports":[{"name":"web","port":80,"targetPort":8080}]}}`)
	live := releaseObjectCopy(old)
	port := releaseMap(releaseItems(releaseMap(live["spec"])["ports"])[0])
	port["protocol"] = "TCP"
	port["nodePort"] = 31000
	next := releaseObjectCopy(old)
	releaseMap(releaseItems(releaseMap(next["spec"])["ports"])[0])["targetPort"] = 9090
	target, _, _, e := releaseThreeWay(old, live, next, false)
	if e != nil {
		t.Fatal("native owned defaulted Service port update rejected", e)
	}
	got := releaseMap(releaseItems(releaseMap(target["spec"])["ports"])[0])
	if releaseInt(got["targetPort"]) != 9090 || got["protocol"] != "TCP" || releaseInt(got["nodePort"]) != 31000 {
		t.Fatal("native default/unowned field lost")
	}
}

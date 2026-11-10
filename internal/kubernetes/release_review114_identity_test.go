package kubernetes

import (
	"encoding/json"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"testing"
)

func TestReview114ExplicitGVKCannotBorrowNativeZeroRules(t *testing.T) {
	id := ReleaseResourceID{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses", Kind: "PriorityClass", Name: "class"}
	want := releaseTestObject(`{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"class","uid":"same"},"value":0,"globalDefault":false,"preemptionPolicy":"Never"}`)
	raw, _ := json.Marshal(want)
	var typed schedulingv1.PriorityClass
	if e := json.Unmarshal(raw, &typed); e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(typed)
	var actual map[string]any
	json.Unmarshal(raw, &actual)
	for _, change := range []string{"control", "id-kind", "id-group", "id-version", "want-kind", "want-version", "actual-kind", "actual-version", "snapshot-kind", "snapshot-version"} {
		t.Run(change, func(t *testing.T) {
			scope := id
			w, a := releaseObjectCopy(want), releaseObjectCopy(actual)
			expected := releaseCanonicalObject(want)
			switch change {
			case "id-kind":
				scope.Kind = "ConfigMap"
			case "id-group":
				scope.Group = "other.invalid"
			case "id-version":
				scope.Version = "v2"
			case "want-kind":
				w["kind"] = "ConfigMap"
			case "want-version":
				w["apiVersion"] = "other.invalid/v1"
			case "actual-kind":
				a["kind"] = "ConfigMap"
			case "actual-version":
				a["apiVersion"] = "other.invalid/v1"
			case "snapshot-kind":
				expected["kind"] = "ConfigMap"
			case "snapshot-version":
				expected["apiVersion"] = "other.invalid/v1"
			}
			resource := releaseResourceFromObject(scope, w, "declaration")
			resource.Expected = expected
			wantExpected := change == "control"
			if releaseResourceExpectedMatches(resource, a) != wantExpected {
				t.Fatal("admitted snapshot used mismatched explicit GVK")
			}
			if change == "snapshot-kind" || change == "snapshot-version" {
				return
			}
			if releaseApprovedFieldsMatch(scope, w, a) != wantExpected {
				t.Fatal("approval used mismatched explicit GVK")
			}
			for _, merge := range []bool{false, true} {
				intent := ReleaseActionIntent{Resource: scope, Verb: "patch"}
				if merge {
					intent.MutationVersion = 1
					intent.ContentType = releaseMergeContentType
				}
				if releaseApprovedActionFieldsMatch(intent, w, a) != wantExpected {
					t.Fatal("action used mismatched explicit GVK")
				}
			}
			if releaseResourceBaselineMatches(resource, w, a) != wantExpected {
				t.Fatal("exact baseline used mismatched explicit GVK")
			}
			resource.Expected = nil
			if releaseExpectedObserved(ReleasePlan{Resources: []ReleaseResource{resource}}, scope, a) != wantExpected {
				t.Fatal("historical expectation used mismatched explicit GVK")
			}
		})
	}
}

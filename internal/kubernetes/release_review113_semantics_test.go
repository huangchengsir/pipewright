package kubernetes

import (
	"encoding/json"
	corev1 "k8s.io/api/core/v1"
	"math"
	"testing"
)

func TestReview113ScalarJSONTypesAndPrecision(t *testing.T) {
	for _, v := range []any{int(7), int8(7), int64(7), uint64(7), float32(7), float64(7), json.Number("7"), json.Number("7.0"), json.Number("7e0")} {
		if !ReleaseObjectMatches(json.Number("7"), v) || !ReleaseObjectMatches(v, json.Number("7")) {
			t.Fatal("valid numeric representation rejected")
		}
	}
	for _, tc := range []struct {
		name         string
		want, actual any
		ok           bool
	}{
		{"string", 7, "7", false}, {"reverse-string", "7", 7, false}, {"bool", 1, true, false}, {"null", 0, nil, false}, {"decimal", json.Number("0.1"), 0.1, true}, {"fractional-precision", json.Number("7.00000000000000000001"), float64(7), false},
		{"max-int", int64(math.MaxInt64), json.Number("9223372036854775807"), true}, {"max-uint", uint64(math.MaxUint64), json.Number("18446744073709551615"), true}, {"exact-large", json.Number("9007199254740993"), json.Number("9007199254740992"), false}, {"rounded-large", json.Number("9007199254740993"), float64(9007199254740992), false}, {"safe-float", json.Number("9007199254740991"), float64(9007199254740991), true}, {"ambiguous-float", json.Number("9007199254740992"), float64(9007199254740992), false},
		{"negative-zero", json.Number("-0"), 0, true}, {"exponent", json.Number("1e3"), int64(1000), true}, {"huge-exponent", json.Number("1e999999999"), json.Number("1e999999999"), false}, {"nan", math.NaN(), math.NaN(), false}, {"inf", math.Inf(1), math.Inf(1), false}, {"bad-number", json.Number("07"), json.Number("07"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ReleaseObjectMatches(tc.want, tc.actual) != tc.ok || ReleaseObjectMatches(tc.actual, tc.want) != tc.ok {
				t.Fatal("JSON scalar type or precision boundary")
			}
		})
	}
	if ReleaseObjectMatches(map[string]any{"missing": nil}, map[string]any{}) {
		t.Fatal("unknown explicit null was silently absent")
	}
}
func TestReview113NativeZeroIsNotGlobalOmission(t *testing.T) {
	pod := func(spec map[string]any) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Pod", "spec": spec}
	}
	for _, tc := range []struct {
		name         string
		want, actual map[string]any
		ok           bool
	}{
		{"false-missing", pod(map[string]any{"hostNetwork": false}), pod(map[string]any{}), true},
		{"false-true", pod(map[string]any{"hostNetwork": false}), pod(map[string]any{"hostNetwork": true}), false},
		{"false-string", pod(map[string]any{"hostNetwork": false}), pod(map[string]any{"hostNetwork": "false"}), false},
		{"false-null", pod(map[string]any{"hostNetwork": false}), pod(map[string]any{"hostNetwork": nil}), false},
		{"optional-map", pod(map[string]any{"nodeSelector": map[string]any{}}), pod(map[string]any{}), true},
		{"optional-null-array", pod(map[string]any{"imagePullSecrets": []any{}}), pod(map[string]any{"imagePullSecrets": nil}), true},
		{"required-array", pod(map[string]any{"containers": []any{}}), pod(map[string]any{}), false},
		{"pointer-false", pod(map[string]any{"automountServiceAccountToken": false}), pod(map[string]any{}), false},
		{"pointer-zero", pod(map[string]any{"terminationGracePeriodSeconds": 0}), pod(map[string]any{}), false},
		{"unknown-field", pod(map[string]any{"unknown": false}), pod(map[string]any{}), false},
		{"map-entry", pod(map[string]any{"nodeSelector": map[string]any{"label": ""}}), pod(map[string]any{"nodeSelector": map[string]any{}}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := stringMustJSON(tc.want) + stringMustJSON(tc.actual)
			if ReleaseObjectMatches(tc.want, tc.actual) != tc.ok || ReleasePodTemplateMatches(tc.want, tc.actual) != tc.ok {
				t.Fatal("wrong native omission semantics")
			}
			if before != stringMustJSON(tc.want)+stringMustJSON(tc.actual) {
				t.Fatal("comparison mutated objects")
			}
		})
	}
	for _, kind := range []string{"ConfigMap", "Secret"} {
		want := map[string]any{"apiVersion": "v1", "kind": kind, "data": map[string]any{"KEY": ""}}
		actual := map[string]any{"apiVersion": "v1", "kind": kind, "data": map[string]any{}}
		if ReleaseObjectMatches(want, actual) || ReleasePodTemplateMatches(want, actual) {
			t.Fatal("real empty dictionary value dropped", kind)
		}
	}
}
func TestReview113OfficialCustomJSONStructsRemainOpaque(t *testing.T) {
	// IntOrString is a nonpointer struct tagged omitempty, yet JSON emits int0.
	var svc corev1.Service
	json.Unmarshal([]byte(`{"apiVersion":"v1","kind":"Service","spec":{"ports":[{"port":80,"targetPort":0}]}}`), &svc)
	raw, e := json.Marshal(svc)
	if e != nil {
		t.Fatal(e)
	}
	var actual map[string]any
	json.Unmarshal(raw, &actual)
	port := releaseMap(releaseItems(releaseMap(actual["spec"])["ports"])[0])
	if _, ok := port["targetPort"]; !ok {
		t.Fatal("official serialization control")
	}
	want := releaseObjectCopy(actual)
	delete(port, "targetPort")
	if ReleasePodTemplateMatches(want, actual) {
		t.Fatal("IntOrString zero omitted")
	}
	port["targetPort"] = "0"
	if ReleasePodTemplateMatches(want, actual) {
		t.Fatal("int0 became string0")
	}
	pod := releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"creationTimestamp":null},"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"0"}}}]}}`)
	other := releaseObjectCopy(pod)
	delete(releaseMap(other["metadata"]), "creationTimestamp")
	if ReleaseObjectMatches(pod, other) {
		t.Fatal("Time struct interpreted as empty omitted value")
	}
	other = releaseObjectCopy(pod)
	delete(releaseMap(releaseMap(releaseMap(releaseItems(releaseMap(other["spec"])["containers"])[0])["resources"])["requests"]), "cpu")
	if ReleasePodTemplateMatches(pod, other) {
		t.Fatal("Quantity entry interpreted as omitted struct")
	}
}

func TestReview113OnlySealedMergeActionsInterpretTombstones(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	want := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","data":{"removed":null}}`)
	actual := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap"}`)
	in := ReleaseActionIntent{MutationVersion: 1, Verb: "patch", ContentType: releaseMergeContentType, Resource: id}
	if !releaseApprovedActionFieldsMatch(in, want, actual) {
		t.Fatal("verified absent merge deletion rejected")
	}
	actual["data"] = map[string]any{"removed": nil}
	if releaseApprovedActionFieldsMatch(in, want, actual) {
		t.Fatal("actual null treated as deleted")
	}
	delete(actual, "data")
	in.ContentType = ""
	if releaseApprovedActionFieldsMatch(in, want, actual) {
		t.Fatal("full intent null treated as absent")
	}
	in.ContentType = releaseMergeContentType
	want = releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","unknown":[{"removed":null}]}`)
	actual = releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","unknown":[{}]}`)
	if releaseApprovedActionFieldsMatch(in, want, actual) {
		t.Fatal("array replacement null treated as tombstone")
	}
}

func TestReview113NativeSemanticsKeepContextInExactSubobjects(t *testing.T) {
	before := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app","uid":"same"},"spec":{"template":{"spec":{"hostNetwork":false,"containers":[{"name":"app","image":"image:tag","env":[]}]}}}}`)
	before, e := review113NativeSerialize(before)
	if e != nil {
		t.Fatal(e)
	}
	baseSpec := releaseMap(releaseMap(releaseMap(before["spec"])["template"])["spec"])
	baseSpec["hostNetwork"] = false
	releaseMap(releaseItems(baseSpec["containers"])[0])["env"] = []any{}
	actual, e := review113NativeSerialize(before)
	if e != nil {
		t.Fatal(e)
	}
	if !releaseBaselineMatches(before, actual) {
		t.Fatal("exact baseline lost native field context")
	}
	releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["hostNetwork"] = true
	if releaseBaselineMatches(before, actual) {
		t.Fatal("exact baseline ignored nonzero drift")
	}
	id := ReleaseResourceID{Version: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "app", Name: "map"}
	want := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"map","namespace":"app"},"data":{}}`)
	r := releaseResourceFromObject(id, want, "declaration")
	r.Expected = releaseObjectCopy(want)
	r.Mutation = &ReleaseMutation{}
	live := releaseObjectCopy(want)
	releaseMap(live["metadata"])["uid"] = "map"
	delete(live, "data")
	if !releaseResourceExpectedMatches(r, live) {
		t.Fatal("exact empty-data comparison lost native field context")
	}
	live["data"] = map[string]any{"foreign": "x"}
	if releaseResourceExpectedMatches(r, live) {
		t.Fatal("exact empty-data observation ignored foreign content")
	}
}

package kubernetes

import (
	"errors"
	"testing"
)

func review112PodSpec(field string, items []any) map[string]any {
	if field == "env" {
		return map[string]any{"containers": []any{map[string]any{"name": "app", "image": "app:1", "env": items}}}
	}
	return map[string]any{field: items}
}
func review112PodObject(field string, items []any) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "pod", "namespace": "app"}, "spec": review112PodSpec(field, items)}
}
func review112Members(names ...string) []any {
	v := []any{}
	for _, name := range names {
		v = append(v, map[string]any{"name": name, "value": name})
	}
	return v
}
func review112List(object map[string]any, field string) []any {
	spec := releaseMap(object["spec"])
	if field == "env" {
		return releaseItems(releaseMap(releaseItems(spec["containers"])[0])["env"])
	}
	return releaseItems(spec[field])
}
func review112Names(v []any) []string {
	out := []string{}
	for _, x := range v {
		out = append(out, releaseString(releaseMap(x)["name"]))
	}
	return out
}
func TestReview112NativeOrderedOwnership(t *testing.T) {
	for _, field := range []string{"env", "initContainers"} {
		for _, tc := range []struct {
			name                  string
			old, live, next, want []string
			reject                bool
		}{
			{"fully-owned-prepend", []string{"B"}, []string{"B"}, []string{"A", "B"}, []string{"A", "B"}, false},
			{"fully-owned-reverse", []string{"A", "B"}, []string{"A", "B"}, []string{"B", "A"}, []string{"B", "A"}, false},
			{"prefix-injection", []string{"B"}, []string{"U", "B"}, []string{"A", "B"}, []string{"A", "U", "B"}, false},
			{"middle-injection", []string{"A", "B"}, []string{"A", "U", "B"}, []string{"A", "N", "B"}, []string{"A", "N", "U", "B"}, false},
			{"reverse-crosses-injection", []string{"A", "B"}, []string{"A", "U", "B"}, []string{"B", "A"}, nil, true},
			{"reverse-before-suffix", []string{"A", "B"}, []string{"A", "B", "U"}, []string{"B", "A"}, []string{"B", "A", "U"}, false},
			{"delete-owned-anchor", []string{"A", "B"}, []string{"A", "U", "B"}, []string{"B"}, []string{"U", "B"}, false},
			{"observe-unowned", []string{"A", "B"}, []string{"A", "U", "B"}, []string{"A", "U", "B"}, []string{"A", "U", "B"}, false},
			{"unowned-order-request-conflict", []string{"A", "B"}, []string{"A", "U", "V", "B"}, []string{"A", "V", "U", "B"}, nil, true},
			{"owned-live-order-drift", []string{"A", "B"}, []string{"B", "A"}, []string{"A", "B"}, nil, true},
			{"missing-owned", []string{"A", "B"}, []string{"A"}, []string{"A"}, nil, true},
			{"duplicate-live", []string{"A"}, []string{"A", "A"}, []string{"A"}, nil, true},
			{"duplicate-desired", []string{"A"}, []string{"A"}, []string{"A", "A"}, nil, true},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				old, live, next := review112PodObject(field, review112Members(tc.old...)), review112PodObject(field, review112Members(tc.live...)), review112PodObject(field, review112Members(tc.next...))
				before := stringMustJSON(live)
				target, managed, patch, e := releaseThreeWay(old, live, next, false)
				if tc.reject {
					if !errors.Is(e, ErrSuperseded) {
						t.Fatal("ambiguous/foreign ownership accepted", e)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				if !releaseExactJSON(review112Names(review112List(target, field)), tc.want) {
					t.Fatal("desired order or unowned anchors changed")
				}
				if !releaseExactJSON(applyFixtureMerge(live, patch), target) || stringMustJSON(live) != before {
					t.Fatal("RFC7386 result/evidence mismatch")
				}
				for _, v := range review112List(managed, field) {
					n := releaseString(releaseMap(v)["name"])
					if n == "U" || n == "V" {
						t.Fatal("unowned identity became deletion authority")
					}
				}
			})
		}
	}
	for _, field := range []string{"env", "initContainers"} {
		t.Run(field+"/external-value", func(t *testing.T) {
			old := review112PodObject(field, review112Members("A"))
			live := review112PodObject(field, review112Members("A", "U"))
			next := review112PodObject(field, review112Members("A", "U"))
			releaseMap(review112List(next, field)[1])["value"] = "takeover"
			if _, _, _, e := releaseThreeWay(old, live, next, false); !errors.Is(e, ErrSuperseded) {
				t.Fatal("unowned value adopted", e)
			}
		})
	}
}
func TestReview112NativeContextCannotBeInferredFromFieldNames(t *testing.T) {
	for _, field := range []string{"steps", "containers", "initContainers", "env", "ports", "volumes", "tolerations", "imagePullSecrets"} {
		t.Run(field, func(t *testing.T) {
			old := map[string]any{"apiVersion": "custom.invalid/v1", "kind": "Workflow", "metadata": map[string]any{"name": "flow", "namespace": "app"}, "spec": map[string]any{field: review112Members("A", "B")}}
			next := releaseObjectCopy(old)
			releaseMap(next["spec"])[field] = review112Members("B", "A")
			if ReleaseObjectMatches(old, next) || ReleasePodTemplateMatches(old, next) {
				t.Fatal("untrusted named list became unordered")
			}
			target, _, patch, e := releaseThreeWay(old, old, next, false)
			if e != nil || !releaseExactJSON(target, next) || !releaseExactJSON(applyFixtureMerge(old, patch), next) {
				t.Fatal("atomic change omitted", e)
			}
			drift := releaseObjectCopy(old)
			releaseMap(drift["spec"])[field] = review112Members("A", "B", "foreign")
			if _, _, _, e = releaseThreeWay(old, drift, next, false); !errors.Is(e, ErrSuperseded) {
				t.Fatal("external atomic drift overwritten", e)
			}
			partial, _, _, e := releaseThreeWay(old, old, map[string]any{"apiVersion": "custom.invalid/v1", "kind": "Workflow", "spec": map[string]any{field: review112Members("B")}}, true)
			if e != nil || len(releaseItems(releaseMap(partial["spec"])[field])) != 1 {
				t.Fatal("generic partial list silently merged", e)
			}
		})
	}
	cases := []map[string]any{
		releaseTestObject(`{"apiVersion":"custom.invalid/v1","kind":"Pod","spec":{"containers":[{"name":"A"},{"name":"B"}]}}`),
		releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","spec":{"config":{"containers":[{"name":"A"},{"name":"B"}]}}}`),
		releaseTestObject(`{"apiVersion":"v1","kind":"Pod","spec":{"containers":[{"name":"app","custom":{"env":[{"name":"A"},{"name":"B"}]}}]}}`),
		releaseTestObject(`{"apiVersion":"custom.invalid/v1","kind":"Workflow","spec":{"payload":{"apiVersion":"v1","kind":"Pod","spec":{"containers":[{"name":"A"},{"name":"B"}]}}}}`),
	}
	for i, want := range cases {
		actual := releaseObjectCopy(want)
		var arrayParent map[string]any
		key := "containers"
		switch i {
		case 0:
			arrayParent = releaseMap(actual["spec"])
		case 1:
			arrayParent = releaseMap(releaseMap(actual["spec"])["config"])
		case 2:
			arrayParent = releaseMap(releaseMap(releaseItems(releaseMap(actual["spec"])["containers"])[0])["custom"])
			key = "env"
		case 3:
			arrayParent = releaseMap(releaseMap(releaseMap(actual["spec"])["payload"])["spec"])
		}
		items := releaseItems(arrayParent[key])
		arrayParent[key] = []any{items[1], items[0]}
		if ReleasePodTemplateMatches(want, actual) || releaseApprovedFieldsMatch(ReleaseResourceID{Kind: releaseString(want["kind"])}, want, actual) {
			t.Fatal("field-name collision gained native semantics", i)
		}
	}
	native := review112PodObject("initContainers", review112Members("A", "B"))
	actual := releaseObjectCopy(native)
	releaseMap(actual["spec"])["initContainers"] = review112Members("U", "A", "B")
	if !ReleasePodTemplateMatches(native, actual) {
		t.Fatal("rooted native admission context lost")
	}
	generic := releaseMap(native["spec"])
	if ReleasePodTemplateMatches(generic, releaseMap(actual["spec"])) || !releasePodSpecMatches(generic, releaseMap(actual["spec"])) {
		t.Fatal("PodSpec-only authority was inferred instead of explicit")
	}
}

func TestReview112NativePartialOverlayOrder(t *testing.T) {
	for _, tc := range []struct {
		name            string
		old, next, want []string
		reject          bool
	}{
		{"keep-unmentioned-order", []string{"A", "B", "C"}, []string{"B"}, []string{"A", "B", "C"}, false},
		{"prepend-selected", []string{"B"}, []string{"A", "B"}, []string{"A", "B"}, false},
		{"reverse-all", []string{"A", "B"}, []string{"B", "A"}, []string{"B", "A"}, false},
		{"unmentioned-anchor-conflict", []string{"A", "U", "B"}, []string{"B", "A"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := review112PodObject("env", review112Members(tc.old...))
			next := review112PodObject("env", review112Members(tc.next...))
			target, _, _, e := releaseThreeWay(old, old, next, true)
			if tc.reject {
				if !errors.Is(e, ErrSuperseded) {
					t.Fatal("partial ordering conflict ignored", e)
				}
				return
			}
			if e != nil || !releaseExactJSON(review112Names(review112List(target, "env")), tc.want) {
				t.Fatal("partial changed unrelated order or omitted requested order", e)
			}
		})
	}
}

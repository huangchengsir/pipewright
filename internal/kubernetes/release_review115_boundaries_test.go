package kubernetes

import (
	"errors"
	"testing"
)

func review115AtomicObject() map[string]any {
	return releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"atomic","namespace":"app"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"image","envFrom":[{"prefix":"","configMapRef":{"name":"source"}}]}]}}}}`)
}
func review115Container(o map[string]any) map[string]any {
	return releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(o["spec"])["template"])["spec"])["containers"])[0])
}
func TestReview115AtomicNativeContextNeverDropsDrift(t *testing.T) {
	for _, scenario := range []string{"retain", "update", "remove", "unknown-kind", "unknown-field", "pointer-zero", "type-drift", "extra-field", "extra-item", "reversed", "external-nonzero"} {
		t.Run(scenario, func(t *testing.T) {
			old := review115AtomicObject()
			live := releaseObjectCopy(old)
			delete(releaseMap(releaseItems(review115Container(live)["envFrom"])[0]), "prefix")
			next := releaseObjectCopy(old)
			ok := scenario == "retain" || scenario == "update" || scenario == "remove"
			switch scenario {
			case "update":
				releaseMap(releaseItems(review115Container(next)["envFrom"])[0])["prefix"] = "UPDATED_"
			case "remove":
				delete(review115Container(next), "envFrom")
			case "unknown-kind":
				for _, o := range []map[string]any{old, live, next} {
					o["apiVersion"] = "custom.invalid/v1"
					o["kind"] = "Workflow"
				}
			case "unknown-field":
				releaseMap(releaseItems(review115Container(old)["envFrom"])[0])["unknown"] = ""
				releaseMap(releaseItems(review115Container(next)["envFrom"])[0])["unknown"] = ""
			case "pointer-zero":
				releaseMap(releaseMap(releaseItems(review115Container(old)["envFrom"])[0])["configMapRef"])["optional"] = false
				releaseMap(releaseMap(releaseItems(review115Container(next)["envFrom"])[0])["configMapRef"])["optional"] = false
			case "type-drift":
				releaseMap(releaseItems(review115Container(live)["envFrom"])[0])["prefix"] = 0
			case "extra-field":
				releaseMap(releaseItems(review115Container(live)["envFrom"])[0])["unknown"] = "foreign"
			case "extra-item":
				review115Container(live)["envFrom"] = append(releaseItems(review115Container(live)["envFrom"]), map[string]any{"secretRef": map[string]any{"name": "foreign"}})
			case "external-nonzero":
				releaseMap(releaseItems(review115Container(live)["envFrom"])[0])["prefix"] = "EXTERNAL_"
			case "reversed":
				second := map[string]any{"configMapRef": map[string]any{"name": "second"}}
				for _, o := range []map[string]any{old, next} {
					review115Container(o)["envFrom"] = append(releaseItems(review115Container(o)["envFrom"]), second)
				}
				review115Container(live)["envFrom"] = []any{second, releaseItems(review115Container(live)["envFrom"])[0]}
			}
			raw := stringMustJSON(live)
			target, managed, patch, e := releaseThreeWay(old, live, next, false)
			if !ok {
				if !errors.Is(e, ErrSuperseded) {
					t.Fatal("atomic drift/unknown context accepted", e)
				}
				return
			}
			if e != nil || !releaseApprovedFieldsMatch(ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"}, next, target) {
				t.Fatal("native atomic semantic update rejected", e)
			}
			if stringMustJSON(live) != raw || !ReleaseObjectMatches(applyFixtureMerge(live, patch), target) {
				t.Fatal("actual evidence mutated or patch changed intent")
			}
			if scenario == "retain" {
				prefix, present := releaseMap(releaseItems(review115Container(managed)["envFrom"])[0])["prefix"]
				if !present || prefix != "" {
					t.Fatal("empty prefix ownership lost")
				}
			}
		})
	}
}
func TestReview115EqualUnownedAtomicValueIsNotAdopted(t *testing.T) {
	old := review115AtomicObject()
	delete(review115Container(old), "envFrom")
	live := review115AtomicObject()
	delete(releaseMap(releaseItems(review115Container(live)["envFrom"])[0]), "prefix")
	desired := review115AtomicObject()
	target, managed, _, e := releaseThreeWay(old, live, desired, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, owned := review115Container(managed)["envFrom"]; owned {
		t.Fatal("observed external array became deletion authority")
	}
	next := releaseObjectCopy(old)
	after, _, _, e := releaseThreeWay(managed, target, next, false)
	if e != nil || review115Container(after)["envFrom"] == nil {
		t.Fatal("external atomic array removed", e)
	}
}

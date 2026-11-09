package kubernetes

import "context"

// Only PriorityClass's API omitempty boolean has an equivalent absent/false
// representation. Explicit true, wrong types and every other field still compare.
func releasePriorityClassComparison(id ReleaseResourceID, want, actual map[string]any) (map[string]any, map[string]any, bool) {
	if id.Group != "scheduling.k8s.io" || id.Version != "v1" || id.Resource != "priorityclasses" || id.Kind != "PriorityClass" {
		return want, actual, true
	}
	semantic := func(o map[string]any) (bool, bool) {
		v, exists := o["globalDefault"]
		if !exists {
			return false, true
		}
		b, ok := v.(bool)
		return b, ok
	}
	w, wok := semantic(want)
	a, aok := semantic(actual)
	if !wok || !aok || w != a {
		return want, actual, false
	}
	if !w {
		want = releaseObjectCopy(want)
		actual = releaseObjectCopy(actual)
		delete(want, "globalDefault")
		delete(actual, "globalDefault")
	}
	return want, actual, true
}
func releaseApprovedFieldsMatch(id ReleaseResourceID, want, actual map[string]any) bool {
	if !releaseResourceGVKMatches(id, want, actual) {
		return false
	}
	want, actual, valid := releasePriorityClassComparison(id, want, actual)
	return valid && ReleasePodTemplateMatches(want, actual)
}

// A class name/label is not ownership proof. Bind the live UID and action marker
// to this release's verified create receipt before candidate or live DNS writes.
func (c *ReleaseController) verifyCoreDNSPriorityClass(ctx context.Context, api releaseAPI, r ReleaseRecord) error {
	if r.Plan.CoreDNS == nil || r.Plan.CoreDNS.PriorityClass == nil {
		return nil
	}
	id := *r.Plan.CoreDNS.PriorityClass
	var resource *ReleaseResource
	for i := range r.Plan.Resources {
		v := &r.Plan.Resources[i]
		if v.ID == id && v.Ephemeral {
			resource = v
			break
		}
	}
	if resource == nil {
		return ErrSuperseded
	}
	actual, err := readReleaseObject(ctx, api, id)
	if err != nil {
		return err
	}
	meta := releaseMap(actual["metadata"])
	facts, err := c.store.ListReleaseActions(ctx, r.ID)
	if err != nil {
		return err
	}
	for _, fact := range facts {
		if fact.Intent.Resource != id || fact.Intent.ParentID != r.PlanID || fact.Intent.Verb != "create" {
			continue
		}
		if fact.State == "outcome_unknown" {
			return ErrOutcomeUnknown
		}
		if fact.State != "verified" || fact.Receipt == nil || fact.Receipt.UID == "" || meta["uid"] != fact.Receipt.UID || releaseMap(meta["annotations"])[ReleaseActionAnnotation] != fact.ID {
			continue
		}
		frozen := *resource
		frozen.ExpectedUID = fact.Receipt.UID
		if EvaluateReleaseHealth(frozen, actual, nil).State != "declared" {
			return ErrSuperseded
		}
		return nil
	}
	return ErrSuperseded
}

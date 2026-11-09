package kubernetes

import (
	"errors"
	"testing"
)

func progressiveReceiptProofFixture() (ReleaseRecord, ReleaseResource, ReleaseAction) {
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "candidate"}
	origin := ReleaseResource{ID: id, Object: map[string]any{}, Expected: map[string]any{}}
	mutation := &ReleaseMutation{Strategy: releasePriorCreateMerge, OriginStepKey: "batch-0-resource-0"}
	resource := ReleaseResource{ID: id, Mutation: mutation}
	parent := ReleaseRecord{ID: "krel-current", PlanID: "sealed-plan", Plan: ReleasePlan{MutationVersion: 1, Mode: "progressive", Progressive: &ReleaseProgressivePlan{Candidate: id}, Batches: [][]ReleaseResource{{origin}, {resource}}}}
	fact := ReleaseAction{ID: "kact-create", ReleaseID: parent.ID, State: "verified", Intent: ReleaseActionIntent{MutationVersion: 1, ParentID: parent.PlanID, StepKey: mutation.OriginStepKey, Resource: id, Verb: "create"}, Receipt: &ReleaseActionReceipt{Resource: id, UID: "uid", Object: map[string]any{"metadata": map[string]any{"name": "candidate", "namespace": "app", "uid": "uid", "annotations": map[string]any{ReleaseActionAnnotation: "kact-create"}}}}}
	return parent, resource, fact
}
func TestProgressiveRecipeCannotBorrowAnotherReleasePlanResourceOrUnknownReceipt(t *testing.T) {
	p, r, f := progressiveReceiptProofFixture()
	if _, e := releasePriorCreateFact(p, r, []ReleaseAction{f}, "uid"); e != nil {
		t.Fatal(e)
	}
	for _, scenario := range []string{"release", "plan", "resource", "version", "verb", "unknown", "uid", "marker", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			parent, resource, fact := progressiveReceiptProofFixture()
			uid := "uid"
			facts := []ReleaseAction{fact}
			want := ErrDenied
			switch scenario {
			case "release":
				facts[0].ReleaseID = "krel-other"
			case "plan":
				facts[0].Intent.ParentID = "other-plan"
			case "resource":
				facts[0].Intent.Resource.Name = "other"
			case "version":
				facts[0].Intent.MutationVersion = 0
			case "verb":
				facts[0].Intent.Verb = "patch"
			case "unknown":
				facts[0].State = "outcome_unknown"
				want = ErrOutcomeUnknown
			case "uid":
				uid = "recreated"
				want = ErrSuperseded
			case "marker":
				releaseMap(releaseMap(facts[0].Receipt.Object["metadata"])["annotations"])[ReleaseActionAnnotation] = "foreign"
				want = ErrSuperseded
			case "missing":
				facts = nil
				want = ErrOutcomeUnknown
			}
			if _, e := releasePriorCreateFact(parent, resource, facts, uid); !errors.Is(e, want) {
				t.Fatal("untrusted prior creation accepted", e)
			}
		})
	}
}

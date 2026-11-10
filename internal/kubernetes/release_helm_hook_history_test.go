package kubernetes

import (
	"context"
	"testing"
)

func TestRollbackDeletedHookHistoryNeverBorrowsForeignOrIncompleteFacts(t *testing.T) {
	for _, scenario := range []string{"ready", "failed", "foreign-release", "foreign-plan", "foreign-receipt-resource", "wrong-create-uid", "wrong-marker", "pending-health", "unknown-delete", "wrong-delete-uid", "wrong-delete-rv", "not-confirmed", "undeclared-delete", "recreated", "same-uid-present", "later-create"} {
		t.Run(scenario, func(t *testing.T) {
			id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "fixture-hook"}
			resource := releaseResourceFromObject(id, testOwnershipHook(), "workload")
			source := ReleaseRecord{ID: "source", PlanID: "sealed-plan", Plan: ReleasePlan{Helm: &ReleaseHelmPlan{Hooks: true}, Resources: []ReleaseResource{resource}, Deletes: []ReleaseResourceID{id}}}
			actual := releaseObjectCopy(resource.Object)
			meta := releaseMap(actual["metadata"])
			meta["uid"] = "hook-uid"
			meta["resourceVersion"] = "17"
			releaseMap(meta["annotations"])[ReleaseActionAnnotation] = "create-action"
			created := ReleaseAction{ID: "create-action", ReleaseID: source.ID, State: "verified", Intent: ReleaseActionIntent{ParentID: source.PlanID, Resource: id, Verb: "create"}, Receipt: &ReleaseActionReceipt{Resource: id, UID: "hook-uid", ResourceVersion: "17", Object: actual, Health: ReleaseHealth{State: "ready", Reason: "job_completed"}}}
			deleted := ReleaseAction{ID: "delete-action", ReleaseID: source.ID, State: "verified", Intent: ReleaseActionIntent{ParentID: source.PlanID, Resource: id, Verb: "delete", ExpectedUID: "hook-uid", ExpectedRV: "18"}, Receipt: &ReleaseActionReceipt{Resource: id, UID: "hook-uid", ResourceVersion: "18", Deleted: true}}
			api := &releaseControllerAPI{objects: map[string]map[string]any{}}
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			switch scenario {
			case "failed":
				created.Receipt.Health = ReleaseHealth{State: "failed", Reason: "job_failed"}
			case "foreign-release":
				created.ReleaseID = "other-release"
			case "foreign-plan":
				created.Intent.ParentID = "other-plan"
			case "foreign-receipt-resource":
				created.Receipt.Resource.Name = "other"
			case "wrong-create-uid":
				meta["uid"] = "foreign"
			case "wrong-marker":
				releaseMap(meta["annotations"])[ReleaseActionAnnotation] = "foreign"
			case "pending-health":
				created.Receipt.Health.State = "pending"
			case "unknown-delete":
				deleted.State = "outcome_unknown"
			case "wrong-delete-uid":
				deleted.Intent.ExpectedUID = "foreign"
			case "wrong-delete-rv":
				deleted.Receipt.ResourceVersion = "foreign"
			case "not-confirmed":
				deleted.Receipt.Deleted = false
			case "undeclared-delete":
				source.Plan.Deletes = nil
			case "recreated":
				api.objects[path] = releaseObjectCopy(actual)
				releaseMap(api.objects[path]["metadata"])["uid"] = "replacement"
			case "same-uid-present":
				api.objects[path] = actual
			}
			facts := []ReleaseAction{created, deleted}
			if scenario == "later-create" {
				facts = append(facts, created)
			}
			completed, err := rollbackCompletedHelmHook(context.Background(), api, source, facts, resource)
			if scenario == "ready" || scenario == "failed" {
				if !completed || err != nil {
					t.Fatal("verified same-incarnation closed hook rejected")
				}
			} else if completed {
				t.Fatal("foreign or incomplete hook history borrowed")
			}
			if api.writes != 0 {
				t.Fatal("historical check dispatched mutation")
			}
		})
	}
}

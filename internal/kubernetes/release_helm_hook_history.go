package kubernetes

import (
	"context"
	"errors"
)

// Helm's hook-succeeded policy can remove a completed Job before rollback.
// Only this release's sealed, same-incarnation completion AND confirmed delete
// receipts can replace the ordinary live-object ownership check. A name/label,
// accepted delete without absence evidence, or another release's fact cannot.
func rollbackCompletedHelmHook(ctx context.Context, api releaseAPI, source ReleaseRecord, facts []ReleaseAction, resource ReleaseResource) (bool, error) {
	if source.Plan.Helm == nil || !source.Plan.Helm.Hooks || releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["helm.sh/hook"] == nil || !validReleaseHelmHook(resource) {
		return false, nil
	}
	declaredDelete := false
	for _, id := range source.Plan.Deletes {
		declaredDelete = declaredDelete || id == resource.ID
	}
	if !declaredDelete {
		return false, nil
	}
	var completed *ReleaseActionReceipt
	closedUID := ""
	for _, fact := range facts {
		if fact.Intent.Resource != resource.ID {
			continue
		}
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return false, ErrOutcomeUnknown
		}
		if fact.ReleaseID != source.ID || fact.Intent.ParentID != source.PlanID {
			return false, ErrSuperseded
		}
		receipt := fact.Receipt
		if fact.Intent.Verb != "delete" {
			completed = nil
			closedUID = ""
			if fact.Intent.Verb != "create" || fact.State != "verified" || receipt == nil || receipt.Resource != resource.ID || receipt.UID == "" || receipt.Object == nil || receipt.Health.State != "ready" && receipt.Health.State != "failed" {
				continue
			}
			meta := releaseMap(receipt.Object["metadata"])
			if meta["uid"] != receipt.UID || releaseMap(meta["annotations"])[ReleaseActionAnnotation] != fact.ID || !releaseResourceExpectedMatches(resource, receipt.Object) {
				return false, ErrSuperseded
			}
			completed = receipt
			continue
		}
		closedUID = ""
		if completed != nil && fact.State == "verified" && receipt != nil && receipt.Resource == resource.ID && receipt.Deleted && fact.Intent.ExpectedUID == completed.UID && receipt.UID == completed.UID && fact.Intent.ExpectedRV != "" && receipt.ResourceVersion == fact.Intent.ExpectedRV {
			closedUID = completed.UID
		}
	}
	if closedUID == "" {
		return false, nil
	}
	if _, err := readReleaseObject(ctx, api, resource.ID); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return false, err
		}
		return false, ErrSuperseded
	}
	return true, nil
}

package kubernetes

import (
	"context"
	"errors"
)

// Future candidate UIDs do not exist at preview. Freeze only the same-plan
// creation step reference and an exact patch computed from its admitted shape.
func releaseProgressiveMutation(plan ReleasePlan, batchIndex int, resource ReleaseResource) (*ReleaseMutation, error) {
	if plan.MutationVersion != 1 || plan.Mode != "progressive" || plan.Progressive == nil || resource.ID != plan.Progressive.Candidate || batchIndex < 1 || batchIndex >= len(plan.Batches) {
		return nil, nil
	}
	for _, baseline := range plan.Baseline {
		if baseline.ID == resource.ID {
			return nil, nil
		}
	}
	var originIndex int
	found := false
	for index, prior := range plan.Batches[0] {
		if prior.ID == resource.ID {
			originIndex = index
			found = true
			break
		}
	}
	if !found {
		return nil, ErrInvalid
	}
	var previous *ReleaseResource
	for index := range plan.Batches[batchIndex-1] {
		if plan.Batches[batchIndex-1][index].ID == resource.ID {
			previous = &plan.Batches[batchIndex-1][index]
			break
		}
	}
	if previous == nil || previous.Expected == nil {
		return nil, ErrInvalid
	}
	managed := releaseCanonicalObject(previous.Object)
	if previous.Mutation != nil {
		managed = releaseObjectCopy(previous.Mutation.Managed)
	}
	_, owned, patch, e := releaseThreeWay(managed, previous.Expected, releaseCanonicalObject(resource.Object), false)
	if e != nil {
		return nil, e
	}
	releaseMutationIdentity(resource.ID, patch)
	return &ReleaseMutation{Strategy: releasePriorCreateMerge, FieldManager: releaseFieldManager, ContentType: releaseMergeContentType, OriginStepKey: "batch-0-resource-" + strconvI(originIndex), Managed: owned, Patch: patch}, nil
}
func releasePriorCreateStepValid(plan ReleasePlan, resource ReleaseResource) bool {
	if plan.MutationVersion != 1 || plan.Mode != "progressive" || plan.Progressive == nil || resource.ID != plan.Progressive.Candidate || resource.Mutation == nil || resource.Mutation.Strategy != releasePriorCreateMerge || len(plan.Batches) < 2 {
		return false
	}
	for _, baseline := range plan.Baseline {
		if baseline.ID == resource.ID {
			return false
		}
	}
	for index, origin := range plan.Batches[0] {
		if origin.ID == resource.ID && resource.Mutation.OriginStepKey == "batch-0-resource-"+strconvI(index) && origin.Mutation == nil && origin.Expected != nil && origin.ID.Group == "apps" && origin.ID.Kind == "Deployment" {
			return true
		}
	}
	return false
}
func releasePriorCreateFact(parent ReleaseRecord, resource ReleaseResource, facts []ReleaseAction, uid string) (ReleaseAction, error) {
	if !releasePriorCreateStepValid(parent.Plan, resource) || uid == "" {
		return ReleaseAction{}, ErrDenied
	}
	for _, fact := range facts {
		if fact.Intent.StepKey != resource.Mutation.OriginStepKey {
			continue
		}
		if fact.ReleaseID != parent.ID || fact.Intent.ParentID != parent.PlanID || fact.Intent.Resource != resource.ID || fact.Intent.Verb != "create" || fact.Intent.MutationVersion != parent.Plan.MutationVersion {
			return ReleaseAction{}, ErrDenied
		}
		if fact.State != "verified" || fact.Receipt == nil {
			return ReleaseAction{}, ErrOutcomeUnknown
		}
		meta := releaseMap(fact.Receipt.Object["metadata"])
		if fact.Receipt.Resource != resource.ID || meta["name"] != resource.ID.Name || releaseString(meta["namespace"]) != resource.ID.Namespace || fact.Receipt.UID != uid || meta["uid"] != uid || releaseMap(meta["annotations"])[ReleaseActionAnnotation] != fact.ID {
			return ReleaseAction{}, ErrSuperseded
		}
		return fact, nil
	}
	return ReleaseAction{}, ErrOutcomeUnknown
}
func releaseMutationCurrent(parent ReleaseRecord, resource ReleaseResource, current map[string]any, facts []ReleaseAction) error {
	m := resource.Mutation
	if m == nil {
		return nil
	}
	if m.Strategy != releaseCoreDNSKeyMerge && releaseHasApplyManager(current) {
		return &releaseAPIStatusError{status: 409}
	}
	if m.Strategy == releaseHelmOwnedPostMerge && releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation] != m.PreviousActionID {
		return ErrSuperseded
	}
	uid := releaseString(releaseMap(current["metadata"])["uid"])
	if m.Strategy == releasePriorCreateMerge {
		_, e := releasePriorCreateFact(parent, resource, facts, uid)
		return e
	}
	if uid == "" || uid != m.UID {
		return ErrSuperseded
	}
	return nil
}
func (s *Service) validatePriorCreateIntent(ctx context.Context, q dbtx, parent ReleaseRecord, resource ReleaseResource, uid string) error {
	var actionID string
	e := q.QueryRowContext(ctx, `SELECT id FROM k8s_release_actions WHERE release_id=? AND step_key=?`, parent.ID, resource.Mutation.OriginStepKey).Scan(&actionID)
	if e != nil {
		return errors.Join(ErrOutcomeUnknown, e)
	}
	fact, e := s.action(ctx, q, actionID)
	if e != nil {
		return e
	}
	_, e = releasePriorCreateFact(parent, resource, []ReleaseAction{fact}, uid)
	return e
}

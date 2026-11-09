package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
)

// An actual baseline includes admission defaults and third-party members. They
// remain the complete drift/CAS snapshot, but must not become a new declaration
// merely because rollback needs the previous user intent. Re-derive that intent
// only from its exact sealed action/plan and same-incarnation verified receipt.
// Without this provenance, preserve the historical full-baseline path; no live
// field comparison can infer ownership or relax an atomic-list conflict.
func (s *Service) rollbackDeclaredBaseline(ctx context.Context, auth ReleaseAuthorization, baseline ReleaseResource) (ReleaseResource, error) {
	marker := releaseString(releaseMap(releaseMap(baseline.Object["metadata"])["annotations"])[ReleaseActionAnnotation])
	if !validID(marker) {
		return baseline, nil
	}
	q := configstate.Queryer(ctx, s.db)
	action, e := s.action(ctx, q, marker)
	if errors.Is(e, ErrNotFound) {
		return baseline, nil
	}
	if e != nil {
		return baseline, e
	}
	uid := releaseString(releaseMap(baseline.Object["metadata"])["uid"])
	if uid == "" || action.State != "verified" || action.Intent.Verb == "delete" || action.Intent.Resource != baseline.ID || action.Receipt == nil || action.Receipt.Resource != baseline.ID || action.Receipt.UID != uid || releaseMap(action.Receipt.Object["metadata"])["uid"] != uid {
		return baseline, nil
	}
	parent, e := s.release(ctx, q, action.ReleaseID)
	if e != nil {
		return baseline, e
	}
	if !releaseSameMutationScope(parent.Plan.Authorization, auth) || action.Intent.ParentID != parent.PlanID || action.Intent.AuthorizationFingerprint != parent.Plan.Authorization.Fingerprint {
		return baseline, nil
	}
	prior, ok := releasePlannedActionResource(parent.Plan, action)
	// Partial image intent cannot replace an entire manifest baseline. Its
	// existing dedicated image rollback path retains the full object snapshot.
	if !ok || prior.ImageOnly || prior.Delete || prior.VerifyOnly || prior.Probe || prior.Ephemeral || !releaseResourceBaselineMatches(prior, action.Receipt.Object, baseline.Object) {
		return baseline, nil
	}
	prior.RollbackImages = baseline.RollbackImages
	return prior, nil
}

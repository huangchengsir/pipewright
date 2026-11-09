package kubernetes

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

func (s *ReleaseSession) requireHelmCASCapability(ctx context.Context) error {
	return s.requireHelmCapabilities(ctx, false)
}
func (s *ReleaseSession) requireHelmCapabilities(ctx context.Context, hooks bool) error {
	reply, e := s.RPC(ctx, kubeprotocol.Frame{Version: 2, Command: "ping"})
	if e != nil || reply.Code != "ok" {
		return ErrVersion
	}
	cas, storageJSON, hookOpenAPI, hookResync := false, false, false, false
	for _, capability := range reply.Capabilities {
		cas = cas || capability == kubeprotocol.PermitCASMergeCapability
		storageJSON = storageJSON || capability == kubeprotocol.HelmStorageJSONCapability
		hookOpenAPI = hookOpenAPI || capability == kubeprotocol.HelmHookOpenAPICapability
		hookResync = hookResync || capability == kubeprotocol.HelmHookWatchResyncCapability
	}
	if !cas || !storageJSON || hooks && (!hookOpenAPI || !hookResync) {
		return ErrVersion
	}
	return nil
}

// The CLI still submits its frozen full rendered intent. Only after this exact
// comparison may its request use the separately frozen, receipt-proven patch.
func releaseHelmPendingMutation(parent ReleaseRecord, id ReleaseResourceID, object map[string]any) (*ReleaseResource, error) {
	if parent.Plan.MutationVersion != 1 || parent.Plan.Helm == nil {
		return nil, nil
	}
	for _, resource := range parent.Plan.Helm.Rendered {
		if resource.ID != id {
			continue
		}
		meta := releaseMap(object["metadata"])
		if meta == nil {
			return nil, ErrDenied
		}
		if id.Namespace != "" && meta["namespace"] == nil {
			meta["namespace"] = id.Namespace
		}
		if !ReleaseObjectMatches(resource.Object, object) || !ReleaseObjectMatches(object, resource.Object) {
			return nil, ErrDenied
		}
		if resource.Mutation == nil {
			return nil, nil
		}
		if resource.Mutation.Strategy != releaseHelmOwnedPostMerge || !validReleaseMutation(resource) {
			return nil, ErrDenied
		}
		return &resource, nil
	}
	return nil, nil
}

// Revalidate sealed provenance at plan/action persistence as well as at compile.
func (s *Service) validateHelmMutationProof(ctx context.Context, q dbtx, plan ReleasePlan, resource ReleaseResource) error {
	m := resource.Mutation
	if m == nil || m.Strategy != releaseHelmOwnedPostMerge {
		return nil
	}
	if plan.MutationVersion != 1 || plan.Helm == nil || !validReleaseMutation(resource) {
		return ErrDenied
	}
	latest, e := s.action(ctx, q, m.PreviousActionID)
	if e != nil {
		return e
	}
	origin, e := s.action(ctx, q, m.OriginActionID)
	if e != nil {
		return e
	}
	for _, fact := range []ReleaseAction{latest, origin} {
		if fact.State != "verified" || fact.Receipt == nil || fact.Intent.Resource != resource.ID || fact.Receipt.Resource != resource.ID || fact.Receipt.UID != m.UID || fact.Intent.Verb == "delete" {
			return ErrDenied
		}
		meta := releaseMap(fact.Receipt.Object["metadata"])
		if meta["uid"] != m.UID || meta["name"] != resource.ID.Name || releaseString(meta["namespace"]) != resource.ID.Namespace || releaseMap(meta["annotations"])[ReleaseActionAnnotation] != fact.ID {
			return ErrDenied
		}
		prior, e := s.release(ctx, q, fact.ReleaseID)
		if e != nil {
			return e
		}
		if fact.Intent.ParentID != prior.PlanID || fact.Intent.MutationVersion != prior.Plan.MutationVersion || !releaseSameMutationScope(prior.Plan.Authorization, plan.Authorization) || !sameReleaseHelmIdentity(prior.Plan.Helm, plan.Helm) {
			return ErrDenied
		}
		if fact.ID == latest.ID && fact.Intent.Verb != "create" {
			previous, ok := releaseMutationPlanResource(prior.Plan, resource.ID)
			if !ok || previous.Mutation == nil || previous.Mutation.Strategy != releaseHelmOwnedPostMerge || previous.Mutation.OriginActionID != origin.ID {
				return ErrDenied
			}
		}
	}
	if origin.Intent.Verb != "create" || latest.Intent.Verb == "create" && latest.ID != origin.ID {
		return ErrDenied
	}
	return nil
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

const releaseFieldManager = "pipewright-release-v2"
const releaseOwnedPostMerge = "owned-post-cas-merge-v1"
const releaseHelmOwnedPostMerge = "helm-owned-post-cas-merge-v1"
const releasePriorCreateMerge = "prior-create-cas-merge-v1"
const releaseCoreDNSKeyMerge = "coredns-key-cas-v1"
const releaseMergeContentType = "application/merge-patch+json"

// Optional and sealed in the plan. Its absence preserves historical SSA wire
// semantics. Managed contains only previous approved fields, never API defaults.
type ReleaseMutation struct {
	OriginStepKey    string         `json:"originStepKey,omitempty"`
	Strategy         string         `json:"strategy"`
	FieldManager     string         `json:"fieldManager"`
	ContentType      string         `json:"contentType"`
	OriginActionID   string         `json:"originActionId,omitempty"`
	PreviousActionID string         `json:"previousActionId,omitempty"`
	UID              string         `json:"uid"`
	Managed          map[string]any `json:"managed,omitempty"`
	Patch            map[string]any `json:"patch"`
}

func releaseSameMutationScope(a, b ReleaseAuthorization) bool {
	x, y := a.Request, b.Request
	return a.ClusterKey == b.ClusterKey && x.ProjectID == y.ProjectID && x.Environment == y.Environment && x.ConnectionID == y.ConnectionID && x.CredentialID == y.CredentialID && x.Context == y.Context && x.Executor == y.Executor
}
func releaseMutationPlanResource(plan ReleasePlan, id ReleaseResourceID) (ReleaseResource, bool) {
	if plan.Helm != nil {
		for _, r := range plan.Helm.Rendered {
			if r.ID == id {
				return r, true
			}
		}
	}
	for _, r := range plan.Resources {
		if r.ID == id {
			return r, true
		}
	}
	for _, batch := range plan.Batches {
		for _, r := range batch {
			if r.ID == id {
				return r, true
			}
		}
	}
	return ReleaseResource{}, false
}
func releaseHasApplyManager(object map[string]any) bool {
	for _, raw := range releaseItems(releaseMap(object["metadata"])["managedFields"]) {
		entry := releaseMap(raw)
		if entry["operation"] == "Apply" && releaseString(entry["subresource"]) != "status" {
			return true
		}
	}
	return false
}
func (s *Service) ownedPostMutation(ctx context.Context, auth ReleaseAuthorization, resource ReleaseResource, current map[string]any) (*ReleaseMutation, error) {
	return s.ownedPostMutationForHelm(ctx, auth, resource, current, nil)
}
func sameReleaseHelmIdentity(a, b *ReleaseHelmPlan) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Name == b.Name && a.Namespace == b.Namespace && a.StorageAnchor == b.StorageAnchor
}
func (s *Service) ownedPostMutationForHelm(ctx context.Context, auth ReleaseAuthorization, resource ReleaseResource, current map[string]any, helm *ReleaseHelmPlan) (*ReleaseMutation, error) {
	meta := releaseMap(current["metadata"])
	marker := releaseString(releaseMap(meta["annotations"])[ReleaseActionAnnotation])
	if marker == "" || !validID(marker) || releaseHasApplyManager(current) {
		return nil, nil
	}
	q := configstate.Queryer(ctx, s.db)
	latest, e := s.action(ctx, q, marker)
	if errors.Is(e, ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if latest.State != "verified" || latest.Receipt == nil || latest.Intent.Resource != resource.ID || latest.Intent.Verb == "delete" || latest.Receipt.UID != meta["uid"] || latest.Receipt.UID == "" {
		return nil, nil
	}
	parent, e := s.release(ctx, q, latest.ReleaseID)
	if e != nil {
		return nil, e
	}
	if !releaseSameMutationScope(parent.Plan.Authorization, auth) || !sameReleaseHelmIdentity(parent.Plan.Helm, helm) || latest.Intent.ParentID != parent.PlanID {
		return nil, nil
	}
	previous, ok := releasePlannedActionResource(parent.Plan, latest)
	if !ok {
		return nil, nil
	}
	origin := latest
	managed := releaseCanonicalObject(previous.Object)
	if latest.Intent.Verb != "create" {
		if previous.Mutation == nil || previous.Mutation.Managed == nil {
			return nil, nil
		}
		if previous.Mutation.Strategy == releasePriorCreateMerge {
			facts, readErr := s.ListReleaseActions(ctx, parent.ID)
			if readErr != nil {
				return nil, readErr
			}
			origin, e = releasePriorCreateFact(parent, previous, facts, latest.Receipt.UID)
		} else if previous.Mutation.Strategy == releaseOwnedPostMerge || previous.Mutation.Strategy == releaseHelmOwnedPostMerge {
			origin, e = s.action(ctx, q, previous.Mutation.OriginActionID)
		} else {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		managed = releaseObjectCopy(previous.Mutation.Managed)
	}
	if origin.Intent.Verb != "create" || origin.State != "verified" || origin.Receipt == nil || origin.Receipt.UID != latest.Receipt.UID || origin.Intent.Resource != resource.ID {
		return nil, nil
	}
	creator, e := s.release(ctx, q, origin.ReleaseID)
	if e != nil {
		return nil, e
	}
	if !sameReleaseHelmIdentity(creator.Plan.Helm, helm) || !releaseSameMutationScope(creator.Plan.Authorization, auth) || origin.Intent.ParentID != creator.PlanID {
		return nil, nil
	}
	if resource.ImageOnly {
		spec := releaseMap(managed["spec"])
		if _, owned := spec["replicas"]; owned && !releaseSameValue(spec["replicas"], releaseMap(current["spec"])["replicas"]) {
			delete(spec, "replicas")
		}
	}
	// All owned intent fields must still agree. Unknown keys and injected named
	// members may be present; they never become deletion authority.
	if !ReleasePodTemplateMatches(managed, releaseCanonicalObject(current)) {
		return nil, ErrSuperseded
	}
	_, owned, patch, e := releaseThreeWay(managed, releaseCanonicalObject(current), releaseCanonicalObject(resource.Object), resource.ImageOnly)
	if e != nil {
		return nil, e
	}
	releaseMutationIdentity(resource.ID, patch)
	strategy := releaseOwnedPostMerge
	if helm != nil {
		strategy = releaseHelmOwnedPostMerge
	}
	return &ReleaseMutation{Strategy: strategy, FieldManager: releaseFieldManager, ContentType: releaseMergeContentType, OriginActionID: origin.ID, PreviousActionID: latest.ID, UID: latest.Receipt.UID, Managed: owned, Patch: patch}, nil
}
func releaseMutationIdentity(id ReleaseResourceID, object map[string]any) {
	version := id.Version
	if id.Group != "" {
		version = id.Group + "/" + version
	}
	object["apiVersion"], object["kind"] = version, id.Kind
	metadata := releaseMap(object["metadata"])
	if metadata == nil {
		metadata = map[string]any{}
		object["metadata"] = metadata
	}
	metadata["name"] = id.Name
	if id.Namespace != "" {
		metadata["namespace"] = id.Namespace
	}
}
func validReleaseMutation(resource ReleaseResource) bool {
	m := resource.Mutation
	if m == nil {
		return true
	}
	if m.FieldManager != releaseFieldManager || m.ContentType != releaseMergeContentType || m.Patch == nil {
		return false
	}
	if m.Strategy != releaseOwnedPostMerge && m.Strategy != releaseHelmOwnedPostMerge && m.Strategy != releaseCoreDNSKeyMerge && m.Strategy != releasePriorCreateMerge {
		return false
	}
	if m.Strategy != releasePriorCreateMerge && m.UID == "" {
		return false
	}
	if m.Strategy == releasePriorCreateMerge && (m.UID != "" || m.OriginStepKey == "" || m.Managed == nil) {
		return false
	}
	if (m.Strategy == releaseOwnedPostMerge || m.Strategy == releaseHelmOwnedPostMerge) && (!validID(m.OriginActionID) || !validID(m.PreviousActionID) || m.Managed == nil) {
		return false
	}
	expected := map[string]any{}
	releaseMutationIdentity(resource.ID, expected)
	return ReleaseObjectMatches(expected, m.Patch) && releaseMap(m.Patch["metadata"])["uid"] == nil && releaseMap(m.Patch["metadata"])["resourceVersion"] == nil
}
func releaseMutationObject(resource ReleaseResource) map[string]any {
	if resource.Mutation != nil {
		return releaseObjectCopy(resource.Mutation.Patch)
	}
	return releaseObjectCopy(resource.Object)
}

// CoreDNS is an explicit maintenance capability, not ownership migration. Only
// the frozen live Corefile key may use this exception, after candidate barriers.
func releaseCoreDNSMutation(plan ReleasePlan, resource ReleaseResource, baseline map[string]any) (*ReleaseMutation, error) {
	if plan.CoreDNS == nil || plan.CoreDNS.LiveIndex < 0 || plan.CoreDNS.LiveIndex >= len(plan.Resources) || plan.Resources[plan.CoreDNS.LiveIndex].ID != resource.ID {
		return nil, nil
	}
	var config pipeline.KubernetesReleaseConfig
	if json.Unmarshal(plan.Config, &config) != nil || config.CoreDNS == nil {
		return nil, ErrInvalid
	}
	key := config.CoreDNS.Key
	if key == "" || resource.ID.Group != "" || resource.ID.Kind != "ConfigMap" || resource.ID.Name != config.CoreDNS.ConfigMap || resource.ID.Namespace != config.Namespace || baseline == nil {
		return nil, ErrInvalid
	}
	uid := releaseString(releaseMap(baseline["metadata"])["uid"])
	if uid == "" {
		return nil, ErrInvalid
	}
	original, desired := releaseMap(baseline["data"]), releaseMap(resource.Object["data"])
	if releaseString(desired[key]) == "" {
		return nil, ErrInvalid
	}
	left, right := releaseObjectCopy(original), releaseObjectCopy(desired)
	delete(left, key)
	delete(right, key)
	if !releaseSameValue(left, right) || !releaseSameValue(baseline["binaryData"], resource.Object["binaryData"]) {
		return nil, ErrDenied
	}
	patch := map[string]any{"data": map[string]any{key: desired[key]}}
	releaseMutationIdentity(resource.ID, patch)
	return &ReleaseMutation{Strategy: releaseCoreDNSKeyMerge, FieldManager: releaseFieldManager, ContentType: releaseMergeContentType, UID: uid, Patch: patch}, nil
}

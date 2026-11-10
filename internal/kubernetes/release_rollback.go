package kubernetes

import (
	"context"
	"errors"
	"sort"
	"time"
)

var ErrRollbackBaseline = errors.New("kubernetes_rollback_baseline_unavailable")

type ReleaseRollbackRetention struct {
	Resource ReleaseResourceID `json:"resource"`
	Reason   string            `json:"reason"`
}

func (s *Service) validateRollbackSource(ctx context.Context, q dbtx, plan ReleasePlan) error {
	source, e := s.release(ctx, q, plan.RollbackOf)
	if e != nil {
		return e
	}
	if !plan.ForceApproval || source.Revision != plan.RollbackRevision || source.PlanID != plan.RollbackPlanID || !source.OwnerFenced || source.Cleanup != CleanupVerified || source.State == "outcome_unknown" || source.State == "resume_requested" || source.State == "rollback_in_progress" || source.Plan.Authorization.ClusterKey != plan.Authorization.ClusterKey || source.Plan.Authorization.Request.ProjectID != plan.Authorization.Request.ProjectID {
		return ErrConflict
	}
	var unknown int
	if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state IN ('outcome_unknown','submitted')`, source.ID).Scan(&unknown); e != nil {
		return e
	}
	if unknown > 0 {
		return ErrOutcomeUnknown
	}
	return nil
}
func rollbackProtected(id ReleaseResourceID) bool {
	return id.Group == "" && (id.Kind == "PersistentVolume" || id.Kind == "PersistentVolumeClaim" || id.Kind == "Namespace") || id.Group == "apiextensions.k8s.io" && id.Kind == "CustomResourceDefinition"
}
func rollbackPinnedImages(resource ReleaseResource) bool {
	if resource.ID.Kind != "Deployment" && resource.ID.Kind != "StatefulSet" && resource.ID.Kind != "DaemonSet" && resource.ID.Kind != "Pod" {
		return true
	}
	pod := releaseMap(resource.Object["spec"])
	if resource.ID.Kind != "Pod" {
		pod = releaseMap(releaseMap(pod["template"])["spec"])
	}
	for _, key := range []string{"containers", "initContainers"} {
		for _, value := range releaseItems(pod[key]) {
			if !ValidImageReference(releaseString(releaseMap(value)["image"])) {
				return false
			}
		}
	}
	return true
}
func rollbackImageObject(resource, baseline ReleaseResource) (map[string]any, error) {
	original := releaseItems(releaseMap(releaseMap(releaseMap(rollbackFrozenImageObject(baseline)["spec"])["template"])["spec"])["containers"])
	desired := releaseItems(releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["spec"])["containers"])
	restored := []any{}
	for _, value := range desired {
		wanted := releaseMap(value)
		found := false
		for _, old := range original {
			container := releaseMap(old)
			if container["name"] == wanted["name"] {
				if !ValidImageReference(releaseString(container["image"])) {
					return nil, ErrRollbackBaseline
				}
				restored = append(restored, map[string]any{"name": container["name"], "image": container["image"]})
				found = true
				break
			}
		}
		if !found {
			return nil, ErrRollbackBaseline
		}
	}
	return map[string]any{"apiVersion": "apps/v1", "kind": resource.ID.Kind, "metadata": map[string]any{"name": resource.ID.Name, "namespace": resource.ID.Namespace}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": restored}}}}, nil
}

// PrepareRollback freezes a new independently approved intent. It references
// exact positively fenced source effects, never a moving tag, current pipeline
// configuration, or an unrelated object's latest contents.
func (c *ReleaseController) PrepareRollback(ctx context.Context, run, node, owner, sourceID string, revision int64, planID string) (out ReleaseRecord, resultErr error) {
	return c.PrepareRollbackWithCommit(ctx, run, node, owner, sourceID, revision, planID, nil)
}

// The read-only preparation finishes before the caller's authenticated atomic
// commit creates its new frozen run, release and durable dispatch together.
func (c *ReleaseController) PrepareRollbackWithCommit(ctx context.Context, run, node, owner, sourceID string, revision int64, planID string, commit func(context.Context, ReleasePlan) (ReleaseRecord, error)) (out ReleaseRecord, resultErr error) {
	source, e := c.store.GetRelease(ctx, sourceID)
	if e != nil {
		return out, e
	}
	if source.Revision != revision || source.PlanID != planID || !source.OwnerFenced || source.Cleanup != CleanupVerified || source.State == "outcome_unknown" || source.State == "resume_requested" || source.State == "rollback_in_progress" {
		return out, ErrConflict
	}
	// Helm revision restoration and CoreDNS maintenance use their specialized
	// controllers; this ordinary resource path cannot impersonate their receipts.
	if source.Plan.RollbackOf != "" {
		return out, ErrRollbackBaseline
	}
	if source.Plan.CoreDNS != nil || source.Plan.Helm != nil {
		return c.prepareSpecialRollback(ctx, run, node, owner, source, commit)
	}
	facts, e := c.store.ListReleaseActions(ctx, source.ID)
	if e != nil {
		return out, e
	}
	for _, fact := range facts {
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return out, ErrOutcomeUnknown
		}
	}
	readAuth, e := c.store.AuthorizeRelease(ctx, releaseReadRequest(source.Plan.Authorization))
	if e != nil {
		return out, e
	}
	api, e := c.preview(ctx, readAuth, owner)
	if e != nil {
		return out, e
	}
	defer func() {
		if api != nil {
			_, e := api.Close(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, e)
		}
	}()
	plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "rollback", ForceApproval: true, RollbackOf: source.ID, RollbackRevision: source.Revision, RollbackPlanID: source.PlanID, Config: append([]byte(nil), source.Plan.Config...), TimeoutSeconds: source.Plan.TimeoutSeconds, ObserveSeconds: source.Plan.ObserveSeconds, Progressive: source.Plan.Progressive}
	request := source.Plan.Authorization.Request
	request.Resources = nil
	for _, resource := range source.Plan.Resources {
		if resource.Probe || resource.Ephemeral || resource.Delete {
			continue
		}
		current, e := readReleaseObject(ctx, api, resource.ID)
		if errors.Is(e, ErrNotFound) && releaseRollbackAbsentUnsubmitted(source, facts, resource) {
			continue
		}
		if e != nil {
			return out, e
		}
		if releaseMap(current["metadata"])["deletionTimestamp"] != nil {
			return out, ErrSuperseded
		}
		marker := releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation]
		var owned *ReleaseAction
		for index := range facts {
			fact := &facts[index]
			if fact.ID == marker && fact.State == "verified" && fact.Intent.Verb != "delete" && fact.Receipt != nil && fact.Intent.Resource == resource.ID && releaseResourceBaselineMatches(resource, fact.Receipt.Object, current) {
				owned = fact
				break
			}
		}
		if owned == nil {
			unchanged := false
			for _, baseline := range source.Plan.Baseline {
				if baseline.ID == resource.ID && releaseResourceBaselineMatches(resource, baseline.Object, current) {
					unchanged = true
				}
			}
			if unchanged {
				continue
			}
			return out, ErrSuperseded
		}
		var baseline *ReleaseResource
		for index := range source.Plan.Baseline {
			if source.Plan.Baseline[index].ID == resource.ID {
				baseline = &source.Plan.Baseline[index]
				break
			}
		}
		access := releaseRequestedAccess(resource.ID, "patch")
		access.Containers = releaseContainerNames(resource)
		access.Actions = append(access.Actions, "rollback")
		if baseline == nil {
			if rollbackProtected(resource.ID) {
				plan.Retained = append(plan.Retained, ReleaseRollbackRetention{Resource: resource.ID, Reason: "persistent_resource_retained"})
				request.Resources = append(request.Resources, ReleaseAccess{ID: resource.ID, Actions: []string{"get", "rollback"}})
				continue
			}
			target := releaseResourceFromObject(resource.ID, releaseCanonicalObject(current), resource.Health)
			target.Delete = true
			target.Phase = 100 - resource.Phase
			plan.Resources = append(plan.Resources, target)
			plan.Baseline = append(plan.Baseline, releaseResourceFromObject(resource.ID, current, resource.Health))
			plan.Deletes = append(plan.Deletes, resource.ID)
			access = ReleaseAccess{ID: resource.ID, Actions: []string{"get", "delete", "rollback"}, Containers: releaseContainerNames(resource)}
			if resource.ID.Kind == "Job" {
				plan.Retained = append(plan.Retained, ReleaseRollbackRetention{Resource: resource.ID, Reason: "external_job_effects_not_reverted"})
			}
		} else {
			declared := *baseline
			if !resource.ImageOnly {
				declared, e = c.store.rollbackDeclaredBaseline(ctx, source.Plan.Authorization, *baseline)
				if e != nil {
					return out, e
				}
			}
			object := rollbackFrozenImageObject(declared)
			releaseDropControllerDeploymentRevision(resource, baseline.Object, object)
			if resource.ImageOnly {
				object, e = rollbackImageObject(resource, *baseline)
				if e != nil {
					return out, e
				}
			} else if !rollbackPinnedImages(ReleaseResource{ID: resource.ID, Object: object}) {
				return out, ErrRollbackBaseline
			}
			target := releaseResourceFromObject(resource.ID, object, resource.Health)
			target.ImageOnly = resource.ImageOnly
			target.Phase = resource.Phase
			target.Predicates = append([]ReleasePredicate(nil), resource.Predicates...)
			target.SchemaDigest = resource.SchemaDigest
			if e = validateReleaseWorkloadStrategy(target); e != nil {
				return out, e
			}
			plan.Resources = append(plan.Resources, target)
			plan.Baseline = append(plan.Baseline, releaseResourceFromObject(resource.ID, current, resource.Health))
			access.Containers = releaseContainerNames(target)
			access.Actions = uniqueReleaseActions(append(access.Actions, releaseAdministrativeActions(target)...))
			if resource.ImageOnly {
				for _, prior := range source.Plan.Authorization.Request.Resources {
					if prior.ID == resource.ID {
						access.Fields = append([]string(nil), prior.Fields...)
					}
				}
			}
		}
		request.Resources = append(request.Resources, access)
	}
	if len(plan.Resources) == 0 {
		return out, ErrRollbackBaseline
	}
	if plan.Progressive != nil {
		if e = c.verifyProgressivePreconditions(ctx, api, source.Plan); e != nil {
			return out, e
		}
		for index := range request.Resources {
			if request.Resources[index].ID == plan.Progressive.Stable || request.Resources[index].ID == plan.Progressive.Candidate {
				request.Resources[index].Actions = uniqueReleaseActions(append(request.Resources[index].Actions, "scale", "related-hpas"))
			}
		}
		request.Resources = append(request.Resources, ReleaseAccess{ID: plan.Progressive.Service.ID, Actions: []string{"get", "related-endpointslices"}})
	}
	sort.SliceStable(plan.Resources, func(i, j int) bool {
		if plan.Resources[i].Delete != plan.Resources[j].Delete {
			return !plan.Resources[i].Delete
		}
		return plan.Resources[i].Phase < plan.Resources[j].Phase
	})
	request.Resources = mergeReleaseScopes(request.Resources)
	plan.Authorization, e = c.store.AuthorizeRelease(ctx, request)
	if e != nil {
		return out, e
	}
	if plan.Authorization.ClusterKey != source.Plan.Authorization.ClusterKey {
		return out, ErrDenied
	}
	if _, e = api.Close(context.WithoutCancel(ctx)); e != nil {
		return out, e
	}
	api = nil
	if e = c.dryRunReleaseResources(ctx, owner, &plan); e != nil {
		return out, e
	}
	if commit != nil {
		return commit(ctx, plan)
	}
	return c.store.CreateRelease(ctx, run, node, owner, plan)
}

// Transfer paused name gates only after the new exact rollback plan was approved.
// The old run stops at its durable frontier; cancellation itself restores nothing.
func (s *Service) transferRollbackGates(ctx context.Context, q dbtx, r ReleaseRecord) error {
	if r.Plan.RollbackOf == "" {
		return nil
	}
	if r.State != "running" || r.ApprovedID != r.PlanID || !r.Plan.ForceApproval {
		return ErrDenied
	}
	source, e := s.release(ctx, q, r.Plan.RollbackOf)
	if e != nil {
		return e
	}
	if source.State == "rollback_in_progress" && source.Revision == r.Plan.RollbackRevision+1 {
		var owned int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_resource_gates WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?`, r.ID, r.OwnerEpoch).Scan(&owned); e != nil {
			return e
		}
		if owned > 0 {
			return nil
		}
		return ErrHeld
	}
	if e = s.validateRollbackSource(ctx, q, r.Plan); e != nil {
		return e
	}
	for _, scope := range r.Plan.Authorization.Request.Resources {
		if _, e = q.ExecContext(ctx, `UPDATE k8s_resource_gates SET owner_id=?,owner_epoch=? WHERE resource_key=? AND owner_kind='v2' AND owner_id=? AND owner_epoch=?`, r.ID, r.OwnerEpoch, releaseGateKey(r.Plan.Authorization.ClusterKey, scope.ID), source.ID, source.OwnerEpoch); e != nil {
			return e
		}
	}
	if _, e = q.ExecContext(ctx, `DELETE FROM k8s_resource_gates WHERE owner_kind='v2' AND owner_id=? AND owner_epoch=?`, source.ID, source.OwnerEpoch); e != nil {
		return e
	}
	if e = requireOne(q.ExecContext(ctx, `UPDATE k8s_releases SET state='rollback_in_progress',revision=revision+1,reason_code='rollback_started',updated_at=? WHERE id=? AND revision=?`, s.now().UnixMilli(), source.ID, source.Revision)); e != nil {
		return e
	}
	if _, e = q.ExecContext(ctx, `UPDATE k8s_release_continuations SET state='canceled',updated_at=? WHERE release_id=? AND state='waiting'`, s.now().UnixMilli(), source.ID); e != nil {
		return e
	}
	_, e = q.ExecContext(ctx, `UPDATE pipeline_runs SET status='failed',finished_at=? WHERE id=? AND status='waiting_action' AND finished_at IS NULL`, s.now().UTC().Format("2006-01-02T15:04:05Z07:00"), source.RunID)
	return e
}

// Observe absence of the exact UID after an already approved delete. A new
// same-named object is external drift and never becomes another delete target.
func (c *ReleaseController) observeReleaseDeletion(ctx context.Context, api releaseAPI, parent ReleaseRecord, action ReleaseAction) error {
	for {
		object, e := readReleaseObject(ctx, api, action.Intent.Resource)
		if errors.Is(e, ErrNotFound) {
			_, e = c.store.RecordReleaseDeletion(ctx, action.ID, parent.OwnerEpoch, action.Revision)
			return e
		}
		if e != nil {
			return e
		}
		if releaseMap(object["metadata"])["uid"] != action.Intent.ExpectedUID {
			return ErrSuperseded
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.poll):
		}
	}
}

func (c *ReleaseController) observeRollbackService(ctx context.Context, api releaseAPI, plan ReleasePlan) error {
	window := ReleaseHealthWindow{}
	for {
		if e := c.verifyProgressivePreconditions(ctx, api, plan); e != nil {
			return e
		}
		e := c.verifyProgressiveService(ctx, api, plan)
		if e == nil {
			if window.Observe(ReleaseHealth{State: "ready"}, time.Now(), plan.ObserveSeconds) {
				return nil
			}
		} else if !errors.Is(e, ErrRollout) {
			return e
		} else {
			window.Since = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.poll):
		}
	}
}

// A journal entry is written before dispatch. For an originally absent resource,
// no action or only positively rejected actions proves that this source never
// created it. A verified, submitted, or unknown effect is never inferred absent.
func releaseRollbackAbsentUnsubmitted(source ReleaseRecord, facts []ReleaseAction, resource ReleaseResource) bool {
	if !source.OwnerFenced || source.Cleanup != CleanupVerified {
		return false
	}
	for _, baseline := range source.Plan.Baseline {
		if baseline.ID == resource.ID {
			return false
		}
	}
	for _, fact := range facts {
		if fact.Intent.Resource != resource.ID {
			continue
		}
		if fact.ReleaseID != source.ID || fact.Intent.ParentID != source.PlanID || fact.Intent.AuthorizationFingerprint != source.Plan.Authorization.Fingerprint || fact.State != "rejected" {
			return false
		}
	}
	return true
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"strconv"
	"time"
)

type releaseReconciliation struct {
	Action  ReleaseAction
	Receipt ReleaseActionReceipt
}

func releaseReadRequest(a ReleaseAuthorization) ReleaseAuthorizationRequest {
	request := a.Request
	request.Resources = append([]ReleaseAccess(nil), request.Resources...)
	for index := range request.Resources {
		scope := &request.Resources[index]
		actions := []string{}
		for _, action := range scope.Actions {
			switch action {
			case "get", "related-pods", "related-replicasets", "related-endpointslices", "related-jobs", "related-hpas", "related-controllerrevisions", "helm-storage":
				actions = append(actions, action)
			}
		}
		scope.Actions = uniqueReleaseActions(append(actions, "get"))
	}
	return request
}
func releasePlannedActionResource(plan ReleasePlan, action ReleaseAction) (ReleaseResource, bool) {
	if action.Intent.MutationVersion != plan.MutationVersion || action.Intent.Verb == "delete" {
		return ReleaseResource{}, false
	}
	resources := append([]ReleaseResource(nil), plan.Resources...)
	if plan.Helm != nil {
		resources = append(resources, plan.Helm.Rendered...)
	}
	for _, batch := range plan.Batches {
		resources = append(resources, batch...)
	}
	if action.Intent.StepKey == releaseCoreDNSFinalizeStep {
		if plan.CoreDNS == nil || plan.CoreDNS.Finalize == nil {
			return ReleaseResource{}, false
		}
		resources = []ReleaseResource{*plan.CoreDNS.Finalize}
	}
	var body map[string]any
	if json.Unmarshal(action.Intent.Body, &body) != nil {
		return ReleaseResource{}, false
	}
	actual := releaseComparableActionBody(body)
	for _, resource := range resources {
		if resource.ID != action.Intent.Resource || resource.Delete || resource.VerifyOnly {
			continue
		}
		contentType := ""
		if resource.Mutation != nil {
			contentType = resource.Mutation.ContentType
			if action.Intent.Verb != "patch" {
				continue
			}
		}
		if action.Intent.ContentType != contentType {
			continue
		}
		expected := releaseComparableActionBody(releaseMutationObject(resource))
		if ReleaseObjectMatches(expected, actual) && ReleaseObjectMatches(actual, expected) {
			return resource, true
		}
	}
	return ReleaseResource{}, false
}
func releaseComparableActionBody(object map[string]any) map[string]any {
	out := releaseObjectCopy(object)
	meta := releaseMap(out["metadata"])
	delete(meta, "uid")
	delete(meta, "resourceVersion")
	annotations := releaseMap(meta["annotations"])
	delete(annotations, ReleaseActionAnnotation)
	if len(annotations) == 0 {
		delete(meta, "annotations")
	}
	return out
}

// ReconcileRelease performs fresh read-only inspection after the old writer was
// fenced and every private tool was positively removed. It never dispatches an
// action, guesses rejection from absence, or changes the historical run result.
type ReleaseReconciliationCommit func(context.Context, func(context.Context) error) error

func (c *ReleaseController) ReconcileRelease(ctx context.Context, id string, expected int64, commits ...ReleaseReconciliationCommit) (out ReleaseRecord, resultErr error) {
	parent, e := c.store.GetRelease(ctx, id)
	if e != nil {
		return parent, e
	}
	if parent.Revision != expected {
		return parent, ErrConflict
	}
	if !parent.OwnerFenced || parent.Cleanup != CleanupVerified || parent.State == "paused" || parent.State == "resume_requested" {
		return parent, ErrHeld
	}
	auth, e := c.store.AuthorizeRelease(ctx, releaseReadRequest(parent.Plan.Authorization))
	if e != nil {
		return parent, e
	}
	api, e := c.preview(ctx, auth, "reconcile_"+uuid.NewString())
	if e != nil {
		return parent, e
	}
	defer func() { _, e := api.Close(context.WithoutCancel(ctx)); resultErr = errors.Join(resultErr, e) }()
	facts, e := c.store.ListReleaseActions(ctx, id)
	if e != nil {
		return parent, e
	}
	observations := []releaseReconciliation{}

	for _, fact := range facts {
		if fact.State == "rejected" {
			continue
		}
		object, e := readReleaseObject(ctx, api, fact.Intent.Resource)
		if errors.Is(e, ErrNotFound) && fact.Intent.Verb == "delete" {
			observations = append(observations, releaseReconciliation{Action: fact, Receipt: ReleaseActionReceipt{Resource: fact.Intent.Resource, UID: fact.Intent.ExpectedUID, ResourceVersion: fact.Intent.ExpectedRV, Deleted: true, Health: ReleaseHealth{State: "declared", Reason: "resource_deleted"}}})
			continue
		}
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return parent, e
		}
		meta := releaseMap(object["metadata"])
		// A replacement, or another action's later version, is never adopted as this
		// request's response. Historical settled receipts are retained independently.
		if releaseMap(meta["annotations"])[ReleaseActionAnnotation] != fact.ID {
			continue
		}
		if fact.Intent.Verb == "delete" {
			continue
		}
		resource, planned := releasePlannedActionResource(parent.Plan, fact)
		if !planned && releaseHelmStorage(parent.Plan, fact.Intent.Resource) {
			resource = ReleaseResource{ID: fact.Intent.Resource, Health: "declaration"}
			planned = true
		}
		if !planned {
			return parent, ErrInvalid
		}
		resource.ExpectedUID = fact.Intent.ExpectedUID
		if fact.Receipt != nil && fact.Receipt.UID != "" {
			resource.ExpectedUID = fact.Receipt.UID
		}
		related, e := c.related(ctx, api, resource)
		if e != nil {
			return parent, e
		}
		health := EvaluateReleaseHealth(resource, object, related)
		if health.State == "ready" && parent.Plan.ObserveSeconds > 0 {
			stable, e := c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds)
			if e != nil {
				return parent, e
			}
			object = stable.Object
			meta = releaseMap(object["metadata"])
			health = stable.Health
		}
		if releaseHelmStorage(parent.Plan, fact.Intent.Resource) {
			health = ReleaseHealth{State: "declared", Reason: "configuration_verified_no_application_health"}
		}
		receipt := ReleaseActionReceipt{Resource: fact.Intent.Resource, UID: releaseString(meta["uid"]), ResourceVersion: releaseString(meta["resourceVersion"]), Object: object, Health: health}
		observations = append(observations, releaseReconciliation{Action: fact, Receipt: receipt})

	}
	checks := []ReleaseCheckReceipt{}
	for _, resource := range parent.Plan.Verify {
		object, e := readReleaseObject(ctx, api, resource.ID)
		if e != nil {
			return parent, e
		}
		related, e := c.related(ctx, api, resource)
		if e != nil {
			return parent, e
		}
		health := EvaluateReleaseHealth(resource, object, related)
		checks = append(checks, ReleaseCheckReceipt{Resource: resource.ID, UID: releaseString(releaseMap(object["metadata"])["uid"]), Object: object, Related: related, Health: health, ObservedAt: time.Now().UnixMilli()})
	}
	complete := true
	if parent.Plan.CoreDNS != nil && parent.Plan.CoreDNS.Finalize != nil {
		complete = false
		for _, observation := range observations {
			if observation.Action.Intent.StepKey == releaseCoreDNSFinalizeStep && observation.Receipt.Resource == parent.Plan.CoreDNS.Finalize.ID && (observation.Receipt.Health.State == "declared" || observation.Receipt.Health.State == "ready") {
				complete = true
			}
		}
	}

	if parent.Plan.CoreDNS != nil {
		if e = c.verifyCoreDNSPopulation(ctx, api, parent.Plan); e != nil {
			return parent, e
		}
	}
	resources := parent.Plan.Resources
	if parent.Plan.Progressive != nil {
		if parent.Cursor != len(parent.Plan.Batches)-1 {
			complete = false
		}
		resources = parent.Plan.Batches[parent.Cursor]
		if e = c.verifyProgressivePreconditions(ctx, api, parent.Plan); e != nil {
			return parent, e
		}
		if e = c.verifyProgressiveService(ctx, api, parent.Plan); e != nil {
			complete = false
		}
	}
	for _, resource := range resources {
		found := false
		for _, observation := range observations {
			if observation.Action.Intent.Resource == resource.ID && observation.Action.Intent.Verb != "delete" && (observation.Receipt.Health.State == "ready" || observation.Receipt.Health.State == "declared") {
				found = true
			}
		}
		if !found && (resource.Probe || resource.Ephemeral) {
			for _, fact := range facts {
				if fact.Intent.Resource == resource.ID && fact.Receipt != nil && (fact.Receipt.Health.State == "ready" || fact.Receipt.Health.State == "declared") && fact.State == "verified" && fact.Intent.Verb != "delete" {
					found = true
				}
			}
		}
		if resource.Probe || resource.Ephemeral || resource.Delete {
			removed := false
			for _, observation := range observations {
				if observation.Action.Intent.Resource == resource.ID && observation.Action.Intent.Verb == "delete" && observation.Receipt.Deleted {
					removed = true
				}
			}
			for _, fact := range facts {
				if fact.Intent.Resource == resource.ID && fact.Intent.Verb == "delete" && fact.State == "verified" && fact.Receipt != nil && fact.Receipt.Deleted {
					removed = true
				}
			}
			if resource.Delete {
				found = removed
			} else {
				found = found && removed
			}
		}
		complete = complete && found
	}
	for _, check := range checks {
		complete = complete && (check.Health.State == "ready" || check.Health.State == "declared")
	}
	if parent.Plan.Helm != nil {
		id := parent.Plan.Helm.StorageAnchor
		id.Name += ".v" + strconv.Itoa(parent.Plan.Helm.Revision)
		object, e := readReleaseObject(ctx, api, id)
		if e != nil {
			return parent, e
		}
		decoded, e := decodeHelmRelease(object)
		complete = complete && e == nil && releaseHelmStorageAllowed(parent.Plan, id, object) && releaseMap(decoded["info"])["status"] == "deployed"
	}
	commit := ReleaseReconciliationCommit(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	if len(commits) > 1 {
		return parent, ErrInvalid
	}
	if len(commits) == 1 {
		if commits[0] == nil {
			return parent, ErrInvalid
		}
		commit = commits[0]
	}
	resultErr = commit(ctx, func(ctx context.Context) error {
		var e error
		out, e = c.store.applyReleaseReconciliation(ctx, parent, auth, observations, checks, complete)
		return e
	})
	return out, resultErr
}
func (s *Service) applyReleaseReconciliation(ctx context.Context, parent ReleaseRecord, auth ReleaseAuthorization, observations []releaseReconciliation, checks []ReleaseCheckReceipt, complete bool) (out ReleaseRecord, resultErr error) {
	resultErr = s.write(ctx, func(ctx context.Context, q dbtx) error {
		current, e := s.release(ctx, q, parent.ID)
		if e != nil {
			return e
		}
		if e = s.validateReleaseAuthorization(ctx, q, auth); e != nil {
			return e
		}
		if current.Revision != parent.Revision {
			return ErrConflict
		}
		if !current.OwnerFenced || current.Cleanup != CleanupVerified {
			return ErrOwner
		}
		for _, observation := range observations {
			action, e := s.action(ctx, q, observation.Action.ID)
			if e != nil {
				return e
			}
			receipt := observation.Receipt
			if action.ReleaseID != parent.ID || action.Revision != observation.Action.Revision || action.State == "rejected" || receipt.Resource != action.Intent.Resource || receipt.UID == "" || receipt.ResourceVersion == "" {
				return ErrConflict
			}
			if action.Intent.ExpectedUID != "" && action.Intent.ExpectedUID != receipt.UID || action.Receipt != nil && action.Receipt.UID != "" && action.Receipt.UID != receipt.UID {
				return ErrSuperseded
			}
			if action.Intent.Verb == "delete" {
				if !receipt.Deleted || receipt.UID != action.Intent.ExpectedUID {
					return ErrConflict
				}
			} else {
				var want map[string]any
				if json.Unmarshal(action.Intent.Body, &want) != nil {
					return ErrInvalid
				}
				delete(releaseMap(want["metadata"]), "resourceVersion")
				if releaseMap(releaseMap(receipt.Object["metadata"])["annotations"])[ReleaseActionAnnotation] != action.ID || !releaseApprovedActionFieldsMatch(action.Intent, want, receipt.Object) || !releaseIntentExpectedObserved(parent.Plan, action.Intent, receipt.Object) {
					return ErrSuperseded
				}
			}
			if action.Receipt != nil {
				receipt.Checks = action.Receipt.Checks
			}
			action.Receipt = &receipt
			action.Receipt.ObservedAt = s.now().UnixMilli()
			if e = s.saveObservedReceipt(ctx, q, &action); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, `UPDATE k8s_release_actions SET state='verified' WHERE id=?`, action.ID); e != nil {
				return e
			}
		}
		for _, check := range checks {
			found := false
			for _, resource := range parent.Plan.Verify {
				if resource.ID == check.Resource && resource.ExpectedUID == check.UID {
					health := EvaluateReleaseHealth(resource, check.Object, check.Related)
					if !ReleaseObjectMatches(map[string]any{"state": health.State, "reason": health.Reason}, map[string]any{"state": check.Health.State, "reason": check.Health.Reason}) {
						return ErrConflict
					}
					found = true
				}
			}
			if !found {
				return ErrConflict
			}
		}
		var unknown, verified int
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state IN ('submitted','outcome_unknown')`, parent.ID).Scan(&unknown); e != nil {
			return e
		}
		if e = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_release_actions WHERE release_id=? AND state='verified'`, parent.ID).Scan(&verified); e != nil {
			return e
		}
		state := "failed"
		if verified > 0 {
			state = "partial_failed"
		}
		retainedRollback := current.Plan.RollbackOf != "" && len(current.Plan.Retained) > 0
		if complete && verified > 0 && !retainedRollback {
			state = "ready"
			if parent.Plan.RollbackOf != "" {
				state = "rolled_back"
			}
		}
		if unknown > 0 {
			state = "outcome_unknown"
		}
		// A successful business fact is not overwritten by a later cleanup inspection.
		if parent.State == "ready" && complete && unknown == 0 && !retainedRollback {
			state = "ready"
		}
		if parent.State == "rolled_back" && complete && unknown == 0 && !retainedRollback {
			state = "rolled_back"
		}
		if len(checks) > 0 && verified > 0 {
			var id string
			if e = q.QueryRowContext(ctx, `SELECT id FROM k8s_release_actions WHERE release_id=? AND state='verified' ORDER BY created_at,id LIMIT 1`, parent.ID).Scan(&id); e != nil {
				return e
			}
			action, e := s.action(ctx, q, id)
			if e != nil {
				return e
			}
			action.Receipt.Checks = checks
			if e = s.saveObservedReceipt(ctx, q, &action); e != nil {
				return e
			}
		}
		reason := "resource_reconciled"
		if retainedRollback && state == "partial_failed" {
			reason = "rollback_partial"
		}
		if _, e = q.ExecContext(ctx, `UPDATE k8s_releases SET state=?,reason_code=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, state, reason, s.now().UnixMilli(), parent.ID, parent.Revision); e != nil {
			return e
		}
		if parent.Plan.RollbackOf != "" && (state == "rolled_back" || state == "partial_failed") {
			if _, e = q.ExecContext(ctx, `UPDATE k8s_releases SET state=?,reason_code=?,revision=revision+1,updated_at=? WHERE id=? AND state='rollback_in_progress'`, state, reason, s.now().UnixMilli(), parent.Plan.RollbackOf); e != nil {
				return e
			}
		}
		out, e = s.release(ctx, q, parent.ID)
		return e
	})
	if resultErr == nil && out.State != "outcome_unknown" {
		resultErr = s.ReleaseGates(ctx, out.ID, out.OwnerEpoch)
	}
	return out, resultErr
}

// CleanupRelease only removes exact leased tools. Cluster resources and their
// business results are deliberately independent from this operation.
func (c *ReleaseController) CleanupRelease(ctx context.Context, id string, expected int64) (ReleaseRecord, error) {
	parent, e := c.store.GetRelease(ctx, id)
	if e != nil {
		return parent, e
	}
	if parent.Revision != expected {
		return parent, ErrConflict
	}
	if !parent.OwnerFenced {
		return parent, ErrOwner
	}
	rows, e := configstate.Queryer(ctx, c.store.db).QueryContext(ctx, `SELECT id FROM k8s_release_runtime WHERE release_id=? AND cleanup_state<>'verified' ORDER BY created_at,id`, id)
	if e != nil {
		return parent, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return parent, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return parent, e
	}
	for _, id := range ids {
		if _, e = c.runtime.CleanupReleaseRuntime(ctx, id); e != nil {
			return parent, e
		}
	}
	parent, e = c.store.GetRelease(ctx, id)
	if e == nil && parent.Cleanup == CleanupVerified && parent.State != "paused" && parent.State != "resume_requested" && parent.State != "outcome_unknown" {
		e = c.store.ReleaseGates(ctx, id, parent.OwnerEpoch)
	}
	return parent, e
}

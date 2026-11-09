package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

func releaseBaselineMatches(baseline, current map[string]any) bool {
	if releaseMap(current["metadata"])["uid"] != releaseMap(baseline["metadata"])["uid"] {
		return false
	}
	for _, key := range []string{ReleaseActionAnnotation, OperationAnnotation} {
		if releaseMap(releaseMap(current["metadata"])["annotations"])[key] != releaseMap(releaseMap(baseline["metadata"])["annotations"])[key] {
			return false
		}
	}
	left, right := releaseCanonicalObject(baseline), releaseCanonicalObject(current)
	if !ReleaseObjectMatches(left, right) {
		return false
	}
	for _, key := range []string{"data", "binaryData", "stringData"} {
		if !releaseObjectMatchesAt(left[key], right[key], "/"+key, releaseNativeListSchema(left, right)) || !releaseObjectMatchesAt(right[key], left[key], "/"+key, releaseNativeListSchema(left, right)) {
			return false
		}
	}
	if baseline["spec"] != nil || current["spec"] != nil {
		return releaseObjectMatchesAt(left["spec"], right["spec"], "/spec", releaseNativeListSchema(left, right)) && releaseObjectMatchesAt(right["spec"], left["spec"], "/spec", releaseNativeListSchema(left, right))
	}
	return true
}
func releaseResourceBaselineMatches(resource ReleaseResource, baseline, current map[string]any) bool {
	if !releaseResourceGVKMatches(resource.ID, resource.Object, baseline, current) {
		return false
	}
	if resource.ImageOnly {
		baseline = releaseObjectCopy(baseline)
		current = releaseObjectCopy(current)
		delete(releaseMap(baseline["spec"]), "replicas")
		delete(releaseMap(current["spec"]), "replicas")
	}
	return releaseBaselineMatches(baseline, current)
}
func (c *ReleaseController) executeFrozenResource(ctx context.Context, api releaseAPI, parent ReleaseRecord, resource ReleaseResource, step string) (ReleaseAction, error) {
	facts, e := c.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return ReleaseAction{}, e
	}
	for _, fact := range facts {
		if fact.Intent.StepKey == step {
			if fact.State == "verified" {
				return fact, nil
			}
			return fact, ErrOutcomeUnknown
		}
	}
	current, e := readReleaseObject(ctx, api, resource.ID)
	var baseline map[string]any
	for _, original := range parent.Plan.Baseline {
		if original.ID == resource.ID {
			baseline = original.Object
		}
	}
	if e == nil {
		if releaseMap(current["metadata"])["deletionTimestamp"] != nil {
			return ReleaseAction{}, ErrSuperseded
		}
		marker := releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation]
		for _, fact := range facts {
			if fact.ID == marker && fact.Intent.Resource == resource.ID && fact.State == "verified" && fact.Receipt != nil {
				baseline = fact.Receipt.Object
				break
			}
		}
		if baseline == nil || !releaseResourceBaselineMatches(resource, baseline, current) {
			return ReleaseAction{}, ErrSuperseded
		}
	} else if !errors.Is(e, ErrNotFound) {
		return ReleaseAction{}, e
	} else if baseline != nil {
		return ReleaseAction{}, ErrSuperseded
	}
	if e := releaseMutationCurrent(parent, resource, current, facts); e != nil {
		return ReleaseAction{}, e
	}
	object := releaseMutationObject(resource)
	meta := releaseMap(object["metadata"])
	intent := ReleaseActionIntent{MutationVersion: parent.Plan.MutationVersion, Version: 2, ParentID: parent.PlanID, StepKey: step, Resource: resource.ID, Verb: "create", AuthorizationFingerprint: parent.Plan.Authorization.Fingerprint}
	name := ""
	if current != nil {
		intent.Verb = "patch"
		name = resource.ID.Name
		intent.ExpectedUID = releaseString(releaseMap(current["metadata"])["uid"])
		intent.ExpectedRV = releaseString(releaseMap(current["metadata"])["resourceVersion"])
		meta["uid"] = intent.ExpectedUID
		meta["resourceVersion"] = intent.ExpectedRV
	}
	if resource.Mutation != nil {
		intent.ContentType = resource.Mutation.ContentType
	}
	intent.Body, _ = json.Marshal(object)
	defer clear(intent.Body)
	path, e := resourceAPI(resource.ID).Path(resource.ID.Namespace, name)
	if e != nil {
		return ReleaseAction{}, e
	}
	fresh, e := c.store.GetRelease(ctx, parent.ID)
	if e != nil {
		return ReleaseAction{}, e
	}
	return api.ExecuteAction(ctx, parent.ID, parent.OwnerEpoch, fresh.Revision, intent, path)
}
func (c *ReleaseController) observePrimaryResources(ctx context.Context, api releaseAPI, parent ReleaseRecord, resources []ReleaseResource) error {
	facts, e := c.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	for _, resource := range resources {
		if resource.Probe || resource.Delete {
			continue
		}
		deletedComplete := false
		for _, fact := range facts {
			if fact.Intent.Resource == resource.ID && fact.Intent.Verb == "delete" && fact.Receipt != nil && fact.Receipt.Deleted {
				for _, created := range facts {
					if created.Intent.Resource == resource.ID && created.Intent.Verb != "delete" && created.Receipt != nil && created.Receipt.Health.State == "ready" {
						deletedComplete = true
						break
					}
				}
			}
		}
		if deletedComplete {
			continue
		}
		if _, e = c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds); e != nil {
			return e
		}
	}
	return nil
}
func (c *ReleaseController) observePositiveControl(ctx context.Context, api releaseAPI, parent ReleaseRecord, resource ReleaseResource, resources []ReleaseResource) error {
	if resource.PositiveControl == "" {
		return nil
	}
	for _, control := range resources {
		if control.Probe && control.ID.Namespace == resource.ID.Namespace && control.ID.Name == resource.PositiveControl {
			receipt, e := c.observeResource(ctx, api, control, 0)
			if e != nil {
				return e
			}
			facts, e := c.store.ListReleaseActions(ctx, parent.ID)
			if e != nil {
				return e
			}
			marker := releaseMap(releaseMap(receipt.Object["metadata"])["annotations"])[ReleaseActionAnnotation]
			for _, fact := range facts {
				if fact.ID == marker && fact.Intent.Resource == control.ID && fact.State == "verified" {
					_, e = c.store.UpdateReleaseActionHealth(ctx, fact.ID, parent.OwnerEpoch, fact.Revision, receipt)
					return e
				}
			}
			return ErrSuperseded
		}
	}
	return ErrInvalid
}
func (c *ReleaseController) cleanupReleaseProbes(ctx context.Context, api releaseAPI, parent ReleaseRecord, resources []ReleaseResource) error {
	for index := len(resources) - 1; index >= 0; index-- {
		resource := resources[index]
		if !resource.Probe && !resource.Ephemeral {
			continue
		}
		current, e := readReleaseObject(ctx, api, resource.ID)
		facts, factsErr := c.store.ListReleaseActions(ctx, parent.ID)
		if factsErr != nil {
			return factsErr
		}
		if errors.Is(e, ErrNotFound) {
			created := false
			deleted := false
			for _, fact := range facts {
				if fact.Intent.Resource == resource.ID && fact.State == "verified" {
					if fact.Intent.Verb == "delete" && fact.Receipt != nil && fact.Receipt.Deleted {
						deleted = true
					} else if fact.Intent.Verb != "delete" {
						created = true
					}
				}
			}
			if !created || deleted {
				continue
			}
		}
		if e != nil {
			return e
		}
		facts, e = c.store.ListReleaseActions(ctx, parent.ID)
		if e != nil {
			return e
		}
		marker := releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation]
		owned := false
		for _, fact := range facts {
			if fact.ID == marker && releaseReuseFact(parent, fact) && fact.Receipt != nil && (fact.Receipt.Health.State == "ready" || resource.Ephemeral || resource.Probe && resource.ID.Group == "batch" && resource.ID.Kind == "Job") && fact.Intent.Resource == resource.ID && releaseBaselineMatches(fact.Receipt.Object, current) {
				owned = true
				break
			}
		}
		if !owned {
			return ErrSuperseded
		}
		uid, rv := releaseString(releaseMap(current["metadata"])["uid"]), releaseString(releaseMap(current["metadata"])["resourceVersion"])
		body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": uid, "resourceVersion": rv}})
		intent := ReleaseActionIntent{MutationVersion: parent.Plan.MutationVersion, Version: 2, ParentID: parent.PlanID, StepKey: "batch-" + strconvI(parent.Cursor) + "-probe-cleanup-" + strconvI(index), Resource: resource.ID, Verb: "delete", Body: body, ExpectedUID: uid, ExpectedRV: rv, AuthorizationFingerprint: parent.Plan.Authorization.Fingerprint}
		path, _ := resourceAPI(resource.ID).Path(resource.ID.Namespace, resource.ID.Name)
		fresh, e := c.store.GetRelease(ctx, parent.ID)
		if e != nil {
			return e
		}
		action, e := api.ExecuteAction(ctx, parent.ID, parent.OwnerEpoch, fresh.Revision, intent, path)
		clear(body)
		if e != nil {
			return e
		}
		for {
			object, e := readReleaseObject(ctx, api, resource.ID)
			if errors.Is(e, ErrNotFound) {
				if _, e = c.store.RecordReleaseDeletion(ctx, action.ID, parent.OwnerEpoch, action.Revision); e != nil {
					return e
				}
				break
			}
			if e != nil {
				return e
			}
			if releaseMap(object["metadata"])["uid"] != uid {
				return ErrSuperseded
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.poll):
			}
		}
	}
	return nil
}
func (c *ReleaseController) recordReleaseVerifications(ctx context.Context, api releaseAPI, parent ReleaseRecord) error {
	if len(parent.Plan.Verify) == 0 {
		return nil
	}
	checks := []ReleaseCheckReceipt{}
	for _, resource := range parent.Plan.Verify {
		if resource.Health == "custom" {
			if e := c.verifyCustomPrerequisites(ctx, api, parent, resource); e != nil {
				return e
			}
		}
		receipt, e := c.observeResource(ctx, api, resource, parent.Plan.ObserveSeconds)
		if e != nil {
			return e
		}
		related, e := c.related(ctx, api, resource)
		if e != nil {
			return e
		}
		checks = append(checks, ReleaseCheckReceipt{Related: related, Resource: resource.ID, UID: receipt.UID, Object: receipt.Object, Health: receipt.Health, ObservedAt: time.Now().UnixMilli()})
	}
	return c.store.RecordReleaseChecks(ctx, parent.ID, parent.OwnerEpoch, checks)
}

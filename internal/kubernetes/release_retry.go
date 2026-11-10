package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
)

// PrepareRetry reuses the prior sealed bytes/variables/images/chart/values.
// It never re-resolves a moving tag or reveals a generic variable credential.
// Fresh cluster baselines must be either unchanged originals or exact effects
// of the prior positively fenced, cleaned writer.
func (c *ReleaseController) PrepareRetry(ctx context.Context, run, node, owner, previous string, catalog ReleaseAuthorization) (out ReleaseRecord, resultErr error) {
	prior, e := c.store.GetRelease(ctx, previous)
	if e != nil {
		return out, e
	}
	if !prior.OwnerFenced || prior.Cleanup != CleanupVerified || prior.State == "outcome_unknown" || prior.State == "paused" || prior.State == "resume_requested" {
		return out, ErrHeld
	}
	if prior.Plan.Authorization.Request.ProjectID != catalog.Request.ProjectID || prior.Plan.Authorization.Request.Environment != catalog.Request.Environment || prior.Plan.Authorization.ClusterKey != catalog.ClusterKey || prior.Plan.Authorization.Request.ConnectionID != catalog.Request.ConnectionID || prior.Plan.Authorization.Request.CredentialID != catalog.Request.CredentialID || prior.Plan.Authorization.Request.Executor != catalog.Request.Executor {
		return out, ErrDenied
	}
	if e = c.store.ValidateReleaseAuthorization(ctx, prior.Plan.Authorization); e != nil {
		return out, e
	}
	facts, e := c.store.ListReleaseActions(ctx, prior.ID)
	if e != nil {
		return out, e
	}
	for _, fact := range facts {
		if fact.State == "outcome_unknown" || fact.State == "submitted" {
			return out, ErrOutcomeUnknown
		}
	}
	if prior.Plan.Helm != nil {
		return c.PrepareHelm(ctx, run, node, owner, catalog, prior.Plan.Helm.Namespace, prior.Plan.Helm.Name, prior.Plan.Chart, prior.Plan.Values, prior.Plan.Helm.Hooks, prior.Plan.Config, prior.Plan.TimeoutSeconds, prior.Plan.ObserveSeconds)
	}
	raw, e := json.Marshal(prior.Plan)
	if e != nil {
		return out, e
	}
	defer clear(raw)
	var plan ReleasePlan
	if json.Unmarshal(raw, &plan) != nil {
		return out, ErrDecrypt
	}
	plan.RetryOf = prior.ID
	if plan.Mode == "progressive" {
		if prior.Cursor >= len(plan.Batches) {
			return out, ErrInvalid
		}
		plan.Batches = plan.Batches[prior.Cursor:]
	}
	api, e := c.preview(ctx, catalog, owner)
	if e != nil {
		return out, e
	}
	defer func() {
		if api != nil {
			_, e := api.Close(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, e)
		}
	}()
	plan.Baseline = nil
	request := prior.Plan.Authorization.Request
	for _, resource := range plan.Resources {
		current, e := readReleaseObject(ctx, api, resource.ID)
		verb := "create"
		if e == nil {
			if resource.Probe {
				return out, ErrHeld
			}
			verb = "patch"
			owned := false
			marker := releaseMap(releaseMap(current["metadata"])["annotations"])[ReleaseActionAnnotation]
			for _, fact := range facts {
				if fact.ID == marker && fact.Intent.Resource == resource.ID && fact.State == "verified" && fact.Receipt != nil && releaseResourceBaselineMatches(resource, fact.Receipt.Object, current) {
					owned = true
					break
				}
			}
			if !owned {
				for _, baseline := range prior.Plan.Baseline {
					if baseline.ID == resource.ID && releaseResourceBaselineMatches(resource, baseline.Object, current) {
						owned = true
						break
					}
				}
			}
			if !owned || releaseMap(current["metadata"])["deletionTimestamp"] != nil {
				return out, ErrSuperseded
			}
			baseline := releaseResourceFromObject(resource.ID, current, resource.Health)
			if e = c.preserveRetryRollbackImages(ctx, api, prior, resource, current, &baseline); e != nil {
				return out, e
			}
			plan.Baseline = append(plan.Baseline, baseline)
		} else if !errors.Is(e, ErrNotFound) {
			return out, e
		} else {
			for _, baseline := range prior.Plan.Baseline {
				if baseline.ID == resource.ID {
					return out, ErrSuperseded
				}
			}
			for _, fact := range facts {
				if fact.Intent.Resource != resource.ID || fact.State != "verified" || fact.Intent.Verb == "delete" {
					continue
				}
				deleted := false
				for _, cleanup := range facts {
					if cleanup.Intent.Resource == resource.ID && cleanup.Intent.Verb == "delete" && cleanup.State == "verified" && cleanup.Receipt != nil && cleanup.Receipt.Deleted && fact.Receipt != nil && cleanup.Intent.ExpectedUID == fact.Receipt.UID {
						deleted = true
					}
				}
				if !deleted {
					return out, ErrSuperseded
				}
			}
		}
		for index := range request.Resources {
			scope := &request.Resources[index]
			if scope.ID != resource.ID {
				continue
			}
			actions := []string{}
			for _, action := range scope.Actions {
				if action != "create" && action != "patch" && action != "update" {
					actions = append(actions, action)
				}
			}
			actions = append(actions, verb)
			if plan.Mode == "progressive" && resource.ID == plan.Progressive.Candidate {
				actions = append(actions, "patch")
			}
			scope.Actions = uniqueReleaseActions(actions)
		}
	}
	if plan.Progressive != nil {
		// A retained, verified source candidate now has a real UID. Its old
		// future-create recipe belongs to the source plan, not this retry.
		// Re-derive every remaining patch from the sealed source creation and
		// current owned receipt through ownedPostMutation; preserve frozen goals.
		for _, baseline := range plan.Baseline {
			if baseline.ID != plan.Progressive.Candidate {
				continue
			}
			uid := releaseString(releaseMap(baseline.Object["metadata"])["uid"])
			if uid == "" {
				return out, ErrSuperseded
			}
			for batchIndex := range plan.Batches {
				for index := range plan.Batches[batchIndex] {
					resource := &plan.Batches[batchIndex][index]
					if resource.ID == baseline.ID {
						resource.Mutation = nil
						resource.ExpectedUID = uid
					}
				}
			}
			for index := range plan.Resources {
				if plan.Resources[index].ID == baseline.ID {
					plan.Resources[index].Mutation = nil
					plan.Resources[index].ExpectedUID = uid
				}
			}
		}
	}
	if plan.Progressive != nil {
		service, e := readReleaseObject(ctx, api, plan.Progressive.Service.ID)
		if e != nil {
			return out, e
		}
		if !releaseBaselineMatches(plan.Progressive.Service.Object, service) {
			return out, ErrSuperseded
		}
	}
	plan.Authorization, e = c.store.AuthorizeRelease(ctx, request)
	if e != nil {
		return out, e
	}
	if _, e = api.Close(context.WithoutCancel(ctx)); e != nil {
		return out, e
	}
	api = nil
	if e = c.dryRunReleaseResources(ctx, owner, &plan); e != nil {
		return out, e
	}
	return c.store.CreateRelease(ctx, run, node, owner, plan)
}

package kubernetes

import "context"

func (c *ReleaseController) runHelmSupplementalChecks(ctx context.Context, api releaseAPI, parent ReleaseRecord) error {
	factsBeforeChecks, e := c.store.ListReleaseActions(ctx, parent.ID)
	if e != nil {
		return e
	}
	// Helm may already have deleted a successful hook. Confirm exact-UID
	// absence before primary readiness gates or supplemental probes inspect it.
	for _, resource := range parent.Plan.Resources {
		if releaseMap(releaseMap(resource.Object["metadata"])["annotations"])["helm.sh/hook"] == nil {
			continue
		}
		var last *ReleaseAction
		for i := range factsBeforeChecks {
			if factsBeforeChecks[i].Intent.Resource == resource.ID {
				last = &factsBeforeChecks[i]
			}
		}
		if last == nil || last.Intent.Verb != "delete" {
			continue
		}
		if last.State != "verified" || last.Receipt == nil {
			return ErrOutcomeUnknown
		}
		complete := false
		for _, created := range factsBeforeChecks {
			if created.Intent.Resource == resource.ID && created.Intent.Verb != "delete" && created.State == "verified" && created.Receipt != nil && created.Receipt.UID == last.Intent.ExpectedUID && created.Receipt.Health.State == "ready" {
				complete = true
			}
		}
		if !complete {
			return ErrRollout
		}
		if !last.Receipt.Deleted {
			if e := c.observeReleaseDeletion(ctx, api, parent, *last); e != nil {
				return e
			}
		}
	}
	byStep := map[string]ReleaseAction{}
	for _, fact := range factsBeforeChecks {
		byStep[fact.Intent.StepKey] = fact
	}
	for index, resource := range parent.Plan.Resources {
		if !resource.Supplemental || resource.HelmCRD {
			continue
		}
		if resource.Probe {
			if e := c.ensureStorageProbeConsumersWithSteps(ctx, api, parent, parent.Plan.Resources, byStep, func(i int) string { return "check-" + strconvI(i) }); e != nil {
				return e
			}
			if e := c.observePrimaryResources(ctx, api, parent, parent.Plan.Resources); e != nil {
				return e
			}
			if e := c.observePositiveControl(ctx, api, parent, resource, parent.Plan.Resources); e != nil {
				return e
			}
		}
		step := "check-" + strconvI(index)
		if prior, ok := byStep[step]; ok {
			if prior.State != "verified" {
				return ErrOutcomeUnknown
			}
			continue
		}
		if _, e := c.executeFrozenResource(ctx, api, parent, resource, step); e != nil {
			return e
		}
	}
	return nil
}

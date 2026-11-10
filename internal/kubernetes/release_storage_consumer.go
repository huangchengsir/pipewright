package kubernetes

import (
	"context"
	"encoding/json"
)

func releaseStorageProbeClaim(resource ReleaseResource) string {
	if !resource.Probe || resource.ID.Group != "batch" || resource.ID.Kind != "Job" {
		return ""
	}
	pod := releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["spec"])
	containers := releaseItems(pod["containers"])
	if len(containers) != 1 {
		return ""
	}
	command := releaseItems(releaseMap(containers[0])["command"])
	if len(command) != 3 || command[1] != "probe" {
		return ""
	}
	var check struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(releaseString(command[2])), &check) != nil || check.Kind != "storage" {
		return ""
	}
	for _, raw := range releaseItems(pod["volumes"]) {
		volume := releaseMap(raw)
		if volume["name"] == "data" {
			return releaseString(releaseMap(volume["persistentVolumeClaim"])["claimName"])
		}
	}
	return ""
}

// Storage checks are approved consumers, not mere observations. Submit them
// before any barrier that waits for their WFFC claims. Original step IDs and
// the normal ledger/permission/unknown fence are retained; no new Job is added.
func (c *ReleaseController) ensureStorageProbeConsumers(ctx context.Context, api releaseAPI, parent ReleaseRecord, resources []ReleaseResource, byStep map[string]ReleaseAction) error {
	return c.ensureStorageProbeConsumersWithSteps(ctx, api, parent, resources, byStep, func(index int) string { return "batch-" + strconvI(parent.Cursor) + "-resource-" + strconvI(index) })
}
func (c *ReleaseController) ensureStorageProbeConsumersWithSteps(ctx context.Context, api releaseAPI, parent ReleaseRecord, resources []ReleaseResource, byStep map[string]ReleaseAction, stepKey func(int) string) error {
	for index, resource := range resources {
		claimName := releaseStorageProbeClaim(resource)
		if claimName == "" {
			continue
		}
		if resource.PositiveControl != "" {
			return ErrInvalid
		}
		step := stepKey(index)
		if prior, ok := byStep[step]; ok {
			if prior.State == "rejected" {
				return ErrDenied
			}
			if prior.State != "verified" {
				return ErrOutcomeUnknown
			}
			continue
		}
		claimID := ReleaseResourceID{Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespace: resource.ID.Namespace, Name: claimName}
		claim, e := readReleaseObject(ctx, api, claimID)
		if e != nil {
			return e
		}
		meta := releaseMap(claim["metadata"])
		if releaseString(meta["uid"]) == "" || meta["deletionTimestamp"] != nil || releaseMap(claim["spec"])["volumeMode"] == "Block" {
			return ErrSuperseded
		}
		for _, verified := range parent.Plan.Verify {
			if verified.ID == claimID && (verified.ExpectedUID != meta["uid"] || !releaseResourceExpectedMatches(verified, claim)) {
				return ErrSuperseded
			}
		}
		facts, e := c.store.ListReleaseActions(ctx, parent.ID)
		if e != nil {
			return e
		}
		for _, fact := range facts {
			if fact.Intent.Resource == claimID && fact.Intent.Verb != "delete" && fact.State == "verified" && fact.Receipt != nil && fact.Receipt.UID != meta["uid"] {
				return ErrSuperseded
			}
		}
		fact, e := c.executeFrozenResource(ctx, api, parent, resource, step)
		if e != nil {
			return e
		}
		byStep[step] = fact
	}
	return nil
}

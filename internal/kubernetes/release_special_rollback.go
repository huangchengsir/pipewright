package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/pipeline"
)

func rollbackOwnedCurrent(ctx context.Context, api releaseAPI, source ReleaseRecord, facts []ReleaseAction, resource ReleaseResource) (map[string]any, error) {
	current, e := readReleaseObject(ctx, api, resource.ID)
	if e != nil {
		return nil, e
	}
	meta := releaseMap(current["metadata"])
	if meta["deletionTimestamp"] != nil {
		return nil, ErrSuperseded
	}
	marker := releaseMap(meta["annotations"])[ReleaseActionAnnotation]
	for _, fact := range facts {
		if fact.ID == marker && fact.State == "verified" && fact.Intent.Verb != "delete" && fact.Receipt != nil && fact.Intent.Resource == resource.ID && releaseResourceBaselineMatches(resource, fact.Receipt.Object, current) {
			return current, nil
		}
	}
	return nil, ErrSuperseded
}
func (c *ReleaseController) prepareSpecialRollback(ctx context.Context, run, node, owner string, source ReleaseRecord, commit func(context.Context, ReleasePlan) (ReleaseRecord, error)) (out ReleaseRecord, resultErr error) {
	facts, e := c.store.ListReleaseActions(ctx, source.ID)
	if e != nil {
		return out, e
	}
	for _, fact := range facts {
		if fact.State == "submitted" || fact.State == "outcome_unknown" {
			return out, ErrOutcomeUnknown
		}
	}
	catalog, e := c.store.ReleasePolicyCatalog(ctx, source.Plan.Authorization.Request)
	if e != nil {
		return out, e
	}
	if catalog.ClusterKey != source.Plan.Authorization.ClusterKey {
		return out, ErrDenied
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
	var cfg pipeline.KubernetesReleaseConfig
	if json.Unmarshal(source.Plan.Config, &cfg) != nil {
		return out, ErrInvalid
	}
	cfg.Variables = nil
	conditions := []pipeline.KubernetesReleaseCheck{}
	for _, check := range cfg.Checks {
		if check.Kind == "condition" {
			conditions = append(conditions, check)
		}
	}
	cfg.Checks = conditions
	var files []ReleaseInputFile
	var helm *ReleaseHelmPlan
	if source.Plan.CoreDNS != nil {
		live := source.Plan.Resources[source.Plan.CoreDNS.LiveIndex]
		if source.Plan.CoreDNS.Finalize != nil {
			live = *source.Plan.CoreDNS.Finalize
		}
		if _, e = rollbackOwnedCurrent(ctx, api, source, facts, live); e != nil {
			return out, e
		}
		var baseline *ReleaseResource
		for i := range source.Plan.Baseline {
			if source.Plan.Baseline[i].ID == live.ID {
				baseline = &source.Plan.Baseline[i]
			}
		}
		if baseline == nil || cfg.CoreDNS == nil {
			return out, ErrRollbackBaseline
		}
		body, e := json.Marshal(releaseCanonicalObject(baseline.Object))
		if e != nil {
			return out, e
		}
		files = []ReleaseInputFile{{Path: "rollback-config.json", Content: body, Digest: releaseByteDigest(body)}}
		reference := checkResourceID(cfg.CoreDNS.ServiceResource, cfg.CoreDNS.ServiceResource.Namespace)
		service, e := readReleaseObject(ctx, api, reference)
		if e != nil {
			return out, e
		}
		ip := releaseString(releaseMap(service["spec"])["clusterIP"])
		cfg.CoreDNS.Records = []pipeline.KubernetesDNSRecord{{Name: cfg.CoreDNS.ServiceDomain, Expected: ip}}
	} else {
		previous := source.Plan.Helm
		if previous == nil || previous.Revision < 2 {
			return out, ErrRollbackBaseline
		}
		latestID := previous.StorageAnchor
		latestID.Name += ".v" + strconvI(previous.Revision)
		latest, e := rollbackOwnedCurrent(ctx, api, source, facts, ReleaseResource{ID: latestID})
		if e != nil {
			return out, e
		}
		if !releaseHelmStorageAllowed(source.Plan, latestID, latest) {
			return out, ErrSuperseded
		}
		var target map[string]any
		storage := []ReleaseResource{}
		for _, prior := range previous.Storage {
			actual, e := readReleaseObject(ctx, api, prior.ID)
			if e != nil {
				return out, e
			}
			if releaseMap(actual["metadata"])["uid"] != releaseMap(prior.Object["metadata"])["uid"] {
				return out, ErrSuperseded
			}
			approved, e := decodeHelmRelease(prior.Object)
			if e != nil {
				return out, e
			}
			current, e := decodeHelmRelease(actual)
			if e != nil || !ReleaseObjectMatches(canonicalHelmRelease(approved), canonicalHelmRelease(current)) || !ReleaseObjectMatches(canonicalHelmRelease(current), canonicalHelmRelease(approved)) {
				return out, ErrSuperseded
			}
			storage = append(storage, releaseResourceFromObject(prior.ID, actual, "declaration"))
			if int(releaseInt(current["version"])) == previous.Revision-1 {
				target = current
			}
		}
		if target == nil {
			return out, ErrRollbackBaseline
		}
		storage = append(storage, releaseResourceFromObject(latestID, latest, "declaration"))
		info := map[string]any{"rollback_revision": previous.Revision - 1}
		if notes := releaseMap(target["info"])["notes"]; notes != nil && notes != "" {
			info["notes"] = notes
		}
		release := releaseObjectCopy(target)
		release["version"] = previous.Revision + 1
		release["info"] = info
		release["apply_method"] = "ssa"
		manifest := releaseString(target["manifest"])
		if previous.Hooks {
			for _, value := range releaseItems(target["hooks"]) {
				manifest += "\n---\n" + releaseString(releaseMap(value)["manifest"])
			}
		}
		files, e = InlineReleaseFile(manifest)
		if e != nil {
			return out, e
		}
		helm = &ReleaseHelmPlan{Name: previous.Name, Namespace: previous.Namespace, Action: "rollback", Revision: previous.Revision + 1, TargetRevision: previous.Revision - 1, Hooks: previous.Hooks, StorageAnchor: previous.StorageAnchor, Storage: storage, Release: release}
		for _, resource := range source.Plan.Resources {
			if resource.Probe || resource.Ephemeral || resource.Supplemental {
				continue
			}
			if resource.Delete {
				if _, e = readReleaseObject(ctx, api, resource.ID); !errors.Is(e, ErrNotFound) {
					return out, ErrSuperseded
				}
				continue
			}
			if completed, err := rollbackCompletedHelmHook(ctx, api, source, facts, resource); err != nil {
				return out, err
			} else if completed {
				continue
			}
			if _, e = rollbackOwnedCurrent(ctx, api, source, facts, resource); e != nil {
				return out, e
			}
		}
	}
	data, e := json.Marshal(cfg)
	if e != nil {
		return out, e
	}
	defer clear(data)
	if _, e = api.Close(context.WithoutCancel(ctx)); e != nil {
		return out, e
	}
	api = nil
	customize := func(plan *ReleasePlan, request *ReleaseAuthorizationRequest) error {
		plan.RollbackOf = source.ID
		plan.RollbackRevision = source.Revision
		plan.RollbackPlanID = source.PlanID
		plan.ForceApproval = true
		for i := range request.Resources {
			scope := &request.Resources[i]
			scope.Actions = uniqueReleaseActions(append(scope.Actions, "rollback"))
		}
		return nil
	}
	return c.prepareManifestWithCommit(ctx, run, node, owner, catalog, files, nil, data, source.Plan.TimeoutSeconds, source.Plan.ObserveSeconds, helm, nil, nil, customize, commit)
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
)

type ReleaseProgressivePlan struct {
	Service   ReleaseResource   `json:"service"`
	Stable    ReleaseResourceID `json:"stable"`
	Candidate ReleaseResourceID `json:"candidate"`
	Total     int64             `json:"total"`
}
type ReleaseProgressiveInput struct {
	Namespace, Stable, Candidate, Service, TrackKey, TrackValue string
	Batches                                                     []int
	Images                                                      []ReleaseImageUpdate
}

func releaseLabelsMatch(selector, labels map[string]any) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
func (c *ReleaseController) PrepareProgressive(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, in ReleaseProgressiveInput, config []byte, timeout, observe int) (out ReleaseRecord, resultErr error) {
	stableID, e := releaseWorkloadID("Deployment", in.Namespace, in.Stable)
	if e != nil {
		return out, e
	}
	candidateID, e := releaseWorkloadID("Deployment", in.Namespace, in.Candidate)
	if e != nil || stableID == candidateID || len(in.Batches) == 0 {
		return out, ErrInvalid
	}
	serviceID := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: in.Namespace, Name: in.Service}
	api, e := c.preview(ctx, auth, owner)
	if e != nil {
		return out, e
	}
	defer func() {
		if api != nil {
			_, e := api.Close(context.WithoutCancel(ctx))
			resultErr = errors.Join(resultErr, e)
		}
	}()
	stable, e := readReleaseObject(ctx, api, stableID)
	if e != nil {
		return out, e
	}
	service, e := readReleaseObject(ctx, api, serviceID)
	if e != nil {
		return out, e
	}
	stableObject := releaseCanonicalObject(stable)
	releaseDropControllerDeploymentRevision(ReleaseResource{ID: stableID, Object: map[string]any{}}, stable, stableObject)
	stableSpec := releaseMap(stableObject["spec"])
	total := releaseInt(stableSpec["replicas"])
	if total < 1 || total > 10000 || int64(in.Batches[len(in.Batches)-1]) != total {
		return out, ErrInvalid
	}
	if e = validateReleaseWorkloadStrategy(ReleaseResource{ID: stableID, Object: stableObject}); e != nil {
		return out, e
	}
	selector := releaseMap(stableSpec["selector"])
	match := releaseMap(selector["matchLabels"])
	if len(releaseItems(selector["matchExpressions"])) != 0 || match[in.TrackKey] == nil || match[in.TrackKey] == in.TrackValue {
		return out, ErrInvalid
	}
	serviceSelector := releaseMap(releaseMap(service["spec"])["selector"])
	labels := releaseMap(releaseMap(releaseMap(stableSpec["template"])["metadata"])["labels"])
	if serviceSelector[in.TrackKey] != nil || !releaseLabelsMatch(serviceSelector, labels) || !releaseLabelsMatch(match, labels) {
		return out, ErrInvalid
	}
	// Explicit related-HPA authority proves absence without disclosing unrelated HPAs.
	raw, e := api.Read(ctx, "/apis/autoscaling/v2/namespaces/"+url.PathEscape(in.Namespace)+"/horizontalpodautoscalers", "limit=200")
	if e != nil {
		return out, e
	}
	var hpas struct {
		Items []map[string]any `json:"items"`
	}
	e = json.Unmarshal(raw, &hpas)
	clear(raw)
	if e != nil {
		return out, ErrInvalid
	}
	for _, hpa := range hpas.Items {
		target := releaseMap(releaseMap(hpa["spec"])["scaleTargetRef"])
		if target["kind"] == "Deployment" && (target["name"] == in.Stable || target["name"] == in.Candidate) {
			return out, ErrDenied
		}
	}
	candidate, e := readReleaseObject(ctx, api, candidateID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return out, e
	}
	candidateExists := e == nil
	candidateObject := releaseObjectCopy(stableObject)
	candidateMeta := releaseMap(candidateObject["metadata"])
	candidateObject["metadata"] = map[string]any{"name": in.Candidate, "namespace": in.Namespace, "labels": candidateMeta["labels"], "annotations": candidateMeta["annotations"]}
	delete(releaseMap(releaseMap(candidateObject["metadata"])["annotations"]), "deployment.kubernetes.io/revision")
	candidateSpec := releaseMap(candidateObject["spec"])
	releaseMap(releaseMap(candidateSpec["selector"])["matchLabels"])[in.TrackKey] = in.TrackValue
	template := releaseMap(candidateSpec["template"])
	releaseMap(releaseMap(template["metadata"])["labels"])[in.TrackKey] = in.TrackValue
	containers := releaseItems(releaseMap(template["spec"])["containers"])
	seen := map[string]bool{}
	for _, update := range in.Images {
		if !ValidImageReference(update.Image) || seen[update.Container] {
			return out, ErrInvalid
		}
		seen[update.Container] = true
		found := false
		for _, v := range containers {
			container := releaseMap(v)
			if container["name"] == update.Container {
				container["image"] = update.Image
				found = true
			}
		}
		if !found {
			return out, ErrInvalid
		}
	}
	if len(seen) == 0 {
		return out, ErrInvalid
	}
	if candidateExists {
		existingSelector := releaseMap(releaseMap(candidate["spec"])["selector"])
		if !ReleaseObjectMatches(candidateSpec["selector"], existingSelector) || !ReleaseObjectMatches(existingSelector, candidateSpec["selector"]) {
			return out, ErrSuperseded
		}
		candidateObject = releaseCanonicalObject(candidate)
		releaseDropControllerDeploymentRevision(ReleaseResource{ID: candidateID, Object: map[string]any{}}, candidate, candidateObject)
		candidateSpec = releaseMap(candidateObject["spec"])
		candidateSpec["template"] = template
	}
	request := auth.Request
	stableAccess := releaseRequestedAccess(stableID, "patch")
	stableAccess.Actions = append(stableAccess.Actions, "scale", "related-hpas")
	stableAccess.Containers = releaseContainerNames(ReleaseResource{ID: stableID, Object: stableObject})
	verb := "create"
	if candidateExists {
		verb = "patch"
	}
	candidateAccess := releaseRequestedAccess(candidateID, verb)
	candidateAccess.Actions = append(candidateAccess.Actions, "patch", "scale", "related-hpas")
	candidateAccess.Actions = uniqueReleaseActions(candidateAccess.Actions)
	candidateAccess.Containers = releaseContainerNames(ReleaseResource{ID: candidateID, Object: candidateObject})
	request.Resources = []ReleaseAccess{stableAccess, candidateAccess, {ID: serviceID, Actions: []string{"get", "related-endpointslices"}}}
	plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "progressive", Config: append([]byte(nil), config...), TimeoutSeconds: timeout, ObserveSeconds: observe, Progressive: &ReleaseProgressivePlan{Stable: stableID, Candidate: candidateID, Total: total, Service: releaseResourceFromObject(serviceID, service, "declaration")}, Baseline: []ReleaseResource{releaseResourceFromObject(stableID, stable, "workload")}}
	if candidateExists {
		plan.Baseline = append(plan.Baseline, releaseResourceFromObject(candidateID, candidate, "workload"))
	}
	for i := range plan.Baseline {
		c.captureRollbackImages(ctx, api, &plan.Baseline[i])
	}
	previous := 0
	for _, count := range in.Batches {
		if count <= previous || int64(count) > total {
			return out, ErrInvalid
		}
		previous = count
		candidateBatch := releaseObjectCopy(candidateObject)
		releaseMap(candidateBatch["spec"])["replicas"] = count
		stableBatch := releaseObjectCopy(stableObject)
		releaseMap(stableBatch["spec"])["replicas"] = total - int64(count)
		batch := []ReleaseResource{releaseResourceFromObject(candidateID, candidateBatch, "workload"), releaseResourceFromObject(stableID, stableBatch, "workload")}

		plan.Batches = append(plan.Batches, batch)
	}
	plan.Resources = plan.Batches[len(plan.Batches)-1]
	extra, e := c.appendReleaseChecks(ctx, api, &plan, auth, config)
	if e != nil {
		return out, e
	}
	for batchIndex := range plan.Batches {
		for resourceIndex := 0; resourceIndex < 2; resourceIndex++ {
			object := releaseObjectCopy(plan.Batches[batchIndex][resourceIndex].Object)
			releaseMap(object["spec"])["template"] = releaseObjectCopy(releaseMap(releaseMap(plan.Resources[resourceIndex].Object["spec"])["template"]))
			plan.Batches[batchIndex][resourceIndex] = releaseResourceFromObject(plan.Resources[resourceIndex].ID, object, "workload")
		}
		for _, resource := range plan.Resources[2:] {
			copy := resource
			copy.Object = releaseObjectCopy(resource.Object)
			plan.Batches[batchIndex] = append(plan.Batches[batchIndex], copy)
		}
	}
	request.Resources = mergeReleaseScopes(request.Resources, extra)
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
func uniqueReleaseActions(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func releaseResourceFromObject(id ReleaseResourceID, object map[string]any, health string) ReleaseResource {
	body, _ := json.Marshal(object)
	return ReleaseResource{ID: id, Object: object, Digest: releaseByteDigest(body), Health: health, Phase: 2}
}
func (c *ReleaseController) verifyProgressiveService(ctx context.Context, api releaseAPI, plan ReleasePlan) error {
	if plan.Progressive == nil {
		return ErrInvalid
	}
	p := plan.Progressive
	service, e := readReleaseObject(ctx, api, p.Service.ID)
	if e != nil {
		return e
	}
	if !releaseBaselineMatches(p.Service.Object, service) {
		return ErrSuperseded
	}
	pods := map[string]bool{}
	for _, id := range []ReleaseResourceID{p.Stable, p.Candidate} {
		object, e := readReleaseObject(ctx, api, id)
		if errors.Is(e, ErrNotFound) && plan.RollbackOf != "" {
			removed := false
			for _, resource := range plan.Resources {
				if resource.ID == id && resource.Delete {
					removed = true
				}
			}
			if removed {
				continue
			}
		}
		if e != nil {
			return e
		}
		related, e := c.related(ctx, api, ReleaseResource{ID: id, Object: object})
		if e != nil {
			return e
		}
		for _, pod := range related {
			if pod["kind"] == "Pod" && releasePodHealth(pod, false).State == "ready" && releasePodSpecMatches(releaseMap(releaseMap(object["spec"])["template"])["spec"], pod["spec"]) {
				pods[releaseString(releaseMap(pod["metadata"])["uid"])] = true
			}
		}
	}
	slices, e := c.related(ctx, api, p.Service)
	if e != nil {
		return e
	}
	ready := map[string]bool{}
	for _, slice := range slices {
		if slice["kind"] != "EndpointSlice" {
			continue
		}
		for _, v := range releaseItems(slice["endpoints"]) {
			endpoint := releaseMap(v)
			conditions := releaseMap(endpoint["conditions"])
			target := releaseMap(endpoint["targetRef"])
			if conditions["ready"] == true && conditions["terminating"] != true && target["kind"] == "Pod" {
				uid := releaseString(target["uid"])
				if !pods[uid] {
					if plan.RollbackOf != "" {
						return ErrRollout
					}
					return ErrSuperseded
				}
				ready[uid] = true
			}
		}
	}
	total := p.Total
	if plan.RollbackOf != "" {
		total = 0
		for _, resource := range plan.Resources {
			if !resource.Delete && resource.ID.Kind == "Deployment" {
				total += releaseInt(releaseMap(resource.Object["spec"])["replicas"])
			}
		}
	}
	if int64(len(ready)) < total {
		return ErrRollout
	}
	return nil
}

func (c *ReleaseController) verifyProgressivePreconditions(ctx context.Context, api releaseAPI, plan ReleasePlan) error {
	if plan.Progressive == nil {
		return ErrInvalid
	}
	p := plan.Progressive
	service, e := readReleaseObject(ctx, api, p.Service.ID)
	if e != nil {
		return e
	}
	if !releaseBaselineMatches(p.Service.Object, service) {
		return ErrSuperseded
	}
	raw, e := api.Read(ctx, "/apis/autoscaling/v2/namespaces/"+url.PathEscape(p.Stable.Namespace)+"/horizontalpodautoscalers", "limit=200")
	if e != nil {
		return e
	}
	defer clear(raw)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ErrInvalid
	}
	for _, hpa := range list.Items {
		target := releaseMap(releaseMap(hpa["spec"])["scaleTargetRef"])
		if target["kind"] == "Deployment" && (target["name"] == p.Stable.Name || target["name"] == p.Candidate.Name) {
			return ErrDenied
		}
	}
	return nil
}

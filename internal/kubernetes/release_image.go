package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
)

type ReleaseImageUpdate struct {
	Container string `json:"container"`
	Image     string `json:"image"`
}

func releaseWorkloadID(kind, namespace, name string) (ReleaseResourceID, error) {
	resource := ""
	switch kind {
	case "Deployment":
		resource = "deployments"
	case "StatefulSet":
		resource = "statefulsets"
	case "DaemonSet":
		resource = "daemonsets"
	default:
		return ReleaseResourceID{}, ErrInvalid
	}
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: resource, Kind: kind, Namespace: namespace, Name: name}
	if !validReleaseAccess(ReleaseAccess{ID: id, Actions: []string{"get"}}) {
		return id, ErrInvalid
	}
	return id, nil
}

// PrepareImage freezes the entire current workload before changing only named
// container images. The original UID and every unrelated declarative field are
// checked again before the writer is allowed to dispatch.
func (c *ReleaseController) PrepareImage(ctx context.Context, run, node, owner string, auth ReleaseAuthorization, kind, namespace, name string, updates []ReleaseImageUpdate, config []byte, timeout, observe int) (out ReleaseRecord, resultErr error) {
	id, e := releaseWorkloadID(kind, namespace, name)
	if e != nil {
		return out, e
	}
	if len(updates) == 0 || len(updates) > 32 {
		return out, ErrInvalid
	}
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
	original, e := readReleaseObject(ctx, api, id)
	if e != nil {
		return out, e
	}
	object := releaseCanonicalObject(original)
	resource := ReleaseResource{ID: id, Object: object, Health: "workload", Phase: 2}
	if e = validateReleaseWorkloadStrategy(resource); e != nil {
		return out, e
	}
	pod := releaseMap(releaseMap(releaseMap(object["spec"])["template"])["spec"])
	containers := releaseItems(pod["containers"])
	access := releaseRequestedAccess(id, "patch")
	seen := map[string]bool{}
	for _, update := range updates {
		if !validName(update.Container, 63) || !ValidImageReference(update.Image) || seen[update.Container] {
			return out, ErrInvalid
		}
		seen[update.Container] = true
		found := false
		for index, value := range containers {
			container := releaseMap(value)
			if container["name"] == update.Container {
				container["image"] = update.Image
				found = true
				access.Fields = append(access.Fields, "/spec/template/spec/containers/"+strconvI(index)+"/image")
			}
		}
		if !found {
			return out, ErrInvalid
		}
		access.Containers = append(access.Containers, update.Container)
	}
	sort.Strings(access.Containers)
	sort.Strings(access.Fields)
	patchContainers := []any{}
	for _, update := range updates {
		patchContainers = append(patchContainers, map[string]any{"name": update.Container, "image": update.Image})
	}
	resource.Object = map[string]any{"apiVersion": "apps/v1", "kind": kind, "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": patchContainers}}}}
	resource.ImageOnly = true
	body, _ := json.Marshal(resource.Object)
	resource.Digest = releaseByteDigest(body)
	clear(body)
	plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "image", Resources: []ReleaseResource{resource}, Baseline: []ReleaseResource{releaseResourceFromObject(id, original, "workload")}, Config: append([]byte(nil), config...), TimeoutSeconds: timeout, ObserveSeconds: observe}
	c.captureRollbackImages(ctx, api, &plan.Baseline[0])
	extra, e := c.appendReleaseChecks(ctx, api, &plan, auth, config)
	if e != nil {
		return out, e
	}
	request := auth.Request
	request.Resources = mergeReleaseScopes([]ReleaseAccess{access}, extra)
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

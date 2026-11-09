package kubernetes

import (
	"context"
	"strings"
)

func releaseAdministrativeActions(resource ReleaseResource) []string {
	actions := []string{}
	if resource.ID.Group == "rbac.authorization.k8s.io" {
		switch resource.ID.Kind {
		case "RoleBinding", "ClusterRoleBinding":
			actions = append(actions, "bind")
		case "Role", "ClusterRole":
			actions = append(actions, "escalate")
		}
		for _, value := range releaseItems(resource.Object["rules"]) {
			for _, verb := range releaseItems(releaseMap(value)["verbs"]) {
				if verb == "impersonate" || verb == "*" {
					actions = append(actions, "impersonate")
				}
			}
		}
	}
	if resource.ID.Group == "" && resource.ID.Kind == "Namespace" {
		labelSets := []map[string]any{releaseMap(releaseMap(resource.Object["metadata"])["labels"])}
		if resource.Mutation != nil {
			labelSets = append(labelSets, releaseMap(releaseMap(resource.Mutation.Patch["metadata"])["labels"]))
		}
		for _, labels := range labelSets {
			for key := range labels {
				if strings.HasPrefix(key, "pod-security.kubernetes.io/") {
					actions = append(actions, "namespace-security")
					break
				}
			}
		}
	}
	return uniqueReleaseActions(actions)
}

func releaseNamespaceSecurityDelta(resource ReleaseResource, baseline map[string]any) bool {
	if resource.ID.Group != "" || resource.ID.Kind != "Namespace" || baseline == nil || resource.VerifyOnly {
		return false
	}
	target := resource.Object
	if resource.Expected != nil {
		target = resource.Expected
	}
	before := releaseMap(releaseMap(baseline["metadata"])["labels"])
	after := releaseMap(releaseMap(target["metadata"])["labels"])
	for key, value := range before {
		if strings.HasPrefix(key, "pod-security.kubernetes.io/") && !releaseSameValue(value, after[key]) {
			return true
		}
	}
	return false
}
func (c *ReleaseController) authorizeReleaseAdministrativeDeltas(ctx context.Context, plan *ReleasePlan) error {
	request := plan.Authorization.Request
	request.Resources = append([]ReleaseAccess(nil), request.Resources...)
	changed := false
	resources := append([]ReleaseResource(nil), plan.Resources...)
	if plan.Helm != nil {
		resources = append(resources, plan.Helm.Rendered...)
	}
	for _, resource := range resources {
		required := releaseAdministrativeActions(resource)
		for _, baseline := range plan.Baseline {
			if baseline.ID == resource.ID && releaseNamespaceSecurityDelta(resource, baseline.Object) {
				required = uniqueReleaseActions(append(required, "namespace-security"))
			}
		}
		for _, action := range required {
			found := false
			for index := range request.Resources {
				scope := &request.Resources[index]
				if scope.ID != resource.ID {
					continue
				}
				found = true
				if !hasReleaseAction(scope.Actions, action) {
					scope.Actions = uniqueReleaseActions(append(append([]string(nil), scope.Actions...), action))
					changed = true
				}
			}
			if !found {
				return ErrDenied
			}
		}
	}
	if changed {
		auth, e := c.store.AuthorizeRelease(ctx, request)
		if e != nil {
			return e
		}
		plan.Authorization = auth
	}
	return nil
}

func releaseResourceAdministrativeAllowed(plan ReleasePlan, resource ReleaseResource) bool {
	required := releaseAdministrativeActions(resource)
	for _, baseline := range plan.Baseline {
		if baseline.ID == resource.ID && releaseNamespaceSecurityDelta(resource, baseline.Object) {
			required = uniqueReleaseActions(append(required, "namespace-security"))
		}
	}
	for _, action := range required {
		found := false
		for _, scope := range plan.Authorization.Request.Resources {
			if scope.ID == resource.ID && hasReleaseAction(scope.Actions, action) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

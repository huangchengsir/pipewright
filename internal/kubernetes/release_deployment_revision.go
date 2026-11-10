package kubernetes

import "strconv"

const deploymentRevisionKey = "deployment.kubernetes.io/revision"

func releaseUndeclaredDeploymentRevision(resource ReleaseResource) bool {
	if resource.ID.Group != "apps" || resource.ID.Version != "v1" || resource.ID.Kind != "Deployment" {
		return false
	}
	_, declared := releaseMap(releaseMap(resource.Object["metadata"])["annotations"])[deploymentRevisionKey]
	return !declared
}
func releaseControllerDeploymentRevision(object map[string]any) bool {
	meta := releaseMap(object["metadata"])
	value := releaseString(releaseMap(meta["annotations"])[deploymentRevisionKey])
	revision, e := strconv.ParseInt(value, 10, 64)
	if e != nil || revision < 1 || strconv.FormatInt(revision, 10) != value {
		return false
	}
	for _, raw := range releaseItems(meta["managedFields"]) {
		manager := releaseMap(raw)
		if manager["manager"] != "k3s" && manager["manager"] != "kube-controller-manager" {
			continue
		}
		// Native DeploymentController writes this root annotation through
		// UpdateStatus as well as ordinary Update. Both still require exact root
		// FieldsV1 ownership; ownership of status fields alone proves nothing.
		subresource := releaseString(manager["subresource"])
		if manager["operation"] != "Update" || manager["apiVersion"] != "apps/v1" || manager["fieldsType"] != "FieldsV1" || subresource != "" && subresource != "status" {
			continue
		}
		annotations := releaseMap(releaseMap(releaseMap(manager["fieldsV1"])["f:metadata"])["f:annotations"])
		if _, owned := annotations["f:"+deploymentRevisionKey].(map[string]any); owned {
			return true
		}
	}
	return false
}
func releaseDropControllerDeploymentRevision(resource ReleaseResource, original, out map[string]any) {
	if !releaseUndeclaredDeploymentRevision(resource) || !releaseControllerDeploymentRevision(original) {
		return
	}
	meta := releaseMap(out["metadata"])
	annotations := releaseMap(meta["annotations"])
	delete(annotations, deploymentRevisionKey)
	if len(annotations) == 0 {
		delete(meta, "annotations")
	}
}

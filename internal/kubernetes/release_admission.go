package kubernetes

// Job controllers allocate selector/owner labels using the newly allocated Job
// UID. Dry-run and live creation must not be mistaken for the same allocation.
// Only undeclared controller-generated labels are excluded; declared selectors,
// template contents, actual Job UID and action ownership remain mandatory.
func releaseAdmittedExpected(resource ReleaseResource, object map[string]any) map[string]any {
	out := releaseCanonicalObject(object)
	// Existing frozen Expected fields retain their historical comparison. New
	// approval snapshots may omit only a proven controller-owned revision.
	if resource.Expected == nil || releaseMap(releaseMap(resource.Expected["metadata"])["annotations"])[deploymentRevisionKey] == nil {
		releaseDropControllerDeploymentRevision(resource, object, out)
	}
	if resource.ImageOnly {
		delete(releaseMap(out["spec"]), "replicas")
	}
	if resource.ID.Group == "" && resource.ID.Kind == "Service" {
		declared, spec := releaseMap(resource.Object["spec"]), releaseMap(out["spec"])
		for _, key := range []string{"clusterIP", "clusterIPs", "healthCheckNodePort"} {
			if declared[key] == nil {
				delete(spec, key)
			}
		}
		for _, value := range releaseItems(spec["ports"]) {
			port := releaseMap(value)
			preserve := false
			for _, source := range releaseItems(declared["ports"]) {
				item := releaseMap(source)
				if item["name"] == port["name"] && ReleaseObjectMatches(item["port"], port["port"]) && item["nodePort"] != nil {
					preserve = true
				}
			}
			if !preserve {
				delete(port, "nodePort")
			}
		}
	}
	if resource.ID.Kind == "Pod" && releaseMap(resource.Object["spec"])["nodeName"] == nil {
		delete(releaseMap(out["spec"]), "nodeName")
	}
	if resource.ID.Kind == "PersistentVolumeClaim" && releaseMap(resource.Object["spec"])["volumeName"] == nil {
		delete(releaseMap(out["spec"]), "volumeName")
	}
	if resource.ID.Kind == "Job" && resource.ID.Group == "batch" {
		declaredRoot := releaseMap(releaseMap(resource.Object["metadata"])["labels"])
		root := releaseMap(releaseMap(out["metadata"])["labels"])
		for _, key := range releaseJobGeneratedLabelKeys {
			if _, declared := declaredRoot[key]; !declared && releaseJobGeneratedLabelValue(object, key, root[key]) {
				delete(root, key)
			}
		}
		declaredSpec := releaseMap(resource.Object["spec"])
		spec := releaseMap(out["spec"])
		if declaredSpec["selector"] == nil {
			delete(spec, "selector")
		}
		declaredLabels := releaseMap(releaseMap(releaseMap(declaredSpec["template"])["metadata"])["labels"])
		labels := releaseMap(releaseMap(releaseMap(spec["template"])["metadata"])["labels"])
		for _, key := range releaseJobGeneratedLabelKeys {
			if _, declared := declaredLabels[key]; !declared && releaseJobGeneratedLabelValue(object, key, labels[key]) {
				delete(labels, key)
			}
		}
	}
	return out
}

func releaseResourceExpectedMatches(resource ReleaseResource, actual map[string]any) bool {
	if !releaseResourceGVKMatches(resource.ID, resource.Object, resource.Expected, actual) {
		return false
	}
	if !releaseJobGeneratedLabelsMatch(resource, actual) {
		return false
	}
	expected := resource.Object
	if resource.Expected != nil {
		expected = resource.Expected
	}
	if resource.Expected != nil && releaseUndeclaredDeploymentRevision(resource) && releaseMap(releaseMap(expected["metadata"])["annotations"])[deploymentRevisionKey] == nil {
		if releaseMap(releaseMap(actual["metadata"])["annotations"])[deploymentRevisionKey] != nil && !releaseControllerDeploymentRevision(actual) {
			return false
		}
	}
	observed := releaseAdmittedExpected(resource, actual)
	var valid bool
	observed, valid = releasePodAdmissionObservation(resource, expected, observed)
	if !valid {
		return false
	}
	expected, observed, valid = releasePriorityClassComparison(resource.ID, expected, observed)
	if !valid || !releaseNativeObjectMatches(expected, observed) {
		return false
	}
	if resource.Mutation != nil && resource.Expected != nil {
		// Also verify removals: a null merge field must really be absent, while all
		// preserved external data from the approved admission snapshot remains.
		for _, key := range []string{"data", "binaryData"} {
			if !releaseObjectMatchesAt(expected[key], observed[key], "/"+key, releaseNativeListSchema(expected, observed)) || !releaseObjectMatchesAt(observed[key], expected[key], "/"+key, releaseNativeListSchema(expected, observed)) {
				return false
			}
		}
	}
	if resource.Expected != nil {
		strict := resource.ID.Group == "apps" && (resource.ID.Kind == "Deployment" || resource.ID.Kind == "StatefulSet" || resource.ID.Kind == "DaemonSet") || resource.ID.Group == "batch" && (resource.ID.Kind == "Job" || resource.ID.Kind == "CronJob") || resource.ID.Group == "" && resource.ID.Kind == "Pod"
		if strict && !releaseTemplateMatchesAt(observed["spec"], expected["spec"], "/spec", releaseNativeListSchema(expected, observed), false) {
			return false
		}
	}
	return true
}

var releaseJobGeneratedLabelKeys = []string{"controller-uid", "job-name", "batch.kubernetes.io/controller-uid", "batch.kubernetes.io/job-name"}

func releaseJobGeneratedLabelValue(object map[string]any, key string, value any) bool {
	meta := releaseMap(object["metadata"])
	switch key {
	case "controller-uid", "batch.kubernetes.io/controller-uid":
		uid := releaseString(meta["uid"])
		return uid != "" && value == uid
	case "job-name", "batch.kubernetes.io/job-name":
		name := releaseString(meta["name"])
		return name != "" && value == name
	}
	return false
}

// Exclusion from dynamic comparison is allowed only after proving each
// undeclared generated label belongs to this Job's own UID/name. A declared
// root/template label or selector always retains its original comparison.
func releaseJobGeneratedLabelsMatch(resource ReleaseResource, object map[string]any) bool {
	if resource.ID.Group != "batch" || resource.ID.Kind != "Job" {
		return true
	}
	pairs := [][2]map[string]any{
		{releaseMap(releaseMap(resource.Object["metadata"])["labels"]), releaseMap(releaseMap(object["metadata"])["labels"])},
		{releaseMap(releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["metadata"])["labels"]), releaseMap(releaseMap(releaseMap(releaseMap(object["spec"])["template"])["metadata"])["labels"])},
	}
	if releaseMap(resource.Object["spec"])["selector"] == nil {
		pairs = append(pairs, [2]map[string]any{nil, releaseMap(releaseMap(releaseMap(object["spec"])["selector"])["matchLabels"])})
	}
	for _, pair := range pairs {
		for _, key := range releaseJobGeneratedLabelKeys {
			if _, declared := pair[0][key]; declared {
				continue
			}
			if value, present := pair[1][key]; present && !releaseJobGeneratedLabelValue(object, key, value) {
				return false
			}
		}
	}
	return true
}

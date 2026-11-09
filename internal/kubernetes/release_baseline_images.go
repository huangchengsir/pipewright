package kubernetes

import "context"

// Preserve the submitted object's original bytes for drift checks, while
// independently recording a pullable image identity observed in its current
// healthy, owner-anchored Pods. Failure to prove a tag never invents a digest.
func (c *ReleaseController) captureRollbackImages(ctx context.Context, api releaseAPI, baseline *ReleaseResource) {
	if baseline.ID.Kind != "Deployment" && baseline.ID.Kind != "StatefulSet" && baseline.ID.Kind != "DaemonSet" {
		return
	}
	if releaseDaemonID(baseline.ID) {
		baseline.DaemonSetProofVersion = releaseDaemonProofVersion
	}
	resource := *baseline
	resource.Object = releaseCanonicalObject(baseline.Object)
	related, e := c.related(ctx, api, resource)
	if e != nil {
		return
	}
	pods, proved := releaseBaselineImageCohort(resource, baseline.Object, related)
	if !proved {
		return
	}
	podSpec := releaseMap(releaseMap(releaseMap(resource.Object["spec"])["template"])["spec"])
	pins := map[string]string{}
	for _, section := range []string{"containers", "initContainers"} {
		for _, value := range releaseItems(podSpec[section]) {
			container := releaseMap(value)
			name := releaseString(container["name"])
			image := releaseString(container["image"])
			if ValidImageReference(image) {
				pins[section+"/"+name] = image
				continue
			}
			observed := ""
			valid := true
			count := 0
			for _, pod := range pods {
				statuses := "containerStatuses"
				if section == "initContainers" {
					statuses = "initContainerStatuses"
				}
				for _, status := range releaseItems(releaseMap(pod["status"])[statuses]) {
					s := releaseMap(status)
					if s["name"] != name {
						continue
					}
					candidate, pullable := releaseBaselinePullableImage(image, releaseString(s["imageID"]))
					if !pullable || observed != "" && !releaseContainerImageMatches(candidate, observed) {
						valid = false
					}
					observed = candidate
					count++
				}
			}
			if valid && count > 0 && observed != "" {
				pins[section+"/"+name] = observed
			}
		}
	}
	if len(pins) > 0 {
		baseline.RollbackImages = pins
	}
}
func rollbackFrozenImageObject(baseline ReleaseResource) map[string]any {
	object := releaseCanonicalObject(baseline.Object)
	spec := releaseMap(releaseMap(releaseMap(object["spec"])["template"])["spec"])
	for _, section := range []string{"containers", "initContainers"} {
		for _, item := range releaseItems(spec[section]) {
			container := releaseMap(item)
			if pin := baseline.RollbackImages[section+"/"+releaseString(container["name"])]; pin != "" {
				container["image"] = pin
			}
		}
	}
	return object
}

package kubernetes

import (
	"context"
	"strings"
)

// The original tag is never resolved again to invent a new old digest. Reuse
// only pins already sealed against the unchanged same-incarnation baseline.
func (c *ReleaseController) preserveRetryRollbackImages(ctx context.Context, api releaseAPI, prior ReleaseRecord, resource ReleaseResource, current map[string]any, next *ReleaseResource) error {
	hadPins := false
	for _, old := range prior.Plan.Baseline {
		if old.ID != resource.ID || len(old.RollbackImages) == 0 {
			continue
		}
		hadPins = true
		if !releaseResourceBaselineMatches(resource, old.Object, current) {
			continue
		}
		pins := map[string]string{}
		for key, pin := range old.RollbackImages {
			parts := strings.Split(key, "/")
			if len(parts) != 2 || (parts[0] != "containers" && parts[0] != "initContainers") || !ValidImageReference(pin) {
				return ErrRollbackBaseline
			}
			oldImage, oldOK := releaseRollbackContainerImage(old, parts[0], parts[1])
			newImage, newOK := releaseRollbackContainerImage(*next, parts[0], parts[1])
			if !oldOK || !newOK || oldImage != newImage || ValidImageReference(oldImage) && oldImage != pin {
				return ErrRollbackBaseline
			}
			pins[key] = pin
		}
		next.RollbackImages = pins
		return nil
	}
	if hadPins {
		// A positively owned effect may be a different current baseline. Only the
		// normal owner/current-template/healthy-Pod capture can provide fresh pins.
		c.captureRollbackImages(ctx, api, next)
		if !rollbackPinnedImages(ReleaseResource{ID: next.ID, Object: rollbackFrozenImageObject(*next)}) {
			return ErrRollbackBaseline
		}
	}
	return nil
}
func releaseRollbackContainerImage(resource ReleaseResource, section, name string) (string, bool) {
	spec := releaseMap(resource.Object["spec"])
	if resource.ID.Kind != "Pod" {
		spec = releaseMap(releaseMap(spec["template"])["spec"])
	}
	image := ""
	count := 0
	for _, v := range releaseItems(spec[section]) {
		c := releaseMap(v)
		if c["name"] == name {
			image = releaseString(c["image"])
			count++
		}
	}
	return image, count == 1 && image != ""
}

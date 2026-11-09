package kubernetes

import "strings"

// Share the unchanged active owner/template cohort with the ordinary health
// gate. Historical or foreign Pods never supply a baseline image identity.
func releaseCurrentWorkloadPods(r ReleaseResource, actual map[string]any, related []map[string]any) ([]map[string]any, bool) {
	meta := releaseMap(actual["metadata"])
	template := releaseMap(releaseMap(actual["spec"])["template"])
	owners := map[string]bool{releaseString(meta["uid"]): true}
	if r.ID.Kind == "Deployment" {
		owners = map[string]bool{}
		for _, obj := range related {
			m := releaseMap(obj["metadata"])
			if obj["kind"] == "ReplicaSet" && releaseString(m["namespace"]) == r.ID.Namespace && releaseOwner(obj) == releaseString(meta["uid"]) && ReleaseObjectMatches(template, releaseMap(obj["spec"])["template"]) {
				owners[releaseString(m["uid"])] = true
			}
		}
	}
	daemonHash := ""
	if r.ID.Kind == "DaemonSet" && r.DaemonSetProofVersion != 0 {
		var ok bool
		daemonHash, ok = releaseDaemonCurrentHash(actual, related)
		if r.DaemonSetProofVersion != releaseDaemonProofVersion || !ok {
			return nil, false
		}
	}
	pods := []map[string]any{}
	for _, p := range related {
		if p["kind"] != "Pod" || releaseString(releaseMap(p["metadata"])["namespace"]) != r.ID.Namespace || !owners[releaseOwner(p)] {
			continue
		}
		if releaseMap(p["metadata"])["deletionTimestamp"] != nil {
			continue
		}
		// A controller replaces terminal attempts. Keep these historical records in
		// related projections, but they are not members of its active desired cohort.
		phase := releaseString(releaseMap(p["status"])["phase"])
		if phase == "Failed" || phase == "Succeeded" {
			continue
		}
		if r.ID.Kind == "DaemonSet" && r.DaemonSetProofVersion != 0 {
			if !releaseDaemonPodMatches(actual, p, daemonHash) {
				continue
			}
		} else if !releasePodSpecMatches(releaseMap(template["spec"]), p["spec"]) {
			continue
		}
		pods = append(pods, p)
	}
	return pods, true
}

// Build only a private comparison view for rollback-baseline evidence. Actual
// receipts, Pod spec/status, ordinary health and all persisted objects retain
// their original bytes. A tag is never resolved through a registry here.
func releaseBaselineImageCohort(r ReleaseResource, actual map[string]any, related []map[string]any) ([]map[string]any, bool) {
	pods, ok := releaseCurrentWorkloadPods(r, actual, related)
	if !ok {
		return nil, false
	}
	view := []map[string]any{}
	for _, object := range related {
		if object["kind"] != "Pod" {
			view = append(view, object)
		}
	}
	for _, pod := range pods {
		if releaseString(releaseMap(pod["metadata"])["uid"]) == "" {
			return nil, false
		}
		copy := releaseObjectCopy(pod)
		spec, status := releaseMap(copy["spec"]), releaseMap(copy["status"])
		for _, section := range []struct{ spec, status string }{{"containers", "containerStatuses"}, {"initContainers", "initContainerStatuses"}} {
			images := map[string]string{}
			for _, v := range releaseItems(spec[section.spec]) {
				c := releaseMap(v)
				images[releaseString(c["name"])] = releaseString(c["image"])
			}
			for _, v := range releaseItems(status[section.status]) {
				c := releaseMap(v)
				want := images[releaseString(c["name"])]
				display := releaseString(c["image"])
				if releaseContainerStatusImageMatches(want, display, releaseString(c["imageID"])) {
					continue
				}
				// This alternative proves only a mutable-tag baseline with the exact
				// native config-display shape and a complete same-repository manifest ID.
				if want == "" || ValidImageReference(want) || !strings.HasPrefix(display, "sha256:") || !fullHash.MatchString(strings.TrimPrefix(display, "sha256:")) {
					return nil, false
				}
				if _, valid := releaseBaselinePullableImage(want, releaseString(c["imageID"])); !valid {
					return nil, false
				}
				c["image"] = want
			}
		}
		view = append(view, copy)
	}
	return pods, EvaluateReleaseHealth(r, actual, view).State == "ready"
}

func releaseBaselinePullableImage(want, observed string) (string, bool) {
	id := observed
	for _, prefix := range []string{"docker-pullable://", "containerd://", "docker://", "cri-o://"} {
		if strings.HasPrefix(id, prefix) {
			id = strings.TrimPrefix(id, prefix)
			break
		}
	}
	if !ValidImageReference(id) || want == "" || !imageValue.MatchString(want) || strings.Contains(want, "@") {
		return "", false
	}
	repo := want
	if colon := strings.LastIndex(repo, ":"); colon > strings.LastIndex(repo, "/") {
		if colon == len(repo)-1 {
			return "", false
		}
		repo = repo[:colon]
	}
	digest := strings.Split(id, "@")[1]
	if !releaseContainerImageMatches(repo+"@"+digest, id) {
		return "", false
	}
	return id, true
}

package kubernetes

// FilterReleaseRelatedObjects is the only projection for controller-created
// names that cannot be known when approving a plan. The explicit capability is
// attached to an exact anchor, not to a namespace-wide resource wildcard.
func FilterReleaseRelatedObjects(scope ReleaseAccess, anchor map[string]any, kind string, objects []map[string]any) ([]map[string]any, error) {
	meta := releaseMap(anchor["metadata"])
	api := scope.ID.Version
	if scope.ID.Group != "" {
		api = scope.ID.Group + "/" + api
	}
	if anchor["apiVersion"] != api || anchor["kind"] != scope.ID.Kind || meta["name"] != scope.ID.Name || releaseString(meta["namespace"]) != scope.ID.Namespace || releaseString(meta["uid"]) == "" {
		return nil, ErrDenied
	}
	action := ""
	switch kind {
	case "Pod":
		action = "related-pods"
	case "ControllerRevision":
		action = releaseDaemonRevisionAction
	case "ReplicaSet":
		action = "related-replicasets"
	case "EndpointSlice":
		action = "related-endpointslices"
	case "Job":
		action = "related-jobs"
	default:
		return nil, ErrDenied
	}
	if !releaseStringSetContains(scope.Actions, []string{action}) {
		return nil, ErrDenied
	}
	if len(objects) > 4096 {
		return nil, ErrInvalid
	}
	root := releaseString(meta["uid"])
	owners := map[string]bool{root: true}
	rootKind := scope.ID.Kind
	switch kind {
	case "ControllerRevision":
		if !releaseDaemonID(scope.ID) {
			return nil, ErrDenied
		}
	case "ReplicaSet":
		if rootKind != "Deployment" {
			return nil, ErrDenied
		}
	case "EndpointSlice":
		if rootKind != "Service" {
			return nil, ErrDenied
		}
	case "Job":
		if rootKind != "CronJob" {
			return nil, ErrDenied
		}
	case "Pod":
		if rootKind != "Deployment" && rootKind != "StatefulSet" && rootKind != "DaemonSet" && rootKind != "Job" && rootKind != "CronJob" {
			return nil, ErrDenied
		}
	}
	// Namespaced objects from another controller are discarded before crossing
	// the private gateway; matching a user-controlled label alone is insufficient.
	if kind == "Pod" && (rootKind == "Deployment" || rootKind == "CronJob") {
		owners = map[string]bool{}
		intermediate := "ReplicaSet"
		if rootKind == "CronJob" {
			intermediate = "Job"
		}
		for _, o := range objects {
			m := releaseMap(o["metadata"])
			if o["kind"] == intermediate && releaseString(m["namespace"]) == scope.ID.Namespace && releaseOwner(o) == root {
				if rootKind == "Deployment" && !ReleaseObjectMatches(releaseMap(anchor["spec"])["template"], releaseMap(o["spec"])["template"]) {
					continue
				}
				owners[releaseString(m["uid"])] = true
			}
		}
	}
	out := []map[string]any{}
	for _, o := range objects {
		m := releaseMap(o["metadata"])
		if o["kind"] == kind && releaseString(m["namespace"]) == scope.ID.Namespace && owners[releaseOwner(o)] {
			if kind == "ControllerRevision" && (o["apiVersion"] != "apps/v1" || !releaseDaemonOwner(o, anchor)) {
				continue
			}
			out = append(out, o)
		}
	}
	return out, nil
}

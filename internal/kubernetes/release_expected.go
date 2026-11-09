package kubernetes

func releaseExpectedObserved(plan ReleasePlan, id ReleaseResourceID, actual map[string]any) bool {
	if releaseHelmStorage(plan, id) {
		return releaseHelmStorageAllowed(plan, id, actual)
	}
	resources := append([]ReleaseResource(nil), plan.Resources...)
	if plan.Helm != nil {
		resources = append(resources, plan.Helm.Rendered...)
	}
	for _, batch := range plan.Batches {
		resources = append(append([]ReleaseResource(nil), resources...), batch...)
	}
	for _, resource := range resources {
		if resource.ID == id && resource.Expected != nil && releaseResourceExpectedMatches(resource, actual) {
			return true
		}
	}
	// Historical v2 fixtures/plans without an admission snapshot still require
	// every declared field. New prepared plans always contain Expected.
	for _, resource := range plan.Resources {
		if resource.ID == id && resource.Expected == nil {
			return releaseResourceGVKMatches(resource.ID, resource.Object, actual) && ReleasePodTemplateMatches(resource.Object, actual)
		}
	}
	return false
}

// The same ConfigMap has two separately approved bodies. An action can use
// only its own frozen expectation; the final target never authorizes the live
// canary step (or vice versa).
func releaseIntentExpectedObserved(plan ReleasePlan, intent ReleaseActionIntent, actual map[string]any) bool {
	if intent.StepKey == releaseCoreDNSFinalizeStep {
		return plan.CoreDNS != nil && plan.CoreDNS.Finalize != nil && plan.CoreDNS.Finalize.ID == intent.Resource && releaseResourceExpectedMatches(*plan.CoreDNS.Finalize, actual)
	}
	return releaseExpectedObserved(plan, intent.Resource, actual)
}

package kubernetes

import "testing"

// Native DeploymentController sets the root revision then calls UpdateStatus.
// Real raw managedFields therefore contains both root metadata and status paths
// under the same status-subresource Update entry, not a main-resource writer.
func TestDeploymentStatusRevisionHandlesFirstCreateAndLaterTemplate(t *testing.T) {
	for _, stage := range []string{"first-create", "later-template"} {
		for _, manager := range []string{"k3s", "kube-controller-manager"} {
			t.Run(stage+"/"+manager, func(t *testing.T) {
				resource, dry, live := deploymentRevisionFixture()
				for _, object := range []map[string]any{dry, live} {
					entry := releaseMap(releaseItems(releaseMap(object["metadata"])["managedFields"])[0])
					entry["manager"] = manager
					entry["subresource"] = "status"
					releaseMap(entry["fieldsV1"])["f:status"] = map[string]any{"f:observedGeneration": map[string]any{}, "f:availableReplicas": map[string]any{}}
				}
				if stage == "first-create" {
					delete(releaseMap(dry["metadata"]), "annotations")
					delete(releaseMap(dry["metadata"]), "managedFields")
					releaseMap(releaseMap(live["metadata"])["annotations"])[deploymentRevisionKey] = "1"
				}
				resource.Expected = releaseAdmittedExpected(resource, dry)
				if !releaseControllerDeploymentRevision(live) {
					t.Fatal("native status Update root-key ownership was rejected")
				}
				if !releaseResourceExpectedMatches(resource, live) {
					t.Fatal("approved current template blocked by controller status revision")
				}
				if releaseMap(releaseMap(resource.Expected["metadata"])["annotations"])[deploymentRevisionKey] != nil {
					t.Fatal("controller bookkeeping frozen into new expected object")
				}
			})
		}
	}
}
func TestDeploymentStatusRevisionDoesNotReplaceIdentityOrSpecProof(t *testing.T) {
	for _, scenario := range []string{"manager", "apply", "scale", "status_only_fields", "wrong_version", "wrong_fields_type", "explicit", "changed_spec", "uid"} {
		t.Run(scenario, func(t *testing.T) {
			resource, dry, live := deploymentRevisionFixture()
			for _, object := range []map[string]any{dry, live} {
				releaseMap(releaseItems(releaseMap(object["metadata"])["managedFields"])[0])["subresource"] = "status"
			}
			if scenario == "explicit" {
				releaseMap(resource.Object["metadata"])["annotations"] = map[string]any{deploymentRevisionKey: "1"}
			}
			resource.Expected = releaseAdmittedExpected(resource, dry)
			entry := releaseMap(releaseItems(releaseMap(live["metadata"])["managedFields"])[0])
			switch scenario {
			case "manager":
				entry["manager"] = "untrusted-controller"
			case "apply":
				entry["operation"] = "Apply"
			case "scale":
				entry["subresource"] = "scale"
			case "status_only_fields":
				entry["fieldsV1"] = map[string]any{"f:status": map[string]any{"f:observedGeneration": map[string]any{}}}
			case "wrong_version":
				entry["apiVersion"] = "apps/v2"
			case "wrong_fields_type":
				entry["fieldsType"] = "unknown"
			case "changed_spec":
				releaseMap(live["spec"])["replicas"] = float64(2)
			case "uid":
				resource.ExpectedUID = "approved-uid"
				releaseMap(live["metadata"])["uid"] = "recreated-uid"
			}
			if scenario == "uid" {
				if health := EvaluateReleaseHealth(resource, live, nil); health.Reason != "resource_identity_changed" {
					t.Fatal("status normalization bypassed UID", health)
				}
				return
			}
			if releaseResourceExpectedMatches(resource, live) {
				t.Fatal("status normalization accepted unproven or changed fields")
			}
		})
	}
}

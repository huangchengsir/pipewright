package kubernetes

import "testing"

func rawAdmissionJob(uid string) map[string]any {
	object := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"task","namespace":"app","labels":{"declared":"kept"}},"spec":{"template":{"metadata":{"labels":{"declared":"kept"}},"spec":{"restartPolicy":"Never","containers":[{"name":"task","image":"registry.invalid/task@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}}}`)
	releaseMap(object["metadata"])["uid"] = uid
	labels := func() map[string]any {
		return map[string]any{"declared": "kept", "controller-uid": uid, "batch.kubernetes.io/controller-uid": uid, "job-name": "task", "batch.kubernetes.io/job-name": "task"}
	}
	releaseMap(object["metadata"])["labels"] = labels()
	releaseMap(releaseMap(releaseMap(object["spec"])["template"])["metadata"])["labels"] = labels()
	releaseMap(object["spec"])["selector"] = map[string]any{"matchLabels": map[string]any{"batch.kubernetes.io/controller-uid": uid}}
	return object
}
func jobRootResource() ReleaseResource {
	object := rawAdmissionJob("unused")
	delete(releaseMap(object["metadata"]), "uid")
	releaseMap(object["metadata"])["labels"] = map[string]any{"declared": "kept"}
	releaseMap(releaseMap(releaseMap(object["spec"])["template"])["metadata"])["labels"] = map[string]any{"declared": "kept"}
	delete(releaseMap(object["spec"]), "selector")
	return ReleaseResource{ID: ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "task"}, Object: object}
}
func TestJobRawRootGeneratedLabelsBindEachOwnUIDAcrossDryRunAndLive(t *testing.T) {
	resource := jobRootResource()
	dry := rawAdmissionJob("dry-uid")
	actual := rawAdmissionJob("live-uid")
	resource.Expected = releaseAdmittedExpected(resource, dry)
	if !releaseResourceExpectedMatches(resource, actual) {
		t.Fatal("real generated root labels made accepted Job receipt unknown")
	}
	for _, key := range []string{"controller-uid", "batch.kubernetes.io/controller-uid", "job-name", "batch.kubernetes.io/job-name"} {
		if releaseMap(releaseMap(resource.Expected["metadata"])["labels"])[key] != nil {
			t.Fatal("dynamic root label remained in dry-run comparison")
		}
	}
}
func TestJobRawRootLabelsNeverEraseDeclaredOrForeignIdentity(t *testing.T) {
	for _, key := range []string{"controller-uid", "batch.kubernetes.io/controller-uid", "job-name", "batch.kubernetes.io/job-name"} {
		t.Run(key, func(t *testing.T) {
			resource := jobRootResource()
			resource.Expected = releaseAdmittedExpected(resource, rawAdmissionJob("dry-uid"))
			foreign := rawAdmissionJob("live-uid")
			releaseMap(releaseMap(foreign["metadata"])["labels"])[key] = "foreign"
			if releaseResourceExpectedMatches(resource, foreign) {
				t.Fatal("foreign automatic Job identity label was ignored")
			}
			declared := jobRootResource()
			releaseMap(releaseMap(declared.Object["metadata"])["labels"])[key] = "explicit-user-value"
			dry := rawAdmissionJob("dry-uid")
			releaseMap(releaseMap(dry["metadata"])["labels"])[key] = "explicit-user-value"
			declared.Expected = releaseAdmittedExpected(declared, dry)
			actual := rawAdmissionJob("live-uid")
			releaseMap(releaseMap(actual["metadata"])["labels"])[key] = "explicit-user-value"
			if !releaseResourceExpectedMatches(declared, actual) {
				t.Fatal("explicit label was wrongly treated as generated")
			}
			releaseMap(releaseMap(actual["metadata"])["labels"])[key] = "changed"
			if releaseResourceExpectedMatches(declared, actual) {
				t.Fatal("declared root label change was hidden")
			}
		})
	}
	resource := jobRootResource()
	resource.Expected = releaseAdmittedExpected(resource, rawAdmissionJob("dry-uid"))
	actual := rawAdmissionJob("live-uid")
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["containers"])[0])["image"] = "foreign"
	if releaseResourceExpectedMatches(resource, actual) {
		t.Fatal("root-label normalization weakened template comparison")
	}
	explicit := jobRootResource()
	releaseMap(explicit.Object["spec"])["selector"] = map[string]any{"matchLabels": map[string]any{"declared": "kept"}}
	dry := rawAdmissionJob("dry-uid")
	releaseMap(dry["spec"])["selector"] = releaseMap(explicit.Object["spec"])["selector"]
	explicit.Expected = releaseAdmittedExpected(explicit, dry)
	if releaseResourceExpectedMatches(explicit, rawAdmissionJob("live-uid")) {
		t.Fatal("explicit selector change was ignored")
	}
}

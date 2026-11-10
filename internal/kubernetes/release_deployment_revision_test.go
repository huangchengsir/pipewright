package kubernetes

import (
	"context"
	"testing"
	"time"
)

func deploymentRevisionFixture() (ReleaseResource, map[string]any, map[string]any) {
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app"}
	object := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"app"}},"template":{"metadata":{"labels":{"app":"app"}},"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}}}`)
	dry := releaseObjectCopy(object)
	meta := releaseMap(dry["metadata"])
	meta["uid"] = "approved-uid"
	meta["annotations"] = map[string]any{deploymentRevisionKey: "1"}
	meta["managedFields"] = []any{map[string]any{"manager": "k3s", "operation": "Update", "apiVersion": "apps/v1", "fieldsType": "FieldsV1", "fieldsV1": map[string]any{"f:metadata": map[string]any{"f:annotations": map[string]any{"f:" + deploymentRevisionKey: map[string]any{}}}}}}
	live := releaseObjectCopy(dry)
	releaseMap(releaseMap(live["metadata"])["annotations"])[deploymentRevisionKey] = "2"
	return releaseResourceFromObject(id, object, "workload"), dry, live
}
func TestDeploymentControllerRevisionDoesNotFreezeUndeclaredOldRevision(t *testing.T) {
	resource, dry, live := deploymentRevisionFixture()
	resource.Expected = releaseAdmittedExpected(resource, dry)
	if !releaseResourceExpectedMatches(resource, live) {
		t.Fatal("controller-owned undeclared revision from dry-run blocks the approved current template")
	}
	if releaseMap(releaseMap(resource.Expected["metadata"])["annotations"])[deploymentRevisionKey] != nil {
		t.Fatal("old controller revision remains frozen")
	}
}
func TestDeploymentRevisionNormalizationKeepsDeclaredUnknownAndSpecEvidence(t *testing.T) {
	for _, scenario := range []string{"explicit", "manager", "operation", "subresource", "fieldpath", "invalid_revision", "other_annotation", "spec", "other_kind"} {
		t.Run(scenario, func(t *testing.T) {
			r, dry, live := deploymentRevisionFixture()
			if scenario == "explicit" {
				releaseMap(r.Object["metadata"])["annotations"] = map[string]any{deploymentRevisionKey: "1"}
			}
			if scenario == "other_annotation" {
				releaseMap(releaseMap(dry["metadata"])["annotations"])["user.example/key"] = "approved"
				releaseMap(releaseMap(live["metadata"])["annotations"])["user.example/key"] = "changed"
			}
			if scenario == "other_kind" {
				r.ID.Kind = "StatefulSet"
				r.ID.Resource = "statefulsets"
				r.Object["kind"] = "StatefulSet"
				dry["kind"] = "StatefulSet"
				live["kind"] = "StatefulSet"
			}
			r.Expected = releaseAdmittedExpected(r, dry)
			field := releaseMap(releaseItems(releaseMap(live["metadata"])["managedFields"])[0])
			switch scenario {
			case "manager":
				field["manager"] = "untrusted-manager"
			case "operation":
				field["operation"] = "Apply"
			case "subresource":
				field["subresource"] = "scale"
			case "fieldpath":
				field["fieldsV1"] = map[string]any{"f:spec": map[string]any{}}
			case "invalid_revision":
				releaseMap(releaseMap(live["metadata"])["annotations"])[deploymentRevisionKey] = "2-private-suffix"
			case "spec":
				releaseMap(live["spec"])["replicas"] = float64(2)
			}
			if releaseResourceExpectedMatches(r, live) {
				t.Fatal("undeclared normalization hid changed or unproven fields")
			}
		})
	}
}
func TestDeploymentHistoricalExpectedAnnotationKeepsItsComparison(t *testing.T) {
	r, dry, live := deploymentRevisionFixture()
	r.Expected = releaseCanonicalObject(dry)
	if !releaseResourceExpectedMatches(r, dry) {
		t.Fatal("historical exact revision no longer matches")
	}
	if releaseResourceExpectedMatches(r, live) {
		t.Fatal("historically frozen annotation was silently rewritten")
	}
}

type revisionLifecycleAPI struct{ progressiveControllerAPI }

func (a *revisionLifecycleAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	action, e := a.progressiveControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e == nil && in.Resource.Kind == "Deployment" {
		actual := a.objects[path]
		meta := releaseMap(actual["metadata"])
		annotations := releaseMap(meta["annotations"])
		annotations[deploymentRevisionKey] = strconvI(a.writes + 1)
	}
	return action, e
}
func TestDeploymentCASUpdateAndRollbackSurviveControllerRevisionChange(t *testing.T) {
	for _, subresource := range []string{"", "status"} {
		t.Run(map[string]string{"": "main-resource", "status": "status-resource"}[subresource], func(t *testing.T) { testDeploymentCASRevisionLifecycle(t, subresource) })
	}
}
func testDeploymentCASRevisionLifecycle(t *testing.T, subresource string) {
	ctx := context.Background()
	f, auth, _, fact, live := ownedMutationFixture(t, true)
	original, e := f.s.GetRelease(ctx, fact.ReleaseID)
	if e != nil {
		t.Fatal(e)
	}
	desired := releaseObjectCopy(original.Plan.Resources[0].Object)
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(desired["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, dry, _ := deploymentRevisionFixture()
	meta := releaseMap(live["metadata"])
	releaseMap(meta["annotations"])[deploymentRevisionKey] = "1"
	meta["managedFields"] = releaseMap(dry["metadata"])["managedFields"]
	releaseMap(releaseItems(meta["managedFields"])[0])["subresource"] = subresource
	meta["generation"] = 1
	live["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	path := "/apis/apps/v1/namespaces/app/deployments/config"
	api := &revisionLifecycleAPI{progressiveControllerAPI: progressiveControllerAPI{releaseControllerAPI: releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live, "/apis/apps/v1": releaseTestObject(`{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true,"verbs":["get","create","patch"]}]}`)}}}}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	files, e := InlineReleaseFile(stringMustJSON(desired))
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := c.PrepareManifest(ctx, "revision-update", "node", "update-owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
	if e != nil {
		t.Fatal("prepare update", e)
	}
	if releaseMap(releaseMap(prepared.Plan.Resources[0].Expected["metadata"])["annotations"])[deploymentRevisionKey] != nil {
		t.Fatal("dry-run froze stale revision")
	}
	if _, e = f.s.db.ExecContext(ctx, `DELETE FROM k8s_resource_gates`); e != nil {
		t.Fatal(e)
	}
	prepared, e = f.s.ApproveRelease(ctx, prepared.ID, prepared.OwnerEpoch, prepared.Revision, prepared.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = prepared
	registerControllerTestWriter(t, f, prepared)
	result, e := c.Run(ctx, prepared.ID, prepared.OwnerEpoch, nil)
	if e != nil || !result.Successful() {
		t.Fatal("controller revision update failed", result.State, e)
	}
	rollback, e := c.PrepareRollback(ctx, "revision-rollback", "node", "rollback-owner", result.ID, result.Revision, result.PlanID)
	if e != nil {
		t.Fatal("prepare rollback", e)
	}
	if releaseMap(releaseMap(rollback.Plan.Resources[0].Object["metadata"])["annotations"])[deploymentRevisionKey] != nil {
		t.Fatal("rollback adopted old controller annotation")
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	registerControllerTestWriter(t, f, rollback)
	restored, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if e != nil || restored.State != "rolled_back" {
		t.Fatal("controller revision rollback failed", restored.State, e)
	}
	if releaseMap(releaseMap(api.objects[path]["metadata"])["annotations"])[deploymentRevisionKey] != "3" {
		t.Fatal("controller revision was rewritten")
	}
}

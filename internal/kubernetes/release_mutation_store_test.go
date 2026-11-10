package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func ownedMutationFixture(t *testing.T, workload ...bool) (*fixture, ReleaseAuthorization, ReleaseResource, ReleaseAction, map[string]any) {
	t.Helper()
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	if len(workload) > 0 && workload[0] {
		id.Group, id.Resource, id.Kind = "apps", "deployments", "Deployment"
	}
	scope := ReleaseAccess{ID: id, Actions: []string{"get", "create", "patch", "rollback"}}
	if id.Kind == "Deployment" {
		scope.Actions = append(scope.Actions, "related-pods", "related-replicasets")
		scope.Containers = []string{"app"}
	}
	_, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	request := p.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	original := releaseResourceFromObject(id, releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"message":"old","remove":"owned"}}`), "declaration")
	if id.Kind == "Deployment" {
		original = releaseResourceFromObject(id, releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"config","namespace":"app"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","env":[{"name":"KEEP","value":"managed"}]}]}}}}`), "workload")
	}
	isHelm := len(workload) > 1 && workload[1]
	helm := &ReleaseHelmPlan{Name: "app", Namespace: "app", StorageAnchor: ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app"}}
	if isHelm {
		releaseMap(original.Object["metadata"])["annotations"] = map[string]any{"meta.helm.sh/release-name": "app", "meta.helm.sh/release-namespace": "app"}
		releaseMap(original.Object["metadata"])["labels"] = map[string]any{"app.kubernetes.io/managed-by": "Helm"}
		original = releaseResourceFromObject(id, original.Object, original.Health)
	}
	plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{original}, TimeoutSeconds: 300}
	if isHelm {
		plan.Mode = "helm"
		plan.Helm = helm
		plan.Helm.Rendered = []ReleaseResource{original}
	}
	parent, e := f.s.CreateRelease(ctx, "owned-create", "node", "owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	parent, e = f.s.ApproveRelease(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, parent.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.AcquireReleaseGates(ctx, parent.ID, parent.OwnerEpoch); e != nil {
		t.Fatal(e)
	}
	registerControllerTestWriter(t, f, parent)
	body, _ := json.Marshal(original.Object)
	fact, e := f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, ReleaseActionIntent{Version: 2, ParentID: parent.PlanID, StepKey: "create", Resource: id, Verb: "create", Body: body, AuthorizationFingerprint: auth.Fingerprint})
	if e != nil {
		t.Fatal(e)
	}
	var live map[string]any
	json.Unmarshal(fact.Intent.Body, &live)
	meta := releaseMap(live["metadata"])
	meta["uid"], meta["resourceVersion"] = "owned-uid", "17"
	meta["managedFields"] = []any{map[string]any{"manager": "Go-http-client", "operation": "Update"}}
	fact, e = f.s.RecordReleaseAction(ctx, fact.ID, parent.OwnerEpoch, fact.Revision, "verified", ReleaseActionReceipt{Resource: id, UID: "owned-uid", ResourceVersion: "17", Object: live})
	if e != nil {
		t.Fatal(e)
	}
	desired := releaseResourceFromObject(id, releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"message":"new","added":"new"}}`), "declaration")
	if id.Kind == "Deployment" {
		desired = releaseResourceFromObject(id, releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"config","namespace":"app"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}}}}`), "workload")
		desired.ImageOnly = true
	}
	if isHelm {
		releaseMap(desired.Object["metadata"])["annotations"] = map[string]any{"meta.helm.sh/release-name": "app", "meta.helm.sh/release-namespace": "app"}
		releaseMap(desired.Object["metadata"])["labels"] = map[string]any{"app.kubernetes.io/managed-by": "Helm"}
		desired = releaseResourceFromObject(id, desired.Object, desired.Health)
	}
	return f, auth, desired, fact, releaseObjectCopy(live)
}
func TestOwnedPostMutationRequiresSealedSameScopeReceiptAndUndriftedUID(t *testing.T) {
	f, auth, desired, fact, live := ownedMutationFixture(t)
	ctx := context.Background()
	releaseMap(live["data"])["outside"] = "external"
	mutation, e := f.s.ownedPostMutation(ctx, auth, desired, live)
	if e != nil || mutation == nil || mutation.OriginActionID != fact.ID || mutation.UID != "owned-uid" {
		t.Fatal("verified owned POST could not compile", e)
	}
	if _, ok := releaseMap(mutation.Patch["data"])["remove"]; !ok {
		t.Fatal("old managed omission not explicitly removed")
	}
	if releaseMap(mutation.Patch["data"])["outside"] != nil {
		t.Fatal("external data became a patch mutation")
	}
	for _, dimension := range []string{"project", "environment", "connection", "credential", "context", "executor", "cluster"} {
		other := auth
		switch dimension {
		case "project":
			other.Request.ProjectID = "other"
		case "environment":
			other.Request.Environment = "other"
		case "connection":
			other.Request.ConnectionID = "other"
		case "credential":
			other.Request.CredentialID = "other"
		case "context":
			other.Request.Context = "other"
		case "executor":
			other.Request.Executor = Executor{Kind: "ssh", ServerID: "other"}
		case "cluster":
			other.ClusterKey = "other"
		}
		if m, e := f.s.ownedPostMutation(ctx, other, desired, live); e != nil || m != nil {
			t.Fatal("cross-scope claim accepted", dimension, e)
		}
	}
	for _, scenario := range []string{"uid", "marker", "foreign_apply"} {
		current := releaseObjectCopy(live)
		meta := releaseMap(current["metadata"])
		switch scenario {
		case "uid":
			meta["uid"] = "recreated"
		case "marker":
			releaseMap(meta["annotations"])[ReleaseActionAnnotation] = "kact-nonexistent"
		case "foreign_apply":
			meta["managedFields"] = []any{map[string]any{"operation": "Apply", "manager": "pipewright-release-v2"}}
		}
		if m, e := f.s.ownedPostMutation(ctx, auth, desired, current); e != nil || m != nil {
			t.Fatal("unproved/external SSA ownership used CAS", scenario, e)
		}
	}
	changed := releaseObjectCopy(live)
	releaseMap(changed["data"])["message"] = "foreign"
	if _, e := f.s.ownedPostMutation(ctx, auth, desired, changed); !errors.Is(e, ErrSuperseded) {
		t.Fatal("changed managed value was overwritten", e)
	}
	// A known action annotation is not sufficient after its verified fact is fenced.
	if _, e := f.s.db.ExecContext(ctx, `UPDATE k8s_release_actions SET state='outcome_unknown' WHERE id=?`, fact.ID); e != nil {
		t.Fatal(e)
	}
	if m, e := f.s.ownedPostMutation(ctx, auth, desired, live); e != nil || m != nil {
		t.Fatal("unknown receipt acquired mutation authority", e)
	}
}
func TestOwnedPostPlanRejectsContentTypeTamperAndKeepsHistoricalDefault(t *testing.T) {
	f, auth, desired, _, live := ownedMutationFixture(t)
	ctx := context.Background()
	mutation, e := f.s.ownedPostMutation(ctx, auth, desired, live)
	if e != nil {
		t.Fatal(e)
	}
	desired.Mutation = mutation
	plan := ReleasePlan{MutationVersion: 1, Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{desired}, Baseline: []ReleaseResource{releaseResourceFromObject(desired.ID, live, "declaration")}, TimeoutSeconds: 300}
	parent, e := f.s.CreateRelease(ctx, "owned-update", "node", "new-owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	parent, e = f.s.ApproveRelease(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, parent.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	// This unit isolates sealed intent validation from the separately covered gate
	// lifecycle. No container/API operation is made by this fixture.
	if _, e = f.s.db.ExecContext(ctx, `DELETE FROM k8s_resource_gates`); e != nil {
		t.Fatal(e)
	}
	if e = f.s.AcquireReleaseGates(ctx, parent.ID, parent.OwnerEpoch); e != nil {
		t.Fatal(e)
	}
	registerControllerTestWriter(t, f, parent)
	object := releaseMutationObject(desired)
	meta := releaseMap(object["metadata"])
	meta["uid"], meta["resourceVersion"] = "owned-uid", "17"
	body, _ := json.Marshal(object)
	intent := ReleaseActionIntent{MutationVersion: 1, Version: 2, ParentID: parent.PlanID, StepKey: "patch", Resource: desired.ID, Verb: "patch", Body: body, ExpectedUID: "owned-uid", ExpectedRV: "17", AuthorizationFingerprint: auth.Fingerprint}
	if _, e = f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, intent); !errors.Is(e, ErrDenied) {
		t.Fatal("approved merge changed into SSA", e)
	}
	intent.ContentType = releaseMergeContentType
	action, e := f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, intent)
	if e != nil || action.Intent.ContentType != releaseMergeContentType {
		t.Fatal("sealed merge rejected", e)
	}
	legacy, _ := json.Marshal(ReleaseActionIntent{Version: 2})
	if string(legacy) != `{"version":2,"parentId":"","stepKey":"","resource":{"group":"","version":"","resource":"","kind":"","name":""},"verb":"","authorizationFingerprint":""}` {
		t.Fatal("historical zero policy altered intent bytes")
	}
}

func TestCoreDNSCASExceptionIsExactKeyAndRequiresDedicatedPlan(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "kube-system", Name: "coredns"}
	baseline := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"coredns","namespace":"kube-system","uid":"dns-uid","resourceVersion":"17"},"data":{"Corefile":"original","NodeHosts":"external"}}`)
	desired := releaseObjectCopy(baseline)
	releaseMap(desired["data"])["Corefile"] = "approved-candidate-checked"
	resource := releaseResourceFromObject(id, releaseCanonicalObject(desired), "declaration")
	plan := ReleasePlan{MutationVersion: 1, Mode: "coredns", Config: json.RawMessage(`{"namespace":"kube-system","coreDNS":{"configMap":"coredns","key":"Corefile"}}`), Resources: []ReleaseResource{resource}, CoreDNS: &ReleaseCoreDNSPlan{LiveIndex: 0}}
	mutation, e := releaseCoreDNSMutation(plan, resource, baseline)
	if e != nil || mutation == nil || mutation.UID != "dns-uid" || len(releaseMap(mutation.Patch["data"])) != 1 || releaseMap(mutation.Patch["data"])["NodeHosts"] != nil {
		t.Fatal("dedicated key CAS broadened", e)
	}
	plan.CoreDNS = nil
	if m, e := releaseCoreDNSMutation(plan, resource, baseline); e != nil || m != nil {
		t.Fatal("ordinary manifest acquired CoreDNS exception", e)
	}
	plan.CoreDNS = &ReleaseCoreDNSPlan{LiveIndex: 0}
	releaseMap(resource.Object["data"])["NodeHosts"] = "takeover"
	if _, e := releaseCoreDNSMutation(plan, resource, baseline); !errors.Is(e, ErrDenied) {
		t.Fatal("CoreDNS exception changed another key", e)
	}
}
func TestOwnedCASObservationRequiresActualOwnedDeletion(t *testing.T) {
	resource := ReleaseResource{ID: ReleaseResourceID{Version: "v1", Kind: "ConfigMap"}, Mutation: &ReleaseMutation{Strategy: releaseOwnedPostMerge}, Expected: releaseTestObject(`{"data":{"new":"value","outside":"preserved"}}`)}
	actual := releaseTestObject(`{"data":{"new":"value","outside":"preserved","removed":"unexpected"}}`)
	if releaseResourceExpectedMatches(resource, actual) {
		t.Fatal("removed owned field silently survived")
	}
	delete(releaseMap(actual["data"]), "removed")
	if !releaseResourceExpectedMatches(resource, actual) {
		t.Fatal("verified removal failed")
	}
}

func TestOwnedPostControllerPreparesUpdatesAndRollsBackWithoutSSAOrLosingOutsideKeys(t *testing.T) {
	f, auth, desired, _, live := ownedMutationFixture(t)
	ctx := context.Background()
	releaseMap(live["data"])["outside"] = "preserve-external"
	discovery := releaseTestObject(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","kind":"ConfigMap","namespaced":true,"verbs":["get","create","patch"]}]}`)
	api := &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{"/api/v1": discovery, "/api/v1/namespaces/app/configmaps/config": live}}}
	controller := NewReleaseController(f.s, nil)
	controller.poll = time.Millisecond
	controller.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	controller.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	files, e := InlineReleaseFile(stringMustJSON(desired.Object))
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := controller.PrepareManifest(ctx, "owned-controller-update", "node", "update-owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
	if e != nil {
		t.Fatal(e)
	}
	if prepared.Plan.MutationVersion != 1 || prepared.Plan.Resources[0].Mutation == nil {
		t.Fatal("owned POST update did not freeze its exception")
	}
	// Complete the previous fixture's isolated name gate; the setup's historical
	// creation receipt stays sealed and is used as proof throughout this test.
	if _, e = f.s.db.ExecContext(ctx, `DELETE FROM k8s_resource_gates`); e != nil {
		t.Fatal(e)
	}
	prepared, e = f.s.ApproveRelease(ctx, prepared.ID, prepared.OwnerEpoch, prepared.Revision, prepared.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = prepared
	api.cleanup = true
	registerControllerTestWriter(t, f, prepared)
	result, e := controller.Run(ctx, prepared.ID, prepared.OwnerEpoch, nil)
	if e != nil || !result.Successful() {
		t.Fatal("owned update failed", result.State, result.Cleanup, e)
	}
	current := api.objects["/api/v1/namespaces/app/configmaps/config"]
	if releaseMap(current["data"])["outside"] != "preserve-external" || releaseMap(current["data"])["remove"] != nil || releaseMap(current["data"])["message"] != "new" {
		t.Fatal("three-way update lost preservation/deletion semantics")
	}
	rollback, e := controller.PrepareRollback(ctx, "owned-controller-rollback", "node", "rollback-owner", result.ID, result.Revision, result.PlanID)
	if e != nil {
		t.Fatal("rollback prepare", e)
	}
	if rollback.Plan.Resources[0].Mutation == nil {
		t.Fatal("rollback lost sealed owned origin chain")
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	registerControllerTestWriter(t, f, rollback)
	restored, e := controller.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if e != nil || restored.State != "rolled_back" {
		t.Fatal("owned rollback failed", restored.State, e)
	}
	current = api.objects["/api/v1/namespaces/app/configmaps/config"]
	if releaseMap(current["data"])["outside"] != "preserve-external" || releaseMap(current["data"])["remove"] != "owned" || releaseMap(current["data"])["added"] != nil || releaseMap(current["data"])["message"] != "old" {
		t.Fatal("rollback erased external keys or left owned update fields")
	}
}

func TestOwnedImageCASPreservesExternalHPAScalingAndUnrelatedContainerFields(t *testing.T) {
	f, auth, desired, _, live := ownedMutationFixture(t, true)
	releaseMap(live["spec"])["replicas"] = float64(7)
	mutation, e := f.s.ownedPostMutation(context.Background(), auth, desired, live)
	if e != nil || mutation == nil {
		t.Fatal("image-only update was blocked by independent HPA scale", e)
	}
	if releaseMap(mutation.Patch["spec"])["replicas"] != nil || releaseMap(mutation.Managed["spec"])["replicas"] != nil {
		t.Fatal("image-only mutation claimed externally scaled replicas")
	}
	patched := applyFixtureMerge(live, mutation.Patch)
	containers := releaseItems(releaseMap(releaseMap(releaseMap(patched["spec"])["template"])["spec"])["containers"])
	if releaseMap(patched["spec"])["replicas"] != float64(7) || len(releaseItems(releaseMap(containers[0])["env"])) != 1 {
		t.Fatal("image-only CAS lost HPA replicas or managed environment")
	}
}

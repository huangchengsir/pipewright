package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func review112OwnedSeed(t *testing.T, id ReleaseResourceID, object map[string]any, admit func(map[string]any)) (*fixture, ReleaseAuthorization, map[string]any) {
	t.Helper()
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	scope := releaseRequestedAccess(id, "create")
	scope.Actions = uniqueReleaseActions(append(scope.Actions, "patch", "delete", "rollback"))
	if id.Kind == "Deployment" {
		scope.Containers = []string{"app", "first", "second"}
	}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	req := p.Authorization.Request
	req.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	health := "declaration"
	if id.Kind == "Deployment" {
		health = "workload"
	}
	resource := releaseResourceFromObject(id, object, health)
	plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300}
	parent, e := f.s.CreateRelease(ctx, "order-seed", "node", "seed-owner", plan)
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
	body, _ := json.Marshal(object)
	action, e := f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, ReleaseActionIntent{Version: 2, ParentID: parent.PlanID, StepKey: "create", Resource: id, Verb: "create", Body: body, AuthorizationFingerprint: auth.Fingerprint})
	if e != nil {
		t.Fatal(e)
	}
	var live map[string]any
	json.Unmarshal(action.Intent.Body, &live)
	meta := releaseMap(live["metadata"])
	meta["uid"] = "order-owned-uid"
	meta["resourceVersion"] = "17"
	meta["generation"] = 1
	if admit != nil {
		admit(live)
	}
	if _, e = f.s.RecordReleaseAction(ctx, action.ID, parent.OwnerEpoch, action.Revision, "verified", ReleaseActionReceipt{Resource: id, UID: "order-owned-uid", ResourceVersion: "17", Object: live}); e != nil {
		t.Fatal(e)
	}
	// End only the fixture's isolated name gate. The sealed ownership action and
	// its real Store receipt remain untouched and are revalidated by the compiler.
	if _, e = f.s.db.ExecContext(ctx, `DELETE FROM k8s_resource_gates`); e != nil {
		t.Fatal(e)
	}
	return f, auth, live
}

type review112OrderAPI struct{ *review110API }

func (a *review112OrderAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	raw, e := a.review110API.Read(ctx, path, query)
	if e != nil || !strings.HasSuffix(path, "/pods") {
		return raw, e
	}
	var list map[string]any
	if json.Unmarshal(raw, &list) != nil {
		return nil, ErrInvalid
	}
	clear(raw)
	for _, v := range releaseItems(list["items"]) {
		pod := releaseMap(v)
		statuses := []any{}
		for _, v := range releaseItems(releaseMap(pod["spec"])["initContainers"]) {
			c := releaseMap(v)
			statuses = append(statuses, map[string]any{"name": c["name"], "image": c["image"], "imageID": c["image"], "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}})
		}
		releaseMap(pod["status"])["initContainerStatuses"] = statuses
	}
	return json.Marshal(list)
}
func (a *review112OrderAPI) DryRun(_ context.Context, in kubeprotocol.Request) (map[string]any, error) {
	var body map[string]any
	if json.Unmarshal(in.Body, &body) != nil {
		return nil, ErrInvalid
	}
	if in.ContentType == releaseMergeContentType {
		return applyFixtureMerge(a.objects[in.Path], body), nil
	}
	return releaseOverlayIntent(a.objects[in.Path], body)
}
func (a *review112OrderAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var body map[string]any
	if json.Unmarshal(action.Intent.Body, &body) != nil {
		return action, ErrInvalid
	}
	live := applyFixtureMerge(a.objects[path], body)
	meta := releaseMap(live["metadata"])
	meta["uid"] = "order-owned-uid"
	meta["resourceVersion"] = fmt.Sprint(18 + a.writes)
	meta["generation"] = 1 + a.writes
	live["status"] = map[string]any{"observedGeneration": 1 + a.writes, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	a.objects[path] = live
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: releaseString(meta["uid"]), ResourceVersion: releaseString(meta["resourceVersion"]), Object: live, Health: ReleaseHealth{State: "pending"}})
}
func TestReview112ControllerEnvInitOrderAndExactRollback(t *testing.T) {
	ctx := context.Background()
	image := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "order"}
	original := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"order","namespace":"app"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"name":"app","image":"` + image + `","env":[{"name":"KEEP","value":"base-done"}]}],"initContainers":[{"name":"first","image":"` + image + `"},{"name":"second","image":"` + image + `"}]}}}}`)
	f, auth, live := review112OwnedSeed(t, id, original, func(o map[string]any) {
		o["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	})
	path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
	api := &review112OrderAPI{review110API: &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}}, workload: id, observedImageID: image}}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	desired := releaseObjectCopy(original)
	spec := releaseMap(releaseMap(releaseMap(desired["spec"])["template"])["spec"])
	inits := releaseItems(spec["initContainers"])
	spec["initContainers"] = []any{inits[1], inits[0]}
	releaseMap(releaseItems(spec["containers"])[0])["env"] = []any{map[string]any{"name": "EARLY", "value": "prefix"}, map[string]any{"name": "KEEP", "value": "$(EARLY)-done"}}
	resource := releaseResourceFromObject(id, desired, "workload")
	plan := ReleasePlan{Version: 2, MutationVersion: 1, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{resource}, Baseline: []ReleaseResource{releaseResourceFromObject(id, releaseObjectCopy(live), "workload")}, TimeoutSeconds: 300}
	if e := c.dryRunReleaseResources(ctx, "order-update-owner", &plan); e != nil || api.writes != 0 {
		t.Fatal("valid ordered update preparation failed or wrote", e)
	}
	prepared, e := f.s.CreateRelease(ctx, "order-update", "node", "order-update-owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	prepared, e = f.s.ApproveRelease(ctx, prepared.ID, prepared.OwnerEpoch, prepared.Revision, prepared.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = prepared
	api.cleanup = true
	registerControllerTestWriter(t, f, prepared)
	done, e := c.Run(ctx, prepared.ID, prepared.OwnerEpoch, nil)
	if e != nil || !done.Successful() {
		t.Fatal("ordered update failed", e)
	}
	current := api.objects[path]
	if !releaseResourceExpectedMatches(done.Plan.Resources[0], current) {
		t.Fatal("verified receipt lost order")
	}
	currentSpec := releaseMap(releaseMap(releaseMap(current["spec"])["template"])["spec"])
	if releaseMap(releaseItems(currentSpec["initContainers"])[0])["name"] != "second" || releaseMap(releaseItems(releaseMap(releaseItems(currentSpec["containers"])[0])["env"])[0])["name"] != "EARLY" {
		t.Fatal("compiled order omitted")
	}
	facts, e := f.s.ListReleaseActions(ctx, done.ID)
	if e != nil || len(facts) != 1 || facts[0].State != "verified" || facts[0].Receipt.Health.State != "ready" {
		t.Fatal("actual ledger health/receipt did not verify", e)
	}
	rollback, e := c.PrepareRollback(ctx, "order-rollback", "node", "order-rb-owner", done.ID, done.Revision, done.PlanID)
	if e != nil {
		t.Fatal("rollback prepare", e)
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	registerControllerTestWriter(t, f, rollback)
	restored, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if e != nil || restored.State != "rolled_back" || api.writes != 2 {
		t.Fatal("exact original order rollback failed", e)
	}
	finalSpec := releaseMap(releaseMap(releaseMap(api.objects[path]["spec"])["template"])["spec"])
	if !releaseExactJSON(finalSpec, releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])) {
		t.Fatal("rollback did not restore exact originally owned order")
	}
}
func TestReview112ServiceStoreDefaultAndPortIdentityUpdate(t *testing.T) {
	for _, changeKey := range []bool{false, true} {
		t.Run(fmt.Sprint(changeKey), func(t *testing.T) {
			id := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "api"}
			object := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"app"},"spec":{"selector":{"app":"api"},"ports":[{"name":"web","port":80,"targetPort":"http"}]}}`)
			f, auth, live := review112OwnedSeed(t, id, object, func(o map[string]any) {
				s := releaseMap(o["spec"])
				s["clusterIP"] = "10.0.0.1"
				s["clusterIPs"] = []any{"10.0.0.1"}
				s["ipFamilies"] = []any{"IPv4"}
				releaseMap(releaseItems(s["ports"])[0])["protocol"] = "TCP"
			})
			next := releaseObjectCopy(object)
			port := releaseMap(releaseItems(releaseMap(next["spec"])["ports"])[0])
			if changeKey {
				port["port"] = 81
			} else {
				port["targetPort"] = "http2"
			}
			r := releaseResourceFromObject(id, next, "declaration")
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			api := &review112OrderAPI{review110API: &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}}, workload: id}}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			plan := ReleasePlan{Version: 2, MutationVersion: 1, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{r}, Baseline: []ReleaseResource{releaseResourceFromObject(id, releaseObjectCopy(live), "declaration")}, TimeoutSeconds: 300}
			if e := c.dryRunReleaseResources(context.Background(), "svc-owner", &plan); e != nil || api.writes != 0 {
				t.Fatal("owned defaulted Service prepare", e)
			}
			if plan.Resources[0].Mutation == nil {
				t.Fatal("sealed create provenance ignored")
			}
			admitted := applyFixtureMerge(live, plan.Resources[0].Mutation.Patch)
			if releaseMap(admitted["spec"])["clusterIP"] != "10.0.0.1" {
				t.Fatal("immutable identity lost")
			}
			p := releaseMap(releaseItems(releaseMap(admitted["spec"])["ports"])[0])
			if !changeKey && p["protocol"] != "TCP" {
				t.Fatal("native default lost")
			}
			if changeKey && releaseInt(p["port"]) != 81 {
				t.Fatal("port key edit omitted")
			}
		})
	}
}
func TestReview112HelmUsesIdenticalNativeOrderingAndFrozenBody(t *testing.T) {
	f, auth, _, fact, live := ownedMutationFixture(t, true, true)
	parent, e := f.s.GetRelease(context.Background(), fact.ReleaseID)
	if e != nil {
		t.Fatal(e)
	}
	desired := releaseCanonicalObject(live)
	spec := releaseMap(releaseMap(releaseMap(desired["spec"])["template"])["spec"])
	container := releaseMap(releaseItems(spec["containers"])[0])
	container["env"] = append(review112Members("EARLY"), releaseItems(container["env"])...)
	r := releaseResourceFromObject(parent.Plan.Helm.Rendered[0].ID, desired, "workload")
	r.Mutation, e = f.s.ownedPostMutationForHelm(context.Background(), auth, r, live, parent.Plan.Helm)
	if e != nil || r.Mutation == nil || r.Mutation.Strategy != releaseHelmOwnedPostMerge {
		t.Fatal("shared Helm compile", e)
	}
	admitted := applyFixtureMerge(live, r.Mutation.Patch)
	if !releaseApprovedFieldsMatch(r.ID, desired, admitted) {
		t.Fatal("Helm compiler changed ordered intent")
	}
	record := ReleaseRecord{Plan: ReleasePlan{MutationVersion: 1, Helm: parent.Plan.Helm}}
	record.Plan.Helm.Rendered = []ReleaseResource{r}
	if _, e = releaseHelmPendingMutation(record, r.ID, releaseObjectCopy(desired)); e != nil {
		t.Fatal(e)
	}
	bad := releaseObjectCopy(desired)
	c := releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(bad["spec"])["template"])["spec"])["containers"])[0])
	env := releaseItems(c["env"])
	c["env"] = []any{env[1], env[0]}
	if _, e = releaseHelmPendingMutation(record, r.ID, bad); e == nil {
		t.Fatal("Helm permit accepted reordered frozen CLI body")
	}
}

func TestReview112CustomAtomicOrderReceiptBoundary(t *testing.T) {
	for _, opposite := range []bool{false, true} {
		t.Run(fmt.Sprint(opposite), func(t *testing.T) {
			ctx := context.Background()
			id := ReleaseResourceID{Group: "custom.invalid", Version: "v1", Resource: "workflows", Kind: "Workflow", Namespace: "app", Name: "flow"}
			original := releaseTestObject(`{"apiVersion":"custom.invalid/v1","kind":"Workflow","metadata":{"name":"flow","namespace":"app"},"spec":{"steps":[{"name":"A","value":"one"},{"name":"B","value":"two"}]}}`)
			f, auth, _ := review112OwnedSeed(t, id, original, nil)
			desired := releaseObjectCopy(original)
			items := releaseItems(releaseMap(desired["spec"])["steps"])
			releaseMap(desired["spec"])["steps"] = []any{items[1], items[0]}
			resource := releaseResourceFromObject(id, desired, "declaration")
			resource.Expected = releaseObjectCopy(desired)
			parent, e := f.s.CreateRelease(ctx, "custom-update", "node", "custom-owner", ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300})
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
			bodyObject := releaseObjectCopy(desired)
			releaseMap(bodyObject["metadata"])["uid"] = "order-owned-uid"
			releaseMap(bodyObject["metadata"])["resourceVersion"] = "17"
			body, _ := json.Marshal(bodyObject)
			action, e := f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, ReleaseActionIntent{Version: 2, ParentID: parent.PlanID, StepKey: "patch", Resource: id, Verb: "patch", ExpectedUID: "order-owned-uid", ExpectedRV: "17", Body: body, AuthorizationFingerprint: auth.Fingerprint})
			if e != nil {
				t.Fatal(e)
			}
			var actual map[string]any
			json.Unmarshal(action.Intent.Body, &actual)
			releaseMap(actual["metadata"])["uid"] = "order-owned-uid"
			releaseMap(actual["metadata"])["resourceVersion"] = "18"
			if opposite {
				releaseMap(actual["spec"])["steps"] = releaseItems(releaseMap(original["spec"])["steps"])
			}
			_, e = f.s.RecordReleaseAction(ctx, action.ID, parent.OwnerEpoch, action.Revision, "verified", ReleaseActionReceipt{Resource: id, UID: "order-owned-uid", ResourceVersion: "18", Object: actual})
			if opposite && e != ErrConflict || !opposite && e != nil {
				t.Fatal("atomic receipt boundary accepted old order or denied approved order", e)
			}
		})
	}
}

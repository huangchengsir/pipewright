package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

type review110API struct {
	*probeControllerAPI
	zero            bool
	workload        ReleaseResourceID
	observedImageID string
}

func (a *review110API) Read(ctx context.Context, path, query string) ([]byte, error) {
	if a.zero && strings.HasSuffix(path, "/pods") {
		return []byte(`{"apiVersion":"v1","kind":"PodList","items":[]}`), nil
	}
	if !a.zero && (strings.HasSuffix(path, "/pods") || strings.HasSuffix(path, "/replicasets")) {
		named, _ := resourceAPI(a.workload).Path(a.workload.Namespace, a.workload.Name)
		object := a.objects[named]
		template := releaseMap(releaseMap(object["spec"])["template"])
		uid := releaseMap(object["metadata"])["uid"]
		if strings.HasSuffix(path, "/replicasets") {
			return json.Marshal(map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSetList", "items": []any{map[string]any{"kind": "ReplicaSet", "metadata": map[string]any{"name": "rs", "namespace": "app", "uid": "rs", "ownerReferences": []any{map[string]any{"uid": uid, "controller": true}}}, "spec": map[string]any{"template": template}}}})
		}
		statuses := []any{}
		for _, v := range releaseItems(releaseMap(template["spec"])["containers"]) {
			c := releaseMap(v)
			statuses = append(statuses, map[string]any{"name": c["name"], "image": c["image"], "imageID": a.observedImageID, "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}})
		}
		return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "PodList", "items": []any{map[string]any{"kind": "Pod", "metadata": map[string]any{"name": "pod", "namespace": "app", "uid": "pod", "ownerReferences": []any{map[string]any{"uid": "rs", "controller": true}}}, "spec": template["spec"], "status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": statuses}}}})
	}
	return a.probeControllerAPI.Read(ctx, path, query)
}
func (a *review110API) DryRun(_ context.Context, in kubeprotocol.Request) (map[string]any, error) {
	var body map[string]any
	if json.Unmarshal(in.Body, &body) != nil {
		return nil, ErrInvalid
	}
	if a.zero {
		return body, nil
	}
	return releaseOverlayIntent(a.objects[in.Path], body)
}
func (a *review110API) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if a.zero {
		return a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	}
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var patch map[string]any
	if json.Unmarshal(action.Intent.Body, &patch) != nil {
		return action, ErrInvalid
	}
	live, e := releaseOverlayIntent(a.objects[path], patch)
	if e != nil {
		return action, e
	}
	meta := releaseMap(live["metadata"])
	meta["resourceVersion"] = "21"
	meta["generation"] = 2
	live["status"] = map[string]any{"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	a.objects[path] = live
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: releaseString(meta["uid"]), ResourceVersion: "21", Object: live, Health: ReleaseHealth{State: "pending"}})
}
func TestReview110ZeroJobLedgerAndRetainedRollback(t *testing.T) {
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "zero"}
	scope := releaseRequestedAccess(id, "create")
	scope.Containers = []string{"app"}
	scope.Actions = uniqueReleaseActions(append(scope.Actions, "delete", "rollback"))
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := p.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	obj := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"zero","namespace":"app"},"spec":{"completions":0,"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"app","image":"registry.invalid/app@sha256:` + strings.Repeat("a", 64) + `"}]}}}}`)
	plan := ReleasePlan{Version: 2, Mode: "manifest", MutationVersion: 1, Authorization: auth, Resources: []ReleaseResource{releaseResourceFromObject(id, obj, "workload")}, TimeoutSeconds: 300}
	api := &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}, zero: true}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	if e = c.dryRunReleaseResources(ctx, "zero-owner", &plan); e != nil {
		t.Fatal(e)
	}
	source, e := f.s.CreateRelease(ctx, "zero-run", "node", "zero-owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	source, e = f.s.ApproveRelease(ctx, source.ID, source.OwnerEpoch, source.Revision, source.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = source
	api.cleanup = true
	registerControllerTestWriter(t, f, source)
	source, e = c.Run(ctx, source.ID, source.OwnerEpoch, nil)
	if e != nil || !source.Successful() {
		t.Fatal("zero Job did not complete without Pods", e)
	}
	facts, e := f.s.ListReleaseActions(ctx, source.ID)
	if e != nil || len(facts) != 1 || len(facts[0].Receipt.Health.Containers) != 0 {
		t.Fatal("zero Job fabricated container evidence", e)
	}
	api.cleanup = false
	rollback, e := c.PrepareRollback(ctx, "zero-rollback", "node", "zero-rollback-owner", source.ID, source.Revision, source.PlanID)
	if e != nil || len(rollback.Plan.Retained) != 1 {
		t.Fatal("zero Job bypassed conservative external effect retention", e)
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	api.cleanup = true
	registerControllerTestWriter(t, f, rollback)
	rollback, e = c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if !errors.Is(e, ErrRollout) || rollback.State != "partial_failed" {
		t.Fatal("retained Job effects became full rollback", e)
	}
	writes := api.writes
	api.cleanup = false
	result, e := c.ReconcileRelease(ctx, rollback.ID, rollback.Revision)
	if e != nil || result.Successful() || result.State != "partial_failed" || api.writes != writes {
		t.Fatal("Job effect retention lost on read", e)
	}
}
func TestReview110RetryPinThroughApprovedRollback(t *testing.T) {
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	id := p.Resources[0].ID
	scope := releaseRequestedAccess(id, "patch")
	scope.Actions = append(scope.Actions, "rollback")
	scope.Containers = []string{"app"}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := p.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	original := releaseObjectCopy(p.Resources[0].Object)
	releaseMap(original["metadata"])["uid"] = "same-workload"
	releaseMap(original["metadata"])["resourceVersion"] = "17"
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app:prior-tag"
	oldPin := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
	nextImage := "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
	desired := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": id.Name, "namespace": id.Namespace}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": nextImage}}}}}}
	target := releaseResourceFromObject(id, desired, "workload")
	target.ImageOnly = true
	baseline := releaseResourceFromObject(id, original, "workload")
	baseline.RollbackImages = map[string]string{"containers/app": oldPin}
	p = ReleasePlan{Version: 2, Mode: "image", MutationVersion: 1, Authorization: auth, Resources: []ReleaseResource{target}, Baseline: []ReleaseResource{baseline}, TimeoutSeconds: 300}
	prior, e := f.s.CreateRelease(ctx, "pin-prior", "node", "pin-prior-owner", p)
	if e != nil {
		t.Fatal(e)
	}
	prior, e = f.s.SetReleaseTerminal(ctx, prior.ID, prior.OwnerEpoch, prior.Revision, "failed", "release_failed")
	if e != nil {
		t.Fatal(e)
	}
	path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
	api := &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: releaseObjectCopy(original)}}}, workload: id}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	retry, e := c.PrepareRetry(ctx, "pin-retry", "node", "pin-retry-owner", prior.ID, auth)
	if e != nil {
		t.Fatal(e)
	}
	retry, e = f.s.ApproveRelease(ctx, retry.ID, retry.OwnerEpoch, retry.Revision, retry.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = retry
	api.cleanup = true
	registerControllerTestWriter(t, f, retry)
	retry, e = c.Run(ctx, retry.ID, retry.OwnerEpoch, nil)
	if e != nil || !retry.Successful() {
		t.Fatal("retry failed", retry.State, e)
	}
	api.cleanup = false
	rollback, e := c.PrepareRollback(ctx, "pin-rollback", "node", "pin-rollback-owner", retry.ID, retry.Revision, retry.PlanID)
	if e != nil {
		t.Fatal("usable retry pin not retained", e)
	}
	rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rollback
	api.cleanup = true
	registerControllerTestWriter(t, f, rollback)
	result, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
	if e != nil || result.State != "rolled_back" || !result.Successful() {
		t.Fatal("approved pinned rollback failed", result.State, e)
	}
	current := releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(api.objects[path]["spec"])["template"])["spec"])["containers"])[0])["image"]
	if current != oldPin || api.writes != 2 {
		t.Fatal("old tag was re-resolved or writes replayed")
	}
	loaded, e := f.s.GetRelease(ctx, prior.ID)
	if e != nil || loaded.PlanID != prior.PlanID || loaded.Plan.Baseline[0].RollbackImages["containers/app"] != oldPin {
		t.Fatal("sealed original baseline altered", e)
	}
}

func TestReview110ChangedBaselineRequiresFreshHealthyPinCapture(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unobserved_tag", true: "current_owned_digest"}[observed], func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			id := p.Resources[0].ID
			old := releaseObjectCopy(p.Resources[0].Object)
			releaseMap(old["metadata"])["uid"] = "same"
			releaseMap(old["metadata"])["resourceVersion"] = "17"
			releaseMap(old["metadata"])["generation"] = 1
			releaseMap(old["spec"])["replicas"] = 1
			releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(old["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app:old"
			baseline := releaseResourceFromObject(id, old, "workload")
			oldPin := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
			baseline.RollbackImages = map[string]string{"containers/app": oldPin}
			p.Baseline = []ReleaseResource{baseline}
			current := releaseObjectCopy(old)
			releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(current["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app:changed"
			current["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			api := &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: current}}}, workload: id}
			freshPin := "registry.invalid/app@sha256:" + strings.Repeat("c", 64)
			if observed {
				api.observedImageID = "docker-pullable://" + freshPin
			}
			next := releaseResourceFromObject(id, current, "workload")
			c := NewReleaseController(f.s, nil)
			e := c.preserveRetryRollbackImages(context.Background(), api, ReleaseRecord{Plan: p}, p.Resources[0], current, &next)
			if !observed {
				if !errors.Is(e, ErrRollbackBaseline) {
					t.Fatal("changed tag without fresh proof accepted", e)
				}
				return
			}
			if e != nil || next.RollbackImages["containers/app"] != freshPin || next.RollbackImages["containers/app"] == oldPin {
				t.Fatal("old pin copied across changed image or fresh proof lost", e)
			}
		})
	}
}

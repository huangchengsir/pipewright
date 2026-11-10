package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

type review111CRI struct {
	*review110API
	opaqueDisplay bool
}

func (a *review111CRI) Read(ctx context.Context, path, query string) ([]byte, error) {
	raw, e := a.review110API.Read(ctx, path, query)
	if e != nil || !a.opaqueDisplay || !strings.HasSuffix(path, "/pods") {
		return raw, e
	}
	var list map[string]any
	if json.Unmarshal(raw, &list) != nil {
		return nil, ErrInvalid
	}
	clear(raw)
	for _, p := range releaseItems(list["items"]) {
		for _, s := range releaseItems(releaseMap(releaseMap(p)["status"])["containerStatuses"]) {
			releaseMap(s)["image"] = "sha256:" + strings.Repeat("c", 64)
		}
	}
	return json.Marshal(list)
}
func TestReview111CaptureRetryApprovedRollback(t *testing.T) {
	for _, opaque := range []bool{false, true} {
		t.Run(map[bool]string{false: "tag-display-control", true: "opaque-display-pullable-id"}[opaque], func(t *testing.T) {
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
			m := releaseMap(original["metadata"])
			m["uid"] = "same-workload"
			m["resourceVersion"] = "17"
			m["generation"] = 1
			releaseMap(original["spec"])["replicas"] = 1
			oldTag := "registry.invalid/app:original-tag"
			oldPin := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
			nextImage := "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
			releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])["containers"])[0])["image"] = oldTag
			original["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			api := &review111CRI{review110API: &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: original}}}, workload: id, observedImageID: oldPin}, opaqueDisplay: opaque}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
			baseline := releaseResourceFromObject(id, releaseObjectCopy(original), "workload")
			healthResource := baseline
			healthResource.Object = releaseCanonicalObject(baseline.Object)
			related, e := c.related(ctx, api, healthResource)
			if e != nil {
				t.Fatal(e)
			}
			health := EvaluateReleaseHealth(healthResource, original, related)
			c.captureRollbackImages(ctx, api, &baseline)
			desired := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": id.Name, "namespace": id.Namespace}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": nextImage}}}}}}
			target := releaseResourceFromObject(id, desired, "workload")
			target.ImageOnly = true
			plan := ReleasePlan{Version: 2, Mode: "image", MutationVersion: 1, Authorization: auth, Resources: []ReleaseResource{target}, Baseline: []ReleaseResource{baseline}, TimeoutSeconds: 300}
			prior, e := f.s.CreateRelease(ctx, "diag111-prior", "node", "prior-owner", plan)
			if e != nil {
				t.Fatal(e)
			}
			prior, e = f.s.SetReleaseTerminal(ctx, prior.ID, prior.OwnerEpoch, prior.Revision, "failed", "release_failed")
			if e != nil {
				t.Fatal(e)
			}
			retry, e := c.PrepareRetry(ctx, "diag111-retry", "node", "retry-owner", prior.ID, auth)
			if e != nil {
				t.Fatal("retry preparation failed", e)
			}
			retry, e = f.s.ApproveRelease(ctx, retry.ID, retry.OwnerEpoch, retry.Revision, retry.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			api.observedImageID = nextImage
			api.record = retry
			api.cleanup = true
			registerControllerTestWriter(t, f, retry)
			retry, e = c.Run(ctx, retry.ID, retry.OwnerEpoch, nil)
			if e != nil || !retry.Successful() {
				t.Fatal("retry not successful", e)
			}
			api.cleanup = false
			rollback, e := c.PrepareRollback(ctx, "diag111-rollback", "node", "rollback-owner", retry.ID, retry.Revision, retry.PlanID)
			t.Logf("opaqueDisplay=%t initialHealth=%s initialReason=%s capturedPins=%d retryPins=%d retryReady=%t rollbackPrepared=%t", opaque, health.State, health.Reason, len(baseline.RollbackImages), len(retry.Plan.Baseline[0].RollbackImages), retry.Successful(), e == nil)
			if e != nil {
				t.Fatal("pullable old runtime image identity did not support later rollback", e)
			}
			if len(baseline.RollbackImages) != 1 || baseline.RollbackImages["containers/app"] != oldPin || len(retry.Plan.Baseline[0].RollbackImages) != 1 {
				t.Fatal("original native pin not sealed and retained")
			}

			rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			api.observedImageID = oldPin
			api.record = rollback
			api.cleanup = true
			registerControllerTestWriter(t, f, rollback)
			rollback, e = c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
			if e != nil || rollback.State != "rolled_back" || !rollback.Successful() {
				t.Fatal("approved rollback did not restore original native pin", e)
			}
			image := releaseString(releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(api.objects[path]["spec"])["template"])["spec"])["containers"])[0])["image"])
			if image != oldPin {
				t.Fatal("rollback used moved mutable tag")
			}
			saved, e := f.s.GetRelease(ctx, prior.ID)
			if e != nil || saved.State != "failed" || saved.Plan.Baseline[0].RollbackImages["containers/app"] != oldPin || api.writes != 2 {
				t.Fatal("original known failure history changed", e)
			}

		})
	}
}

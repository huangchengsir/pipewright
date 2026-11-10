package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

type review106FailingProbeAPI struct {
	*probeControllerAPI
	unsafe string
}

func (a *review106FailingProbeAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	action, e := a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e == nil && in.Resource.Kind == "Job" && in.Verb != "delete" {
		named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
		a.objects[named]["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Failed", "status": "True"}}}
		switch a.unsafe {
		case "uid":
			releaseMap(a.objects[named]["metadata"])["uid"] = "replacement-uid"
		case "marker":
			releaseMap(releaseMap(a.objects[named]["metadata"])["annotations"])[ReleaseActionAnnotation] = "foreign-action"
		case "spec":
			releaseMap(a.objects[named]["spec"])["backoffLimit"] = 1
		case "unknown":
			_, e = a.f.db.DB.Exec("UPDATE k8s_release_actions SET state='outcome_unknown' WHERE id=?", action.ID)
		}
	}
	return action, e
}
func TestReview106KnownFailedOwnedProbeCleansAndRetries(t *testing.T) {
	for _, scenario := range []string{"known", "uid", "marker", "spec", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			f, p := releaseStoreFixture(t, st)
			sourceID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
			probeID := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "check-http"}
			sourceScope := ReleaseAccess{ID: sourceID, Actions: []string{"get", "create", "patch"}}
			probeScope := ReleaseAccess{ID: probeID, Actions: []string{"get", "create", "delete", "probe", "related-pods"}, Containers: []string{"probe"}}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{sourceScope, probeScope}, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			request := p.Authorization.Request
			request.Resources = []ReleaseAccess{sourceScope, probeScope}
			catalog, e := f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			obj := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "config", "namespace": "app"}, "data": map[string]any{"key": "value"}}
			p = ReleasePlan{Version: 2, Mode: "manifest", Resources: []ReleaseResource{releaseResourceFromObject(sourceID, obj, "declaration")}, TimeoutSeconds: 300}
			api := &review106FailingProbeAPI{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}, unsafe: scenario}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			config := []byte("{\"namespace\":\"app\",\"checks\":[{\"kind\":\"http\",\"name\":\"check-http\",\"target\":\"http://service.app:8080/healthz\",\"expected\":\"v2\"}]}")
			extra, e := c.appendReleaseChecks(ctx, api, &p, catalog, config)
			if e != nil {
				t.Fatal(e)
			}
			request.Resources = mergeReleaseScopes([]ReleaseAccess{sourceScope}, extra)
			p.Authorization, e = f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			r, e := f.s.CreateRelease(ctx, "failed-probe-review", "node", "owner", p)
			if e != nil {
				t.Fatal(e)
			}
			r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			api.record = r
			api.cleanup = true
			registerControllerTestWriter(t, f, r)
			c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			work := ctx
			if scenario != "known" {
				var cancel context.CancelFunc
				work, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
			}
			result, e := c.Run(work, r.ID, r.OwnerEpoch, nil)
			if e == nil {
				t.Fatal("fixture probe did not fail")
			}
			if scenario == "spec" && !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal("foreign spec fixture must end through caller observation deadline", e)
			}
			if !result.OwnerFenced || result.Cleanup != CleanupVerified {
				t.Fatal("fixture writer not positively cleaned", result.State, result.Cleanup)
			}
			actions, readErr := f.s.ListReleaseActions(ctx, result.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			failed, deleted := false, false
			for _, action := range actions {
				if action.Intent.Resource == probeID && action.Receipt != nil {
					if action.Intent.Verb == "delete" && action.Receipt.Deleted {
						deleted = true
					}
					if action.Intent.Verb != "delete" && action.Receipt.Health.State == "failed" {
						failed = true
					}
				}
			}
			if scenario != "known" {
				if deleted || api.objects["/apis/batch/v1/namespaces/app/jobs/check-http"] == nil {
					t.Fatal("unproved or unknown Job was deleted", scenario)
				}
				for _, a := range actions {
					if a.Intent.Verb == "delete" {
						t.Fatal("delete submitted before ownership proof")
					}
				}
				before := api.writes
				if _, err := c.PrepareRetry(ctx, "unsafe-retry", "node", "owner", result.ID, catalog); err == nil {
					t.Fatal("unsafe Job effect reused", scenario)
				}
				if api.writes != before {
					t.Fatal("retry wrote while checking")
				}
				return
			}

			if !failed || !deleted {
				t.Fatal("failed probe evidence or exact deletion receipt lost", failed, deleted)
			}
			if api.objects["/apis/batch/v1/namespaces/app/jobs/check-http"] != nil {
				t.Fatal("known failed owned probe Job remains")
			}
			if _, e = c.PrepareRetry(ctx, "failed-probe-retry", "node", "owner", result.ID, catalog); e != nil {
				t.Fatalf("known failed owned probe prevents immediate retry after verified tool cleanup: %v", e)
			}
		})
	}

}

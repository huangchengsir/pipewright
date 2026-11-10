package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

type review106PartialAPI struct{ *probeControllerAPI }

func (a *review106PartialAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.Resource.Name != "second" {
		return a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	}
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	action, e = a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "rejected", ReleaseActionReceipt{Resource: in.Resource})
	if e != nil {
		return action, e
	}
	return action, ErrDenied
}
func TestReview106PartialFirstCreateRollbackUsesOnlyProvenEffects(t *testing.T) {
	for _, scenario := range []string{"settled", "unknown", "verified-missing", "baseline-missing", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, seed := releaseStoreFixture(t, st)
				scopes := []ReleaseAccess{}
				resources := []ReleaseResource{}
				for _, name := range []string{"first", "second", "third"} {
					id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: name}
					scopes = append(scopes, ReleaseAccess{ID: id, Actions: []string{"get", "create", "patch", "delete", "rollback"}})
					resources = append(resources, releaseResourceFromObject(id, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": "app"}, "data": map[string]any{"owned": "value"}}, "declaration"))
				}
				if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
					t.Fatal(e)
				}
				request := seed.Authorization.Request
				request.Resources = scopes
				auth, e := f.s.AuthorizeRelease(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: resources, TimeoutSeconds: 300}
				if scenario == "baseline-missing" {
					prior := releaseObjectCopy(resources[2].Object)
					releaseMap(prior["metadata"])["uid"] = "prior-uid"
					plan.Baseline = []ReleaseResource{releaseResourceFromObject(resources[2].ID, prior, "declaration")}
				}
				api := &review106PartialAPI{&probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}}
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				r, e := f.s.CreateRelease(ctx, "partial-first", "node", "owner", plan)
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
				result, e := c.Run(ctx, r.ID, r.OwnerEpoch, nil)
				if e == nil || result.State != "partial_failed" || result.Cleanup != CleanupVerified {
					t.Fatal("seed not settled partial", result.State, e)
				}
				if scenario == "unknown" {
					st.DB.Exec("UPDATE k8s_release_actions SET state='outcome_unknown' WHERE release_id=? AND step_key='batch-0-resource-1'", r.ID)
				}
				if scenario == "verified-missing" {
					delete(api.objects, "/api/v1/namespaces/app/configmaps/first")
				}
				if scenario == "foreign" {
					api.objects["/api/v1/namespaces/app/configmaps/third"] = releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"third","namespace":"app","uid":"foreign"},"data":{"owned":"value"}}`)
				}
				api.cleanup = false
				before := api.writes
				rollback, e := c.PrepareRollback(ctx, "new-rollback", "node", "new-owner", result.ID, result.Revision, result.PlanID)
				if api.writes != before {
					t.Fatal("preparation wrote to cluster")
				}
				if scenario != "settled" {
					if e == nil {
						t.Fatal("missing/unknown/foreign evidence accepted", scenario)
					}
					return
				}
				if e != nil {
					t.Fatal("settled rejected and unsubmitted absence blocked rollback", e)
				}
				if len(rollback.Plan.Resources) != 1 || rollback.Plan.Resources[0].ID.Name != "first" || !rollback.Plan.Resources[0].Delete || !rollback.Plan.ForceApproval {
					t.Fatal("rollback exceeded proven first-create effect")
				}
				rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = rollback
				api.cleanup = true
				registerControllerTestWriter(t, f, rollback)
				done, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
				if e != nil || done.State != "rolled_back" {
					t.Fatal("approved exact rollback failed", done.State, e)
				}
				if _, e = api.Read(ctx, "/api/v1/namespaces/app/configmaps/first", ""); !errors.Is(e, ErrNotFound) {
					t.Fatal("first create not deleted", e)
				}
			})
		})
	}
}

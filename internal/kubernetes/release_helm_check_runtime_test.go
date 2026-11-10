package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func TestHelmSupplementalStorageConsumersKeepCheckStepIDsAndUnknownFence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bind, unknown bool
		want          string
	}{{"bound", true, false, "ready"}, {"still_pending", false, false, "partial_failed"}, {"unknown", true, true, "outcome_unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			ctx := context.Background()
			claimID := ReleaseResourceID{Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespace: "app", Name: "data"}
			scopes := []ReleaseAccess{{ID: claimID, Actions: []string{"get", "create", "probe"}}}
			for _, name := range []string{"before-http", "storage"} {
				scopes = append(scopes, ReleaseAccess{ID: ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: name}, Actions: []string{"get", "create", "delete", "probe", "related-pods"}, Containers: []string{"probe"}})
			}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			request := p.Authorization.Request
			request.Resources = scopes
			auth, e := f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			claim := releaseResourceFromObject(claimID, releaseTestObject(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"data","namespace":"app"},"spec":{"storageClassName":"local-path","accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"8Mi"}}}}`), "pvc")
			plan := ReleasePlan{Version: 2, Mode: "helm", Helm: &ReleaseHelmPlan{Name: "app", Namespace: "app"}, Resources: []ReleaseResource{claim}, TimeoutSeconds: 30}
			api := &wffcProbeAPI{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}, cleanup: true}, allowBind: tc.bind, unknownStorage: tc.unknown}
			controller := NewReleaseController(f.s, nil)
			controller.poll = time.Millisecond
			checks := []byte(`{"namespace":"app","checks":[{"kind":"http","name":"before-http","target":"http://owned.invalid/healthz"},{"kind":"storage","name":"storage","target":"data"}]}`)
			extra, e := controller.appendReleaseChecks(ctx, api, &plan, auth, checks)
			if e != nil {
				t.Fatal(e)
			}
			request.Resources = mergeReleaseScopes([]ReleaseAccess{scopes[0]}, extra)
			plan.Authorization, e = f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			record, e := f.s.CreateRelease(ctx, "helm-wffc-"+tc.name, "node", "owner", plan)
			if e != nil {
				t.Fatal(e)
			}
			record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			api.record = record
			registerControllerTestWriter(t, f, record)
			controller.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }

			if e = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); e != nil {
				t.Fatal(e)
			}
			if _, e = controller.executeFrozenResource(ctx, api, record, claim, "helm-fixture-claim"); e != nil {
				t.Fatal(e)
			}
			work, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			runErr := controller.runHelmSupplementalChecks(work, api, record)
			if api.storageWrites != 1 || api.earlyHTTP {
				t.Fatal("Helm WFFC consumer did not precede health barrier", api.storageWrites, api.earlyHTTP, runErr)
			}
			if tc.want == "ready" && runErr != nil || tc.want != "ready" && runErr == nil {
				t.Fatal("final Bound gate was lost", runErr)
			}
			facts, e := f.s.ListReleaseActions(ctx, record.ID)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, fact := range facts {
				if fact.Intent.Resource.Name == "storage" {
					found = true
					if fact.Intent.StepKey != "check-2" {
						t.Fatal("Helm storage step was renamed", fact.Intent.StepKey)
					}
				}
			}
			if !found {
				t.Fatal("storage action missing")
			}
			if tc.unknown {
				if e := controller.runHelmSupplementalChecks(ctx, api, record); !errors.Is(e, ErrOutcomeUnknown) || api.storageWrites != 1 {
					t.Fatal("unknown Helm consumer replayed", e)
				}
			}
		})
	}
}
func TestHelmCompletedHookDeletionIsVerifiedBeforeSupplementalHealth(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "replaced"}[replaced], func(t *testing.T) {
			ctx := context.Background()
			f, p := releaseStoreFixture(t, storetest.Open(t))
			id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "hook"}
			scope := ReleaseAccess{ID: id, Actions: []string{"get", "create", "delete", "related-pods", "helm-hook"}, Containers: []string{"probe"}}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			req := p.Authorization.Request
			req.Resources = []ReleaseAccess{scope}
			auth, e := f.s.AuthorizeRelease(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			object := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"hook","namespace":"app","annotations":{"helm.sh/hook":"post-install"}},"spec":{"template":{"spec":{"containers":[{"name":"probe","image":"registry.invalid/probe@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}}}`)
			resource := releaseResourceFromObject(id, object, "workload")
			plan := ReleasePlan{Version: 2, Mode: "helm", Helm: &ReleaseHelmPlan{Name: "app", Namespace: "app"}, Authorization: auth, Resources: []ReleaseResource{resource}, Deletes: []ReleaseResourceID{id}, TimeoutSeconds: 30}
			record, e := f.s.CreateRelease(ctx, "hook-check", "node", "owner", plan)
			if e != nil {
				t.Fatal(e)
			}
			record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); e != nil {
				t.Fatal(e)
			}
			registerControllerTestWriter(t, f, record)
			api := &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, record: record, objects: map[string]map[string]any{}}}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			created, e := c.executeFrozenResource(ctx, api, record, resource, "helm-0")
			if e != nil {
				t.Fatal(e)
			}
			receipt, e := c.observeResource(ctx, api, resource, 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.UpdateReleaseActionHealth(ctx, created.ID, record.OwnerEpoch, created.Revision, receipt); e != nil {
				t.Fatal(e)
			}
			fresh, e := f.s.GetRelease(ctx, record.ID)
			if e != nil {
				t.Fatal(e)
			}
			body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": receipt.UID, "resourceVersion": receipt.ResourceVersion}})
			path, _ := resourceAPI(id).Path("app", "hook")
			deleted, e := api.ExecuteAction(ctx, record.ID, record.OwnerEpoch, fresh.Revision, ReleaseActionIntent{Version: 2, ParentID: record.PlanID, StepKey: "helm-1", Resource: id, Verb: "delete", Body: body, ExpectedUID: receipt.UID, ExpectedRV: receipt.ResourceVersion, AuthorizationFingerprint: auth.Fingerprint}, path)
			if e != nil {
				t.Fatal(e)
			}
			if replaced {
				api.objects[path] = releaseObjectCopy(receipt.Object)
				releaseMap(api.objects[path]["metadata"])["uid"] = "replacement"
			}
			e = c.runHelmSupplementalChecks(ctx, api, record)
			if replaced && !errors.Is(e, ErrSuperseded) || !replaced && e != nil {
				t.Fatal("hook absence proof is incorrect", e)
			}
			fact, e := f.s.GetReleaseAction(ctx, deleted.ID)
			if e != nil {
				t.Fatal(e)
			}
			if fact.Receipt.Deleted == replaced {
				t.Fatal("deleted proof invented or omitted")
			}
		})
	}
}

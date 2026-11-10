package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

type probeControllerAPI struct {
	*releaseControllerAPI
	cleanup bool
}

func (a *probeControllerAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if strings.HasSuffix(path, "/pods") {
		pods := []any{}
		for _, object := range a.objects {
			if object["kind"] != "Job" {
				continue
			}
			meta := releaseMap(object["metadata"])
			spec := releaseMap(releaseMap(releaseMap(object["spec"])["template"])["spec"])
			pods = append(pods, map[string]any{"kind": "Pod", "metadata": map[string]any{"name": "observed-probe-pod", "namespace": meta["namespace"], "uid": "probe-pod-uid", "ownerReferences": []any{map[string]any{"uid": meta["uid"], "controller": true}}}, "spec": spec, "status": map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": "probe", "image": releaseMap(releaseItems(spec["containers"])[0])["image"], "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}}}}})
		}
		return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "PodList", "items": pods})
	}
	return a.releaseControllerAPI.Read(ctx, path, query)
}
func (a *probeControllerAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.Verb == "delete" {
		a.writes++
		action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
		if e != nil {
			return action, e
		}
		delete(a.objects, path)
		return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: in.ExpectedUID, ResourceVersion: in.ExpectedRV, Health: ReleaseHealth{State: "declared", Reason: "delete_accepted"}})
	}
	action, e := a.releaseControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e != nil {
		return action, e
	}
	if in.Resource.Kind == "Job" {
		named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
		a.objects[named]["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
		delete(a.objects, path)
	}
	return action, nil
}
func (a *probeControllerAPI) Close(ctx context.Context) (ReleaseRuntimeLease, error) {
	if !a.cleanup {
		return ReleaseRuntimeLease{}, nil
	}
	var id string
	if e := a.f.db.DB.QueryRowContext(ctx, `SELECT id FROM k8s_release_runtime WHERE release_id=?`, a.record.ID).Scan(&id); e != nil {
		return ReleaseRuntimeLease{}, e
	}
	lease, e := a.f.s.GetReleaseRuntime(ctx, id)
	if e != nil {
		return lease, e
	}
	r := lease.Runtime
	if r.Cleanup == CleanupVerified {
		return lease, nil
	}
	return a.f.s.RecordReleaseRuntimeCleanup(ctx, id, r.OwnerEpoch, r.Revision, CleanupEvidence{ContainerName: r.ContainerName, ContainerID: r.ContainerID, EngineID: r.EngineID, OwnerEpoch: r.OwnerEpoch, LabelsMatched: true, WriterExited: true, ContainerAbsent: true})
}
func TestReleaseProbePlanRunsThroughLedgerHealthAndVerifiedDeletion(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		clock := time.Now()
		f.s.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
		sourceID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
		probeID := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "check-http"}
		sourceScope := ReleaseAccess{ID: sourceID, Actions: []string{"get", "create"}}
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
		object := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"private":"private-check-fixture"}}`)
		p = ReleasePlan{Version: 2, Mode: "manifest", Resources: []ReleaseResource{releaseResourceFromObject(sourceID, object, "declaration")}, TimeoutSeconds: 300, ObserveSeconds: 0}
		api := &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}
		controller := NewReleaseController(f.s, nil)
		controller.poll = time.Millisecond
		config := []byte(`{"namespace":"app","checks":[{"kind":"http","name":"check-http","target":"http://service.app:8080/healthz","expected":"v2"}]}`)
		extra, e := controller.appendReleaseChecks(ctx, api, &p, catalog, config)
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Resources) != 2 || !p.Resources[1].Probe || len(p.Deletes) != 1 {
			t.Fatal("probe missing from frozen action set")
		}
		request.Resources = mergeReleaseScopes([]ReleaseAccess{sourceScope}, extra)
		p.Authorization, e = f.s.AuthorizeRelease(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		record, e := f.s.CreateRelease(ctx, "run-probe-chain", "node", "owner", p)
		if e != nil {
			t.Fatal(e)
		}
		record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api.record = record
		api.cleanup = true
		registerControllerTestWriter(t, f, record)
		controller.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
		result, e := controller.Run(ctx, record.ID, record.OwnerEpoch, nil)
		if e != nil || !result.Successful() {
			t.Fatal("probe chain did not settle", result.State, result.Cleanup, e)
		}
		controller.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		before := api.writes
		if _, e = controller.VerifyReuse(ctx, result.ID, result.PlanID); e != nil || api.writes != before {
			t.Fatal("verified deleted probe could not be inherited without replay", e)
		}
		actions, e := f.s.ListReleaseActions(ctx, record.ID)
		if e != nil || len(actions) != 3 || api.writes != 3 {
			t.Fatal("probe write/health/delete ledger incomplete", len(actions), api.writes, e)
		}
		deleted := false
		completed := false
		for _, action := range actions {
			if action.Intent.Resource == probeID {
				if action.Intent.Verb == "delete" {
					deleted = action.Receipt != nil && action.Receipt.Deleted
				} else {
					completed = action.Receipt != nil && action.Receipt.Health.State == "ready" && len(action.Receipt.Health.Containers) == 1
				}
			}
		}
		if !deleted || !completed {
			t.Fatal("probe completion or exact deletion proof lost")
		}
		results, e := f.s.ReleaseResults(ctx, record.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range results {
			if row.Resource == probeID && (!row.Deleted || row.Health.State != "ready") {
				t.Fatal("public result lost task success during cleanup", row)
			}
		}
		public, _ := json.Marshal(results)
		if strings.Contains(string(public), "private-check-fixture") {
			t.Fatal("resource data exposed in safe results")
		}
	})
}

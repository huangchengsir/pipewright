package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

type progressiveControllerAPI struct {
	releaseControllerAPI
	hpa bool
}

func (a *progressiveControllerAPI) liveRelated() ([]map[string]any, []map[string]any) {
	sets, pods := []map[string]any{}, []map[string]any{}
	for _, object := range a.objects {
		if object["kind"] != "Deployment" {
			continue
		}
		m := releaseMap(object["metadata"])
		spec := releaseMap(object["spec"])
		template := releaseMap(spec["template"])
		key := releaseString(m["uid"]) + "-" + releaseByteDigest([]byte(stringMustJSON(template)))[:12]
		sets = append(sets, map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"name": key, "namespace": "app", "uid": key, "ownerReferences": []any{map[string]any{"uid": m["uid"], "controller": true}}}, "spec": map[string]any{"template": template}})
		for i := int64(0); i < releaseInt(spec["replicas"]); i++ {
			statuses := []any{}
			for _, container := range releaseItems(releaseMap(template["spec"])["containers"]) {
				c := releaseMap(container)
				statuses = append(statuses, map[string]any{"name": c["name"], "image": c["image"], "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "now"}}})
			}
			name := key + "-" + strconvI(int(i))
			pods = append(pods, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": "app", "uid": name, "ownerReferences": []any{map[string]any{"uid": key, "controller": true}}}, "spec": releaseMap(template["spec"]), "status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": statuses}})
		}
	}
	return sets, pods
}
func (a *progressiveControllerAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if strings.HasSuffix(path, "/horizontalpodautoscalers") {
		items := []any{}
		if a.hpa {
			items = append(items, map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"kind": "Deployment", "name": "stable"}}})
		}
		return json.Marshal(map[string]any{"items": items})
	}
	if strings.HasSuffix(path, "/replicasets") {
		sets, _ := a.liveRelated()
		return json.Marshal(map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSetList", "items": sets})
	}
	if strings.HasSuffix(path, "/pods") {
		_, pods := a.liveRelated()
		return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "PodList", "items": pods})
	}
	if strings.HasSuffix(path, "/endpointslices") {
		_, pods := a.liveRelated()
		endpoints := []any{}
		for _, pod := range pods {
			endpoints = append(endpoints, map[string]any{"conditions": map[string]any{"ready": true}, "targetRef": map[string]any{"kind": "Pod", "uid": releaseMap(pod["metadata"])["uid"]}})
		}
		return json.Marshal(map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSliceList", "items": []any{map[string]any{"kind": "EndpointSlice", "metadata": map[string]any{"namespace": "app", "ownerReferences": []any{map[string]any{"uid": "service-uid", "controller": true}}}, "endpoints": endpoints}}})
	}
	return a.releaseControllerAPI.Read(ctx, path, query)
}
func (a *progressiveControllerAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var object map[string]any
	if json.Unmarshal(action.Intent.Body, &object) != nil {
		return action, ErrInvalid
	}
	if in.ContentType == releaseMergeContentType {
		object = applyFixtureMerge(a.objects[path], object)
	}
	meta := releaseMap(object["metadata"])
	meta["uid"] = in.ExpectedUID
	if in.ExpectedUID == "" {
		meta["uid"] = in.Resource.Name + "-uid"
	}
	meta["resourceVersion"] = strconvI(a.writes + 100)
	meta["generation"] = a.writes + 1
	replicas := releaseInt(releaseMap(object["spec"])["replicas"])
	object["status"] = map[string]any{"observedGeneration": meta["generation"], "replicas": replicas, "updatedReplicas": replicas, "availableReplicas": replicas}
	named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
	a.objects[named] = object
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: releaseString(meta["uid"]), ResourceVersion: releaseString(meta["resourceVersion"]), Object: releaseObjectCopy(object), Health: ReleaseHealth{State: "pending"}})
}
func (a *progressiveControllerAPI) Close(ctx context.Context) (ReleaseRuntimeLease, error) {
	var id string
	e := a.f.db.DB.QueryRowContext(ctx, `SELECT id FROM k8s_release_runtime WHERE release_id=? AND cleanup_state='pending' ORDER BY created_at DESC,id LIMIT 1`, a.record.ID).Scan(&id)
	if e != nil {
		return ReleaseRuntimeLease{}, nil
	}
	lease, e := a.f.s.GetReleaseRuntime(ctx, id)
	if e != nil {
		return lease, e
	}
	r := lease.Runtime
	return a.f.s.RecordReleaseRuntimeCleanup(ctx, id, r.OwnerEpoch, r.Revision, CleanupEvidence{ContainerName: r.ContainerName, ContainerID: r.ContainerID, EngineID: r.EngineID, OwnerEpoch: r.OwnerEpoch, LabelsMatched: true, WriterExited: true, ContainerAbsent: true})
}
func TestProgressiveRealControllerPersistsPauseAndContinuesOriginalPlan(t *testing.T) {
	for _, change := range []string{"none", "hpa", "service", "ssa_owner"} {
		t.Run(change, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, p := releaseStoreFixture(t, st)
				stableID, _ := releaseWorkloadID("Deployment", "app", "stable")
				candidateID, _ := releaseWorkloadID("Deployment", "app", "candidate")
				serviceID := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "route"}
				stableScope := releaseRequestedAccess(stableID, "patch")
				stableScope.Actions = append(stableScope.Actions, "scale", "related-hpas")
				stableScope.Containers = []string{"app"}
				candidateScope := releaseRequestedAccess(candidateID, "create")
				candidateScope.Actions = append(candidateScope.Actions, "patch", "scale", "related-hpas")
				candidateScope.Containers = []string{"app"}
				scopes := []ReleaseAccess{stableScope, candidateScope, {ID: serviceID, Actions: []string{"get", "related-endpointslices"}}}
				if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
					t.Fatal(e)
				}
				request := p.Authorization.Request
				request.Resources = scopes
				auth, e := f.s.AuthorizeRelease(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				stable := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"stable","namespace":"app","uid":"stable-uid","resourceVersion":"17","generation":1},"spec":{"replicas":2,"selector":{"matchLabels":{"app":"backend","track":"stable"}},"template":{"metadata":{"labels":{"app":"backend","track":"stable"}},"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}},"status":{"observedGeneration":1,"replicas":2,"updatedReplicas":2,"availableReplicas":2}}`)
				service := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"route","namespace":"app","uid":"service-uid","resourceVersion":"17"},"spec":{"selector":{"app":"backend"},"ports":[{"port":80,"targetPort":8080}],"clusterIP":"10.0.0.10"}}`)
				api := &progressiveControllerAPI{releaseControllerAPI: releaseControllerAPI{f: f, objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/stable": stable, "/api/v1/namespaces/app/services/route": service}}}
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				if _, e = st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES('progressive-run','project-1','running','now')`); e != nil {
					t.Fatal(e)
				}
				record, e := c.PrepareProgressive(ctx, "progressive-run", "node", "first-owner", auth, ReleaseProgressiveInput{Namespace: "app", Stable: "stable", Candidate: "candidate", Service: "route", TrackKey: "track", TrackValue: "next", Batches: []int{1, 2}, Images: []ReleaseImageUpdate{{Container: "app", Image: "registry.invalid/app@sha256:" + strings.Repeat("b", 64)}}}, []byte(`{"namespace":"app"}`), 300, 0)
				if e != nil {
					t.Fatal(e)
				}
				encoded, _ := json.Marshal(record.Plan.Batches[1][0])
				var frozen map[string]any
				json.Unmarshal(encoded, &frozen)
				mutation := releaseMap(frozen["mutation"])
				if mutation["strategy"] != "prior-create-cas-merge-v1" || mutation["originStepKey"] != "batch-0-resource-0" {
					t.Fatal("later candidate update lacks frozen same-plan create receipt recipe")
				}
				planID := record.PlanID
				record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = record
				registerControllerTestWriter(t, f, record)
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				var cleanupLogs []string
				result, e := c.Run(ctx, record.ID, record.OwnerEpoch, func(p, code string) {
					if p == "cleanup" {
						cleanupLogs = append(cleanupLogs, code)
					}
				})
				if strings.Join(cleanupLogs, ",") != "cleanup_started,cleanup_verified" {
					t.Fatal("verified cleanup phase missing", cleanupLogs)
				}
				if !errors.Is(e, ErrReleasePaused) || result.Cleanup != CleanupVerified || api.writes != 2 {
					t.Fatal("first batch did not release tool and preserve pause", result.State, result.Cleanup, api.writes, e)
				}
				record, e = f.s.SuspendRelease(ctx, result.ID, result.OwnerEpoch, result.Revision, ReleaseSuspension{Version: 2, RunID: result.RunID, NodeKey: result.NodeKey, PlanID: planID, Cursor: 1, Nodes: map[string]string{"node": "waiting_action"}})
				if e != nil {
					t.Fatal(e)
				}
				if change == "hpa" {
					api.hpa = true
				}
				if change == "service" {
					releaseMap(service["spec"])["externalTrafficPolicy"] = "Local"
				}
				if change == "ssa_owner" {
					candidatePath, _ := resourceAPI(candidateID).Path("app", "candidate")
					releaseMap(api.objects[candidatePath]["metadata"])["managedFields"] = []any{map[string]any{"operation": "Apply", "manager": "pipewright-release-v2"}}
				}
				continuation, e := f.s.RequestReleaseContinue(ctx, record.ID, record.Revision, planID)
				if e != nil {
					t.Fatal(e)
				}
				continuation, e = f.s.ClaimReleaseContinuation(ctx, continuation.ID, "second-owner")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = st.DB.Exec(`UPDATE pipeline_runs SET status='running' WHERE id='progressive-run'`); e != nil {
					t.Fatal(e)
				}
				if e = f.s.ActivateReleaseContinuation(ctx, continuation.ID, "second-owner"); e != nil {
					t.Fatal(e)
				}
				record, e = f.s.GetRelease(ctx, record.ID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = record
				registerControllerTestWriter(t, f, record)
				result, e = c.Run(ctx, record.ID, record.OwnerEpoch, nil)
				if change != "none" {
					if e == nil || result.State != "partial_failed" || api.writes != 2 {
						t.Fatal("changed HPA/service dispatched next batch or erased prior effects", change, result.State, api.writes, e)
					}
					return
				}
				if e != nil || !result.Successful() || api.writes != 4 || result.PlanID != planID || result.RunID != "progressive-run" {
					t.Fatal("same-run continuation changed plan/replayed batch", result.State, result.Cleanup, api.writes, e)
				}
				actions, e := f.s.ListReleaseActions(ctx, result.ID)
				if e != nil || len(actions) != 4 {
					t.Fatal("per-batch receipts missing", len(actions), e)
				}
			})
		})
	}
}

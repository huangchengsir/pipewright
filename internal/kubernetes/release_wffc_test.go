package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

type wffcProbeAPI struct {
	*probeControllerAPI
	storageCreated, allowBind, unknownStorage, earlyHTTP bool
	storageWrites                                        int
}

func (a *wffcProbeAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/api/v1/namespaces/app/persistentvolumeclaims/data" {
		object := a.objects[path]
		if object == nil {
			return nil, ErrNotFound
		}
		object = releaseObjectCopy(object)
		phase := "Pending"
		if a.storageCreated && a.allowBind {
			phase = "Bound"
			releaseMap(object["spec"])["volumeName"] = "allocated-volume"
		}
		object["status"] = map[string]any{"phase": phase}
		return json.Marshal(object)
	}
	return a.probeControllerAPI.Read(ctx, path, query)
}
func (a *wffcProbeAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.Resource.Kind == "Job" && in.Verb == "create" {
		if in.Resource.Name == "before-http" && !a.storageCreated {
			a.earlyHTTP = true
		}
		if in.Resource.Name == "storage" {
			a.storageWrites++
			if a.unknownStorage {
				fact, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
				if e != nil {
					return fact, e
				}
				return fact, ErrOutcomeUnknown
			}
		}
	}
	fact, e := a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e == nil && in.Resource.Kind == "Job" && in.Resource.Name == "storage" && in.Verb == "create" {
		a.storageCreated = true
	}
	return fact, e
}
func TestStorageProbeCreatesWFFCConsumerBeforeAnyPVCHealthBarrier(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bind, unknown bool
		want          string
	}{{"bind_and_marker_success", true, false, "ready"}, {"bound_is_still_required", false, false, "partial_failed"}, {"unknown_consumer_is_never_replayed", true, true, "outcome_unknown"}} {
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
			plan := ReleasePlan{Version: 2, Mode: "manifest", Resources: []ReleaseResource{claim}, TimeoutSeconds: 30}
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
			record, e := f.s.CreateRelease(ctx, "wffc-"+tc.name, "node", "owner", plan)
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
			work, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			result, runErr := controller.Run(work, record.ID, record.OwnerEpoch, nil)
			if result.State != tc.want || api.storageWrites != 1 || api.earlyHTTP {
				t.Fatal("storage consumer/health ordering incorrect", result.State, api.storageWrites, api.earlyHTTP, runErr)
			}
			if tc.want == "ready" && runErr != nil {
				t.Fatal("WFFC consumer chain did not finish", runErr)
			}
			if tc.want != "ready" && runErr == nil {
				t.Fatal("failed storage gate became success")
			}
			if tc.unknown {
				_, second := controller.Run(ctx, record.ID, record.OwnerEpoch, nil)
				if !errors.Is(second, ErrOwner) || api.storageWrites != 1 {
					t.Fatal("unknown storage consumer was replayed")
				}
			}
		})
	}
}

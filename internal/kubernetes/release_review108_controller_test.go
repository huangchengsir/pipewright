package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
	"strings"
	"testing"
	"time"
)

type review108DaemonAPI struct {
	*probeControllerAPI
	denyRevision bool
	history, pod map[string]any
}

func (a *review108DaemonAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if strings.HasSuffix(path, "/controllerrevisions") {
		if a.denyRevision {
			return nil, ErrDenied
		}
		items := []any{}
		if a.history != nil {
			items = append(items, a.history)
		}
		return json.Marshal(map[string]any{"apiVersion": "apps/v1", "kind": "ControllerRevisionList", "items": items})
	}
	if strings.HasSuffix(path, "/pods") {
		items := []any{}
		if a.pod != nil {
			items = append(items, a.pod)
		}
		return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "PodList", "items": items})
	}
	return a.probeControllerAPI.Read(ctx, path, query)
}
func (a *review108DaemonAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	action, e := a.probeControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e != nil {
		return action, e
	}
	named, _ := resourceAPI(in.Resource).Path(in.Resource.Namespace, in.Resource.Name)
	ds := a.objects[named]
	releaseMap(ds["metadata"])["generation"] = 2
	_, native, history, pod := review108Daemon()
	ds["status"] = native["status"]
	uid := releaseMap(ds["metadata"])["uid"]
	releaseMap(releaseItems(releaseMap(history["metadata"])["ownerReferences"])[0])["uid"] = uid
	releaseMap(releaseItems(releaseMap(pod["metadata"])["ownerReferences"])[0])["uid"] = uid
	a.history, a.pod = history, pod
	return action, nil
}
func TestReview108DaemonServiceStoreControllerProof(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, seed := releaseStoreFixture(t, st)
		resource, _, _, _ := review108Daemon()
		resource.Expected = nil
		resource.ExpectedUID = ""
		resource.DaemonSetProofVersion = 0
		scope := releaseRequestedAccess(resource.ID, "create")
		scope.Containers = []string{"app"}
		if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		request := seed.Authorization.Request
		request.Resources = []ReleaseAccess{scope}
		auth, e := f.s.AuthorizeRelease(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		plan := ReleasePlan{Version: 2, Mode: "manifest", MutationVersion: 1, Authorization: auth, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300}
		api := &review108DaemonAPI{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}}
		c := NewReleaseController(f.s, nil)
		c.poll = time.Millisecond
		c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
		api.denyRevision = true
		if e = c.dryRunReleaseResources(ctx, "owner", &plan); !errors.Is(e, ErrDenied) || api.writes != 0 {
			t.Fatal("native revision permission not required before write", e)
		}
		api.denyRevision = false
		if e = c.dryRunReleaseResources(ctx, "owner", &plan); e != nil {
			t.Fatal(e)
		}
		if plan.Resources[0].DaemonSetProofVersion != 1 {
			t.Fatal("new plan omitted proof version")
		}
		record, e := f.s.CreateRelease(ctx, "daemon108", "node", "owner", plan)
		if e != nil {
			t.Fatal(e)
		}
		loaded, e := f.s.GetRelease(ctx, record.ID)
		if e != nil || loaded.Plan.Resources[0].DaemonSetProofVersion != 1 {
			t.Fatal("sealed proof did not roundtrip", e)
		}
		record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api.record = record
		api.cleanup = true
		registerControllerTestWriter(t, f, record)
		result, e := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
		if e != nil || !result.Successful() || api.writes != 1 {
			t.Fatal("native DS controller path failed", result.State, e)
		}
		actions, e := f.s.ListReleaseActions(ctx, record.ID)
		if e != nil || len(actions) != 1 || actions[0].State != "verified" || actions[0].Receipt.Health.State != "ready" {
			t.Fatal("controller proof did not persist health receipt", e)
		}
		public, _ := json.Marshal(PreviewRelease(result))
		if strings.Contains(string(public), "nodeSelectorTerms") || strings.Contains(string(public), "controller-revision-hash") || strings.Contains(string(public), "requiredDuringScheduling") {
			t.Fatal("private native history exposed in public projection")
		}
		// Omitting the optional field remains byte compatible and does not infer new
		// permissions when simply loading an already sealed historical plan.
		legacy := resource
		legacy.DaemonSetProofVersion = 0
		b, _ := json.Marshal(legacy)
		if strings.Contains(string(b), "daemonSetProofVersion") {
			t.Fatal("absent proof changed legacy JSON")
		}
		bad := plan
		bad.Resources = append([]ReleaseResource(nil), plan.Resources...)
		bad.Resources[0].DaemonSetProofVersion = 2
		if _, e = f.s.CreateRelease(ctx, "daemon108bad", "node", "other", bad); !errors.Is(e, ErrInvalid) {
			t.Fatal("unknown proof version accepted", e)
		}
	})
}
func TestReview108DaemonOldGrantAndHelperNeverExpanded(t *testing.T) {
	for _, grant := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_grant", true: "new_grant"}[grant], func(t *testing.T) {
			f, seed := releaseStoreFixture(t, storetest.Open(t))
			resource, _, _, _ := review108Daemon()
			scope := releaseRequestedAccess(resource.ID, "create")
			if !grant {
				scope.Actions = []string{"get", "create", "related-pods"}
			}
			if _, e := f.s.SaveReleaseGrant(context.Background(), f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			catalog, e := f.s.ReleasePolicyCatalog(context.Background(), seed.Authorization.Request)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, s := range catalog.Request.Resources {
				if s.ID == resource.ID {
					found = releaseStringSetContains(s.Actions, []string{releaseDaemonRevisionAction})
				}
			}
			if found != grant {
				t.Fatal("catalog broadened/invented related permission")
			}
			request := seed.Authorization.Request
			request.Resources = []ReleaseAccess{releaseRequestedAccess(resource.ID, "create")}
			_, e = f.s.AuthorizeRelease(context.Background(), request)
			if !grant && !errors.Is(e, ErrDenied) {
				t.Fatal("old grant expanded", e)
			}
			if grant && e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "old_tool", true: "new_tool"}[capable], func(t *testing.T) {
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := newHelmCASRPCTransport(true, nil)
			if capable {
				tr.capabilities = []string{kubeprotocol.DaemonSetRevisionReadCapability}
			}
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, e := runtime.OpenReleasePreview(context.Background(), p.Authorization, "cap108")
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close(context.Background())
			e = session.requireDaemonRevisionCapability(context.Background())
			if capable && e != nil || !capable && !errors.Is(e, ErrVersion) {
				t.Fatal("proof capability inferred", e)
			}
			if tr.permit != nil {
				t.Fatal("capability probe dispatched write")
			}
		})
	}
}

func TestReview108DaemonOldHelperFailsBeforeCredentialInjection(t *testing.T) {
	ctx := context.Background()
	f, seed := releaseStoreFixture(t, storetest.Open(t))
	resource, _, _, _ := review108Daemon()
	scope := releaseRequestedAccess(resource.ID, "create")
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := seed.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	auth, e := f.s.AuthorizeRelease(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	tr := newHelmCASRPCTransport(false, nil)
	runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
	session, e := runtime.OpenReleasePreview(ctx, auth, "old-ds-tool")
	if session != nil || !errors.Is(e, ErrVersion) || len(tr.uploads) != 0 || tr.permit != nil {
		t.Fatal("old helper consumed private init or reached write", e)
	}
	var id string
	if e = f.db.DB.QueryRow(`SELECT id FROM k8s_release_runtime ORDER BY created_at DESC LIMIT 1`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	lease, e := f.s.GetReleaseRuntime(ctx, id)
	if e != nil || lease.Runtime.CredentialInjected || lease.Runtime.Cleanup != CleanupVerified {
		t.Fatal("failed capability did not clean credential-free runtime", e)
	}
}

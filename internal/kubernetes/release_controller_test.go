package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type releaseControllerAPI struct {
	f       *fixture
	record  ReleaseRecord
	objects map[string]map[string]any
	writes  int
	unready bool
}

func (a *releaseControllerAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if strings.HasSuffix(path, "/replicasets") {
		return []byte(`{"apiVersion":"apps/v1","kind":"ReplicaSetList","items":[]}`), nil
	}
	if strings.HasSuffix(path, "/pods") {
		return []byte(`{"apiVersion":"v1","kind":"PodList","items":[]}`), nil
	}
	o := a.objects[path]
	if o == nil {
		return nil, ErrNotFound
	}
	return json.Marshal(o)
}
func (a *releaseControllerAPI) DryRun(ctx context.Context, r kubeprotocol.Request) (map[string]any, error) {
	var o map[string]any
	if json.Unmarshal(r.Body, &o) != nil {
		return nil, ErrInvalid
	}
	if r.ContentType == releaseMergeContentType {
		o = applyFixtureMerge(a.objects[r.Path], o)
	}
	return o, nil
}
func applyFixtureMerge(current, patch map[string]any) map[string]any {
	out := releaseObjectCopy(current)
	if out == nil {
		out = map[string]any{}
	}
	for key, value := range patch {
		if value == nil {
			delete(out, key)
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			out[key] = applyFixtureMerge(releaseMap(out[key]), nested)
		} else {
			out[key] = value
		}
	}
	return out
}
func (a *releaseControllerAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	out, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return out, e
	}
	var object map[string]any
	if json.Unmarshal(out.Intent.Body, &object) != nil {
		return out, ErrInvalid
	}
	if in.ContentType == releaseMergeContentType {
		object = applyFixtureMerge(a.objects[path], object)
	}
	m := releaseMap(object["metadata"])
	m["uid"] = "created-uid-" + in.Resource.Name
	if in.ExpectedUID != "" {
		m["uid"] = in.ExpectedUID
	}
	m["resourceVersion"] = "18"
	a.objects[path] = object
	if in.Verb == "create" {
		a.objects[path+"/"+in.Resource.Name] = object
	}
	receipt := ReleaseActionReceipt{Resource: in.Resource, UID: m["uid"].(string), ResourceVersion: "18", Object: releaseObjectCopy(object), Health: ReleaseHealth{State: "pending"}}
	return a.f.s.RecordReleaseAction(ctx, out.ID, owner, out.Revision, "verified", receipt)
}
func (a *releaseControllerAPI) Close(context.Context) (ReleaseRuntimeLease, error) {
	return ReleaseRuntimeLease{}, nil
}
func registerControllerTestWriter(t *testing.T, f *fixture, r ReleaseRecord) {
	t.Helper()
	ctx := context.Background()
	l, e := f.s.RegisterReleaseRuntime(ctx, r.ID, r.OwnerEpoch, "engine-test", r.Plan.Authorization)
	if e != nil {
		t.Fatal(e)
	}
	l, e = f.s.PrepareReleaseRuntimeCreate(ctx, l.ID, r.OwnerEpoch, l.Runtime.Revision, nil, "sha256:"+strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	p := RuntimeProof{OperationID: l.ID, OwnerEpoch: r.OwnerEpoch, EngineID: "engine-test", ContainerID: strings.Repeat("b", 64), ProfileFingerprint: fingerprint(r.Plan.Authorization.Profile.Profile), AuthorizationFingerprint: r.Plan.Authorization.Fingerprint, Linux: true, PrivateTmpfs: true, SwapProtected: true, CoreDisabled: true, NonRoot: true, ReadOnlyRoot: true, BootstrapVerified: true, ClientVersionVerified: true, HostIdentityVerified: true}
	l, e = f.s.VerifyReleaseRuntime(ctx, l.ID, r.OwnerEpoch, l.Runtime.Revision, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.UseReleaseCredential(ctx, l.ID, r.OwnerEpoch, func(context.Context, []byte) error { return nil }); e != nil {
		t.Fatal(e)
	}
}
func TestReleaseControllerConfigurationWritesAndVerifiesPrivately(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
		scope := ReleaseAccess{ID: id, Actions: []string{"get", "create"}}
		if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		request := p.Authorization.Request
		request.Resources = []ReleaseAccess{scope}
		authorization, e := f.s.AuthorizeRelease(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		object := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"private":"private-fixture"}}`)
		b, _ := json.Marshal(object)
		p = ReleasePlan{Version: 2, Mode: "manifest", Authorization: authorization, Resources: []ReleaseResource{{ID: id, Object: object, Digest: releaseByteDigest(b), Health: "declaration"}}, TimeoutSeconds: 300, ObserveSeconds: 0}
		record, e := f.s.CreateRelease(ctx, "run-controller", "node", "owner", p)
		if e != nil {
			t.Fatal(e)
		}
		record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		registerControllerTestWriter(t, f, record)
		api := &releaseControllerAPI{f: f, record: record, objects: map[string]map[string]any{}}
		controller := NewReleaseController(f.s, nil)
		controller.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
		controller.poll = time.Millisecond
		var cleanupLogs []string
		result, e := controller.Run(ctx, record.ID, record.OwnerEpoch, func(p, code string) {
			if p == "cleanup" {
				cleanupLogs = append(cleanupLogs, code)
			}
		})
		if strings.Join(cleanupLogs, ",") != "cleanup_started,cleanup_unverified" {
			t.Fatal("cleanup without durable proof was reported verified", cleanupLogs)
		}
		if e != nil && !errors.Is(e, ErrHeld) {
			t.Fatal(e)
		}
		if result.State != "ready" || api.writes != 1 {
			t.Fatal("configuration not verified", result.State, api.writes, e)
		}
		actions, e := f.s.ListReleaseActions(ctx, record.ID)
		if e != nil || len(actions) != 1 || actions[0].Receipt.Health.State != "declared" {
			t.Fatal("write and health receipts missing", e)
		}
		public, _ := json.Marshal(PreviewRelease(result))
		if strings.Contains(string(public), "private-fixture") {
			t.Fatal("preview exposed raw resource data")
		}
		if result.Cleanup != CleanupPending {
			t.Fatal("fake writer cleanup must not become invented proof")
		}
	})
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func releaseStoreFixture(t *testing.T, st *store.Store) (*fixture, ReleasePlan) {
	t.Helper()
	f := newFixture(t, st)
	ctx := context.Background()
	base := testProfile()
	base.Protocol = ReleaseProfileProtocol
	if _, e := f.s.SaveReleaseProfile(ctx, f.conn.ID, 0, ReleaseToolProfile{Runtime: base, HelperPath: "/usr/bin/pipewright-kube-tool", HelmPath: "/usr/bin/helm", HelmVersion: ReleaseHelmVersion}, true); e != nil {
		t.Fatal(e)
	}
	id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "backend"}
	scope := ReleaseAccess{ID: id, Actions: []string{"get", "patch", "related-pods", "related-replicasets"}}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	a, e := f.s.AuthorizeRelease(ctx, ReleaseAuthorizationRequest{ProjectID: "project-1", Environment: "production", ConnectionID: f.conn.ID, CredentialID: f.c.ID, Context: f.c.Context, Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}})
	if e != nil {
		t.Fatal(e)
	}
	obj := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"backend","namespace":"app"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","env":[{"name":"PRIVATE","value":"lowentropy-secret"}]}]}}}}`)
	b, _ := json.Marshal(obj)
	return f, ReleasePlan{Version: 2, Mode: "manifest", Authorization: a, Resources: []ReleaseResource{{ID: id, Object: obj, Digest: releaseByteDigest(b), Phase: 2, Health: "workload"}}, TimeoutSeconds: 300, ObserveSeconds: 30}
}
func TestReleasePlanCipherIdentityAndV1V2GateConflict(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		r, e := f.s.CreateRelease(ctx, "run-v2", "node", "owner-v2", p)
		if e != nil {
			t.Fatal(e)
		}
		var cipher []byte
		if e = st.DB.QueryRow(`SELECT plan_cipher FROM k8s_releases WHERE id=?`, r.ID).Scan(&cipher); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(cipher), "lowentropy-secret") || r.PlanID == fingerprint(p) || r.PlanID != releaseByteDigest(cipher) {
			t.Fatal("private plan identity is plaintext-derived")
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		legacy := f.operation(t, "run-v1")
		if _, e = f.s.AcquireHold(ctx, legacy.ID, legacy.OwnerEpoch, "uid"); !errors.Is(e, ErrHeld) {
			t.Fatal("v1 bypassed v2 name gate", e)
		}
		loaded, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || loaded.Plan.Resources[0].Digest != p.Resources[0].Digest {
			t.Fatal("plan seal lost intent", e)
		}
	})
}
func TestReleaseV1HistoricalHoldBridgesBeforeV2(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		legacy := f.operation(t, "run-v1")
		if _, e := f.s.AcquireHold(ctx, legacy.ID, legacy.OwnerEpoch, "old-uid"); e != nil {
			t.Fatal(e)
		}
		if _, e := st.DB.Exec(`DELETE FROM k8s_resource_gates`); e != nil {
			t.Fatal(e)
		}
		r, e := f.s.CreateRelease(ctx, "run-v2", "node", "owner-v2", p)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); !errors.Is(e, ErrHeld) {
			t.Fatal("unbridged old hold bypassed", e)
		}
		var count int
		st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_resource_gates WHERE owner_kind='v2'`).Scan(&count)
		if count != 0 {
			t.Fatal("failed multi-lock acquisition leaked a partial gate")
		}
	})
}
func TestReleaseActionUnknownFenceIsNeverReplayed(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		r, e := f.s.CreateRelease(ctx, "run-v2", "node", "owner-v2", p)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		lease, err := f.s.RegisterReleaseRuntime(ctx, r.ID, r.OwnerEpoch, "engine-test", p.Authorization)
		if err != nil {
			t.Fatal(err)
		}
		lease, err = f.s.PrepareReleaseRuntimeCreate(ctx, lease.ID, r.OwnerEpoch, lease.Runtime.Revision, nil, "sha256:"+strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		proof := RuntimeProof{OperationID: lease.ID, OwnerEpoch: r.OwnerEpoch, EngineID: "engine-test", ContainerID: strings.Repeat("b", 64), ProfileFingerprint: fingerprint(p.Authorization.Profile.Profile), AuthorizationFingerprint: p.Authorization.Fingerprint, Linux: true, PrivateTmpfs: true, SwapProtected: true, CoreDisabled: true, NonRoot: true, ReadOnlyRoot: true, BootstrapVerified: true, ClientVersionVerified: true, HostIdentityVerified: true}
		lease, err = f.s.VerifyReleaseRuntime(ctx, lease.ID, r.OwnerEpoch, lease.Runtime.Revision, proof)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.s.UseReleaseCredential(ctx, lease.ID, r.OwnerEpoch, func(context.Context, []byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
		obj := releaseTestObject(stringMustJSON(p.Resources[0].Object))
		meta := releaseMap(obj["metadata"])
		meta["uid"] = "uid"
		meta["resourceVersion"] = "17"
		body, _ := json.Marshal(obj)
		intent := ReleaseActionIntent{Version: 2, ParentID: r.PlanID, StepKey: "apply-0", Resource: p.Resources[0].ID, Verb: "patch", Body: body, ExpectedUID: "uid", ExpectedRV: "17", AuthorizationFingerprint: p.Authorization.Fingerprint}
		a, e := f.s.PrepareReleaseAction(ctx, r.ID, r.OwnerEpoch, r.Revision, intent)
		if e != nil || a.State != "outcome_unknown" {
			t.Fatal(a, e)
		}
		intent.StepKey = "second-permit"
		if _, e = f.s.PrepareReleaseAction(ctx, r.ID, r.OwnerEpoch, r.Revision, intent); !errors.Is(e, ErrOutcomeUnknown) {
			t.Fatal("new permit bypassed unknown request", e)
		}
		if _, e = f.s.RecordReleaseAction(ctx, a.ID, r.OwnerEpoch, a.Revision, "verified", ReleaseActionReceipt{Resource: intent.Resource, UID: "foreign", ResourceVersion: "18", Object: obj}); !errors.Is(e, ErrConflict) {
			t.Fatal("replacement UID adopted", e)
		}
	})
}
func stringMustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestRelatedReadRequiresExplicitScopeAndOwnerChain(t *testing.T) {
	id := ReleaseResourceID{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespace: "app", Name: "backend"}
	anchor := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"backend","namespace":"app","uid":"root"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"approved"}]}}}}`)
	rs := releaseTestObject(`{"kind":"ReplicaSet","metadata":{"namespace":"app","uid":"rs","ownerReferences":[{"uid":"root","controller":true}]},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"approved"}]}}}}`)
	pod := releaseTestObject(`{"kind":"Pod","metadata":{"name":"pod-generated","namespace":"app","uid":"pod","ownerReferences":[{"uid":"rs","controller":true}]}}`)
	scope := ReleaseAccess{ID: id, Actions: []string{"get"}}
	if _, e := FilterReleaseRelatedObjects(scope, anchor, "Pod", []map[string]any{rs, pod}); !errors.Is(e, ErrDenied) {
		t.Fatal("ordinary get expanded to Pod access", e)
	}
	scope.Actions = append(scope.Actions, "related-pods")
	got, e := FilterReleaseRelatedObjects(scope, anchor, "Pod", []map[string]any{rs, pod, releaseTestObject(`{"kind":"Pod","metadata":{"namespace":"app","ownerReferences":[{"uid":"foreign","controller":true}]}}`)})
	if e != nil || len(got) != 1 {
		t.Fatal("owner projection", len(got), e)
	}
}

func TestReleaseContinuationDurablePauseAndDuplicateDelivery(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		p.Mode = "progressive"
		p.Batches = [][]ReleaseResource{p.Resources, p.Resources}
		if _, e := st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at,started_at) VALUES(?,?,?,?,?)`, "run-resume", "project-1", "running", "2026-11-07T00:00:00Z", "2026-11-07T00:00:01Z"); e != nil {
			t.Fatal(e)
		}
		r, e := f.s.CreateRelease(ctx, "run-resume", "node", "owner-first", p)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.SuspendRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, ReleaseSuspension{Version: 2, RunID: r.RunID, NodeKey: r.NodeKey, PlanID: r.PlanID, Cursor: 1, Stages: map[string]string{"source": "success", "release": "waiting_action", "after": "pending"}, Nodes: map[string]string{"source": "success", "node": "waiting_action"}})
		if e != nil {
			t.Fatal(e)
		}
		var status string
		var finished any
		if e = st.DB.QueryRow(`SELECT status,finished_at FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&status, &finished); e != nil || status != "waiting_action" || finished != nil {
			t.Fatal(status, finished, e)
		}
		c, e := f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.RequestReleaseContinue(ctx, r.ID, r.Revision, r.PlanID); !errors.Is(e, ErrConflict) {
			t.Fatal("duplicate continue accepted", e)
		}
		pending, e := f.s.PendingReleaseContinuations(ctx)
		if e != nil || len(pending) != 1 {
			t.Fatal("request lost before enqueue", e)
		}
		c, e = f.s.ClaimReleaseContinuation(ctx, c.ID, "owner-second")
		if e != nil || c.Checkpoint.Cursor != 1 || c.Checkpoint.Stages["source"] != "success" {
			t.Fatal(c, e)
		}
		if e = st.DB.QueryRow(`SELECT status FROM pipeline_runs WHERE id=?`, r.RunID).Scan(&status); e != nil || status != "queued" {
			t.Fatal("claim not queued", status, e)
		}
		if _, e = st.DB.Exec(`UPDATE pipeline_runs SET status='running' WHERE id=? AND status='queued'`, r.RunID); e != nil {
			t.Fatal(e)
		}
		if e = f.s.ActivateReleaseContinuation(ctx, c.ID, "owner-second"); e != nil {
			t.Fatal(e)
		}
		if e = f.s.ActivateReleaseContinuation(ctx, c.ID, "owner-third"); !errors.Is(e, ErrConflict) {
			t.Fatal("duplicate worker owner activated", e)
		}
		resumed, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || resumed.OwnerEpoch != "owner-second" || resumed.PlanID != r.PlanID || resumed.Cursor != 1 {
			t.Fatal("continuation changed immutable plan", e)
		}
		resumed, e = f.s.SuspendRelease(ctx, resumed.ID, resumed.OwnerEpoch, resumed.Revision, ReleaseSuspension{Version: 2, RunID: resumed.RunID, NodeKey: resumed.NodeKey, PlanID: resumed.PlanID, Cursor: 2})
		if e != nil {
			t.Fatal(e)
		}
		var completed, waiting, running int
		st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_release_continuations WHERE release_id=? AND state='completed'`, r.ID).Scan(&completed)
		st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_release_continuations WHERE release_id=? AND state='waiting'`, r.ID).Scan(&waiting)
		st.DB.QueryRow(`SELECT COUNT(*) FROM k8s_release_continuations WHERE release_id=? AND state='running'`, r.ID).Scan(&running)
		if completed != 1 || waiting != 1 || running != 0 {
			t.Fatal("second pause retained old running continuation", completed, waiting, running)
		}
		if pending, e := f.s.PendingReleaseContinuations(ctx); e != nil || len(pending) != 0 {
			t.Fatal("second pause continued without new user action", e)
		}
		if e = f.s.RecoverReleaseContinuation(ctx, c.ID, "owner-second"); !errors.Is(e, ErrConflict) {
			t.Fatal("old request revived next pause", e)
		}

	})
}
func TestReleaseGrantLargerThanMySQLTextIsPortable(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, p := releaseStoreFixture(t, st)
		scope := p.Authorization.Request.Resources[0]
		for i := 0; i < 128; i++ {
			scope.Fields = append(scope.Fields, "/spec/"+strings.Repeat("a", 500)+string(rune('A'+i)))
		}
		g, e := f.s.SaveReleaseGrant(context.Background(), f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, Enabled: true, RequireApproval: true})
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(g)
		if len(body) <= 65535 {
			t.Fatal("fixture did not cross TEXT boundary", len(body))
		}
		loaded, e := f.s.ListReleaseGrants(context.Background(), f.conn.ID)
		if e != nil || len(loaded) != 2 {
			t.Fatal("large grant failed roundtrip", e)
		}
	})
}

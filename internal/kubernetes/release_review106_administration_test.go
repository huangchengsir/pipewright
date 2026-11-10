package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func TestReview106NamespaceActualDeltaRequiresPrivilege(t *testing.T) {
	for _, scenario := range []string{"remove-denied", "remove-explicit", "external-preserved"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, base := releaseStoreFixture(t, st)
				id := ReleaseResourceID{Version: "v1", Resource: "namespaces", Kind: "Namespace", Name: "protected"}
				scope := ReleaseAccess{ID: id, Actions: []string{"get", "create", "patch", "namespace-security"}}
				grant, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true})
				if e != nil {
					t.Fatal(e)
				}
				request := base.Authorization.Request
				request.Resources = []ReleaseAccess{scope}
				auth, e := f.s.AuthorizeRelease(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				api := &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{"/api/v1": releaseTestObject(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"namespaces","kind":"Namespace","namespaced":false,"verbs":["get","create","patch"]}]}`)}}, cleanup: false}
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				original := releaseTestObject(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"protected","labels":{"managed":"first","pod-security.kubernetes.io/enforce":"restricted"}}}`)
				if scenario == "external-preserved" {
					delete(releaseMap(releaseMap(original["metadata"])["labels"]), "pod-security.kubernetes.io/enforce")
				}
				files, _ := InlineReleaseFile(stringMustJSON(original))
				r, e := c.PrepareManifest(ctx, "namespace-source", "node", "owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
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
				r, e = c.Run(ctx, r.ID, r.OwnerEpoch, nil)
				if e != nil || !r.Successful() {
					t.Fatal("namespace source", e)
				}
				if scenario == "external-preserved" {
					releaseMap(releaseMap(api.objects["/api/v1/namespaces/protected"]["metadata"])["labels"])["pod-security.kubernetes.io/enforce"] = "restricted"
				}
				if scenario != "remove-explicit" {
					scope.Actions = []string{"get", "create", "patch"}
					if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, grant.ID, grant.Revision, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
						t.Fatal(e)
					}
				}
				catalog, e := f.s.ReleasePolicyCatalog(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				desired := releaseTestObject(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"protected","labels":{"managed":"second"}}}`)
				files, _ = InlineReleaseFile(stringMustJSON(desired))
				before := api.writes
				next, e := c.PrepareManifest(ctx, "namespace-update", "node", "next-owner", catalog, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
				if api.writes != before {
					t.Fatal("preparation wrote namespace")
				}
				if scenario == "remove-denied" {
					if !errors.Is(e, ErrDenied) {
						t.Fatal("PSA removal bypassed dedicated permission", e)
					}
					return
				}
				if e != nil {
					t.Fatal("explicit removal or preserved foreign label denied", e)
				}
				required := false
				for _, access := range next.Plan.Authorization.Request.Resources {
					if access.ID == id && hasReleaseAction(access.Actions, "namespace-security") {
						required = true
					}
				}
				if required != (scenario == "remove-explicit") {
					t.Fatal("actual namespace delta authority incorrect", required)
				}
				if scenario == "external-preserved" && releaseMap(releaseMap(next.Plan.Resources[0].Expected["metadata"])["labels"])["pod-security.kubernetes.io/enforce"] != "restricted" {
					t.Fatal("unowned policy label was removed")
				}
			})
		})
	}
}
func TestReview106NamespaceFrozenDeletionBodyCannotOmitPrivilege(t *testing.T) {
	id := ReleaseResourceID{Version: "v1", Resource: "namespaces", Kind: "Namespace", Name: "protected"}
	resource := ReleaseResource{ID: id, Object: map[string]any{}, Mutation: &ReleaseMutation{Patch: releaseTestObject(`{"metadata":{"labels":{"pod-security.kubernetes.io/enforce":null}}}`)}}
	if !hasReleaseAction(releaseAdministrativeActions(resource), "namespace-security") {
		t.Fatal("null policy label deletion lost capability")
	}
	if releaseResourceAdministrativeAllowed(ReleasePlan{Authorization: ReleaseAuthorization{Request: ReleaseAuthorizationRequest{Resources: []ReleaseAccess{{ID: id, Actions: []string{"patch"}}}}}}, resource) {
		t.Fatal("ordinary patch authorized special label deletion")
	}
}
func TestReview106RecoveryFenceRemainsPrivateAndIdempotent(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		r, e := f.s.CreateRelease(ctx, "recovery-known", "node", "departed-owner", p)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.ApproveRelease(ctx, r.ID, r.OwnerEpoch, r.Revision, r.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		if e = f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		c := NewReleaseController(f.s, nil)
		if e = c.RecoverDeparted(ctx, []string{"someone-else"}); e != nil {
			t.Fatal(e)
		}
		before, _ := f.s.GetRelease(ctx, r.ID)
		if before.OwnerFenced {
			t.Fatal("unproved departed owner fenced")
		}
		if e = c.RecoverDeparted(ctx, []string{r.OwnerEpoch}); e != nil {
			t.Fatal(e)
		}
		after, _ := f.s.GetRelease(ctx, r.ID)
		if _, e = f.s.SetReleaseTerminal(ctx, after.ID, after.OwnerEpoch, after.Revision, "failed", "worker_interrupted"); !errors.Is(e, ErrOwner) {
			t.Fatal("ordinary fenced owner acquired recovery-only privilege", e)
		}
		if e = c.RecoverDeparted(ctx, []string{r.OwnerEpoch}); e != nil {
			t.Fatal("recovery not idempotent", e)
		}
	})
}

func TestReview106DepartedUnknownKeepsResourceHolds(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, c, _, r := review106ConfigMapRun(t, st, false)
		if _, e := st.DB.Exec("UPDATE k8s_releases SET state='running',owner_fenced=0 WHERE id=?", r.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := st.DB.Exec("UPDATE k8s_release_actions SET state='outcome_unknown' WHERE release_id=?", r.ID); e != nil {
			t.Fatal(e)
		}
		if e := f.s.AcquireReleaseGates(ctx, r.ID, r.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		if e := c.RecoverDeparted(ctx, []string{r.OwnerEpoch}); e != nil {
			t.Fatal(e)
		}
		fresh, e := f.s.GetRelease(ctx, r.ID)
		if e != nil || fresh.State != "outcome_unknown" || !fresh.OwnerFenced {
			t.Fatal("unknown recovery lost fence", e)
		}
		var gates int
		if e = st.DB.QueryRow("SELECT COUNT(*) FROM k8s_resource_gates WHERE owner_id=?", r.ID).Scan(&gates); e != nil || gates != 1 {
			t.Fatal("unknown hold dropped", gates, e)
		}
	})
}

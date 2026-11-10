package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
)

func TestReleaseReadOnlyReconciliationResolvesOwnedLostResponseWithoutRedispatch(t *testing.T) {
	for _, observed := range []bool{true, false} {
		storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
			ctx := context.Background()
			f, p := releaseStoreFixture(t, st)
			id := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
			scope := ReleaseAccess{ID: id, Actions: []string{"get", "create"}}
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, RequireApproval: true, Enabled: true, Resources: []ReleaseAccess{scope}}); e != nil {
				t.Fatal(e)
			}
			request := p.Authorization.Request
			request.Resources = []ReleaseAccess{scope}
			a, e := f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			resource := releaseResourceFromObject(id, releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"private":"private-reconciliation-fixture"}}`), "declaration")
			p = ReleasePlan{Version: 2, Mode: "manifest", Authorization: a, Resources: []ReleaseResource{resource}, TimeoutSeconds: 300}
			if _, e = st.DB.Exec(`INSERT INTO pipeline_runs(id,project_id,status,created_at,finished_at) VALUES('reconcile-run','project-1','failed','now','finished')`); e != nil {
				t.Fatal(e)
			}
			parent, e := f.s.CreateRelease(ctx, "reconcile-run", "node", "owner", p)
			if e != nil {
				t.Fatal(e)
			}
			parent, e = f.s.ApproveRelease(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, parent.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.s.AcquireReleaseGates(ctx, parent.ID, parent.OwnerEpoch); e != nil {
				t.Fatal(e)
			}
			registerControllerTestWriter(t, f, parent)
			body, _ := json.Marshal(resource.Object)
			fact, e := f.s.PrepareReleaseAction(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, ReleaseActionIntent{Version: 2, ParentID: parent.PlanID, StepKey: "lost-response", Resource: id, Verb: "create", Body: body, AuthorizationFingerprint: a.Fingerprint})
			if e != nil {
				t.Fatal(e)
			}
			objects := map[string]map[string]any{}
			if observed {
				var object map[string]any
				json.Unmarshal(fact.Intent.Body, &object)
				meta := releaseMap(object["metadata"])
				meta["uid"] = "owned-uid"
				meta["resourceVersion"] = "17"
				objects["/api/v1/namespaces/app/configmaps/config"] = object
			}
			parent, e = f.s.SetReleaseTerminal(ctx, parent.ID, parent.OwnerEpoch, parent.Revision, "outcome_unknown", "worker_interrupted")
			if e != nil {
				t.Fatal(e)
			}
			cleaner := &probeControllerAPI{cleanup: true, releaseControllerAPI: &releaseControllerAPI{f: f, record: parent}}
			if _, e = cleaner.Close(ctx); e != nil {
				t.Fatal(e)
			}
			parent, e = f.s.GetRelease(ctx, parent.ID)
			if e != nil {
				t.Fatal(e)
			}
			api := &releaseControllerAPI{objects: objects}
			c := NewReleaseController(f.s, nil)
			c.preview = func(_ context.Context, a ReleaseAuthorization, _ string) (releaseAPI, error) {
				for _, scope := range a.Request.Resources {
					for _, verb := range scope.Actions {
						if verb != "get" {
							t.Fatal("inspection acquired write capability", verb)
						}
					}
				}
				return api, nil
			}
			result, e := c.ReconcileRelease(ctx, parent.ID, parent.Revision)
			if e != nil {
				t.Fatal(e)
			}
			expected := "outcome_unknown"
			if observed {
				expected = "ready"
			}
			if result.State != expected || api.writes != 0 {
				t.Fatal("unknown replayed or absence interpreted as rejection", result.State, api.writes)
			}
			var runState string
			if e = st.DB.QueryRow(`SELECT status FROM pipeline_runs WHERE id='reconcile-run'`).Scan(&runState); e != nil || runState != "failed" {
				t.Fatal("historical run rewritten", e)
			}
			if _, e = c.ReconcileRelease(ctx, parent.ID, parent.Revision); !errors.Is(e, ErrConflict) {
				t.Fatal("stale reconciliation accepted", e)
			}
		})
	}
}
func TestReleaseAdmissionDynamicServiceAddressAndWorkloadSecurity(t *testing.T) {
	service := ReleaseResource{ID: ReleaseResourceID{Kind: "Service"}, Object: releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"app"},"spec":{"type":"NodePort","ports":[{"port":80}]}}`)}
	dry := releaseObjectCopy(service.Object)
	releaseMap(dry["spec"])["clusterIP"] = "10.0.0.1"
	releaseMap(releaseItems(releaseMap(dry["spec"])["ports"])[0])["nodePort"] = float64(30001)
	service.Expected = releaseAdmittedExpected(service, dry)
	live := releaseObjectCopy(dry)
	releaseMap(live["spec"])["clusterIP"] = "10.0.0.2"
	releaseMap(releaseItems(releaseMap(live["spec"])["ports"])[0])["nodePort"] = float64(30002)
	if !releaseResourceExpectedMatches(service, live) {
		t.Fatal("server allocated addresses treated as approved constants")
	}
	workload := ReleaseResource{ID: ReleaseResourceID{Group: "apps", Kind: "Deployment"}, Object: releaseTestObject(`{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"fixed"}]}}}}`)}
	workload.Expected = releaseObjectCopy(workload.Object)
	mutated := releaseObjectCopy(workload.Object)
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(mutated["spec"])["template"])["spec"])["containers"])[0])["securityContext"] = map[string]any{"privileged": true}
	if releaseResourceExpectedMatches(workload, mutated) {
		t.Fatal("external unapproved privilege addition accepted")
	}
}

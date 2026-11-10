package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/configstate"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

type rollbackControllerAPI struct{ *probeControllerAPI }

func (a *rollbackControllerAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if strings.HasSuffix(path, "/endpointslices") {
		return []byte(`{"apiVersion":"discovery.k8s.io/v1","kind":"EndpointSliceList","items":[]}`), nil
	}
	return a.probeControllerAPI.Read(ctx, path, query)
}
func TestRollbackRestoresFrozenPrivateBaselineAndDeletesOnlyOwnedNewObject(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		f, p := releaseStoreFixture(t, st)
		configID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
		serviceID := ReleaseResourceID{Version: "v1", Resource: "services", Kind: "Service", Namespace: "app", Name: "route"}
		scopes := []ReleaseAccess{{ID: configID, Actions: []string{"get", "patch"}}, {ID: serviceID, Actions: []string{"get", "create", "related-endpointslices"}}}
		if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: false, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		request := p.Authorization.Request
		request.Resources = scopes
		auth, e := f.s.AuthorizeRelease(ctx, request)
		if e != nil {
			t.Fatal(e)
		}
		before := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"original-uid","resourceVersion":"17"},"data":{"private":"old-private-value"}}`)
		desired := releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"private":"new-private-value"}}`)
		service := releaseTestObject(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"route","namespace":"app"},"spec":{"selector":{"app":"backend"},"ports":[{"port":80}]}}`)
		p = ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{releaseResourceFromObject(configID, desired, "declaration"), releaseResourceFromObject(serviceID, service, "declaration")}, Baseline: []ReleaseResource{releaseResourceFromObject(configID, before, "declaration")}, TimeoutSeconds: 300}
		source, e := f.s.CreateRelease(ctx, "publish-run", "node", "publish-owner", p)
		if e != nil {
			t.Fatal(e)
		}
		source, e = f.s.ApproveRelease(ctx, source.ID, source.OwnerEpoch, source.Revision, source.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api := &rollbackControllerAPI{probeControllerAPI: &probeControllerAPI{cleanup: true, releaseControllerAPI: &releaseControllerAPI{f: f, record: source, objects: map[string]map[string]any{"/api/v1/namespaces/app/configmaps/config": before}}}}
		c := NewReleaseController(f.s, nil)
		c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
		registerControllerTestWriter(t, f, source)
		source, e = c.Run(ctx, source.ID, source.OwnerEpoch, nil)
		if e != nil || !source.Successful() {
			t.Fatal("source did not settle", source.State, e)
		}
		rollbackScopes := []ReleaseAccess{{ID: configID, Actions: []string{"get", "patch", "rollback"}}, {ID: serviceID, Actions: []string{"get", "delete", "rollback"}}}
		if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: rollbackScopes, RequireApproval: false, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		rollback, e := c.PrepareRollback(ctx, "rollback-run", "rollback-node", "rollback-owner", source.ID, source.Revision, source.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		requestInput := ReleaseRollbackRequest{SourceID: source.ID, ExpectedRevision: source.Revision, PlanID: source.PlanID, RequestID: "fixed-request"}
		if e = configstate.WithWriteTxContext(ctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
			if _, e := q.ExecContext(tx, `INSERT INTO pipeline_runs(id,project_id,status,created_at) VALUES(?,'project-1','queued','now')`, rollback.RunID); e != nil {
				return e
			}
			if _, e := configstate.AcquireActivityTx(tx, q, st.DB, configstate.Activity{ID: rollback.RunID, ProjectID: "project-1", RunID: rollback.RunID, OwnerEpoch: "queue-reader", Kind: "run", Phase: "queued"}); e != nil {
				return e
			}
			if e := f.s.BindReleaseRunReader(tx, rollback.ID, rollback.OwnerEpoch); e != nil {
				return e
			}
			return f.s.RecordReleaseRollbackRequest(tx, requestInput, rollback)
		}); e != nil {
			t.Fatal(e)
		}
		found, e := f.s.LookupReleaseRollback(ctx, requestInput)
		if e != nil || found.RunID != rollback.RunID || found.Release.ID != rollback.ID {
			t.Fatal("lost-ack request lost", e)
		}
		changed := requestInput
		changed.ExpectedRevision++
		if _, e = f.s.LookupReleaseRollback(ctx, changed); !errors.Is(e, ErrConflict) {
			t.Fatal("request identity rebound", e)
		}
		pending, e := f.s.PendingReleaseContinuations(ctx)
		if e != nil || len(pending) != 1 {
			t.Fatal("rollback dispatch missing", e)
		}
		claimed, e := f.s.ClaimReleaseContinuation(ctx, pending[0].ID, rollback.OwnerEpoch)
		if e != nil {
			t.Fatal("queued rollback reader blocked its own dispatch", e)
		}
		if e = configstate.WithWriteTxContext(ctx, st.DB, func(tx context.Context, q configstate.DBTX) error {
			if e := f.s.BindReleaseContinuationReader(tx, rollback.RunID, rollback.OwnerEpoch, "queue-reader"); e != nil {
				return e
			}
			_, e := q.ExecContext(tx, `UPDATE pipeline_runs SET status='running' WHERE id=?`, rollback.RunID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e = f.s.ActivateReleaseContinuation(ctx, claimed.ID, rollback.OwnerEpoch); e != nil {
			t.Fatal(e)
		}
		rollback, e = f.s.GetRelease(ctx, rollback.ID)
		if e != nil || rollback.State != "prepared" || rollback.ApprovedID != "" {
			t.Fatal("dispatch implicitly approved rollback", e)
		}
		if rollback.Plan.RollbackOf != source.ID || rollback.RollbackOf != source.ID || rollback.PlanID == source.PlanID || !rollback.Plan.ForceApproval || rollback.ApprovedID != "" || rollback.Plan.Authorization.RequireApproval {
			t.Fatal("fresh rollback approval was inferred from original grant/approval")
		}
		if e = f.s.AcquireReleaseGates(ctx, rollback.ID, rollback.OwnerEpoch); !errors.Is(e, ErrDenied) {
			t.Fatal("unapproved rollback acquired source gates", e)
		}
		preview, _ := json.Marshal(PreviewRelease(rollback))
		if strings.Contains(string(preview), "old-private-value") || strings.Contains(string(preview), "new-private-value") {
			t.Fatal("rollback preview exposed private baseline")
		}
		// Exercise the forced approval check independently of the state check.
		if _, e = st.DB.Exec(`UPDATE k8s_releases SET state='running' WHERE id=?`, rollback.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.RegisterReleaseRuntime(ctx, rollback.ID, rollback.OwnerEpoch, "engine", rollback.Plan.Authorization); !errors.Is(e, ErrDenied) {
			t.Fatal("false-requireApproval grant bypassed rollback approval", e)
		}
		if _, e = st.DB.Exec(`UPDATE k8s_releases SET state='prepared' WHERE id=?`, rollback.ID); e != nil {
			t.Fatal(e)
		}
		rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
		if e != nil {
			t.Fatal(e)
		}
		api.record = rollback
		registerControllerTestWriter(t, f, rollback)
		result, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
		if e != nil || !result.Successful() || result.State != "rolled_back" {
			t.Fatal("rollback did not settle", result.State, result.Cleanup, e)
		}
		current := api.objects["/api/v1/namespaces/app/configmaps/config"]
		if releaseMap(current["data"])["private"] != "old-private-value" || releaseMap(current["metadata"])["uid"] != "original-uid" || api.objects["/api/v1/namespaces/app/services/route"] != nil {
			t.Fatal("rollback changed identity or failed exact restoration/deletion")
		}
		facts, e := f.s.ListReleaseActions(ctx, result.ID)
		if e != nil || len(facts) != 2 {
			t.Fatal("rollback receipts missing", len(facts), e)
		}
		removed := false
		for _, fact := range facts {
			if fact.Intent.Resource == serviceID && fact.Intent.Verb == "delete" && fact.Receipt != nil && fact.Receipt.Deleted {
				removed = true
			}
		}
		if !removed {
			t.Fatal("absence of owned UID not recorded")
		}
		original, e := f.s.GetRelease(ctx, source.ID)
		if e != nil || original.State != "rolled_back" || original.PlanID != source.PlanID {
			t.Fatal("source relationship lost", original.State, e)
		}
	})
}

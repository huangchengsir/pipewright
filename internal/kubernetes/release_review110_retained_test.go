package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

func TestReview110RetainedRollbackReconciliation(t *testing.T) {
	ctx := context.Background()
	f, seed := releaseStoreFixture(t, storetest.Open(t))
	cmID := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	nsID := ReleaseResourceID{Version: "v1", Resource: "namespaces", Kind: "Namespace", Name: "retained-ns"}
	scopes := []ReleaseAccess{{ID: cmID, Actions: []string{"get", "create", "delete", "rollback"}}, {ID: nsID, Actions: []string{"get", "create", "rollback"}}}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	req := seed.Authorization.Request
	req.Resources = scopes
	auth, e := f.s.AuthorizeRelease(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	cm := releaseResourceFromObject(cmID, releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"setting":"synthetic"}}`), "declaration")
	ns := releaseResourceFromObject(nsID, releaseTestObject(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"retained-ns"}}`), "declaration")
	plan := ReleasePlan{Version: 2, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{cm, ns}, TimeoutSeconds: 300}
	api := &probeControllerAPI{cleanup: true, releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	source, e := f.s.CreateRelease(ctx, "edges109-source", "node", "source-owner", plan)
	if e != nil {
		t.Fatal(e)
	}
	source, e = f.s.ApproveRelease(ctx, source.ID, source.OwnerEpoch, source.Revision, source.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = source
	registerControllerTestWriter(t, f, source)
	source, e = c.Run(ctx, source.ID, source.OwnerEpoch, nil)
	if e != nil || !source.Successful() {
		t.Fatal("source", source.State, e)
	}
	api.cleanup = false
	rb, e := c.PrepareRollback(ctx, "edges109-rollback", "node", "rollback-owner", source.ID, source.Revision, source.PlanID)
	if e != nil {
		t.Fatal("prepare", e)
	}
	if len(rb.Plan.Retained) != 1 {
		t.Fatal("missing retained fixture")
	}
	rb, e = f.s.ApproveRelease(ctx, rb.ID, rb.OwnerEpoch, rb.Revision, rb.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = rb
	api.cleanup = true
	registerControllerTestWriter(t, f, rb)
	rb, e = c.Run(ctx, rb.ID, rb.OwnerEpoch, nil)
	if !errors.Is(e, ErrRollout) || rb.State != "partial_failed" {
		t.Fatal("initial result", rb.State, e)
	}
	if api.objects["/api/v1/namespaces/retained-ns"] == nil {
		t.Fatal("retained namespace missing")
	}
	writes := api.writes
	api.cleanup = false
	reconciled, e := c.ReconcileRelease(ctx, rb.ID, rb.Revision)
	if e != nil {
		t.Fatal("reconcile", e)
	}
	t.Logf("initial=partial_failed retained=%d reconciled=%s successful=%v additional_writes=%d", len(reconciled.Plan.Retained), reconciled.State, reconciled.Successful(), api.writes-writes)
	if reconciled.State != "partial_failed" || reconciled.Successful() {
		t.Fatal("retained effects promoted to full rollback")
	}
	if api.objects["/api/v1/namespaces/retained-ns"] == nil {
		t.Fatal("retained namespace changed")
	}
	expected, _ := json.Marshal(rb.Plan.Retained)
	for attempt := 0; attempt < 3; attempt++ {
		// Recreate the service/controller from the same persisted DB and key.
		restored := New(f.s.db, f.s.sealer)
		fresh := NewReleaseController(restored, nil)
		fresh.preview = c.preview
		if attempt == 1 {
			if _, err := f.s.db.Exec(`UPDATE k8s_releases SET state='rolled_back' WHERE id=?`, reconciled.ID); err != nil {
				t.Fatal(err)
			}
		}
		current, err := restored.GetRelease(ctx, reconciled.ID)
		if err != nil {
			t.Fatal(err)
		}
		reconciled, err = fresh.ReconcileRelease(ctx, current.ID, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(reconciled.Plan.Retained)
		if reconciled.State != "partial_failed" || reconciled.Successful() || reconciled.ReasonCode != "rollback_partial" || string(actual) != string(expected) || api.writes != writes {
			t.Fatal("restart/repeated read lost retained truth", attempt, reconciled.State, err)
		}
	}

}

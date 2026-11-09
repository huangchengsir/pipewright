package kubernetes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

func TestReleaseResultsUnknownDeletionRetainsOnlyVerifiedSameUIDObservation(t *testing.T) {
	for _, scenario := range []string{"matching", "foreign-uid", "foreign-resource", "no-observation", "submitted-observation", "pending-ack"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, p := releaseStoreFixture(t, st)
				clock := time.Now()
				f.s.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
				id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "check-dns"}
				priorID := id
				if scenario == "foreign-resource" {
					priorID.Name = "other-check-dns"
				}
				object := func(target ReleaseResourceID) map[string]any {
					return map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": target.Name, "namespace": target.Namespace}, "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"restartPolicy": "Never", "containers": []any{map[string]any{"name": "probe", "image": "registry.invalid/probe@sha256:" + strings.Repeat("a", 64), "args": []any{"private-projection-script-canary"}}}}}}}
				}
				scopes := []ReleaseAccess{{ID: id, Actions: []string{"get", "create", "delete"}}}
				p.Resources = []ReleaseResource{releaseResourceFromObject(id, object(id), "workload")}
				p.Deletes = []ReleaseResourceID{id}
				if priorID != id {
					scopes = append(scopes, ReleaseAccess{ID: priorID, Actions: []string{"get", "create", "delete"}})
					p.Resources = append(p.Resources, releaseResourceFromObject(priorID, object(priorID), "workload"))
				}
				if _, err := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); err != nil {
					t.Fatal(err)
				}
				request := p.Authorization.Request
				request.Resources = scopes
				var err error
				p.Authorization, err = f.s.AuthorizeRelease(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				record, err := f.s.CreateRelease(ctx, "run-result-projection", "node", "owner", p)
				if err != nil {
					t.Fatal(err)
				}
				record, err = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.AcquireReleaseGates(ctx, record.ID, record.OwnerEpoch); err != nil {
					t.Fatal(err)
				}
				registerControllerTestWriter(t, f, record)
				observedAt := int64(0)
				if scenario != "no-observation" {
					body, _ := json.Marshal(object(priorID))
					intent := ReleaseActionIntent{Version: 2, ParentID: record.PlanID, StepKey: "batch-0-resource-0", Resource: priorID, Verb: "create", Body: body, AuthorizationFingerprint: p.Authorization.Fingerprint}
					create, e := f.s.PrepareReleaseAction(ctx, record.ID, record.OwnerEpoch, record.Revision, intent)
					if e != nil {
						t.Fatal(e)
					}
					actual := map[string]any{}
					_ = json.Unmarshal(create.Intent.Body, &actual)
					meta := releaseMap(actual["metadata"])
					meta["uid"] = "observed-job-uid"
					meta["resourceVersion"] = "17"
					health := ReleaseHealth{State: "ready", Reason: "job_completed", Containers: []ReleaseContainerHealth{{Pod: "observed-pod", Name: "probe", Ready: true}}}
					if scenario == "pending-ack" {
						health = ReleaseHealth{State: "pending", Reason: "resource_not_observed"}
					}
					state := "verified"
					if scenario == "submitted-observation" {
						state = "submitted"
					}
					create, e = f.s.RecordReleaseAction(ctx, create.ID, record.OwnerEpoch, create.Revision, state, ReleaseActionReceipt{Resource: priorID, UID: "observed-job-uid", ResourceVersion: "17", Object: actual, Health: health})
					if e != nil {
						t.Fatal(e)
					}
					observedAt = create.Receipt.ObservedAt
				}
				uid := "observed-job-uid"
				if scenario == "foreign-uid" {
					uid = "replaced-job-uid"
				}
				body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]any{"uid": uid, "resourceVersion": "18"}})
				deletion, e := f.s.PrepareReleaseAction(ctx, record.ID, record.OwnerEpoch, record.Revision, ReleaseActionIntent{Version: 2, ParentID: record.PlanID, StepKey: "batch-0-probe-cleanup-0", Resource: id, Verb: "delete", Body: body, ExpectedUID: uid, ExpectedRV: "18", AuthorizationFingerprint: p.Authorization.Fingerprint})
				if e != nil {
					t.Fatal(e)
				}
				if deletion.State != "outcome_unknown" || deletion.Receipt != nil {
					t.Fatal("fixture did not preserve nil-receipt unknown deletion")
				}
				rows, e := f.s.ReleaseResults(ctx, record.ID)
				if e != nil {
					t.Fatal(e)
				}
				var row ReleaseResourceResult
				for _, result := range rows {
					if result.Resource == id {
						row = result
					}
				}
				if row.ActionID != deletion.ID || row.State != "outcome_unknown" || row.Deleted {
					t.Fatal("projection changed unknown action or invented deletion proof")
				}
				if scenario == "matching" {
					if row.Health.State != "ready" || row.Health.Reason != "job_completed" || len(row.Health.Containers) != 1 || row.ObservedAt != observedAt || row.ObservedAt == 0 {
						t.Fatal("unknown cleanup erased verified same-UID health/time")
					}
				} else if row.Health.State != "pending" || row.ObservedAt != 0 || len(row.Health.Containers) != 0 {
					t.Fatal("projection borrowed foreign or unverified health")
				}
				encoded, _ := json.Marshal(rows)
				if strings.Contains(string(encoded), "private-projection-script-canary") {
					t.Fatal("projection exposed private source")
				}
				after, e := f.s.GetReleaseAction(ctx, deletion.ID)
				if e != nil || after.Revision != deletion.Revision || after.State != "outcome_unknown" || after.Receipt != nil {
					t.Fatal("reading projection mutated unknown action")
				}
				fresh, e := f.s.GetRelease(ctx, record.ID)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.s.PrepareReleaseAction(ctx, record.ID, record.OwnerEpoch, fresh.Revision, ReleaseActionIntent{Version: 2, ParentID: record.PlanID, StepKey: "never-replay", Resource: id, Verb: "delete", Body: body, ExpectedUID: uid, ExpectedRV: "18", AuthorizationFingerprint: p.Authorization.Fingerprint}); e != ErrOutcomeUnknown {
					t.Fatal("projection relaxed unknown write fence", e)
				}
			})
		})
	}
}

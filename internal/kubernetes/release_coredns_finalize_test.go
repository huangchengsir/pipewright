package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"testing"
	"time"
)

type coreDNSFinalizeAPI struct {
	*coreDNSExecutionAPI
	mode            string
	injected        bool
	finalDispatches int
	proofRejected   bool
	restartObserved bool
}

func (a *coreDNSFinalizeAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	if a.mode == "uid" || a.mode == "rv" || a.mode == "drift" || a.mode == "outside-key" {
		var object map[string]any
		_ = json.Unmarshal(in.Body, &object)
		if !a.injected && a.record.Plan.CoreDNS != nil && a.record.Plan.CoreDNS.Finalize != nil && a.liveWrites > 0 && releaseMap(object["data"])["Corefile"] == releaseMap(a.record.Plan.CoreDNS.Finalize.Object["data"])["Corefile"] {
			a.injected = true
			live := a.objects["/api/v1/namespaces/app/configmaps/dns-config"]
			switch a.mode {
			case "uid":
				releaseMap(live["metadata"])["uid"] = "foreign-incarnation"
			case "rv":
				releaseMap(live["metadata"])["resourceVersion"] = "external-rv"
			case "drift":
				releaseMap(live["data"])["Corefile"] = "external-config"
			case "outside-key":
				releaseMap(live["data"])["NodeHosts"] = "external-update"
			}
		}
	}
	return a.coreDNSExecutionAPI.DryRun(ctx, in)
}
func (a *coreDNSFinalizeAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	if in.StepKey != releaseCoreDNSFinalizeStep {
		return a.coreDNSExecutionAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	}
	if a.mode == "restart-before-dispatch" && !a.injected {
		a.injected = true
		restarted := NewReleaseController(a.f.s, nil)
		restarted.poll = time.Millisecond
		parent, e := a.f.s.GetRelease(ctx, id)
		if e != nil {
			return ReleaseAction{}, e
		}
		if e = restarted.finishCoreDNSFinalization(ctx, a, parent); e != nil {
			return ReleaseAction{}, e
		}
		facts, e := a.f.s.ListReleaseActions(ctx, id)
		if e != nil {
			return ReleaseAction{}, e
		}
		for _, fact := range facts {
			if fact.Intent.StepKey == releaseCoreDNSFinalizeStep {
				a.restartObserved = true
				return fact, nil
			}
		}
		return ReleaseAction{}, ErrOutcomeUnknown
	}
	if a.mode == "forged-body" {
		body := releaseTestObject(string(in.Body))
		releaseMap(body["data"])["Corefile"] = "not-approved"
		forged := in
		forged.Body, _ = json.Marshal(body)
		if _, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, forged); !errors.Is(e, ErrDenied) {
			return ReleaseAction{}, errors.New("forged final body was not denied")
		}
		forged = in
		forged.StepKey = "unapproved-final-step"
		if _, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, forged); !errors.Is(e, ErrDenied) {
			return ReleaseAction{}, errors.New("final body acquired another step")
		}
		a.proofRejected = true
	}
	if a.mode == "unknown-before-send" {
		action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
		if e != nil {
			return action, e
		}
		return action, ErrOutcomeUnknown
	}
	action, e := a.coreDNSExecutionAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	if e != nil {
		return action, e
	}
	a.finalDispatches++
	if a.mode == "submitted-after-accept" {
		// Preserve the actual accepted receipt, but model a process stop before the
		// caller could advance a persisted submitted action to verified.
		_, e = a.f.s.db.ExecContext(ctx, "UPDATE k8s_release_actions SET state='submitted' WHERE id=?", action.ID)
		if e != nil {
			return action, e
		}
		return action, ErrOutcomeUnknown
	}
	if a.mode == "restart-after-receipt" && !a.injected {
		a.injected = true
		restarted := NewReleaseController(a.f.s, nil)
		restarted.poll = time.Millisecond
		parent, err := a.f.s.GetRelease(ctx, id)
		if err != nil {
			return action, err
		}
		if err = restarted.finishCoreDNSFinalization(ctx, a, parent); err != nil {
			return action, err
		}
		a.restartObserved = true
		return a.f.s.GetReleaseAction(ctx, action.ID)
	}
	return action, nil
}

func coreDNS105Prepare(t *testing.T, st *store.Store, mode string) (*ReleaseController, *coreDNSFinalizeAPI, *coreDNSFixture, ReleaseRecord) {
	t.Helper()
	ctx := context.Background()
	fixture := newCoreDNSFixture(t, st)
	f := fixture.f
	api := &coreDNSFinalizeAPI{coreDNSExecutionAPI: &coreDNSExecutionAPI{coreDNSPlanAPI: fixture.api}, mode: mode}
	c := NewReleaseController(f.s, nil)
	c.poll = time.Millisecond
	data, _ := json.Marshal(fixture.config)
	scout := fixture.plan
	scopes, e := c.prepareCoreDNSPlan(ctx, api, &scout, fixture.policy.Authorization, data)
	if e != nil {
		t.Fatal(e)
	}
	for _, resource := range scout.Resources {
		scopes = append(scopes, ReleaseAccess{ID: resource.ID, Actions: []string{"get", "create", "patch", "delete", "rollback", "coredns", "probe", "related-pods", "related-replicasets", "related-endpointslices"}, Containers: releaseContainerNames(resource)})
	}
	scopes = mergeReleaseScopes(scopes)
	for i := range scopes {
		scopes[i].Actions = uniqueReleaseActions(append(scopes[i].Actions, "rollback"))
	}
	if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: scopes, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	auth, e := f.s.ReleasePolicyCatalog(ctx, fixture.policy.Authorization.Request)
	if e != nil {
		t.Fatal(e)
	}
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	files, _ := InlineReleaseFile(stringMustJSON(fixture.desired))
	record, e := c.PrepareManifest(ctx, "finalize-run", "node", "owner", auth, files, nil, data, 600, 0)
	if e != nil {
		t.Fatal(e)
	}
	if record.Plan.CoreDNS == nil || record.Plan.CoreDNS.Finalize == nil || !validCoreDNSFinalization(record.Plan) || !PreviewRelease(record).CoreDNSFinalize {
		t.Fatal("final exact target was not sealed and projected before approval")
	}
	record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	api.record = record
	registerControllerTestWriter(t, f, record)
	return c, api, fixture, record
}

func TestCoreDNS105FinalizeFencesAndRecovery(t *testing.T) {
	for _, mode := range []string{"uid", "rv", "drift", "outside-key", "unknown-before-send", "submitted-after-accept", "restart-before-dispatch", "restart-after-receipt", "forged-body"} {
		t.Run(mode, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				c, api, fixture, record := coreDNS105Prepare(t, st, mode)
				result, e := c.Run(context.Background(), record.ID, record.OwnerEpoch, nil)
				facts, err := fixture.f.s.ListReleaseActions(context.Background(), record.ID)
				if err != nil {
					t.Fatal(err)
				}
				finals := []ReleaseAction{}
				for _, fact := range facts {
					if fact.Intent.StepKey == releaseCoreDNSFinalizeStep {
						finals = append(finals, fact)
					}
				}
				switch mode {
				case "unknown-before-send", "submitted-after-accept":
					if !errors.Is(e, ErrOutcomeUnknown) || result.State != "outcome_unknown" || len(finals) != 1 {
						t.Fatal("unsettled final write did not fence release", result.State, e)
					}
					// A fresh controller encountering the same durable action must not send it.
					calls := api.finalDispatches
					restarted := NewReleaseController(fixture.f.s, nil)
					if err = restarted.finishCoreDNSFinalization(context.Background(), api, record); !errors.Is(err, ErrOutcomeUnknown) || api.finalDispatches != calls {
						t.Fatal("unsettled final action replayed", err)
					}
					views, err := fixture.f.s.ReleaseResults(context.Background(), record.ID)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, view := range views {
						if view.Resource == record.Plan.CoreDNS.Finalize.ID {
							found = true
							if view.State == "verified" {
								t.Fatal("unknown final write projected expected success")
							}
						}
					}
					if !found {
						t.Fatal("final source result missing")
					}
				case "uid", "rv", "drift", "outside-key":
					if e == nil || result.State != "partial_failed" || api.finalDispatches != 0 || api.liveWrites != 1 || len(finals) != 0 {
						t.Fatal("external change admitted to terminal CAS", result.State, api.finalDispatches, e)
					}
				default:
					if e != nil || !result.Successful() || api.finalDispatches != 1 || len(finals) != 1 || finals[0].State != "verified" {
						t.Fatal("finalization failed or repeated", result.State, api.finalDispatches, e)
					}
					if mode == "forged-body" && !api.proofRejected {
						t.Fatal("forged final body checks not executed")
					}
					if mode != "forged-body" && !api.restartObserved {
						t.Fatal("fresh controller did not read durable finalization boundary")
					}
					current := api.objects["/api/v1/namespaces/app/configmaps/dns-config"]
					if releaseMap(current["data"])["Corefile"] != releaseMap(fixture.desired["data"])["Corefile"] || releaseMap(current["data"])["NodeHosts"] != "preserved-import-value" {
						t.Fatal("terminal write changed approved target or outside key")
					}
				}
			})
		})
	}
}

func TestCoreDNS105HistoricalPlanSerializationHasNoFinalize(t *testing.T) {
	legacy := ReleaseCoreDNSPlan{Container: "coredns", LiveIndex: 2, Canary: "canary.invalid"}
	data, e := json.Marshal(legacy)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	if fields["finalize"] != nil {
		t.Fatal("old plan acquired new terminal write")
	}
	if !validCoreDNSFinalization(ReleasePlan{CoreDNS: &legacy}) {
		t.Fatal("historical plan rejected")
	}
}

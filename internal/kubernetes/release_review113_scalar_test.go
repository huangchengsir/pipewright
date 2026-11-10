package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type review113ScalarAPI struct {
	*probeControllerAPI
	changeType  bool
	replacement any
	liveOnly    bool
}

func (a *review113ScalarAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/apis/example.invalid/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"example.invalid/v1","resources":[{"name":"widgets","kind":"Widget","namespaced":true,"verbs":["get","create","patch"]}]}`), nil
	}
	if path == "/openapi/v3/apis/example.invalid/v1" {
		return []byte(`{"components":{"schemas":{"Widget":{"type":"object","x-kubernetes-group-version-kind":[{"group":"example.invalid","version":"v1","kind":"Widget"}],"properties":{"spec":{"type":"object","properties":{"argument":{"x-kubernetes-int-or-string":true,"anyOf":[{"type":"integer"},{"type":"string"}]}}}}}}}}`), nil
	}
	return a.probeControllerAPI.Read(ctx, path, query)
}
func (a *review113ScalarAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	o, e := a.releaseControllerAPI.DryRun(ctx, in)
	if e == nil && a.changeType && !a.liveOnly {
		releaseMap(o["spec"])["argument"] = a.replacement
	}
	return o, e
}
func (a *review113ScalarAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var object map[string]any
	if json.Unmarshal(action.Intent.Body, &object) != nil {
		return action, ErrInvalid
	}
	if a.changeType {
		releaseMap(object["spec"])["argument"] = a.replacement
	}
	meta := releaseMap(object["metadata"])
	meta["uid"] = "review113-widget-uid"
	meta["resourceVersion"] = "17"
	meta["generation"] = 1
	object["status"] = map[string]any{"observedGeneration": 1, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
	a.objects[path+"/"+in.Resource.Name] = object
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: "review113-widget-uid", ResourceVersion: "17", Object: object})
}
func TestReview113ScalarTypeAdmissionEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      bool
		replacement any
		reject      bool
		liveOnly    bool
	}{{"unchanged_numeric_control", false, nil, false, false}, {"different_number_control", true, float64(8), true, false}, {"different_string_control", true, "8", true, false}, {"number_to_string", true, "7", true, false}, {"live_only_number_to_string", true, "7", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			changeType := tc.change
			ctx := context.Background()
			f, p := releaseStoreFixture(t, storetest.Open(t))
			rid := ReleaseResourceID{Group: "example.invalid", Version: "v1", Resource: "widgets", Kind: "Widget", Namespace: "app", Name: "widget"}
			scope := releaseRequestedAccess(rid, "create")
			if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			request := p.Authorization.Request
			request.Resources = []ReleaseAccess{scope}
			auth, e := f.s.AuthorizeRelease(ctx, request)
			if e != nil {
				t.Fatal(e)
			}
			api := &review113ScalarAPI{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}, changeType: changeType, replacement: tc.replacement, liveOnly: tc.liveOnly}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
			files, e := InlineReleaseFile("apiVersion: example.invalid/v1\nkind: Widget\nmetadata: {name: widget, namespace: app}\nspec: {argument: 7}\n")
			if e != nil {
				t.Fatal(e)
			}
			record, e := c.PrepareManifest(ctx, "review113-scalar-run", "node", "review113-owner", auth, files, nil, json.RawMessage(`{"namespace":"app","checks":[{"kind":"condition","name":"widget","predicate":"condition:Ready","expected":"True"}]}`), 300, 0)
			t.Logf("changeType=%v PrepareManifestError=%v writes=%d", changeType, e, api.writes)
			if tc.reject {
				if !errors.Is(e, errReleaseAdmissionMismatch) || api.writes != 0 {
					t.Fatal("changed-value control was not rejected before writes", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			original := releaseMap(record.Plan.Resources[0].Object["spec"])["argument"]
			admitted := releaseMap(record.Plan.Resources[0].Expected["spec"])["argument"]
			t.Logf("originalType=%T admittedType=%T", original, admitted)
			record, e = f.s.ApproveRelease(ctx, record.ID, record.OwnerEpoch, record.Revision, record.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			registerControllerTestWriter(t, f, record)
			api.record = record
			api.cleanup = true
			done, e := c.Run(ctx, record.ID, record.OwnerEpoch, nil)
			facts, fe := f.s.ListReleaseActions(ctx, record.ID)
			t.Logf("changeType=%v RunError=%v state=%s successful=%v writes=%d facts=%d factsError=%v", changeType, e, done.State, done.Successful(), api.writes, len(facts), fe)
			if changeType {
				if e == nil || done.Successful() {
					t.Fatal("numeric intent changed to string during live response but still succeeded")
				}
				for _, fact := range facts {
					if fact.State == "verified" {
						t.Fatal("wrong type received verified receipt")
					}
				}
			} else if e != nil || !done.Successful() {
				t.Fatal(e, done.State)
			}
		})
	}
}

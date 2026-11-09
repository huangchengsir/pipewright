package kubernetes

import (
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func review107AdmitPod(source map[string]any, name string) map[string]any {
	out := releaseObjectCopy(source)
	spec := releaseMap(out["spec"])
	spec["serviceAccountName"] = "default"
	volume := releaseTestObject(`{"name":"placeholder","projected":{"defaultMode":420,"sources":[{"serviceAccountToken":{"path":"token","expirationSeconds":3607}},{"configMap":{"name":"kube-root-ca.crt","items":[{"key":"ca.crt","path":"ca.crt"}]}},{"downwardAPI":{"items":[{"path":"namespace","fieldRef":{"apiVersion":"v1","fieldPath":"metadata.namespace"}}]}}]}}`)
	volume["name"] = name
	spec["volumes"] = append(releaseItems(spec["volumes"]), volume)
	for _, key := range []string{"containers", "initContainers"} {
		for _, v := range releaseItems(spec[key]) {
			c := releaseMap(v)
			c["volumeMounts"] = append(releaseItems(c["volumeMounts"]), map[string]any{"name": name, "mountPath": "/var/run/secrets/kubernetes.io/serviceaccount", "readOnly": true})
		}
	}
	return out
}

type review107PodAPI struct {
	*probeControllerAPI
	change string
}

func (a *review107PodAPI) DryRun(ctx context.Context, in kubeprotocol.Request) (map[string]any, error) {
	var object map[string]any
	if json.Unmarshal(in.Body, &object) != nil {
		return nil, ErrInvalid
	}
	return review107AdmitPod(object, "kube-api-access-dry01"), nil
}
func (a *review107PodAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var object map[string]any
	if json.Unmarshal(action.Intent.Body, &object) != nil {
		return action, ErrInvalid
	}
	object = review107AdmitPod(object, "kube-api-access-live1")
	meta := releaseMap(object["metadata"])
	meta["uid"] = "direct-pod-uid"
	meta["resourceVersion"] = "21"
	releaseMap(object["spec"])["nodeName"] = "fixture-node"
	statuses := []any{}
	for _, v := range releaseItems(releaseMap(object["spec"])["containers"]) {
		c := releaseMap(v)
		statuses = append(statuses, map[string]any{"name": c["name"], "image": c["image"], "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}})
	}
	object["status"] = map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": statuses}
	switch a.change {
	case "projection":
		v := releaseMap(releaseItems(specVolume107(object))[1])
		releaseMap(releaseItems(releaseMap(v["projected"])["sources"])[0])["serviceAccountToken"].(map[string]any)["audience"] = "foreign-audience"
	case "declared-mount":
		releaseMap(releaseItems(releaseMap(releaseItems(releaseMap(object["spec"])["containers"])[0])["volumeMounts"])[0])["mountPath"] = "/foreign"
	case "action-marker":
		releaseMap(meta["annotations"])[ReleaseActionAnnotation] = "foreign-action"
	}
	a.objects[path+"/"+in.Resource.Name] = object
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: "direct-pod-uid", ResourceVersion: "21", Object: object, Health: ReleaseHealth{State: "pending"}})
}
func specVolume107(object map[string]any) []any {
	return releaseItems(releaseMap(object["spec"])["volumes"])
}
func TestReview107DirectPodAdmissionReceiptController(t *testing.T) {
	for _, scenario := range []string{"valid", "projection", "declared-mount", "action-marker"} {
		t.Run(scenario, func(t *testing.T) {
			storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
				ctx := context.Background()
				f, seed := releaseStoreFixture(t, st)
				id := ReleaseResourceID{Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "app", Name: "direct"}
				scope := ReleaseAccess{ID: id, Actions: []string{"get", "create"}, Containers: []string{"app"}}
				if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
					t.Fatal(e)
				}
				request := seed.Authorization.Request
				request.Resources = []ReleaseAccess{scope}
				auth, e := f.s.AuthorizeRelease(ctx, request)
				if e != nil {
					t.Fatal(e)
				}
				source := releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"direct","namespace":"app"},"spec":{"restartPolicy":"Always","containers":[{"name":"app","image":"registry.invalid/app@sha256:` + strings.Repeat("a", 64) + `","command":["fixture","serve"],"args":["first","second"],"envFrom":[{"configMapRef":{"name":"approved"}}],"volumeMounts":[{"name":"data","mountPath":"/data"}]}],"volumes":[{"name":"data","emptyDir":{}}]}}`)
				plan := ReleasePlan{Version: 2, MutationVersion: 1, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{releaseResourceFromObject(id, source, "workload")}, TimeoutSeconds: 300}
				api := &review107PodAPI{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{}}}, change: scenario}
				c := NewReleaseController(f.s, nil)
				c.poll = time.Millisecond
				c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
				c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
				if e = c.dryRunReleaseResources(ctx, "owner", &plan); e != nil {
					t.Fatal("preparation", e)
				}
				r, e := f.s.CreateRelease(ctx, "pod107", "node", "owner", plan)
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
				result, e := c.Run(ctx, r.ID, r.OwnerEpoch, nil)
				if scenario != "valid" {
					if e == nil || result.State != "outcome_unknown" || api.writes != 1 || !result.OwnerFenced || result.Cleanup != CleanupVerified {
						t.Fatal("unapproved live admission did not fence accepted single write", scenario, result.State, e)
					}
					actions, err := f.s.ListReleaseActions(ctx, r.ID)
					if err != nil || len(actions) != 1 || actions[0].State != "outcome_unknown" {
						t.Fatal("unapproved action acquired verified receipt", err)
					}
					return
				}
				if e != nil || !result.Successful() {
					t.Fatal("successful native Pod admission was not verified and healthy", result.State, e)
				}
				actions, e := f.s.ListReleaseActions(ctx, r.ID)
				if e != nil || len(actions) != 1 || actions[0].State != "verified" || actions[0].Receipt.Health.State != "ready" {
					t.Fatal("real receipt did not persist", e)
				}
				volumes := releaseItems(releaseMap(actions[0].Receipt.Object["spec"])["volumes"])
				if releaseMap(volumes[len(volumes)-1])["name"] != "kube-api-access-live1" {
					t.Fatal("actual receipt name was rewritten")
				}
			})
		})
	}

}

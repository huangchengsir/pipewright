package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"strings"
	"testing"
	"time"
)

func review113NativeSerialize(object map[string]any) (map[string]any, error) {
	var native any
	switch object["kind"] {
	case "Deployment":
		native = &appsv1.Deployment{}
	case "ConfigMap":
		native = &corev1.ConfigMap{}
	case "Secret":
		native = &corev1.Secret{}
	default:
		return nil, ErrInvalid
	}
	raw, e := json.Marshal(object)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(raw, native); e != nil {
		return nil, e
	}
	raw, e = json.Marshal(native)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(raw, &out)
	return out, e
}

type review113NativeAPI struct{ *review112OrderAPI }

func (a *review113NativeAPI) DryRun(_ context.Context, in kubeprotocol.Request) (map[string]any, error) {
	var body map[string]any
	if json.Unmarshal(in.Body, &body) != nil {
		return nil, ErrInvalid
	}
	return review113NativeSerialize(applyFixtureMerge(a.objects[in.Path], body))
}
func (a *review113NativeAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	a.writes++
	action, e := a.f.s.PrepareReleaseAction(ctx, id, owner, revision, in)
	if e != nil {
		return action, e
	}
	var patch map[string]any
	json.Unmarshal(action.Intent.Body, &patch)
	body := applyFixtureMerge(a.objects[path], patch)
	meta := releaseMap(body["metadata"])
	meta["uid"] = "order-owned-uid"
	meta["resourceVersion"] = fmt.Sprint(18 + a.writes)
	meta["generation"] = 1 + a.writes
	if in.Resource.Kind == "Deployment" {
		body["status"] = map[string]any{"observedGeneration": 1 + a.writes, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	}
	live, e := review113NativeSerialize(body)
	if e != nil {
		return action, e
	}
	a.objects[path] = live
	return a.f.s.RecordReleaseAction(ctx, action.ID, owner, action.Revision, "verified", ReleaseActionReceipt{Resource: in.Resource, UID: releaseString(releaseMap(live["metadata"])["uid"]), ResourceVersion: releaseString(releaseMap(live["metadata"])["resourceVersion"]), Object: live, Health: ReleaseHealth{State: "pending"}})
}
func TestReview113OwnedClearRepopulateAndApprovedRollback(t *testing.T) {
	for _, kind := range []string{"Deployment", "ConfigMap", "Secret"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			id := ReleaseResourceID{Version: "v1", Namespace: "app", Name: "clear", Kind: kind}
			health := "declaration"
			image := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
			object := map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"name": "clear", "namespace": "app"}}
			switch kind {
			case "Deployment":
				id.Group = "apps"
				id.Resource = "deployments"
				health = "workload"
				object["apiVersion"] = "apps/v1"
				object["spec"] = releaseTestObject(`{"replicas":1,"template":{"spec":{"nodeSelector":{"kubernetes.io/os":"linux"},"containers":[{"name":"app","image":"` + image + `","stdin":true,"command":["run"],"args":["arg"],"env":[{"name":"KEEP","value":"x"}],"envFrom":[{"configMapRef":{"name":"source"}}],"securityContext":{"allowPrivilegeEscalation":false}}]}}}`)
			case "ConfigMap":
				id.Resource = "configmaps"
				object["data"] = map[string]any{"KEEP": "value", "EMPTY": ""}
			case "Secret":
				id.Resource = "secrets"
				object["data"] = map[string]any{"KEEP": "dmFsdWU=", "EMPTY": ""}
			}
			f, auth, live := review112OwnedSeed(t, id, object, func(o map[string]any) {
				releaseMap(o["metadata"])["annotations"].(map[string]any)["outside"] = "retained"
				if kind == "Deployment" {
					o["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
				}
				native, e := review113NativeSerialize(o)
				if e != nil {
					t.Fatal(e)
				}
				for k := range o {
					delete(o, k)
				}
				for k, v := range native {
					o[k] = v
				}
			})
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			api := &review113NativeAPI{review112OrderAPI: &review112OrderAPI{review110API: &review110API{probeControllerAPI: &probeControllerAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}}, workload: id, observedImageID: image}}}
			c := NewReleaseController(f.s, nil)
			c.poll = time.Millisecond
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			c.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
			empty := releaseObjectCopy(object)
			if kind == "Deployment" {
				spec := releaseMap(releaseMap(releaseMap(empty["spec"])["template"])["spec"])
				spec["nodeSelector"] = map[string]any{}
				container := releaseMap(releaseItems(spec["containers"])[0])
				container["stdin"] = false
				for _, field := range []string{"command", "args", "env", "envFrom"} {
					container[field] = []any{}
				}
			} else {
				empty["data"] = map[string]any{}
			}
			var source ReleaseRecord
			for index, target := range []map[string]any{empty, object, empty} {
				resource := releaseResourceFromObject(id, releaseObjectCopy(target), health)
				plan := ReleasePlan{Version: 2, MutationVersion: 1, Mode: "manifest", Authorization: auth, Resources: []ReleaseResource{resource}, Baseline: []ReleaseResource{releaseResourceFromObject(id, releaseObjectCopy(api.objects[path]), health)}, TimeoutSeconds: 300}
				owner := fmt.Sprint("clear-owner-", index)
				if e := c.dryRunReleaseResources(ctx, owner, &plan); e != nil {
					t.Fatal("clear/repopulate preparation", index, e)
				}
				var e error
				source, e = f.s.CreateRelease(ctx, fmt.Sprint("clear-run-", index), "node", owner, plan)
				if e != nil {
					t.Fatal(e)
				}
				source, e = f.s.ApproveRelease(ctx, source.ID, source.OwnerEpoch, source.Revision, source.PlanID)
				if e != nil {
					t.Fatal(e)
				}
				api.record = source
				api.cleanup = true
				registerControllerTestWriter(t, f, source)
				source, e = c.Run(ctx, source.ID, source.OwnerEpoch, nil)
				if e != nil || !source.Successful() {
					t.Fatal("clear/repopulate execution", index, e)
				}
				actual := api.objects[path]
				if releaseMap(releaseMap(actual["metadata"])["annotations"])["outside"] != "retained" {
					t.Fatal("external field erased")
				}
				if index%2 == 0 {
					if kind == "Deployment" {
						container := releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["containers"])[0])
						if _, present := container["env"]; present {
							t.Fatal("native API did not clear env")
						}
						managed := releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(source.Plan.Resources[0].Mutation.Managed["spec"])["template"])["spec"])["containers"])[0])
						if v, exists := managed["stdin"]; !exists || v != false {
							t.Fatal("empty-field ownership discarded")
						}
					} else if _, present := actual["data"]; present {
						t.Fatal("native API did not clear data")
					}
				}
			}
			rollback, e := c.PrepareRollback(ctx, "clear-rollback", "node", "clear-rb-owner", source.ID, source.Revision, source.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			rollback, e = f.s.ApproveRelease(ctx, rollback.ID, rollback.OwnerEpoch, rollback.Revision, rollback.PlanID)
			if e != nil {
				t.Fatal(e)
			}
			api.record = rollback
			registerControllerTestWriter(t, f, rollback)
			done, e := c.Run(ctx, rollback.ID, rollback.OwnerEpoch, nil)
			if e != nil || done.State != "rolled_back" || api.writes != 4 {
				t.Fatal("independent rollback failed", e)
			}
			final := api.objects[path]
			if !ReleaseObjectMatches(object, final) {
				t.Fatal("original nonempty owned fields not restored")
			}
		})
	}
}

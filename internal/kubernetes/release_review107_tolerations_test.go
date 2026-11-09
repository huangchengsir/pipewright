package kubernetes

import (
	"encoding/json"
	"testing"
)

func TestReview107ExplicitTolerationAdmission(t *testing.T) {
	var dep map[string]any
	if e := json.Unmarshal([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app","uid":"dep","generation":1},"spec":{"replicas":1,"template":{"metadata":{"labels":{"app":"app"}},"spec":{"containers":[{"name":"app","image":"nginx:1"}],"tolerations":[{"key":"dedicated","operator":"Exists","effect":"NoSchedule"}]}}},"status":{"observedGeneration":1,"updatedReplicas":1,"replicas":1,"availableReplicas":1}}`), &dep); e != nil {
		t.Fatal(e)
	}
	template := releaseMap(releaseMap(dep["spec"])["template"])
	rs := map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"name": "app-rs", "namespace": "app", "uid": "rs", "ownerReferences": []any{map[string]any{"uid": "dep", "controller": true}}}, "spec": map[string]any{"template": template}}
	encoded, _ := json.Marshal(template["spec"])
	var podspec map[string]any
	_ = json.Unmarshal(encoded, &podspec)
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "app-pod", "namespace": "app", "uid": "pod", "ownerReferences": []any{map[string]any{"uid": "rs", "controller": true}}}, "spec": podspec, "status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "app", "image": "nginx:1", "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-08T00:00:00Z"}}}}}}
	resource := ReleaseResource{ID: ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app"}, Object: dep, Expected: releaseAdmittedExpected(ReleaseResource{}, dep), Health: "workload", ExpectedUID: "dep"}
	before := EvaluateReleaseHealth(resource, dep, []map[string]any{rs, pod})
	if before.State != "ready" {
		t.Fatalf("control should be ready: %#v", before)
	}
	for _, key := range []string{"node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable"} {
		podspec["tolerations"] = append(releaseItems(podspec["tolerations"]), map[string]any{"key": key, "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": float64(300)})
	}
	after := EvaluateReleaseHealth(resource, dep, []map[string]any{rs, pod})
	if after.State != "ready" {
		t.Fatalf("approved workload + ordinary Pod defaults did not become ready: %s/%s", after.State, after.Reason)
	}
	for _, seconds := range []int64{0, 45, 901, -1} {
		fresh := releaseObjectCopy(pod)
		list := releaseItems(releaseMap(fresh["spec"])["tolerations"])
		releaseMap(list[1])["tolerationSeconds"] = seconds
		releaseMap(list[2])["tolerationSeconds"] = seconds + 7
		if h := EvaluateReleaseHealth(resource, dep, []map[string]any{rs, fresh}); h.State != "ready" {
			t.Fatal("cluster-configurable seconds rejected", h.Reason)
		}
	}
	for _, scenario := range []string{"declared-value", "declared-missing", "foreign-extra", "duplicate-default", "wrong-operator", "wrong-effect", "fractional", "no-seconds", "owner"} {
		t.Run(scenario, func(t *testing.T) {
			fresh := releaseObjectCopy(pod)
			spec := releaseMap(fresh["spec"])
			list := releaseItems(spec["tolerations"])
			switch scenario {
			case "declared-value":
				releaseMap(list[0])["tolerationSeconds"] = 60
			case "declared-missing":
				spec["tolerations"] = list[1:]
			case "foreign-extra":
				spec["tolerations"] = append(list, map[string]any{"key": "unapproved", "operator": "Exists", "effect": "NoSchedule"})
			case "duplicate-default":
				spec["tolerations"] = append(list, releaseObjectCopy(releaseMap(list[1])))
			case "wrong-operator":
				releaseMap(list[1])["operator"] = "Equal"
			case "wrong-effect":
				releaseMap(list[1])["effect"] = "NoSchedule"
			case "fractional":
				releaseMap(list[1])["tolerationSeconds"] = 0.5
			case "no-seconds":
				delete(releaseMap(list[1]), "tolerationSeconds")
			case "owner":
				releaseMap(releaseItems(releaseMap(fresh["metadata"])["ownerReferences"])[0])["uid"] = "foreign-owner"
			}
			if h := EvaluateReleaseHealth(resource, dep, []map[string]any{rs, fresh}); h.State == "ready" {
				t.Fatal("unapproved/foreign Pod evidence became ready", scenario)
			}
		})
	}

}

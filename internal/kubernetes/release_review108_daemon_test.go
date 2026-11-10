package kubernetes

import (
	"encoding/json"
	"testing"
)

func review108Daemon() (ReleaseResource, map[string]any, map[string]any, map[string]any) {
	ds := releaseTestObject(`{"apiVersion":"apps/v1","kind":"DaemonSet","metadata":{"name":"agent","namespace":"app","uid":"ds","generation":2},"spec":{"minReadySeconds":2,"template":{"metadata":{"labels":{"app":"agent"},"annotations":{"user":"retained"}},"spec":{"containers":[{"name":"app","image":"nginx:1","command":["sh","-c"],"args":["echo","ok"],"env":[{"name":"FIRST","value":"v"},{"name":"SECOND","value":"$(FIRST)"}]}],"tolerations":[{"key":"dedicated","operator":"Exists","effect":"NoSchedule"},{"key":"node.kubernetes.io/not-ready","operator":"Exists","effect":"NoExecute","tolerationSeconds":30}],"nodeSelector":{"zone":"chosen"},"affinity":{"nodeAffinity":{"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"zone","operator":"In","values":["chosen"]}]}]},"preferredDuringSchedulingIgnoredDuringExecution":[{"weight":1,"preference":{"matchExpressions":[{"key":"rack","operator":"In","values":["one"]}]}}]},"podAntiAffinity":{"preferredDuringSchedulingIgnoredDuringExecution":[]}}}}},"status":{"observedGeneration":2,"desiredNumberScheduled":1,"updatedNumberScheduled":1,"numberReady":1,"numberAvailable":1,"numberMisscheduled":0}}`)
	template := releaseMap(releaseMap(ds["spec"])["template"])
	revision := map[string]any{"apiVersion": "apps/v1", "kind": "ControllerRevision", "metadata": map[string]any{"name": "agent-abc123", "namespace": "app", "uid": "history", "labels": map[string]any{"controller-revision-hash": "abc123"}, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "agent", "uid": "ds", "controller": true}}}, "revision": int64(1), "data": map[string]any{"spec": map[string]any{"template": releaseObjectCopy(template)}}}
	releaseMap(releaseMap(revision["data"])["spec"])["template"].(map[string]any)["$patch"] = "replace"
	podSpec := releaseObjectCopy(releaseMap(template["spec"]))
	podSpec["nodeName"] = "node-one"
	tolerations := []any{map[string]any{"key": "dedicated", "operator": "Exists", "effect": "NoSchedule"}}
	for _, x := range []struct{ key, effect string }{{"not-ready", "NoExecute"}, {"unreachable", "NoExecute"}, {"disk-pressure", "NoSchedule"}, {"memory-pressure", "NoSchedule"}, {"pid-pressure", "NoSchedule"}, {"unschedulable", "NoSchedule"}} {
		tolerations = append(tolerations, map[string]any{"key": "node.kubernetes.io/" + x.key, "operator": "Exists", "effect": x.effect})
	}
	podSpec["tolerations"] = tolerations
	releaseMap(releaseMap(podSpec["affinity"])["nodeAffinity"])["requiredDuringSchedulingIgnoredDuringExecution"] = map[string]any{"nodeSelectorTerms": []any{map[string]any{"matchFields": []any{map[string]any{"key": "metadata.name", "operator": "In", "values": []any{"node-one"}}}}}}
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "agent-pod", "namespace": "app", "uid": "pod", "labels": map[string]any{"app": "agent", "controller-revision-hash": "abc123"}, "annotations": map[string]any{"user": "retained"}, "ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "agent", "uid": "ds", "controller": true}}}, "spec": podSpec, "status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "ContainersReady", "status": "True"}, map[string]any{"type": "Initialized", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "app", "image": "nginx:1", "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}}}}}
	resource := ReleaseResource{DaemonSetProofVersion: 1, ID: ReleaseResourceID{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet", Namespace: "app", Name: "agent"}, Object: releaseCanonicalObject(ds), Expected: releaseAdmittedExpected(ReleaseResource{}, ds), ExpectedUID: "ds", Health: "workload"}
	b, _ := json.Marshal(resource.Object)
	resource.Digest = releaseByteDigest(b)
	return resource, ds, revision, pod
}
func TestReview108DaemonNativeProjection(t *testing.T) {
	r, ds, revision, pod := review108Daemon()
	if h := EvaluateReleaseHealth(r, ds, []map[string]any{revision, pod}); h.State != "ready" {
		t.Fatal("current owner/revision native projection rejected", h.State, h.Reason)
	}
}
func TestReview108NativeMinimumAvailability(t *testing.T) {
	for _, kind := range []string{"DaemonSet", "StatefulSet"} {
		t.Run(kind, func(t *testing.T) {
			r, ds, _, pod := review108Daemon()
			r.DaemonSetProofVersion = 0
			spec := releaseMap(ds["spec"])
			spec["template"].(map[string]any)["spec"] = releaseObjectCopy(releaseMap(pod["spec"]))
			ds["kind"] = kind
			r.ID.Kind = kind
			r.Object = releaseCanonicalObject(ds)
			r.Expected = nil
			status := releaseMap(ds["status"])
			available := "numberAvailable"
			if kind == "StatefulSet" {
				r.ID.Resource = "statefulsets"
				spec["replicas"] = 1
				status["currentRevision"] = "one"
				status["updateRevision"] = "one"
				status["updatedReplicas"] = 1
				status["readyReplicas"] = 1
				available = "availableReplicas"
			}
			status[available] = 1
			if h := EvaluateReleaseHealth(r, ds, []map[string]any{pod}); h.State != "ready" {
				t.Fatal("available control", h.Reason)
			}
			status[available] = 0
			if h := EvaluateReleaseHealth(r, ds, []map[string]any{pod}); h.State == "ready" {
				t.Fatal("native minReadySeconds bypassed")
			}
			spec["minReadySeconds"] = 0
			r.Object = releaseCanonicalObject(ds)
			if h := EvaluateReleaseHealth(r, ds, []map[string]any{pod}); h.State != "ready" {
				t.Fatal("zero minReadySeconds compatibility", h.Reason)
			}
		})
	}
}

func TestReview108DaemonProofAndProjectionNeighbors(t *testing.T) {
	for _, change := range []string{"missing-history", "foreign-history", "history-namespace", "missing-hash", "ambiguous-history", "bad-directive", "extra-history-data", "old-ready-new-unready", "only-affinity-new-revision", "only-toleration-new-revision", "pod-owner", "pod-owner-kind", "pod-hash", "pod-namespace", "wrong-node", "extra-native-affinity", "finite-native-toleration", "missing-custom", "foreign-toleration", "node-selector", "preferred-affinity", "foreign-affinity", "annotation", "command-order", "args-order", "env-order", "init-order", "envFrom", "projected-sources", "generation", "available", "missing-available"} {
		t.Run(change, func(t *testing.T) {
			r, ds, revision, pod := review108Daemon()
			related := []map[string]any{revision, pod}
			ps := releaseMap(pod["spec"])
			rm := releaseMap(revision["metadata"])
			pm := releaseMap(pod["metadata"])
			switch change {
			case "missing-history":
				related = related[1:]
			case "foreign-history":
				releaseMap(releaseItems(rm["ownerReferences"])[0])["uid"] = "foreign"
			case "history-namespace":
				rm["namespace"] = "other"
			case "missing-hash":
				delete(releaseMap(rm["labels"]), releaseDaemonHashLabel)
			case "ambiguous-history":
				other := releaseObjectCopy(revision)
				releaseMap(other["metadata"])["uid"] = "other-history"
				related = append(related, other)
			case "bad-directive":
				releaseMap(releaseMap(releaseMap(revision["data"])["spec"])["template"])["$patch"] = "merge"
			case "extra-history-data":
				releaseMap(revision["data"])["unapproved"] = true
			case "old-ready-new-unready":
				old := releaseObjectCopy(pod)
				releaseMap(releaseMap(old["metadata"])["labels"])[releaseDaemonHashLabel] = "oldhash"
				releaseMap(releaseItems(releaseMap(pod["status"])["conditions"])[0])["status"] = "False"
				related = append(related, old)
			case "only-affinity-new-revision", "only-toleration-new-revision":
				old := releaseMap(releaseMap(releaseMap(revision["data"])["spec"])["template"])
				if change == "only-affinity-new-revision" {
					releaseMap(old["spec"])["affinity"] = nil
				} else {
					releaseMap(old["spec"])["tolerations"] = nil
				}
			case "pod-owner":
				releaseMap(releaseItems(pm["ownerReferences"])[0])["uid"] = "foreign"
			case "pod-owner-kind":
				releaseMap(releaseItems(pm["ownerReferences"])[0])["kind"] = "Deployment"
			case "pod-hash":
				releaseMap(pm["labels"])[releaseDaemonHashLabel] = "oldhash"
			case "pod-namespace":
				pm["namespace"] = "other"
			case "wrong-node":
				ps["nodeName"] = "node-other"
			case "extra-native-affinity":
				req := releaseMap(releaseMap(releaseMap(ps["affinity"])["nodeAffinity"])["requiredDuringSchedulingIgnoredDuringExecution"])
				req["extra"] = true
			case "finite-native-toleration":
				releaseMap(releaseItems(ps["tolerations"])[1])["tolerationSeconds"] = 30
			case "missing-custom":
				ps["tolerations"] = releaseItems(ps["tolerations"])[1:]
			case "foreign-toleration":
				ps["tolerations"] = append(releaseItems(ps["tolerations"]), map[string]any{"key": "foreign", "operator": "Exists"})
			case "node-selector":
				releaseMap(ps["nodeSelector"])["zone"] = "other"
			case "preferred-affinity":
				releaseMap(releaseMap(ps["affinity"])["nodeAffinity"])["preferredDuringSchedulingIgnoredDuringExecution"] = []any{}
			case "foreign-affinity":
				releaseMap(ps["affinity"])["podAffinity"] = map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{"topologyKey": "foreign"}}}
			case "annotation":
				releaseMap(pm["annotations"])["user"] = "drift"
			case "command-order", "args-order", "env-order":
				container := releaseMap(releaseItems(ps["containers"])[0])
				key := map[string]string{"command-order": "command", "args-order": "args", "env-order": "env"}[change]
				a := releaseItems(container[key])
				a[0], a[1] = a[1], a[0]
			case "init-order", "envFrom", "projected-sources":
				// Add exact approved atomic/order-sensitive neighbors to both the current
				// template and its native history, but reverse only the actual Pod.
				expectedSpec := releaseMap(releaseMap(releaseMap(ds["spec"])["template"])["spec"])
				switch change {
				case "init-order":
					expectedSpec["initContainers"] = []any{map[string]any{"name": "first", "image": "nginx:1"}, map[string]any{"name": "second", "image": "nginx:1"}}
					ps["initContainers"] = []any{map[string]any{"name": "second", "image": "nginx:1"}, map[string]any{"name": "first", "image": "nginx:1"}}
				case "envFrom":
					releaseMap(releaseItems(expectedSpec["containers"])[0])["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "one"}}, map[string]any{"configMapRef": map[string]any{"name": "two"}}}
					releaseMap(releaseItems(ps["containers"])[0])["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "two"}}, map[string]any{"configMapRef": map[string]any{"name": "one"}}}
				case "projected-sources":
					expectedSpec["volumes"] = []any{map[string]any{"name": "projected", "projected": map[string]any{"sources": []any{map[string]any{"configMap": map[string]any{"name": "one"}}, map[string]any{"secret": map[string]any{"name": "two"}}}}}}
					ps["volumes"] = []any{map[string]any{"name": "projected", "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": "two"}}, map[string]any{"configMap": map[string]any{"name": "one"}}}}}}
				}
				template := releaseObjectCopy(releaseMap(releaseMap(ds["spec"])["template"]))
				template["$patch"] = "replace"
				releaseMap(releaseMap(revision["data"])["spec"])["template"] = template
				r.Object = releaseCanonicalObject(ds)
				r.Expected = nil
			case "generation":
				releaseMap(ds["status"])["observedGeneration"] = 1
			case "available":
				releaseMap(ds["status"])["numberAvailable"] = 0
			case "missing-available":
				delete(releaseMap(ds["status"]), "numberAvailable")
			}
			if h := EvaluateReleaseHealth(r, ds, related); h.State == "ready" {
				t.Fatal("unproved/mutated current daemon Pod accepted", change)
			}
		})
	}
}
func TestReview108DaemonOlderEqualHistoryAndNativeHostNetwork(t *testing.T) {
	r, ds, revision, pod := review108Daemon()
	old := releaseObjectCopy(revision)
	releaseMap(old["metadata"])["name"] = "agent-old"
	releaseMap(old["metadata"])["uid"] = "other"
	releaseMap(old["metadata"])["labels"] = map[string]any{releaseDaemonHashLabel: "old"}
	old["revision"] = 99
	releaseMap(releaseMap(releaseMap(releaseMap(old["data"])["spec"])["template"])["spec"])["hostNetwork"] = true
	// The lower numeric matching history remains current; no hash recreation or
	// guessing a previous collision seed. HostNetwork's extra native addition.
	template := releaseMap(releaseMap(ds["spec"])["template"])
	releaseMap(template["metadata"])["creationTimestamp"] = nil
	releaseMap(template["spec"])["hostNetwork"] = true
	releaseMap(pod["spec"])["hostNetwork"] = true
	releaseMap(pod["spec"])["tolerations"] = append(releaseItems(releaseMap(pod["spec"])["tolerations"]), map[string]any{"key": "node.kubernetes.io/network-unavailable", "operator": "Exists", "effect": "NoSchedule"})
	updated := releaseObjectCopy(template)
	updated["$patch"] = "replace"
	releaseMap(releaseMap(revision["data"])["spec"])["template"] = updated
	r.Object = releaseCanonicalObject(ds)
	r.Expected = nil
	if h := EvaluateReleaseHealth(r, ds, []map[string]any{old, revision, pod}); h.State != "ready" {
		t.Fatal("current equal history/native hostnetwork rejected", h.Reason)
	}
}

func TestReview108DaemonNativeProjectionKeepsAdmissionAndContainerGates(t *testing.T) {
	r, ds, revision, pod := review108Daemon()
	ps := releaseMap(pod["spec"])
	container := releaseMap(releaseItems(ps["containers"])[0])
	container["env"] = append(releaseItems(container["env"]), map[string]any{"name": "ADMISSION", "value": "injected"})
	ps["volumes"] = []any{map[string]any{"name": "admission", "emptyDir": map[string]any{}}}
	ps["containers"] = append(releaseItems(ps["containers"]), map[string]any{"name": "sidecar", "image": "nginx:1"})
	status := releaseMap(pod["status"])
	sidecar := map[string]any{"name": "sidecar", "image": "nginx:1", "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}}
	status["containerStatuses"] = append(releaseItems(status["containerStatuses"]), sidecar)
	if h := EvaluateReleaseHealth(r, ds, []map[string]any{revision, pod}); h.State != "ready" {
		t.Fatal("distinct admission fields rejected", h.Reason)
	}
	sidecar["ready"] = false
	if h := EvaluateReleaseHealth(r, ds, []map[string]any{revision, pod}); h.State == "ready" {
		t.Fatal("unhealthy actual sidecar accepted")
	}
}
func TestReview108DaemonTolerationNativeDuplicateAndCustomSemantics(t *testing.T) {
	// Native helper returns unchanged if any exactly equal target is found;
	// it must not accidentally replace an earlier finite duplicate in that case.
	finite := map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 30}
	infinite := map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute"}
	custom := map[string]any{"key": "custom", "operator": "Equal", "value": "retained", "effect": "NoSchedule"}
	spec := map[string]any{"tolerations": []any{finite, infinite, custom}}
	if !releaseDaemonTolerations(spec) {
		t.Fatal("native inputs rejected")
	}
	values := releaseItems(spec["tolerations"])
	if !releaseExactJSON(values[0], finite) || !releaseExactJSON(values[1], infinite) || !releaseExactJSON(values[2], custom) {
		t.Fatal("native duplicate/custom semantics altered")
	}
}

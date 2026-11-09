package kubernetes

import "testing"

func review110Cohort(kind string) (ReleaseResource, map[string]any, []map[string]any, map[string]any) {
	r, ds, revision, pod := review108Daemon()
	related := []map[string]any{revision}
	if kind != "DaemonSet" {
		r.DaemonSetProofVersion = 0
		r.Expected = nil
		ds["kind"] = kind
		r.ID.Kind = kind
		spec := releaseMap(ds["spec"])
		spec["template"].(map[string]any)["spec"] = releaseObjectCopy(releaseMap(pod["spec"]))
		spec["replicas"] = 1
		status := releaseMap(ds["status"])
		status["updatedReplicas"] = 1
		status["readyReplicas"] = 1
		status["availableReplicas"] = 1
		status["replicas"] = 1
		status["currentRevision"] = "one"
		status["updateRevision"] = "one"
		if kind == "Deployment" {
			r.ID.Resource = "deployments"
			rs := map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"name": "current-rs", "namespace": "app", "uid": "rs", "ownerReferences": []any{map[string]any{"controller": true, "uid": "ds"}}}, "spec": map[string]any{"template": releaseMap(spec["template"])}}
			related = []map[string]any{rs}
			releaseMap(releaseItems(releaseMap(pod["metadata"])["ownerReferences"])[0])["uid"] = "rs"
		} else {
			r.ID.Resource = "statefulsets"
			related = nil
		}
		r.Object = releaseCanonicalObject(ds)
	}
	return r, ds, related, pod
}
func TestReview110ControllerActiveCohort(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
		t.Run(kind, func(t *testing.T) {
			for _, scenario := range []string{"Failed", "Succeeded", "active-unready", "Unknown", "missing-status", "terminating-extra", "terminating-only", "terminal-only", "foreign-owner-extra", "only-foreign", "old-revision-extra", "only-old", "unready-plus-old"} {
				t.Run(scenario, func(t *testing.T) {
					r, actual, parents, ready := review110Cohort(kind)
					if h := EvaluateReleaseHealth(r, actual, append(parents, ready)); h.State != "ready" {
						t.Fatal("invalid control", h.Reason)
					}
					neighbor := releaseObjectCopy(ready)
					releaseMap(neighbor["metadata"])["uid"] = "neighbor"
					releaseMap(neighbor["metadata"])["name"] = "neighbor"
					wantReady := false
					only := false
					switch scenario {
					case "Failed", "Succeeded":
						neighbor["status"] = map[string]any{"phase": scenario}
						wantReady = true
					case "active-unready":
						releaseMap(releaseItems(releaseMap(neighbor["status"])["conditions"])[0])["status"] = "False"
					case "Unknown":
						releaseMap(neighbor["status"])["phase"] = "Unknown"
					case "missing-status":
						delete(neighbor, "status")
					case "terminating-extra", "terminating-only":
						releaseMap(neighbor["metadata"])["deletionTimestamp"] = "2026-11-09T00:00:00Z"
						wantReady = scenario == "terminating-extra"
						only = !wantReady
					case "terminal-only":
						neighbor["status"] = map[string]any{"phase": "Failed"}
						only = true
					case "foreign-owner-extra", "only-foreign":
						releaseMap(releaseItems(releaseMap(neighbor["metadata"])["ownerReferences"])[0])["uid"] = "foreign"
						wantReady = scenario == "foreign-owner-extra"
						only = !wantReady
					case "old-revision-extra", "only-old", "unready-plus-old":
						if kind == "DaemonSet" {
							releaseMap(releaseMap(neighbor["metadata"])["labels"])[releaseDaemonHashLabel] = "old"
						} else {
							releaseMap(releaseItems(releaseMap(neighbor["spec"])["containers"])[0])["image"] = "old:1"
						}
						wantReady = scenario == "old-revision-extra"
						only = scenario == "only-old"
						if scenario == "unready-plus-old" {
							releaseMap(releaseItems(releaseMap(ready["status"])["conditions"])[0])["status"] = "False"
						}
					}
					related := append(append([]map[string]any{}, parents...), neighbor)
					if !only {
						related = append(related, ready)
					}
					if scenario == "Failed" || scenario == "Succeeded" {
						pods, e := FilterReleaseRelatedObjects(ReleaseAccess{ID: r.ID, Actions: []string{"related-pods"}}, actual, "Pod", related)
						if e != nil || len(pods) != 2 {
							t.Fatal("historical related records were hidden", e)
						}
					}
					h := EvaluateReleaseHealth(r, actual, related)
					if (h.State == "ready") != wantReady {
						t.Fatal("wrong active cohort", scenario, h.State, h.Reason)
					}
				})
			}
		})
	}
}
func TestReview110StandaloneTerminalPodRemainsUnhealthy(t *testing.T) {
	for _, phase := range []string{"Failed", "Succeeded"} {
		_, _, _, pod := review108Daemon()
		releaseMap(pod["spec"])["restartPolicy"] = "Always"
		releaseMap(pod["status"])["phase"] = phase
		m := releaseMap(pod["metadata"])
		r := releaseResourceFromObject(ReleaseResourceID{Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "app", Name: releaseString(m["name"])}, releaseCanonicalObject(pod), "workload")
		if h := EvaluateReleaseHealth(r, pod, nil); h.State == "ready" {
			t.Fatal("standalone terminal failure hidden", phase)
		}
	}
}
func TestReview110ExplicitZeroJob(t *testing.T) {
	obj := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"zero","namespace":"app","uid":"job","generation":1},"spec":{"completions":0,"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"app","image":"nginx:1"}]}}},"status":{"conditions":[{"type":"Complete","status":"True"}]}}`)
	r := releaseResourceFromObject(ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "zero"}, releaseCanonicalObject(obj), "workload")
	if h := EvaluateReleaseHealth(r, obj, nil); h.State != "ready" || h.Desired != 0 || len(h.Containers) != 0 {
		t.Fatal("explicit zero Job fabricated Pod requirement", h.State, h.Reason, h.Desired)
	}
}

func TestReview110ZeroCompletionJobNeighbors(t *testing.T) {
	for _, scenario := range []string{"nil", "one", "negative", "fractional", "string-zero", "missing-complete", "false-complete", "failed", "suspended", "suspend-spec", "active", "terminating", "owned-active", "owned-missing-status", "foreign-active", "predicate", "uid"} {
		t.Run(scenario, func(t *testing.T) {
			obj := releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"zero","namespace":"app","uid":"job","generation":1},"spec":{"completions":0,"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"app","image":"nginx:1"}]}}},"status":{"conditions":[{"type":"Complete","status":"True"}]}}`)
			spec, status := releaseMap(obj["spec"]), releaseMap(obj["status"])
			related := []map[string]any{}
			switch scenario {
			case "nil":
				delete(spec, "completions")
			case "one":
				spec["completions"] = 1
			case "negative":
				spec["completions"] = -1
			case "fractional":
				spec["completions"] = 0.5
			case "string-zero":
				spec["completions"] = "0"
			case "missing-complete":
				delete(status, "conditions")
			case "false-complete":
				releaseMap(releaseItems(status["conditions"])[0])["status"] = "False"
			case "failed":
				status["conditions"] = append(releaseItems(status["conditions"]), map[string]any{"type": "Failed", "status": "True"})
			case "suspended":
				status["conditions"] = append(releaseItems(status["conditions"]), map[string]any{"type": "Suspended", "status": "True"})
			case "suspend-spec":
				spec["suspend"] = true
			case "active":
				status["active"] = 1
			case "terminating":
				status["terminating"] = 1
			case "owned-active", "owned-missing-status", "foreign-active":
				owner := "job"
				if scenario == "foreign-active" {
					owner = "foreign"
				}
				pod := map[string]any{"kind": "Pod", "metadata": map[string]any{"namespace": "app", "ownerReferences": []any{map[string]any{"controller": true, "uid": owner}}}, "status": map[string]any{"phase": "Running"}}
				if scenario == "owned-missing-status" {
					delete(pod, "status")
				}
				related = append(related, pod)
			}
			r := releaseResourceFromObject(ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "zero"}, releaseCanonicalObject(obj), "workload")
			if scenario == "predicate" {
				r.Predicates = []ReleasePredicate{{Predicate: "condition:Ready", Expected: "True"}}
			}
			if scenario == "uid" {
				r.ExpectedUID = "other"
			}
			h := EvaluateReleaseHealth(r, obj, related)
			if (h.State == "ready") != (scenario == "foreign-active") {
				t.Fatal("zero completion bypassed native/explicit evidence", scenario, h.State, h.Reason)
			}
		})
	}
}

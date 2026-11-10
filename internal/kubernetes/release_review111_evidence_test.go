package kubernetes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type review111SnapshotAPI struct {
	*releaseControllerAPI
	related []map[string]any
}

func (a *review111SnapshotAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	version, kind := "", ""
	switch {
	case strings.HasSuffix(path, "/pods"):
		version, kind = "v1", "Pod"
	case strings.HasSuffix(path, "/replicasets"):
		version, kind = "apps/v1", "ReplicaSet"
	case strings.HasSuffix(path, "/controllerrevisions"):
		version, kind = "apps/v1", "ControllerRevision"
	}
	if kind == "" {
		return a.releaseControllerAPI.Read(ctx, path, query)
	}
	items := []any{}
	for _, o := range a.related {
		if o["kind"] == kind {
			items = append(items, o)
		}
	}
	return json.Marshal(map[string]any{"apiVersion": version, "kind": kind + "List", "items": items})
}
func review111Baseline(kind string) (ReleaseResource, map[string]any, []map[string]any, map[string]any) {
	r, actual, parents, pod := review110Cohort(kind)
	image := "registry.invalid/app:old-tag"
	pin := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["containers"])[0])["image"] = image
	releaseMap(releaseItems(releaseMap(pod["spec"])["containers"])[0])["image"] = image
	status := releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])
	status["image"] = "sha256:" + strings.Repeat("c", 64)
	status["imageID"] = pin
	review111SyncParents(actual, parents)
	r = releaseResourceFromObject(r.ID, releaseObjectCopy(actual), "workload")
	r.ExpectedUID = releaseString(releaseMap(actual["metadata"])["uid"])
	if kind == "DaemonSet" {
		r.DaemonSetProofVersion = releaseDaemonProofVersion
	}
	return r, actual, parents, pod
}
func review111SyncParents(actual map[string]any, parents []map[string]any) {
	for _, o := range parents {
		if o["kind"] == "ReplicaSet" {
			releaseMap(o["spec"])["template"] = releaseObjectCopy(releaseMap(releaseMap(actual["spec"])["template"]))
		}
		if o["kind"] == "ControllerRevision" {
			template := releaseObjectCopy(releaseMap(releaseMap(actual["spec"])["template"]))
			template["$patch"] = "replace"
			releaseMap(releaseMap(o["data"])["spec"])["template"] = template
		}
	}
}
func review111Capture(t *testing.T, r ReleaseResource, actual map[string]any, related []map[string]any) ReleaseResource {
	t.Helper()
	path, _ := resourceAPI(r.ID).Path(r.ID.Namespace, r.ID.Name)
	api := &review111SnapshotAPI{releaseControllerAPI: &releaseControllerAPI{objects: map[string]map[string]any{path: actual}}, related: related}
	before, _ := json.Marshal([]any{r.Object, actual, related})
	NewReleaseController(nil, nil).captureRollbackImages(context.Background(), api, &r)
	after, _ := json.Marshal([]any{r.Object, actual, related})
	if string(before) != string(after) || api.writes != 0 {
		t.Fatal("baseline comparison changed native objects or dispatched a write")
	}
	return r
}
func TestReview111BaselinePullableEvidence(t *testing.T) {
	pin := "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
	config := "sha256:" + strings.Repeat("c", 64)
	for _, tc := range []struct {
		name, display, id string
		ok                bool
	}{
		{"native", config, pin, true}, {"normal-display", "registry.invalid/app:old-tag", pin, true},
		{"docker-pullable", config, "docker-pullable://" + pin, true}, {"containerd-pullable", config, "containerd://" + pin, true}, {"docker-pullable-id", config, "docker://" + pin, true}, {"crio-pullable", config, "cri-o://" + pin, true},
		{"raw-opaque-id", config, "sha256:" + strings.Repeat("a", 64), false}, {"containerd-opaque-id", config, "containerd://sha256:" + strings.Repeat("a", 64), false},
		{"foreign-repository", config, "registry.invalid/other@sha256:" + strings.Repeat("a", 64), false}, {"normal-display-foreign-id", "registry.invalid/app:old-tag", "registry.invalid/other@sha256:" + strings.Repeat("a", 64), false},
		{"unknown-uri", config, "unknown://" + pin, false}, {"double-uri", config, "docker://containerd://" + pin, false}, {"id-suffix", config, pin + "?x=1", false}, {"id-fragment", config, pin + "#x", false}, {"id-space", config, pin + " ", false}, {"id-uppercase", config, "registry.invalid/app@sha256:" + strings.Repeat("A", 64), false}, {"short-id", config, "registry.invalid/app@sha256:abc", false},
		{"explicit-other-tag", "registry.invalid/app:moved", pin, false}, {"explicit-other-repo", "registry.invalid/other:old-tag", pin, false}, {"explicit-wrong-digest", "registry.invalid/app@sha256:" + strings.Repeat("b", 64), pin, false},
		{"config-uppercase", "sha256:" + strings.Repeat("C", 64), pin, false}, {"config-prefix", "containerd://" + config, pin, false}, {"config-suffix", config + "tail", pin, false}, {"config-short", "sha256:abc", pin, false}, {"empty-display", "", pin, false}, {"missing-id", config, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, actual, parents, pod := review111Baseline("Deployment")
			status := releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])
			status["image"] = tc.display
			status["imageID"] = tc.id
			r = review111Capture(t, r, actual, append(parents, pod))
			if (len(r.RollbackImages) == 1) != tc.ok {
				t.Fatal("unproved baseline image accepted or native evidence rejected")
			}
			if tc.ok && r.RollbackImages["containers/app"] != pin {
				t.Fatal("config SHA substituted for manifest identity")
			}
		})
	}
	for _, repo := range []string{"nginx:stable", "docker.io/library/nginx:stable", "index.docker.io/library/nginx:stable", "registry.invalid:5443/app:stable"} {
		t.Run("canonical-"+repo, func(t *testing.T) {
			r, actual, parents, pod := review111Baseline("Deployment")
			id := "docker.io/library/nginx@sha256:" + strings.Repeat("a", 64)
			if strings.HasPrefix(repo, "registry.") {
				id = "registry.invalid:5443/app@sha256:" + strings.Repeat("a", 64)
			}
			releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])["containers"])[0])["image"] = repo
			releaseMap(releaseItems(releaseMap(pod["spec"])["containers"])[0])["image"] = repo
			releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["imageID"] = id
			review111SyncParents(actual, parents)
			r.Object = releaseObjectCopy(actual)
			r = review111Capture(t, r, actual, append(parents, pod))
			if r.RollbackImages["containers/app"] != id {
				t.Fatal("safe canonical repository rejected")
			}
		})
	}
}
func TestReview111CaptureUsesOnlyHealthyCurrentCohort(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
		t.Run(kind, func(t *testing.T) {
			scenarios := []string{"control", "Failed-extra", "Succeeded-extra", "terminating-extra", "foreign-extra", "namespace-extra", "old-extra", "Failed-only", "Succeeded-only", "terminating-only", "foreign-only", "old-only", "unready", "missing-status", "pod-uid", "root-uid", "generation", "available", "different-pin", "same-pin", "current-unready-old-ready", "drift-spec", "drift-command"}
			if kind == "DaemonSet" {
				scenarios = append(scenarios, "missing-history", "foreign-history", "ambiguous-history")
			}
			for _, scenario := range scenarios {
				t.Run(scenario, func(t *testing.T) {
					r, actual, parents, pod := review111Baseline(kind)
					neighbor := releaseObjectCopy(pod)
					pm := releaseMap(neighbor["metadata"])
					pm["uid"] = "neighbor"
					pm["name"] = "neighbor"
					releaseMap(releaseItems(releaseMap(neighbor["status"])["containerStatuses"])[0])["imageID"] = "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
					related := append(append([]map[string]any{}, parents...), pod)
					ok := false
					switch scenario {
					case "control":
						ok = true
					case "Failed-extra", "Succeeded-extra", "Failed-only", "Succeeded-only":
						releaseMap(neighbor["status"])["phase"] = strings.Split(scenario, "-")[0]
						ok = strings.HasSuffix(scenario, "extra")
					case "terminating-extra", "terminating-only":
						pm["deletionTimestamp"] = "2026-11-09T00:00:00Z"
						ok = strings.HasSuffix(scenario, "extra")
					case "foreign-extra", "foreign-only":
						releaseMap(releaseItems(pm["ownerReferences"])[0])["uid"] = "foreign"
						ok = strings.HasSuffix(scenario, "extra")
					case "namespace-extra":
						pm["namespace"] = "other"
						ok = true
					case "old-extra", "old-only", "current-unready-old-ready":
						if kind == "DaemonSet" {
							releaseMap(pm["labels"])[releaseDaemonHashLabel] = "old"
						} else {
							releaseMap(releaseItems(releaseMap(neighbor["spec"])["containers"])[0])["image"] = "registry.invalid/app:previous"
						}
						ok = scenario == "old-extra"
						if scenario == "current-unready-old-ready" {
							releaseMap(releaseItems(releaseMap(pod["status"])["conditions"])[0])["status"] = "False"
						}
					case "unready":
						releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["ready"] = false
					case "missing-status":
						delete(pod, "status")
					case "pod-uid":
						delete(releaseMap(pod["metadata"]), "uid")
					case "root-uid":
						r.ExpectedUID = "different-incarnation"
					case "generation":
						releaseMap(actual["status"])["observedGeneration"] = 0
						r.Object = releaseObjectCopy(actual)
					case "available":
						if kind == "DaemonSet" {
							releaseMap(actual["status"])["numberAvailable"] = 0
						} else {
							releaseMap(actual["status"])["availableReplicas"] = 0
						}
						r.Object = releaseObjectCopy(actual)
					case "different-pin", "same-pin":
						if kind == "DaemonSet" {
							for _, k := range []string{"desiredNumberScheduled", "updatedNumberScheduled", "numberReady", "numberAvailable"} {
								releaseMap(actual["status"])[k] = 2
							}
						} else {
							releaseMap(actual["spec"])["replicas"] = 2
							for _, k := range []string{"replicas", "updatedReplicas", "readyReplicas", "availableReplicas"} {
								releaseMap(actual["status"])[k] = 2
							}
						}
						r.Object = releaseObjectCopy(actual)
						if scenario == "same-pin" {
							releaseMap(releaseItems(releaseMap(neighbor["status"])["containerStatuses"])[0])["imageID"] = "registry.invalid/app@sha256:" + strings.Repeat("a", 64)
							ok = true
						}
					case "drift-spec":
						releaseMap(releaseItems(releaseMap(pod["spec"])["containers"])[0])["image"] = "registry.invalid/app:other"
					case "drift-command":
						releaseMap(releaseItems(releaseMap(pod["spec"])["containers"])[0])["command"] = []any{"not-approved"}
					case "missing-history", "foreign-history", "ambiguous-history":
						switch scenario {
						case "missing-history":
							related = related[1:]
						case "foreign-history":
							releaseMap(releaseItems(releaseMap(parents[0]["metadata"])["ownerReferences"])[0])["uid"] = "other"
						case "ambiguous-history":
							copy := releaseObjectCopy(parents[0])
							releaseMap(copy["metadata"])["uid"] = "second-history"
							related = append(related, copy)
						}
					}
					if strings.HasSuffix(scenario, "-extra") || scenario == "different-pin" || scenario == "same-pin" || scenario == "current-unready-old-ready" {
						related = append(related, neighbor)
					}
					if strings.HasSuffix(scenario, "-only") {
						related = append(append([]map[string]any{}, parents...), neighbor)
					}
					r = review111Capture(t, r, actual, related)
					if (len(r.RollbackImages) > 0) != ok {
						t.Fatal("capture borrowed excluded or unhealthy Pod evidence", scenario)
					}
					if ok && r.RollbackImages["containers/app"] != "registry.invalid/app@sha256:"+strings.Repeat("a", 64) {
						t.Fatal("historical/foreign identity polluted current pin")
					}
				})
			}
		})
	}
}
func TestReview111CaptureKeepsAllActualInitAndSidecarHealth(t *testing.T) {
	for _, scenario := range []string{"healthy", "init-exit", "init-missing", "init-name", "sidecar-unready", "sidecar-id", "fixed-init-wrong-digest"} {
		t.Run(scenario, func(t *testing.T) {
			r, actual, parents, pod := review111Baseline("Deployment")
			ps := releaseMap(pod["spec"])
			ts := releaseMap(releaseMap(releaseMap(actual["spec"])["template"])["spec"])
			init := map[string]any{"name": "init", "image": "registry.invalid/init:stable"}
			ts["initContainers"] = []any{init}
			ps["initContainers"] = []any{releaseObjectCopy(init)}
			initStatus := map[string]any{"name": "init", "image": "sha256:" + strings.Repeat("c", 64), "imageID": "registry.invalid/init@sha256:" + strings.Repeat("a", 64), "ready": false, "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}}
			status := releaseMap(pod["status"])
			status["initContainerStatuses"] = []any{initStatus}
			ps["containers"] = append(releaseItems(ps["containers"]), map[string]any{"name": "admitted-sidecar", "image": "registry.invalid/sidecar:stable"})
			sidecar := map[string]any{"name": "admitted-sidecar", "image": "sha256:" + strings.Repeat("c", 64), "imageID": "registry.invalid/sidecar@sha256:" + strings.Repeat("a", 64), "ready": true, "started": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-09T00:00:00Z"}}}
			status["containerStatuses"] = append(releaseItems(status["containerStatuses"]), sidecar)
			switch scenario {
			case "init-exit":
				releaseMap(releaseMap(initStatus["state"])["terminated"])["exitCode"] = 1
			case "init-missing":
				status["initContainerStatuses"] = []any{}
			case "init-name":
				initStatus["name"] = "foreign-init"
			case "sidecar-unready":
				sidecar["ready"] = false
			case "sidecar-id":
				sidecar["imageID"] = "registry.invalid/foreign@sha256:" + strings.Repeat("a", 64)
			case "fixed-init-wrong-digest":
				init["image"] = "registry.invalid/init@sha256:" + strings.Repeat("b", 64)
				releaseMap(releaseItems(ps["initContainers"])[0])["image"] = init["image"]
			}
			review111SyncParents(actual, parents)
			r.Object = releaseObjectCopy(actual)
			r = review111Capture(t, r, actual, append(parents, pod))
			if scenario == "healthy" {
				if len(r.RollbackImages) != 2 || r.RollbackImages["initContainers/init"] != initStatus["imageID"] {
					t.Fatal("healthy init/sidecar native proof failed")
				}
			} else if len(r.RollbackImages) != 0 {
				t.Fatal("image identity bypassed actual init/sidecar health")
			}
		})
	}
}

func TestReview111HistoricalPodsCannotPoisonMatchingDisplayPins(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
		t.Run(kind, func(t *testing.T) {
			for _, phase := range []string{"Failed", "Succeeded"} {
				t.Run(phase, func(t *testing.T) {
					r, actual, parents, pod := review111Baseline(kind)
					status := releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])
					status["image"] = "registry.invalid/app:old-tag"
					historical := releaseObjectCopy(pod)
					releaseMap(historical["metadata"])["uid"] = "historical"
					releaseMap(historical["metadata"])["name"] = "historical"
					releaseMap(historical["status"])["phase"] = phase
					releaseMap(releaseItems(releaseMap(historical["status"])["containerStatuses"])[0])["imageID"] = "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
					related := append(parents, historical, pod)
					if h := EvaluateReleaseHealth(ReleaseResource{ID: r.ID, Object: releaseCanonicalObject(actual), Health: "workload", DaemonSetProofVersion: r.DaemonSetProofVersion}, actual, related); h.State != "ready" {
						t.Fatal("ordinary healthy replacement control", h.Reason)
					}
					r = review111Capture(t, r, actual, related)
					if r.RollbackImages["containers/app"] != "registry.invalid/app@sha256:"+strings.Repeat("a", 64) {
						t.Fatal("historical terminal Pod poisoned the healthy cohort pin")
					}
				})
			}
		})
	}
}

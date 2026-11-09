package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
	"time"
)

func containerdReadyPod(specImage, display, imageID string) map[string]any {
	return releaseTestObject(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"pod","namespace":"app","uid":"pod-uid"},"spec":{"containers":[{"name":"app","image":"` + specImage + `"}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"},{"type":"ContainersReady","status":"True"},{"type":"Initialized","status":"True"}],"containerStatuses":[{"name":"app","image":"` + display + `","imageID":"` + imageID + `","ready":true,"started":true,"state":{"running":{"startedAt":"2026-11-08T00:00:00Z"}}}]}}`)
}
func TestReleaseContainerdPinnedImageIDMatchesRealDigestShapeOnly(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "registry.invalid/fixture@" + digest
	config := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, want, display, id string
		ready                   bool
	}{
		{"real_containerd_repository_id", image, config, image, true},
		{"real_containerd_content_id", image, config, digest, true},
		{"containerd_uri", image, config, "containerd://" + digest, true},
		{"docker_pullable_uri", image, config, "docker-pullable://" + image, true},
		{"wrong_digest", image, config, "registry.invalid/fixture@sha256:" + strings.Repeat("c", 64), false},
		{"wrong_repository", image, config, "registry.invalid/other@" + digest, false},
		{"suffix", image, config, image + "?private=tail", false},
		{"fragment", image, config, image + "#tail", false},
		{"extra_digest", image, config, image + "@" + digest, false},
		{"space", image, config, image + " ", false},
		{"unknown_runtime", image, config, "untrusted://" + digest, false},
		{"uppercase_hash", image, config, "sha256:" + strings.Repeat("A", 64), false},
		{"tag_cannot_use_digest_fallback", "registry.invalid/fixture:stable", config, image, false},
		{"tag_display_still_matches", "registry.invalid/fixture:stable", "registry.invalid/fixture:stable", "untrusted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := containerdReadyPod(tc.want, tc.display, tc.id)
			if h := releasePodHealth(pod, false); (h.State == "ready") != tc.ready {
				t.Fatalf("ready=%v, health=%s/%s", tc.ready, h.State, h.Reason)
			}
		})
	}
}
func TestReleaseContainerdDigestDoesNotBypassOwnerSpecContainerSidecarOrInit(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "registry.invalid/fixture@" + digest
	base := containerdReadyPod(image, "sha256:"+strings.Repeat("b", 64), image)
	spec := releaseMap(base["spec"])
	status := releaseMap(base["status"])
	spec["initContainers"] = []any{map[string]any{"name": "init", "image": image}}
	status["initContainerStatuses"] = []any{map[string]any{"name": "init", "image": "sha256:" + strings.Repeat("b", 64), "imageID": "containerd://" + digest, "ready": true, "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}}}
	spec["containers"] = append(releaseItems(spec["containers"]), map[string]any{"name": "sidecar", "image": "registry.invalid/sidecar:stable"})
	status["containerStatuses"] = append(releaseItems(status["containerStatuses"]), map[string]any{"name": "sidecar", "image": "registry.invalid/sidecar:stable", "ready": true, "state": map[string]any{"running": map[string]any{"startedAt": "2026-11-08T00:00:00Z"}}})
	if h := releasePodHealth(base, false); h.State != "ready" {
		t.Fatal("valid actual init/sidecar shape rejected", h.Reason)
	}
	for _, change := range []func(map[string]any){
		func(p map[string]any) {
			releaseMap(releaseItems(releaseMap(p["status"])["containerStatuses"])[0])["name"] = "other"
		},
		func(p map[string]any) {
			releaseMap(releaseItems(releaseMap(p["status"])["containerStatuses"])[1])["ready"] = false
		},
		func(p map[string]any) {
			releaseMap(releaseItems(releaseMap(p["status"])["initContainerStatuses"])[0])["imageID"] = "sha256:" + strings.Repeat("c", 64)
		},
		func(p map[string]any) {
			releaseMap(releaseItems(releaseMap(p["spec"])["containers"])[0])["image"] = "registry.invalid/changed@sha256:" + strings.Repeat("c", 64)
		},
	} {
		pod := releaseObjectCopy(base)
		change(pod)
		if releasePodHealth(pod, false).State == "ready" {
			t.Fatal("digest bypassed container checks")
		}
	}
	wanted := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"app"}},"template":{"metadata":{"labels":{"app":"app"}},"spec":{"containers":[{"name":"app","image":"` + image + `"}]}}}}`)
	actual := releaseObjectCopy(wanted)
	releaseMap(actual["metadata"])["uid"] = "deployment-uid"
	releaseMap(actual["metadata"])["generation"] = 1
	actual["status"] = map[string]any{"observedGeneration": 1, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1}
	rs := map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "metadata": map[string]any{"name": "rs", "namespace": "app", "uid": "rs-uid", "ownerReferences": []any{map[string]any{"uid": "deployment-uid", "controller": true}}}, "spec": map[string]any{"template": releaseObjectCopy(releaseMap(releaseMap(wanted["spec"])["template"]))}}
	pod := containerdReadyPod(image, "sha256:"+strings.Repeat("b", 64), image)
	releaseMap(pod["metadata"])["ownerReferences"] = []any{map[string]any{"uid": "rs-uid", "controller": true}}
	resource := releaseResourceFromObject(ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "app"}, wanted, "workload")
	if h := EvaluateReleaseHealth(resource, actual, []map[string]any{rs, pod}); h.State != "ready" {
		t.Fatal("valid owned digest Pod rejected", h.Reason)
	}
	releaseMap(releaseItems(releaseMap(pod["spec"])["containers"])[0])["image"] = "registry.invalid/foreign@" + digest
	releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["image"] = "registry.invalid/foreign@" + digest
	releaseMap(releaseItems(releaseMap(pod["status"])["containerStatuses"])[0])["imageID"] = "registry.invalid/foreign@" + digest
	if EvaluateReleaseHealth(resource, actual, []map[string]any{rs, pod}).State == "ready" {
		t.Fatal("status digest bypassed approved Pod template")
	}
	pod = containerdReadyPod(image, "sha256:"+strings.Repeat("b", 64), image)
	releaseMap(pod["metadata"])["ownerReferences"] = []any{map[string]any{"uid": "foreign-rs", "controller": true}}
	if EvaluateReleaseHealth(resource, actual, []map[string]any{rs, pod}).State == "ready" {
		t.Fatal("digest bypassed Pod owner")
	}
}

type canceledObservationAPI struct {
	*releaseControllerAPI
	cancel            context.CancelFunc
	writing, observed bool
}

func (a *canceledObservationAPI) ExecuteAction(ctx context.Context, id, owner string, revision int64, in ReleaseActionIntent, path string) (ReleaseAction, error) {
	action, e := a.releaseControllerAPI.ExecuteAction(ctx, id, owner, revision, in, path)
	a.writing = true
	return action, e
}
func (a *canceledObservationAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	raw, e := a.releaseControllerAPI.Read(ctx, path, query)
	if a.writing && !a.observed && strings.Contains(path, "/deployments/") {
		a.observed = true
		a.cancel()
	}
	return raw, e
}
func TestReleaseObservationLastHealthSurvivesExpiredWorkContext(t *testing.T) {
	f, p := releaseStoreFixture(t, storetest.Open(t))
	ctx := context.Background()
	scope := p.Authorization.Request.Resources[0]
	scope.Actions = append(scope.Actions, "create")
	scope.Containers = []string{"app"}
	if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	request := p.Authorization.Request
	request.Resources = []ReleaseAccess{scope}
	var authErr error
	p.Authorization, authErr = f.s.AuthorizeRelease(ctx, request)
	if authErr != nil {
		t.Fatal(authErr)
	}
	record, e := f.s.CreateRelease(ctx, "run-last-health", "node", "owner", p)
	if e != nil {
		t.Fatal(e)
	}
	record, e = f.s.ApproveRelease(ctx, record.ID, "owner", record.Revision, record.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	registerControllerTestWriter(t, f, record)
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	api := &canceledObservationAPI{releaseControllerAPI: &releaseControllerAPI{f: f, record: record, objects: map[string]map[string]any{}}, cancel: cancel}
	controller := NewReleaseController(f.s, nil)
	controller.poll = time.Millisecond
	controller.writer = func(context.Context, string, string) (releaseAPI, error) { return api, nil }
	logs := []string{}
	_, e = controller.Run(work, record.ID, "owner", func(phase, code string) { logs = append(logs, phase+":"+code) })
	if !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation lost", e)
	}
	actions, e := f.s.ListReleaseActions(ctx, record.ID)
	if e != nil || len(actions) != 1 || actions[0].State != "verified" || actions[0].Receipt == nil || actions[0].Receipt.Health.Reason != "deployment_rollout_pending" || actions[0].Receipt.ObservedAt == 0 {
		t.Fatal("last known health lost after work context expired")
	}
	if _, err := persistReleaseActionHealth(work, f.s, actions[0].ID, "owner", actions[0].Revision-1, *actions[0].Receipt); !errors.Is(err, ErrConflict) {
		t.Fatal("cleanup context bypassed revision CAS")
	}
	if api.writes != 1 {
		t.Fatal("health persistence replayed a side effect")
	}
	if !strings.Contains(strings.Join(logs, "\n"), "kind=Deployment state=pending reason=deployment_rollout_pending") {
		t.Fatal("closed final health diagnostic missing")
	}
}

func TestReleaseObservationLogOnlyClosedKindStateAndReason(t *testing.T) {
	got := releaseHealthLog("SecretKindCanary", ReleaseHealth{State: "SecretStateCanary", Reason: "AdmissionSecretCanary"})
	if got != "kind=CustomResource state=unverified reason=resource_check_failed" {
		t.Fatal("untrusted health diagnostic leaked")
	}
	if got := releaseHealthLog("Deployment", ReleaseHealth{State: "pending", Reason: "container_image_not_observed"}); got != "kind=Deployment state=pending reason=container_image_not_observed" {
		t.Fatal("known health reason lost")
	}
}

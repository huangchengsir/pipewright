package kubernetes

import (
	"context"
	"errors"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

func TestReview110RetryPreservesFrozenRollbackImagePin(t *testing.T) {
	for _, scenario := range []string{"unchanged", "uid", "image", "scope", "missing-pin", "invalid-pin", "foreign-container"} {
		t.Run(scenario, func(t *testing.T) { review110RetryPin(t, scenario) })
	}
}
func review110RetryPin(t *testing.T, scenario string) {
	st := storetest.Open(t)
	f, p := releaseStoreFixture(t, st)
	ctx := context.Background()
	id := p.Resources[0].ID
	original := releaseObjectCopy(p.Resources[0].Object)
	releaseMap(original["metadata"])["uid"] = "same-workload"
	releaseMap(original["metadata"])["resourceVersion"] = "17"
	releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app:prior-tag"
	desired := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"backend","namespace":"app"},"spec":{"template":{"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}}}}`)
	target := releaseResourceFromObject(id, desired, "workload")
	target.ImageOnly = true
	p.Mode = "image"
	p.MutationVersion = 1
	p.Resources = []ReleaseResource{target}
	baseline := releaseResourceFromObject(id, original, "workload")
	baseline.RollbackImages = map[string]string{"containers/app": "registry.invalid/app@sha256:" + strings.Repeat("a", 64)}
	if scenario == "missing-pin" {
		baseline.RollbackImages = nil
	}
	if scenario == "invalid-pin" {
		baseline.RollbackImages["containers/app"] = "registry.invalid/app:still-mutable"
	}
	if scenario == "foreign-container" {
		baseline.RollbackImages = map[string]string{"containers/foreign": "registry.invalid/app@sha256:" + strings.Repeat("a", 64)}
	}
	p.Baseline = []ReleaseResource{baseline}
	if _, err := rollbackImageObject(target, baseline); scenario == "unchanged" && err != nil {
		t.Fatal("sealed original pin unusable", err)
	}
	prior, err := f.s.CreateRelease(ctx, "blind109-retry-prior", "node", "owner", p)
	if err != nil {
		t.Fatal(err)
	}
	prior, err = f.s.SetReleaseTerminal(ctx, prior.ID, prior.OwnerEpoch, prior.Revision, "failed", "release_failed")
	if err != nil {
		t.Fatal(err)
	}
	api := &releaseImagePreview{original: original, releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/backend": original}}}
	c := NewReleaseController(f.s, nil)
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	catalog := p.Authorization
	if scenario == "uid" {
		releaseMap(original["metadata"])["uid"] = "new-incarnation"
	}
	if scenario == "image" {
		releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(original["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/app:new-tag"
	}
	if scenario == "scope" {
		catalog.Request.Environment = "other"
	}
	retry, err := c.PrepareRetry(ctx, "blind109-retry-next", "node", "next-owner", prior.ID, catalog)
	if scenario == "uid" || scenario == "image" || scenario == "scope" || scenario == "invalid-pin" || scenario == "foreign-container" {
		if err == nil {
			t.Fatal("unproved old pin/current context accepted", scenario)
		}
		return
	}
	if err != nil {
		t.Fatal("retry preparation failed", err)
	}
	_, rollbackErr := rollbackImageObject(retry.Plan.Resources[0], retry.Plan.Baseline[0])
	t.Logf("sourcePins=%d retryPins=%d sourceUID=%s retryUID=%s rollbackBaselineUnavailable=%t", len(prior.Plan.Baseline[0].RollbackImages), len(retry.Plan.Baseline[0].RollbackImages), releaseMap(prior.Plan.Baseline[0].Object["metadata"])["uid"], releaseMap(retry.Plan.Baseline[0].Object["metadata"])["uid"], errors.Is(rollbackErr, ErrRollbackBaseline))
	if scenario == "missing-pin" {
		if len(retry.Plan.Baseline[0].RollbackImages) != 0 || !errors.Is(rollbackErr, ErrRollbackBaseline) {
			t.Fatal("missing pin invented from mutable tag")
		}
		return
	}
	if len(retry.Plan.Baseline[0].RollbackImages) != 1 || rollbackErr != nil {
		t.Fatal("sealed rollback pin lost on unchanged retry", rollbackErr)
	}
}

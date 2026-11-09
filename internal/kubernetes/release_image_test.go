package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/store"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"strings"
	"testing"
)

type releaseImagePreview struct {
	releaseControllerAPI
	original map[string]any
	patch    map[string]any
}

func (a *releaseImagePreview) DryRun(_ context.Context, r kubeprotocol.Request) (map[string]any, error) {
	if json.Unmarshal(r.Body, &a.patch) != nil {
		return nil, ErrInvalid
	}
	out := releaseObjectCopy(a.original)
	source := releaseItems(releaseMap(releaseMap(releaseMap(a.patch["spec"])["template"])["spec"])["containers"])
	target := releaseItems(releaseMap(releaseMap(releaseMap(out["spec"])["template"])["spec"])["containers"])
	for _, v := range source {
		for _, w := range target {
			if releaseMap(v)["name"] == releaseMap(w)["name"] {
				releaseMap(w)["image"] = releaseMap(v)["image"]
			}
		}
	}
	return out, nil
}
func TestReleaseImageSSAOmitsHPAReplicasAndPreservesFrozenTemplate(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, p := releaseStoreFixture(t, st)
		ctx := context.Background()
		original := releaseObjectCopy(p.Resources[0].Object)
		meta := releaseMap(original["metadata"])
		meta["uid"] = "workload-uid"
		meta["resourceVersion"] = "17"
		releaseMap(original["spec"])["replicas"] = float64(3)
		api := &releaseImagePreview{original: original, releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/backend": original}}}
		c := NewReleaseController(f.s, nil)
		c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		scope := releaseRequestedAccess(p.Resources[0].ID, "patch")
		scope.Containers = []string{"app"}
		if _, e := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
			t.Fatal(e)
		}
		digest := "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
		record, e := c.PrepareImage(ctx, "image-hpa", "node", "owner", p.Authorization, "Deployment", "app", "backend", []ReleaseImageUpdate{{Container: "app", Image: digest}}, []byte(`{}`), 300, 0)
		if e != nil {
			t.Fatal(e)
		}
		resource := record.Plan.Resources[0]
		if !resource.ImageOnly || releaseMap(resource.Object["spec"])["replicas"] != nil || strings.Contains(stringMustJSON(resource.Object), "PRIVATE") {
			t.Fatal("image patch claimed unrelated fields")
		}
		if releaseMap(resource.Expected["spec"])["replicas"] != nil || !strings.Contains(stringMustJSON(resource.Expected), "lowentropy-secret") {
			t.Fatal("admitted comparison lost protected template or pinned replicas")
		}
		scaled := releaseObjectCopy(original)
		releaseMap(scaled["spec"])["replicas"] = float64(7)
		if !releaseResourceBaselineMatches(resource, original, scaled) {
			t.Fatal("HPA scaling blocked image-only update")
		}
		releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(scaled["spec"])["template"])["spec"])["containers"])[0])["env"] = []any{}
		if releaseResourceBaselineMatches(resource, original, scaled) {
			t.Fatal("template change hidden as HPA scaling")
		}
	})
}
func TestReleaseRetryReusesSealedInputAndRejectsExternalDrift(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, st *store.Store) {
		f, p := releaseStoreFixture(t, st)
		ctx := context.Background()
		original := releaseObjectCopy(p.Resources[0].Object)
		meta := releaseMap(original["metadata"])
		meta["uid"] = "original"
		meta["resourceVersion"] = "17"
		p.Baseline = []ReleaseResource{releaseResourceFromObject(p.Resources[0].ID, original, "workload")}
		p.Config = json.RawMessage(`{"frozen":"${NO_SECOND_LOOKUP}"}`)
		r, e := f.s.CreateRelease(ctx, "before-retry", "node", "owner", p)
		if e != nil {
			t.Fatal(e)
		}
		r, e = f.s.SetReleaseTerminal(ctx, r.ID, r.OwnerEpoch, r.Revision, "failed", "release_failed")
		if e != nil {
			t.Fatal(e)
		}
		api := &releaseControllerAPI{objects: map[string]map[string]any{"/apis/apps/v1/namespaces/app/deployments/backend": original}}
		c := NewReleaseController(f.s, nil)
		c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
		next, e := c.PrepareRetry(ctx, "after-retry", "node", "new-owner", r.ID, p.Authorization)
		if e != nil {
			t.Fatal(e)
		}
		if next.Plan.RetryOf != r.ID || string(next.Plan.Config) != string(p.Config) || next.Plan.Resources[0].Digest != p.Resources[0].Digest || next.ApprovedID != "" || next.PlanID == r.PlanID {
			t.Fatal("retry changed frozen input or inherited approval")
		}
		changed := releaseObjectCopy(original)
		releaseMap(changed["spec"])["unexpected"] = true
		api.objects["/apis/apps/v1/namespaces/app/deployments/backend"] = changed
		if _, e = c.PrepareRetry(ctx, "drift-retry", "node", "new-owner", r.ID, p.Authorization); !errors.Is(e, ErrSuperseded) {
			t.Fatal("external template addition adopted", e)
		}
	})
}

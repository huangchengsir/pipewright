package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"strings"
	"testing"
)

type review115PrepareAPI struct {
	*releaseControllerAPI
	dryRuns int
}

func (a *review115PrepareAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/apis/apps/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true}]}`), nil
	}
	return a.releaseControllerAPI.Read(ctx, path, query)
}
func (a *review115PrepareAPI) DryRun(ctx context.Context, req kubeprotocol.Request) (map[string]any, error) {
	a.dryRuns++
	object, e := a.releaseControllerAPI.DryRun(ctx, req)
	if e != nil {
		return nil, e
	}
	return review113NativeSerialize(object)
}
func TestReview115AtomicOmissionPrepareManifest(t *testing.T) {
	for _, tc := range []struct {
		name, fragment string
		drift          bool
	}{
		{"absent-prefix-control", `"configMapRef":{"name":"source"}`, false},
		{"nonempty-prefix-control", `"prefix":"APP_","configMapRef":{"name":"source"}`, false},
		{"empty-prefix-native-omission", `"prefix":"","configMapRef":{"name":"source"}`, false},
		{"nonempty-drift-control", `"prefix":"APP_","configMapRef":{"name":"source"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			id := ReleaseResourceID{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "app", Name: "atomic"}
			original := releaseTestObject(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"atomic","namespace":"app"},"spec":{"replicas":1,"selector":{"matchLabels":{"app":"atomic"}},"template":{"metadata":{"labels":{"app":"atomic"}},"spec":{"containers":[{"name":"app","image":"registry.invalid/app@sha256:` + strings.Repeat("a", 64) + `","envFrom":[{` + tc.fragment + `}]}]}}}}`)
			f, auth, live := review112OwnedSeed(t, id, original, func(o map[string]any) {
				typed, e := review113NativeSerialize(o)
				if e != nil {
					t.Fatal(e)
				}
				for k := range o {
					delete(o, k)
				}
				for k, v := range typed {
					o[k] = v
				}
			})
			container := func(o map[string]any) map[string]any {
				return releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(o["spec"])["template"])["spec"])["containers"])[0])
			}
			if tc.drift {
				releaseMap(releaseItems(container(live)["envFrom"])[0])["prefix"] = "EXTERNAL_"
			}
			wanted := releaseObjectCopy(original)
			container(wanted)["image"] = "registry.invalid/app@sha256:" + strings.Repeat("b", 64)
			path, _ := resourceAPI(id).Path(id.Namespace, id.Name)
			api := &review115PrepareAPI{releaseControllerAPI: &releaseControllerAPI{f: f, objects: map[string]map[string]any{path: live}}}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			raw, _ := json.Marshal(wanted)
			files, e := InlineReleaseFile(string(raw))
			if e != nil {
				t.Fatal(e)
			}
			out, e := c.PrepareManifest(ctx, "review115-run", "node", "review115-owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
			_, present := releaseMap(releaseItems(container(live)["envFrom"])[0])["prefix"]
			t.Logf("officialSerializedPrefixPresent=%t templateMatches=%t dryRuns=%d writes=%d releaseCreated=%t superseded=%t error=%v", present, ReleasePodTemplateMatches(original, live), api.dryRuns, api.writes, out.ID != "", errors.Is(e, ErrSuperseded), e)
			if api.writes != 0 {
				t.Fatal("unexpected write")
			}
			if tc.drift {
				if !errors.Is(e, ErrSuperseded) || out.ID != "" {
					t.Fatal("drift fence failed", e)
				}
				return
			}
			if e != nil || out.ID == "" {
				t.Fatal("valid native-serialized owned deployment cannot be updated", e)
			}
		})
	}
}

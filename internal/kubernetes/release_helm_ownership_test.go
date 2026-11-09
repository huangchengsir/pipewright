package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
)

type helmOwnershipAPI struct{ helmResourcesAPI }

func (a *helmOwnershipAPI) Read(ctx context.Context, path, query string) ([]byte, error) {
	if path == "/apis/batch/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"batch/v1","resources":[{"name":"jobs","kind":"Job","namespaced":true,"verbs":["get","create","patch","delete","watch"]}]}`), nil
	}
	return a.helmResourcesAPI.Read(ctx, path, query)
}
func helmOwnershipPlanFixture(t *testing.T, hook map[string]any) ReleaseRecord {
	t.Helper()
	ctx := context.Background()
	f, policy := releaseStoreFixture(t, storetest.Open(t))
	id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "fixture-hook"}
	cm := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
	anchor := ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.fixture"}
	scopes := []ReleaseAccess{{ID: id, Actions: []string{"get", "create", "patch", "delete", "watch", "helm-hook", "related-pods"}, Containers: []string{"hook"}}, {ID: cm, Actions: []string{"get", "create", "patch"}}, {ID: anchor, Actions: []string{"get", "create", "update", "patch", "helm-storage"}}}
	if _, err := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Enabled: true, RequireApproval: true, Resources: scopes}); err != nil {
		t.Fatal(err)
	}
	request := policy.Authorization.Request
	request.Resources = scopes
	auth, err := f.s.AuthorizeRelease(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	api := &helmOwnershipAPI{helmResourcesAPI{releaseControllerAPI{objects: map[string]map[string]any{}}}}
	c := NewReleaseController(f.s, nil)
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	source := stringMustJSON(hook) + "\n---\n" + `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"message":"approved"}}`
	files, err := InlineReleaseFile(source)
	if err != nil {
		t.Fatal(err)
	}
	helm := &ReleaseHelmPlan{Name: "fixture", Namespace: "app", Action: "install", Hooks: true, Revision: 1, StorageAnchor: anchor, Release: map[string]any{}}
	record, err := c.prepareManifest(ctx, "helm-ownership-run", "node", "owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0, helm, nil, nil)
	if err != nil {
		t.Fatal("real host freeze failed", err)
	}
	return record
}
func testOwnershipHook() map[string]any {
	return releaseTestObject(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"fixture-hook","namespace":"app","annotations":{"helm.sh/hook":"pre-install,pre-upgrade","helm.sh/hook-delete-policy":"before-hook-creation,hook-succeeded"}},"spec":{"template":{"spec":{"restartPolicy":"Never","serviceAccountName":"no-permissions","automountServiceAccountToken":false,"securityContext":{"runAsNonRoot":true},"containers":[{"name":"hook","image":"registry.invalid/fixture@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","args":["private-hook-script-canary"],"securityContext":{"runAsNonRoot":true,"readOnlyRootFilesystem":true,"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}}]}}}}`)
}
func TestHelmHookFreezePreservesRenderedOwnershipAndExactHostMatcher(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "undeclared", true: "explicit"}[explicit], func(t *testing.T) {
			hook := testOwnershipHook()
			meta := releaseMap(hook["metadata"])
			if explicit {
				releaseMap(meta["annotations"])["meta.helm.sh/release-name"] = "declared-name"
				releaseMap(meta["annotations"])["meta.helm.sh/release-namespace"] = "declared-namespace"
				meta["labels"] = map[string]any{"app.kubernetes.io/managed-by": "DeclaredManager"}
			}
			record := helmOwnershipPlanFixture(t, hook)
			var frozen ReleaseResource
			for _, resource := range record.Plan.Helm.Rendered {
				if resource.ID.Kind == "Job" {
					frozen = resource
				} else if resource.ID.Kind == "ConfigMap" {
					annotations := releaseMap(releaseMap(resource.Object["metadata"])["annotations"])
					labels := releaseMap(releaseMap(resource.Object["metadata"])["labels"])
					if annotations["meta.helm.sh/release-name"] != "fixture" || annotations["meta.helm.sh/release-namespace"] != "app" || labels["app.kubernetes.io/managed-by"] != "Helm" {
						t.Fatal("ordinary Helm resource lost official ownership")
					}
				}
			}
			if !ReleaseObjectMatches(frozen.Object, hook) || !ReleaseObjectMatches(hook, frozen.Object) {
				t.Fatal("hook frozen input differs from exact original rendering")
			}
			if _, err := releaseHelmPendingMutation(record, frozen.ID, releaseObjectCopy(hook)); err != nil {
				t.Fatal("actual host matcher rejected unchanged hook rendering", err)
			}
			for _, field := range []string{"name", "image", "args", "new-annotation", "ownership"} {
				changed := releaseObjectCopy(hook)
				switch field {
				case "name":
					releaseMap(changed["metadata"])["name"] = "foreign"
				case "image":
					releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(changed["spec"])["template"])["spec"])["containers"])[0])["image"] = "registry.invalid/foreign@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				case "args":
					releaseMap(releaseItems(releaseMap(releaseMap(releaseMap(changed["spec"])["template"])["spec"])["containers"])[0])["args"] = []any{"foreign"}
				case "new-annotation":
					releaseMap(releaseMap(changed["metadata"])["annotations"])["foreign"] = "changed"
				case "ownership":
					releaseMap(releaseMap(changed["metadata"])["annotations"])["meta.helm.sh/release-name"] = "foreign"
				}
				if _, err := releaseHelmPendingMutation(record, frozen.ID, changed); !errors.Is(err, ErrDenied) {
					t.Fatal("changed hook escaped exact matching", field)
				}
			}
		})
	}
}

// The companion actual Agent/Helm4.3.1 test writes only its credential-free
// rendered Job and actual pending request to a private tmpfs fixture. Feed that
// exact CLI body into the production host preparation and matcher, without
// reimplementing its comparison or blindly granting a synthetic permit.
func TestHelm431CapturedPendingMatchesHostFrozenHook(t *testing.T) {
	path := os.Getenv("PIPEWRIGHT_TEST_HELM_PENDING_INPUT")
	if path == "" {
		t.Skip("actual CLI pending fixture not provided")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("captured CLI fixture unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaxReleaseObjectBytes+1))
	defer clear(raw)
	var capture struct {
		Rendered map[string]any       `json:"rendered"`
		Request  kubeprotocol.Request `json:"request"`
	}
	if err != nil || len(raw) > MaxReleaseObjectBytes || json.Unmarshal(raw, &capture) != nil || capture.Rendered["kind"] != "Job" || capture.Request.Path != "/apis/batch/v1/namespaces/app/jobs/fixture-hook" || capture.Request.Method != "PATCH" && capture.Request.Method != "POST" {
		t.Fatal("captured CLI fixture shape invalid")
	}
	record := helmOwnershipPlanFixture(t, capture.Rendered)
	actual := map[string]any{}
	if json.Unmarshal(capture.Request.Body, &actual) != nil {
		t.Fatal("captured CLI request not JSON")
	}
	id := ReleaseResourceID{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "fixture-hook"}
	if _, err = releaseHelmPendingMutation(record, id, releaseCanonicalObject(actual)); err != nil {
		t.Fatal("actual pinned CLI pending rejected by real frozen host matcher")
	}
	t.Log("actual pinned CLI pending accepted by same production host freeze/matcher; exact scope retained")
}

package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
)

type review113TypedPodAPI struct {
	releaseAPI
	calls, writes int
}

func (a *review113TypedPodAPI) Close(context.Context) (ReleaseRuntimeLease, error) {
	return ReleaseRuntimeLease{}, nil
}
func (a *review113TypedPodAPI) Read(_ context.Context, path, query string) ([]byte, error) {
	if path == "/api/v1" {
		return []byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true},{"name":"configmaps","kind":"ConfigMap","namespaced":true},{"name":"secrets","kind":"Secret","namespaced":true}]}`), nil
	}
	return nil, ErrNotFound
}
func (a *review113TypedPodAPI) ExecuteAction(context.Context, string, string, int64, ReleaseActionIntent, string) (ReleaseAction, error) {
	a.writes++
	return ReleaseAction{}, ErrTool
}
func (a *review113TypedPodAPI) DryRun(_ context.Context, in kubeprotocol.Request) (map[string]any, error) {
	a.calls++
	var header struct{ Kind string }
	if e := json.Unmarshal(in.Body, &header); e != nil {
		return nil, e
	}
	var typed any
	switch header.Kind {
	case "Pod":
		typed = &corev1.Pod{}
	case "ConfigMap":
		typed = &corev1.ConfigMap{}
	case "Secret":
		typed = &corev1.Secret{}
	default:
		return nil, ErrInvalid
	}
	if e := json.Unmarshal(in.Body, typed); e != nil {
		return nil, e
	}
	b, e := json.Marshal(typed)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(b, &out)
	return out, e
}
func TestReview113TypedOmissionPreparation(t *testing.T) {
	cases := []struct {
		name, kind, resource, container, spec, root string
		reject                                      bool
	}{
		{name: "control-nonempty-env", container: `env: [{name: KEY, value: text}]`},
		{name: "control-true-stdin", container: `stdin: true`},
		{name: "control-pointer-false", container: `securityContext: {runAsNonRoot: false}`},
		{name: "control-pointer-zero", spec: "  terminationGracePeriodSeconds: 0\n"},
		{name: "empty-env-value", container: `env: [{name: KEY, value: ''}]`},
		{name: "empty-env-array", container: `env: []`},
		{name: "empty-command-array", container: `command: []`},
		{name: "empty-envFrom-array", container: `envFrom: []`},
		{name: "false-stdin", container: `stdin: false`},
		{name: "false-hostNetwork", spec: "  hostNetwork: false\n"},
		{name: "empty-nodeSelector-map", spec: "  nodeSelector: {}\n"},
		{name: "empty-imagePullSecrets-array", spec: "  imagePullSecrets: []\n"},
		{name: "control-configmap-empty-string-value", kind: "ConfigMap", resource: "configmaps", root: "data: {KEY: ''}\n"},
		{name: "empty-configmap-data-map", kind: "ConfigMap", resource: "configmaps", root: "data: {}\n"},
		{name: "control-secret-nonempty-data", kind: "Secret", resource: "secrets", root: "data: {KEY: YQ==}\n"},
		{name: "implicit-empty-secret-data-map", kind: "Secret", resource: "secrets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			kind, resource := tc.kind, tc.resource
			if kind == "" {
				kind, resource = "Pod", "pods"
			}
			input := "apiVersion: v1\nkind: " + kind + "\nmetadata: {name: omission, namespace: app}\n" + tc.root
			if kind == "Pod" {
				input += "spec:\n  restartPolicy: Never\n" + tc.spec + "  containers:\n  - name: app\n    image: example.invalid/app:stable\n"
				if tc.container != "" {
					input += "    " + tc.container + "\n"
				}
			}
			files, e := InlineReleaseFile(input)
			if e != nil {
				t.Fatal(e)
			}
			frozen, e := FreezeReleaseManifest(files, nil, releaseTestDiscovery(t), "app")
			if e != nil {
				t.Fatal(e)
			}
			f, p := releaseStoreFixture(t, storetest.Open(t))
			id := ReleaseResourceID{Version: "v1", Resource: resource, Kind: kind, Namespace: "app", Name: "omission"}
			scope := releaseRequestedAccess(id, "create")
			if kind == "Pod" {
				scope.Containers = []string{"app"}
			}
			if _, e = f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Resources: []ReleaseAccess{scope}, RequireApproval: true, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			req := p.Authorization.Request
			req.Resources = []ReleaseAccess{scope}
			auth, e := f.s.AuthorizeRelease(ctx, req)
			if e != nil {
				t.Fatal(e)
			}
			api := &review113TypedPodAPI{}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			out, e := c.PrepareManifest(ctx, "review113-run", "node", "review113-owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0)
			var records int
			f.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM k8s_releases`).Scan(&records)
			phase, reason, _ := ReleasePreparationDetails(e)
			t.Logf("frozenResources=%d typedAPIcalls=%d writes=%d admissionMismatch=%t releaseCreated=%t records=%d phase=%s reason=%s", len(frozen.Resources), api.calls, api.writes, errors.Is(e, errReleaseAdmissionMismatch), out.ID != "", records, phase, reason)
			if api.writes != 0 || api.calls != 1 {
				t.Fatal("unexpected dispatch/path")
			}
			if tc.reject {
				if !errors.Is(e, errReleaseAdmissionMismatch) || out.ID != "" || records != 0 {
					t.Fatal("expected recorded omission preparation failure", e)
				}
			} else if e != nil || out.ID == "" || records != 1 {
				t.Fatal("control failed", e)
			}
		})
	}
}
func TestReview113OmissionNormalizationContrasts(t *testing.T) {
	// Unknown schemas cannot equate explicit false/empty values with absence.
	for _, spec := range []map[string]any{{"enabled": false}, {"items": []any{}}, {"settings": map[string]any{}}} {
		want := map[string]any{"apiVersion": "custom.invalid/v1", "kind": "Workflow", "spec": spec}
		actual := map[string]any{"apiVersion": "custom.invalid/v1", "kind": "Workflow", "spec": map[string]any{}}
		if ReleasePodTemplateMatches(want, actual) {
			t.Fatal("unknown CRD omission wrongly accepted")
		}
	}
	// The existing special case does handle PriorityClass globalDefault=false.
	raw := []byte(`{"apiVersion":"scheduling.k8s.io/v1","kind":"PriorityClass","metadata":{"name":"priority"},"value":100,"globalDefault":false}`)
	var typed schedulingv1.PriorityClass
	json.Unmarshal(raw, &typed)
	b, _ := json.Marshal(typed)
	var want, actual map[string]any
	json.Unmarshal(raw, &want)
	json.Unmarshal(b, &actual)
	id := ReleaseResourceID{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses", Kind: "PriorityClass", Name: "priority"}
	if !releaseApprovedFieldsMatch(id, want, actual) {
		t.Fatal("existing PriorityClass normalization control failed")
	}
	t.Log("unknown-CRD false/empty array/map omissions rejected; existing PriorityClass false omission accepted")
}

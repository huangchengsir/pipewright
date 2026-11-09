package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"github.com/huangchengsir/pipewright/internal/storetest"
	"github.com/huangchengsir/pipewright/internal/target"
)

// Exercise the actual private RPC -> ReleaseSession -> preparation diagnostic
// chain. No raw Status text, resource body or credential can become the reason.
type dryRunStatusTransport struct {
	*releasePhaseTransport
	status        int
	lastDryRun    kubeprotocol.Request
	dryRunCalls   int
	mutationCalls int
}

func (t *dryRunStatusTransport) Docker(ctx context.Context, args []string, in []byte) (*target.SecretResult, error) {
	if len(args) > 0 && args[0] == "exec" && len(in) > 0 {
		var frame kubeprotocol.Frame
		if json.Unmarshal(in, &frame) == nil && frame.Version == 2 {
			reply := kubeprotocol.Reply{Version: 2, Code: "ok"}
			switch frame.Command {
			case "ping", "upload", "init":
			case "read":
				reply.Result = &kubeprotocol.APIResult{Status: 200, Body: []byte(`{"major":"1","minor":"34"}`)}
			case "dry-run":
				t.dryRunCalls++
				if frame.Request != nil {
					t.lastDryRun = *frame.Request
					t.lastDryRun.Body = nil
				}
				reply.Result = &kubeprotocol.APIResult{Status: t.status, Body: []byte(`{"apiVersion":"v1","kind":"Status","reason":"PrivateAdmissionCanary","message":"private-admission-value"}`)}
			default:
				t.mutationCalls++
				return nil, ErrTool
			}
			return fakeResult(reply), nil
		}
	}
	return t.releasePhaseTransport.Docker(ctx, args, in)
}
func TestReleaseDryRunStatusReasonsNeverMislabelAPIConflictAsAuthorization(t *testing.T) {
	for _, tc := range []struct {
		status int
		reason string
	}{{400, "kubernetes_request_invalid"}, {401, "kubernetes_credential_rejected"}, {403, "kubernetes_not_authorized"}, {404, "kubernetes_not_found"}, {409, "kubernetes_resource_conflict"}, {422, "kubernetes_resource_invalid"}, {429, "kubernetes_api_throttled"}, {503, "kubernetes_api_unavailable"}, {418, "dry_run_failed"}} {
		t.Run(tc.reason, func(t *testing.T) {
			ctx := context.Background()
			f, p := releaseStoreFixture(t, storetest.Open(t))
			tr := &dryRunStatusTransport{releasePhaseTransport: &releasePhaseTransport{fakePrivateDocker: &fakePrivateDocker{}, helperVersion: true, helperPing: true}, status: tc.status}
			runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
			session, e := runtime.OpenReleasePreview(ctx, p.Authorization, "status-owner")
			if e != nil {
				t.Fatal("runtime fixture failed", e)
			}
			defer session.Close(ctx)
			_, e = session.DryRun(ctx, kubeprotocol.Request{Method: "PATCH", Path: "/apis/apps/v1/namespaces/app/deployments/backend", Body: []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"backend","namespace":"app"}}`)})
			wrapped := WithReleasePreparationPhase(e, "dry_run")
			phase, reason, ok := ReleasePreparationDetails(wrapped)
			if !ok || phase != "dry_run" || reason != tc.reason {
				t.Fatalf("HTTP %d has wrong closed diagnostic: %s/%s", tc.status, phase, reason)
			}
			if strings.Contains(wrapped.Error(), "Canary") || strings.Contains(wrapped.Error(), "private-admission-value") {
				t.Fatal("raw API status leaked")
			}
			if tc.status == 409 && errors.Is(e, ErrDenied) {
				t.Fatal("API conflict is not a permission denial")
			}
		})
	}
}

type mutationDryRunAPI struct {
	releaseAPI
	request  kubeprotocol.Request
	mismatch bool
}

func (a *mutationDryRunAPI) Close(context.Context) (ReleaseRuntimeLease, error) {
	return ReleaseRuntimeLease{}, nil
}
func (a *mutationDryRunAPI) DryRun(_ context.Context, r kubeprotocol.Request) (map[string]any, error) {
	a.request = r
	a.request.Body = append([]byte(nil), r.Body...)
	if a.mismatch {
		return map[string]any{}, nil
	}
	if r.Method == "PATCH" && r.ContentType != "application/merge-patch+json" {
		return nil, errors.New("simulated_post_ssa_conflict")
	}
	var object map[string]any
	if json.Unmarshal(r.Body, &object) != nil {
		return nil, ErrInvalid
	}
	return object, nil
}
func TestReleaseDryRunHonorsExplicitFrozenOwnedCASPolicy(t *testing.T) {
	// This is an already prepared/sealed policy, not a label-driven fallback.
	var plan ReleasePlan
	if e := json.Unmarshal([]byte(`{"version":2,"mutationVersion":1,"mode":"manifest","resources":[{"id":{"version":"v1","resource":"configmaps","kind":"ConfigMap","namespace":"app","name":"config"},"object":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"message":"next"}},"mutation":{"strategy":"owned-post-cas-merge-v1","fieldManager":"pipewright-release-v2","contentType":"application/merge-patch+json","originActionId":"kact-create","previousActionId":"kact-create","uid":"owned-uid","managed":{"data":{"message":"next"}},"patch":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"message":"next"}}}}],"baseline":[{"id":{"version":"v1","resource":"configmaps","kind":"ConfigMap","namespace":"app","name":"config"},"object":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app","uid":"owned-uid","resourceVersion":"17"},"data":{"message":"previous","outside":"preserve"}}}]}`), &plan); e != nil {
		t.Fatal(e)
	}
	api := &mutationDryRunAPI{}
	c := NewReleaseController(nil, nil)
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	if e := c.dryRunReleaseResources(context.Background(), "owner", &plan); e != nil {
		t.Fatal("frozen owned CAS plan was silently sent through SSA", e)
	}
	var body map[string]any
	json.Unmarshal(api.request.Body, &body)
	meta := releaseMap(body["metadata"])
	if api.request.ContentType != "application/merge-patch+json" || meta["uid"] != "owned-uid" || meta["resourceVersion"] != "17" {
		t.Fatal("dry-run lost frozen content type or actual CAS identity")
	}
}
func TestReleaseDryRunAdmissionMismatchIsNotAuthorizationFailure(t *testing.T) {
	api := &mutationDryRunAPI{mismatch: true}
	c := NewReleaseController(nil, nil)
	c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
	p := ReleasePlan{Resources: []ReleaseResource{{ID: ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}, Object: releaseTestObject(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"value":"approved"}}`)}}}
	e := WithReleasePreparationPhase(c.dryRunReleaseResources(context.Background(), "owner", &p), "dry_run")
	_, reason, _ := ReleasePreparationDetails(e)
	if reason != "kubernetes_admission_mismatch" {
		t.Fatal("successful API response mismatch mislabeled", reason)
	}
}

// Disabling the incompatible Helm CLI schema decoder does not disable the
// host-to-server Strict dry-run, even when the body contains invalid fields.
func TestHelmStrictAdmissionUsesPrivateRPCAndRejectsBeforeMutation(t *testing.T) {
	ctx := context.Background()
	f, p := releaseStoreFixture(t, storetest.Open(t))
	tr := &dryRunStatusTransport{releasePhaseTransport: &releasePhaseTransport{fakePrivateDocker: &fakePrivateDocker{}, helperVersion: true, helperPing: true}, status: 422}
	runtime := NewRuntime(f.s, func(context.Context, Executor, *target.ConnectionSnapshot) (PrivateTransport, error) { return tr, nil }, nil)
	session, err := runtime.OpenReleasePreview(ctx, p.Authorization, "strict-owner")
	if err != nil {
		t.Fatal("runtime fixture failed")
	}
	defer session.Close(ctx)
	_, err = session.DryRun(ctx, kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/configmaps", Body: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"invalidPrivateField":"private-admission-value"}`)})
	phase, reason, ok := ReleasePreparationDetails(WithReleasePreparationPhase(err, "dry_run"))
	query, parseErr := url.ParseQuery(tr.lastDryRun.Query)
	if !ok || phase != "dry_run" || reason != "kubernetes_resource_invalid" || parseErr != nil || query.Get("dryRun") != "All" || query.Get("fieldValidation") != "Strict" || query.Get("fieldManager") != "pipewright-release-v2" || tr.dryRunCalls != 1 || tr.mutationCalls != 0 {
		t.Fatal("server Strict admission or no-mutation fence changed")
	}
	if strings.Contains(err.Error(), "private-admission") {
		t.Fatal("raw admission response escaped")
	}
}

type helmAdmissionRejectAPI struct {
	helmResourcesAPI
	reject  bool
	dryRuns int
}

func (a *helmAdmissionRejectAPI) DryRun(ctx context.Context, req kubeprotocol.Request) (map[string]any, error) {
	a.dryRuns++
	if a.reject {
		return nil, &releaseAPIStatusError{status: 422}
	}
	object, err := a.helmResourcesAPI.DryRun(ctx, req)
	if err != nil {
		return nil, err
	}
	// A successful admission response that rewrites the approved value must
	// still fail exact frozen intent comparison before CreateRelease.
	releaseMap(object["data"])["value"] = "private-admission-rewrite"
	return object, nil
}
func TestHelmPreparationRejectsInvalidOrRewrittenAdmissionWithoutPlanOrWrites(t *testing.T) {
	for _, reject := range []bool{true, false} {
		name := "rewritten-approved-field"
		if reject {
			name = "server-invalid-field"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := storetest.Open(t)
			f, p := releaseStoreFixture(t, st)
			resource := ReleaseResourceID{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "app", Name: "config"}
			anchor := ReleaseResourceID{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app"}
			scopes := []ReleaseAccess{{ID: resource, Actions: []string{"get", "create", "patch"}}, {ID: anchor, Actions: []string{"get", "create", "update", "patch", "helm-storage"}}}
			if _, err := f.s.SaveReleaseGrant(ctx, f.conn.ID, "", 0, ReleaseGrantInput{ProjectID: "project-1", Environment: "production", Executor: Executor{Kind: "local"}, Enabled: true, Resources: scopes}); err != nil {
				t.Fatal("grant fixture failed")
			}
			request := p.Authorization.Request
			request.Resources = scopes
			auth, err := f.s.AuthorizeRelease(ctx, request)
			if err != nil {
				t.Fatal("authorization fixture failed")
			}
			api := &helmAdmissionRejectAPI{reject: reject, helmResourcesAPI: helmResourcesAPI{releaseControllerAPI: releaseControllerAPI{objects: map[string]map[string]any{}}}}
			c := NewReleaseController(f.s, nil)
			c.preview = func(context.Context, ReleaseAuthorization, string) (releaseAPI, error) { return api, nil }
			files, _ := InlineReleaseFile(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"config","namespace":"app"},"data":{"value":"approved-private-value"}}`)
			helm := &ReleaseHelmPlan{Name: "app", Namespace: "app", Action: "install", Revision: 1, StorageAnchor: anchor, Release: map[string]any{}}
			record, err := c.prepareManifest(ctx, "helm-denied-run", "node", "owner", auth, files, nil, []byte(`{"namespace":"app"}`), 300, 0, helm, nil, nil)
			phase, reason, ok := ReleasePreparationDetails(err)
			want := "kubernetes_admission_mismatch"
			if reject {
				want = "kubernetes_resource_invalid"
			}
			if !ok || phase != "dry_run" || reason != want || record.ID != "" || api.dryRuns != 1 || api.writes != 0 {
				t.Fatalf("admission failure crossed preparation fence: %s/%s", phase, reason)
			}
			for _, table := range []string{"k8s_releases", "k8s_release_actions"} {
				var count int
				if st.DB.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count) != nil || count != 0 {
					t.Fatal("rejected admission persisted executable state")
				}
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("private admission value leaked")
			}
		})
	}
}

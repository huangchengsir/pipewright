package kubeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"gopkg.in/yaml.v3"
	kfields "k8s.io/apimachinery/pkg/fields"
)

type helmGatewayRequestFacts struct {
	sync.Mutex
	requests, rejected, forceTrue, forceFalse, forceAbsent int
	hookAbsent                                             int
	canceledWatch, watchListAttempts                       int
	lastRejected                                           string
	trace                                                  *helmCLIUpstreamTrace
	next                                                   atomic.Uint64
}

func (f *helmGatewayRequestFacts) handler(a *Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := f.next.Add(1)
		r = r.WithContext(context.WithValue(r.Context(), helmCLITraceKey{}, id))
		input, _ := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		defer clear(input)
		r.Body = io.NopCloser(bytes.NewReader(input))
		var fields map[string]json.RawMessage
		validObject := json.Unmarshal(input, &fields) == nil && fields != nil
		var version, kind string
		_ = json.Unmarshal(fields["apiVersion"], &version)
		_ = json.Unmarshal(fields["kind"], &kind)
		shape := fmt.Sprintf("jsonObject_%v_keys_%d_v1_%v_deleteOptions_%v_preconditions_%v_propagationPolicy_%v", validObject, len(fields), version == "v1", kind == "DeleteOptions", fields["preconditions"] != nil, fields["propagationPolicy"] != nil)
		recorder := httptest.NewRecorder()
		a.proxy(recorder, r)
		upstreamStatus := 0
		if f.trace != nil {
			f.trace.Lock()
			upstreamStatus = f.trace.status[id]
			delete(f.trace.status, id)
			f.trace.Unlock()
		}
		a.mu.Lock()
		namedScope := a.readPermission(r.URL.Path)
		_, watchAllowed := a.watchPermission(r.URL.Path, r.URL.Query())
		a.mu.Unlock()
		selector := r.URL.Query().Get("fieldSelector")
		parsed, parseErr := kfields.ParseSelector(selector)
		boundName, bound := false, false
		if parseErr == nil {
			name, ok := parsed.RequiresExactMatch("metadata.name")
			boundName = name == "fixture-hook"
			bound = ok
		}
		selectorFacts := fmt.Sprintf("hasFS_%v_emptyFS_%v_eq_%v_eqeq_%v_parsedExact_%v_AND_%v_jobCollection_%v", r.URL.Query().Has("fieldSelector"), selector == "", selector == "metadata.name=fixture-hook", selector == "metadata.name==fixture-hook", bound && boundName, strings.Contains(selector, ","), r.URL.Path == "/apis/batch/v1/namespaces/app/jobs")
		details := fmt.Sprintf("ctxCanceled_%v_watch_%v_namedScope_%v_watchAllowed_%v_upstreamStatus_%d_hasRV_%v_hasRVMatch_%v_hasSendInitial_%v", r.Context().Err() != nil, r.URL.Query().Get("watch") == "true", namedScope, watchAllowed, upstreamStatus, r.URL.Query().Has("resourceVersion"), r.URL.Query().Has("resourceVersionMatch"), r.URL.Query().Has("sendInitialEvents"))
		f.Lock()
		f.requests++
		if r.URL.Query().Get("sendInitialEvents") == "true" {
			f.watchListAttempts++
		}
		canceledHookWatch := r.Context().Err() != nil && r.Method == "GET" && r.URL.Query().Get("watch") == "true" && r.URL.Path == "/apis/batch/v1/namespaces/app/jobs"
		if canceledHookWatch {
			f.canceledWatch++
		}
		if (r.Method == "GET" || r.Method == "DELETE") && recorder.Code == 404 && r.URL.Path == "/apis/batch/v1/namespaces/app/jobs/fixture-hook" {
			f.hookAbsent++
		}
		force := "absent"
		switch r.URL.Query().Get("force") {
		case "true":
			f.forceTrue++
			force = "true"
		case "false":
			f.forceFalse++
			force = "false"
		default:
			f.forceAbsent++
		}
		if recorder.Code >= 400 && !canceledHookWatch && !(r.Method == "GET" && recorder.Code == 404) && !(r.Method == "DELETE" && recorder.Code == 404 && r.URL.Path == "/apis/batch/v1/namespaces/app/jobs/fixture-hook") {
			f.rejected++
			resource := "other"
			if strings.Contains(r.URL.Path, "/configmaps") {
				resource = "configmap"
			}
			if strings.Contains(r.URL.Path, "/secrets") {
				resource = "storage_secret"
			}
			if strings.Contains(r.URL.Path, "/jobs") {
				resource = "hook_job"
			}
			method := "other"
			switch r.Method {
			case "GET":
				method = "get"
			case "POST":
				method = "post"
			case "PUT":
				method = "put"
			case "PATCH":
				method = "patch"
			case "DELETE":
				method = "delete"
			}
			content := "other"
			switch r.Header.Get("Content-Type") {
			case "application/json":
				content = "json"
			case "application/apply-patch+yaml":
				content = "apply"
			case "application/json-patch+json":
				content = "json_patch"
			case helmStorageProtobufType:
				content = "protobuf"
			}
			f.lastRejected = fmt.Sprintf("%s_%s_%s_force_%s_status_%d", method, resource, content, force, recorder.Code) + "_" + shape + "_" + details + "_" + selectorFacts
		}
		f.Unlock()
		for name, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		clear(recorder.Body.Bytes())
	}
}
func (f *helmGatewayRequestFacts) snapshot() (int, int, int, int, int, string) {
	f.Lock()
	defer f.Unlock()
	return f.requests, f.rejected, f.forceTrue, f.forceFalse, f.forceAbsent, f.lastRejected
}

// This optional real CLI lifecycle uses production proxy/permission/codec and
// one-shot permits, but only a credential-free in-process Kubernetes fixture.
// It is distinct from real cluster approval/storage/rollback acceptance.
func TestHelm431CLILifecycleThroughGuardedJSONGateway(t *testing.T) {
	binary := os.Getenv("PIPEWRIGHT_TEST_HELM4")
	if binary == "" {
		t.Skip("PIPEWRIGHT_TEST_HELM4 not set: physical Helm 4.3.1 lifecycle not executed")
	}
	for _, scenario := range []string{"install-upgrade-rollback", "force-true-is-rejected", "approved-pre-upgrade-hook", "approved-pre-upgrade-hook-v3-fallback", "approved-pre-upgrade-hook-delayed", "approved-pre-upgrade-hook-failed"} {
		t.Run(scenario, func(t *testing.T) {
			hookCase := strings.HasPrefix(scenario, "approved-pre-upgrade-hook")
			fallback := scenario == "approved-pre-upgrade-hook-v3-fallback"
			root := t.TempDir()
			env := []string{"HOME=" + root, "PATH=" + root, "LANG=C", "LC_ALL=C", "HELM_CACHE_HOME=" + filepath.Join(root, "cache"), "HELM_CONFIG_HOME=" + filepath.Join(root, "config"), "HELM_DATA_HOME=" + filepath.Join(root, "data"), "HELM_PLUGINS=" + filepath.Join(root, "no-plugins"), "HELM_DRIVER=secret", "GODEBUG=http2client=0", helmWatchListEnvironment}
			run := func(args []string) (bool, []byte) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Env = env
				out := &cappedOutput{limit: 128 << 10}
				cmd.Stdout = out
				cmd.Stderr = io.Discard
				err := cmd.Run()
				return err == nil && !out.exceeded, out.data
			}
			ok, version := run([]string{"version", "--template={{.Version}}"})
			if !ok || strings.TrimSpace(string(version)) != "v4.3.1" {
				t.Fatal("physical CLI missing or pinned version mismatch")
			}
			chart := filepath.Join(root, "chart")
			if os.MkdirAll(filepath.Join(chart, "templates"), 0700) != nil {
				t.Fatal("fixture directory failed")
			}
			for name, body := range map[string]string{"Chart.yaml": "apiVersion: v2\nname: fixture\nversion: 1.0.0\n", "values.yaml": "message: first\nhooksEnabled: false\n", "templates/config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n  namespace: app\ndata:\n  value: {{ .Values.message | quote }}\n"} {
				if os.WriteFile(filepath.Join(chart, name), []byte(body), 0600) != nil {
					t.Fatal("fixture chart write failed")
				}
			}
			hook := `{{- if .Values.hooksEnabled }}
apiVersion: batch/v1
kind: Job
metadata:
  name: fixture-hook
  namespace: app
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
    helm.sh/hook-weight: "-5"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 30
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: no-permissions
      automountServiceAccountToken: false
      securityContext: {runAsNonRoot: true, runAsUser: 10001}
      containers:
      - name: hook
        image: registry.invalid/fixture@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
        securityContext:
          runAsNonRoot: true
          readOnlyRootFilesystem: true
          allowPrivilegeEscalation: false
          capabilities: {drop: [ALL]}
{{- end }}
`
			if os.WriteFile(filepath.Join(chart, "templates", "hook.yaml"), []byte(hook), 0600) != nil {
				t.Fatal("fixture hook write failed")
			}
			values := filepath.Join(root, "second-values.yaml")
			if os.WriteFile(values, []byte("message: second\nhooksEnabled: true\n"), 0600) != nil {
				t.Fatal("fixture values failed")
			}
			var renderedHook map[string]any
			if scenario == "approved-pre-upgrade-hook" {
				ok, raw := run([]string{"template", "fixture", chart, "--namespace", "app", "--values", values, "--show-only", "templates/hook.yaml"})
				if !ok || yaml.Unmarshal(raw, &renderedHook) != nil || renderedHook["kind"] != "Job" {
					clear(raw)
					t.Fatal("pinned CLI rendered hook fixture failed")
				}
				clear(raw)
			}
			var capturedHook sync.Once
			api := &helmCLIValidationAPI{objects: map[string]map[string]any{}}
			if hookCase {
				api.advertiseBatch = true
				if strings.HasSuffix(scenario, "-delayed") {
					api.jobWatchMode = "delayed"
				}
				if strings.HasSuffix(scenario, "-failed") {
					api.jobWatchMode = "failed"
				}
				api.v3Unavailable = fallback
				fixturePath := os.Getenv("PIPEWRIGHT_TEST_OPENAPI_V2_PROTO")
				if fixturePath == "" {
					t.Fatal("PIPEWRIGHT_TEST_OPENAPI_V2_PROTO required: approved hook cannot skip native schema validation")
				}
				file, err := os.Open(fixturePath)
				if err != nil {
					t.Fatal("native public schema fixture unavailable")
				}
				api.nativeOpenAPI, err = io.ReadAll(io.LimitReader(file, kubeprotocol.MaxResponseBytes+1))
				file.Close()
				if err != nil || len(api.nativeOpenAPI) == 0 || len(api.nativeOpenAPI) > kubeprotocol.MaxResponseBytes {
					t.Fatal("native public schema fixture exceeds existing budget")
				}
				defer clear(api.nativeOpenAPI)
			}
			upstream := httptest.NewServer(api)
			defer upstream.Close()
			agent := New()
			agent.initialized = true
			agent.deadline = time.Now().Add(3 * time.Minute)
			agent.endpoint, _ = url.Parse(upstream.URL)
			agent.client = upstream.Client()
			storage := storageCodecScope()
			storage.Name = "sh.helm.release.v1.fixture"
			agent.resources = []kubeprotocol.Resource{configScope(), storage}
			if hookCase {
				agent.resources = append(agent.resources, kubeprotocol.Resource{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "fixture-hook", Actions: []string{"get", "create", "patch", "delete", "watch", "helm-hook"}})
			}
			trace := &helmCLIUpstreamTrace{RoundTripper: agent.client.Transport, status: map[uint64]int{}}
			agent.client.Transport = trace
			facts := &helmGatewayRequestFacts{trace: trace}
			gateway := httptest.NewServer(facts.handler(agent))
			defer gateway.Close()
			agent.gateway = gateway.URL
			permitCtx, stop := context.WithCancel(context.Background())
			defer stop()
			done := make(chan struct{})
			var permitFailures atomic.Int32
			go func() {
				defer close(done)
				for permitCtx.Err() == nil {
					pending := agent.Handle(permitCtx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
					if pending != nil {
						if scenario == "approved-pre-upgrade-hook" && strings.HasSuffix(pending.Request.Path, "/jobs/fixture-hook") && (pending.Request.Method == "PATCH" || pending.Request.Method == "POST") {
							capturedHook.Do(func() {
								var actual map[string]any
								if json.Unmarshal(pending.Request.Body, &actual) != nil {
									permitFailures.Add(1)
									return
								}
								metadata := mapValue(actual["metadata"])
								annotations := mapValue(metadata["annotations"])
								labels := mapValue(metadata["labels"])
								t.Logf("closed real CLI hook ownership releaseNamePresent=%v releaseNameMatches=%v releaseNamespacePresent=%v releaseNamespaceMatches=%v managedByPresent=%v managedByMatches=%v", annotations["meta.helm.sh/release-name"] != nil, annotations["meta.helm.sh/release-name"] == "fixture", annotations["meta.helm.sh/release-namespace"] != nil, annotations["meta.helm.sh/release-namespace"] == "app", labels["app.kubernetes.io/managed-by"] != nil, labels["app.kubernetes.io/managed-by"] == "Helm")
								if path := os.Getenv("PIPEWRIGHT_TEST_HELM_PENDING_OUTPUT"); path != "" {
									body, err := json.Marshal(map[string]any{"rendered": renderedHook, "request": pending.Request})
									defer clear(body)
									if err != nil || os.WriteFile(path, body, 0600) != nil {
										permitFailures.Add(1)
									}
								}
							})
						}
						permit := &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "synthetic-" + pending.ID, Digest: pending.Digest, Expires: time.Now().Add(5 * time.Second).UnixMilli(), Allow: true}
						if pending.Request.Method == "DELETE" {
							agent.mu.Lock()
							current := agent.cache[pending.Request.Path]
							metadata := mapValue(current["metadata"])
							uid, rv := stringValue(metadata["uid"]), stringValue(metadata["resourceVersion"])
							agent.mu.Unlock()
							if uid == "" || rv == "" {
								permitFailures.Add(1)
								return
							}
							cas, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": uid, "resourceVersion": rv}})
							permit.BodyStream = "synthetic-cas-" + pending.ID
							uploaded := agent.Handle(permitCtx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: permit.BodyStream, Index: 0, Count: 1, Size: len(cas), Digest: privateDigest(cas), Data: cas})
							clear(cas)
							if uploaded.Code != "ok" {
								permitFailures.Add(1)
								return
							}
						}
						reply := agent.Handle(permitCtx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit})
						if reply.Code != "ok" {
							permitFailures.Add(1)
							return
						}
					}
					time.Sleep(time.Millisecond)
				}
			}()
			defer func() { stop(); <-done }()
			cfg := filepath.Join(root, "kubeconfig")
			config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "fixture", "clusters": []any{map[string]any{"name": "fixture", "cluster": map[string]any{"server": gateway.URL}}}, "users": []any{map[string]any{"name": "none", "user": map[string]any{}}}, "contexts": []any{map[string]any{"name": "fixture", "context": map[string]any{"cluster": "fixture", "user": "none", "namespace": "app"}}}}
			body, _ := json.Marshal(config)
			if os.WriteFile(cfg, body, 0600) != nil {
				t.Fatal("fixture kubeconfig failed")
			}
			command := func(action string, revision int, valuesPath string) []string {
				return helmCommandArgs(kubeprotocol.HelmTask{Action: action, Name: "fixture", Namespace: "app", TimeoutSeconds: 30, Revision: revision, Hooks: hookCase && action == "upgrade"}, false, chart, valuesPath, cfg)
			}
			check := func(action string, revision int, valuesPath, want string) {
				t.Helper()
				succeeded, out := run(command(action, revision, valuesPath))
				clear(out)
				requests, rejected, forceTrue, forceFalse, forceAbsent, last := facts.snapshot()
				facts.Lock()
				watchListAttempts, canceled := facts.watchListAttempts, facts.canceledWatch
				facts.Unlock()
				if watchListAttempts != 0 {
					t.Fatal("official fixed WatchListClient=false environment did not disable WatchList negotiation")
				}
				t.Logf("closed CLI lifecycle cancellation count=%d; WatchList attempts=%d", canceled, watchListAttempts)
				if !succeeded || rejected != 0 || forceTrue != 0 || permitFailures.Load() != 0 {
					api.mu.Lock()
					schema, jobs, unexpected, category := api.schemaReads, api.jobWrites, api.unexpected, api.unexpectedCategory
					api.mu.Unlock()
					facts.Lock()
					hook404 := facts.hookAbsent
					facts.Unlock()
					t.Fatalf("closed CLI phase=%s success=%v requests=%d rejected=%d forceTrue=%d forceFalse=%d forceAbsent=%d schemaReads=%d hook404=%d hookWrites=%d fixtureRejected=%d fixtureCategory=%s last=%s", action, succeeded, requests, rejected, forceTrue, forceFalse, forceAbsent, schema, hook404, jobs, unexpected, category, last)
				}
				api.mu.Lock()
				object := api.objects["/api/v1/namespaces/app/configmaps/config"]
				value := mapValue(object["data"])["value"]
				schema, unexpected, category := api.schemaReads, api.unexpected, api.unexpectedCategory
				api.mu.Unlock()
				expectedSchema := hookCase && fallback && action != "install"
				if value != want || (!expectedSchema && schema != 0) || (expectedSchema && schema == 0) || unexpected != 0 {
					t.Fatalf("closed CLI phase=%s approvedValueMatches=%v schema=%d fixtureRejected=%d category=%s", action, value == want, schema, unexpected, category)
				}
				if action == "upgrade" || action == "rollback" {
					if forceFalse == 0 {
						t.Fatal("real SSA did not expose explicit false force query")
					}
				}
				t.Logf("closed CLI phase=%s accepted; forceTrue=%d forceFalse=%d; approved value matched", action, forceTrue, forceFalse)
			}
			check("install", 0, "", "first")
			if scenario == "force-true-is-rejected" {
				args := append(command("upgrade", 0, values), "--force-conflicts=true")
				succeeded, out := run(args)
				clear(out)
				_, rejected, forceTrue, _, _, last := facts.snapshot()
				api.mu.Lock()
				business := api.businessWrites
				api.mu.Unlock()
				if succeeded || rejected == 0 || forceTrue == 0 || business != 1 {
					t.Fatalf("force prohibition failed success=%v rejected=%d forceTrue=%d business=%d last=%s", succeeded, rejected, forceTrue, business, last)
				}
				t.Logf("closed CLI force=true rejected before additional business write; last=%s", last)
				return
			}
			if scenario == "approved-pre-upgrade-hook-failed" {
				api.mu.Lock()
				beforeBusiness := api.businessWrites
				api.mu.Unlock()
				succeeded, output := run(command("upgrade", 0, values))
				clear(output)
				api.mu.Lock()
				watches, jobs, business := api.jobWatches, api.jobWrites, api.businessWrites
				api.mu.Unlock()
				_, rejected, _, _, _, last := facts.snapshot()
				if succeeded || watches < 2 || jobs != 1 || business != beforeBusiness || rejected != 0 || permitFailures.Load() != 0 {
					t.Fatalf("failed hook was not observed before business update success=%v watches=%d jobs=%d business=%d rejected=%d last=%s", succeeded, watches, jobs, business, rejected, last)
				}
				t.Log("actual failed hook stopped upgrade before business mutation; exact named watch observed failure")
				return
			}
			check("upgrade", 0, values, "second")
			if hookCase {
				api.mu.Lock()
				created, deleted := api.jobWrites, api.jobDeletes
				watches := api.jobWatches
				remaining := api.objects["/apis/batch/v1/namespaces/app/jobs/fixture-hook"] != nil
				api.mu.Unlock()
				if created < 1 || deleted < 1 || remaining || scenario == "approved-pre-upgrade-hook-delayed" && watches < 2 {
					t.Fatal("approved hook did not create, complete and delete")
				}
				t.Log("approved pre-upgrade Job created, completed and deleted through actual proxy/native schema")
			}
			check("rollback", 1, "", "first")
			succeeded, out := run(command("status", 0, ""))
			defer clear(out)
			var release struct {
				Version int `json:"version"`
			}
			if !succeeded || json.Unmarshal(out, &release) != nil || release.Version != 3 {
				t.Fatal("actual rollback status did not record new revision3")
			}
			agent.mu.Lock()
			consumptions := len(agent.consumed)
			agent.mu.Unlock()
			api.mu.Lock()
			business := api.businessWrites
			api.mu.Unlock()
			if business != 3 || consumptions < 6 {
				t.Fatal("lifecycle did not use distinct approved writes")
			}
		})
	}
}

// Transparent test-only transport observation: no request/header/body mutation.
// Context IDs correlate each guarded request to its real loopback HTTP status.
type helmCLITraceKey struct{}
type helmCLIUpstreamTrace struct {
	http.RoundTripper
	sync.Mutex
	status map[uint64]int
}

func (t *helmCLIUpstreamTrace) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(request)
	if id, ok := request.Context().Value(helmCLITraceKey{}).(uint64); ok {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Lock()
		t.status[id] = status
		t.Unlock()
	}
	return response, err
}

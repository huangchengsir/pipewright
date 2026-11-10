package kubeagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/labels"
)

// Optional physical CLI regression: the explicit executable must be Helm 4.3.1.
// This server is a credential-free in-process fixture, not a Kubernetes cluster.
// It reproduces the gateway's JSON-only OpenAPI representation and counts real
// CLI requests; production authorization/Strict admission are tested separately.
func TestHelm431CLIJSONGatewayValidationPolicy(t *testing.T) {
	binary := os.Getenv("PIPEWRIGHT_TEST_HELM4")
	if binary == "" {
		t.Skip("PIPEWRIGHT_TEST_HELM4 not set: physical Helm 4.3.1 CLI not executed")
	}
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
		t.Fatal("physical CLI missing or pinned Helm version mismatch")
	}
	chart := filepath.Join(root, "chart")
	if os.MkdirAll(filepath.Join(chart, "templates"), 0700) != nil {
		t.Fatal("fixture directory failed")
	}
	for name, body := range map[string]string{"Chart.yaml": "apiVersion: v2\nname: fixture\nversion: 1.0.0\n", "templates/config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n  namespace: app\ndata:\n  value: fixture\n"} {
		if os.WriteFile(filepath.Join(chart, name), []byte(body), 0600) != nil {
			t.Fatal("fixture write failed")
		}
	}
	for _, clientValidationDisabled := range []bool{false, true} {
		name := "json-only-schema-upstream-rejected"
		if clientValidationDisabled {
			name = "controlled-json-client"
		}
		t.Run(name, func(t *testing.T) {
			api := &helmCLIValidationAPI{objects: map[string]map[string]any{}, v3Unavailable: !clientValidationDisabled}
			upstream := httptest.NewServer(api)
			defer upstream.Close()
			agent := New()
			agent.initialized = true
			agent.deadline = time.Now().Add(time.Minute)
			agent.endpoint, _ = url.Parse(upstream.URL)
			agent.client = upstream.Client()
			storage := storageCodecScope()
			storage.Name = "sh.helm.release.v1.fixture"
			agent.resources = []kubeprotocol.Resource{configScope(), storage}
			server := httptest.NewServer(http.HandlerFunc(agent.proxy))
			defer server.Close()
			agent.gateway = server.URL
			permitCtx, stopPermits := context.WithCancel(context.Background())
			defer stopPermits()
			permitDone := make(chan struct{})
			var permitFailures atomic.Int32
			go func() {
				defer close(permitDone)
				for permitCtx.Err() == nil {
					pending := agent.Handle(permitCtx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
					if pending != nil {
						decision := agent.Handle(permitCtx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "synthetic-" + pending.ID, Digest: pending.Digest, Expires: time.Now().Add(5 * time.Second).UnixMilli(), Allow: true}})
						if decision.Code != "ok" {
							permitFailures.Add(1)
							return
						}
					}
					time.Sleep(time.Millisecond)
				}
			}()
			defer func() { stopPermits(); <-permitDone }()
			cfg := filepath.Join(root, name+"-config")
			config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "fixture", "clusters": []any{map[string]any{"name": "fixture", "cluster": map[string]any{"server": server.URL}}}, "users": []any{map[string]any{"name": "none", "user": map[string]any{}}}, "contexts": []any{map[string]any{"name": "fixture", "context": map[string]any{"cluster": "fixture", "user": "none", "namespace": "app"}}}}
			b, _ := json.Marshal(config)
			if os.WriteFile(cfg, b, 0600) != nil {
				t.Fatal("fixture kubeconfig failed")
			}
			args := helmCommandArgs(kubeprotocol.HelmTask{Action: "install", Name: "fixture", Namespace: "app", TimeoutSeconds: 30}, false, chart, "", cfg)
			if !clientValidationDisabled {
				args = slices.DeleteFunc(args, func(s string) bool { return s == "--disable-openapi-validation" })
			}
			succeeded, output := run(args)
			defer clear(output)
			agent.mu.Lock()
			uniqueConsumptions := len(agent.consumed)
			agent.mu.Unlock()
			if permitFailures.Load() != 0 {
				t.Fatal("synthetic controller single-use permit rejected")
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if !clientValidationDisabled {
				if succeeded || api.schemaReads == 0 || api.writes != 0 {
					t.Fatalf("old client did not reproduce bounded JSON/protobuf failure (success=%v schema=%d writes=%d)", succeeded, api.schemaReads, api.writes)
				}
				t.Log("closed cause: native_openapi_representation_required; zero writes")
			} else {
				var release struct {
					Version int `json:"version"`
				}
				decoded := json.Unmarshal(output, &release) == nil
				if !succeeded || !decoded || release.Version != 1 || api.schemaReads != 0 || api.businessWrites != 1 || api.writes < 2 || uniqueConsumptions < 2 || api.unexpected != 0 {
					t.Fatalf("controlled real Helm install failed (success=%v decoded=%v revision=%d schema=%d business=%d writes=%d unexpected=%d category=%s)", succeeded, decoded, release.Version, api.schemaReads, api.businessWrites, api.writes, api.unexpected, api.unexpectedCategory)
				}
				t.Log("real pinned CLI install completed; zero OpenAPI reads; one ConfigMap write; distinct single-use action permits")
			}
		})
	}
}

type helmCLIValidationAPI struct {
	mu                                              sync.Mutex
	objects                                         map[string]map[string]any
	schemaReads, writes, businessWrites, unexpected int
	jobWrites, jobDeletes                           int
	unexpectedCategory                              string
	nativeOpenAPI                                   []byte
	advertiseBatch                                  bool
	v3Unavailable                                   bool
	jobWatchMode                                    string
	jobWatches                                      int
}

func (a *helmCLIValidationAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	notFound := func() {
		w.WriteHeader(404)
		write(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404})
	}
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/version":
			write(map[string]any{"major": "1", "minor": "36", "gitVersion": "v1.36.5", "gitCommit": "fixture", "platform": "linux/amd64"})
			return
		case "/api":
			write(map[string]any{"apiVersion": "v1", "kind": "APIVersions", "versions": []string{"v1"}})
			return
		case "/apis":
			if !a.advertiseBatch {
				write(map[string]any{"apiVersion": "v1", "kind": "APIGroupList", "groups": []any{}})
				return
			}
			write(map[string]any{"apiVersion": "v1", "kind": "APIGroupList", "groups": []any{map[string]any{"name": "batch", "versions": []any{map[string]any{"groupVersion": "batch/v1", "version": "v1"}}, "preferredVersion": map[string]any{"groupVersion": "batch/v1", "version": "v1"}}}})
			return
		case "/apis/batch":
			write(map[string]any{"apiVersion": "v1", "kind": "APIGroup", "name": "batch", "versions": []any{map[string]any{"groupVersion": "batch/v1", "version": "v1"}}, "preferredVersion": map[string]any{"groupVersion": "batch/v1", "version": "v1"}})
			return
		case "/apis/batch/v1":
			write(map[string]any{"apiVersion": "v1", "kind": "APIResourceList", "groupVersion": "batch/v1", "resources": []any{map[string]any{"name": "jobs", "singularName": "job", "kind": "Job", "namespaced": true, "verbs": []string{"get", "list", "watch", "create", "patch", "delete"}}}})
			return
		case "/apis/batch/v1/namespaces/app/jobs":
			items := []any{}
			if object := a.objects["/apis/batch/v1/namespaces/app/jobs/fixture-hook"]; object != nil {
				items = append(items, object)
			}
			if r.URL.Query().Get("watch") == "true" {
				a.jobWatches++
				if a.jobWatchMode != "" {
					time.Sleep(25 * time.Millisecond)
				}
				if a.jobWatchMode != "" && a.jobWatches >= 2 {
					for _, value := range items {
						object := value.(map[string]any)
						condition := "Complete"
						if a.jobWatchMode == "failed" {
							condition = "Failed"
						}
						object["status"] = map[string]any{"conditions": []any{map[string]any{"type": condition, "status": "True"}}}
						if condition == "Complete" {
							mapValue(object["status"])["succeeded"] = 1
						}
					}
				}
				for _, object := range items {
					write(map[string]any{"type": "MODIFIED", "object": object})
				}
				return
			}
			write(map[string]any{"apiVersion": "batch/v1", "kind": "JobList", "metadata": map[string]any{"resourceVersion": "1"}, "items": items})
			return
		case "/api/v1":
			write(map[string]any{"apiVersion": "v1", "kind": "APIResourceList", "groupVersion": "v1", "resources": []any{map[string]any{"name": "configmaps", "singularName": "configmap", "kind": "ConfigMap", "namespaced": true, "verbs": []string{"get", "list", "create", "patch", "update", "delete"}}, map[string]any{"name": "secrets", "singularName": "secret", "kind": "Secret", "namespaced": true, "verbs": []string{"get", "list", "create", "patch", "update", "delete"}}}})
			return
		case "/openapi/v3":
			if a.v3Unavailable {
				notFound()
				return
			}
			paths := map[string]any{"api/v1": map[string]any{"serverRelativeURL": "/openapi/v3/api/v1"}}
			if a.advertiseBatch {
				paths["apis/batch/v1"] = map[string]any{"serverRelativeURL": "/openapi/v3/apis/batch/v1"}
			}
			write(map[string]any{"paths": paths})
			return
		case "/openapi/v3/api/v1", "/openapi/v3/apis/batch/v1":
			group, version, kind, resource := "", "v1", "ConfigMap", "configmaps"
			if strings.Contains(r.URL.Path, "/apis/batch/") {
				group, kind, resource = "batch", "Job", "jobs"
			}
			path := "/api/v1/namespaces/{namespace}/" + resource
			if group != "" {
				path = "/apis/" + group + "/" + version + "/namespaces/{namespace}/" + resource
			}
			operation := map[string]any{"x-kubernetes-group-version-kind": map[string]any{"group": group, "version": version, "kind": kind}, "parameters": []any{map[string]any{"name": "fieldValidation", "in": "query", "schema": map[string]any{"type": "string"}}}, "responses": map[string]any{"200": map[string]any{"description": "synthetic fixture"}}}
			write(map[string]any{"openapi": "3.0.0", "info": map[string]any{"title": "fixture", "version": "v1"}, "paths": map[string]any{path: map[string]any{"post": operation}, path + "/{name}": map[string]any{"patch": operation, "get": operation}}})
			return
		case "/openapi/v2":
			a.schemaReads++
			if r.Header.Get("Accept") == helmOpenAPIProtobufAccept && len(a.nativeOpenAPI) > 0 {
				w.Header().Set("Content-Type", helmOpenAPIProtobufContentType)
				_, _ = w.Write(a.nativeOpenAPI)
				return
			}
			write(map[string]any{"swagger": "2.0", "info": map[string]any{"title": "fixture", "version": "v1"}, "paths": map[string]any{}})
			return
		case "/api/v1/namespaces/app/secrets":
			selector, err := labels.Parse(r.URL.Query().Get("labelSelector"))
			if err != nil {
				a.reject(r, nil, "selector_invalid")
				w.WriteHeader(400)
				return
			}
			items := []any{}
			for key, obj := range a.objects {
				metadata, _ := obj["metadata"].(map[string]any)
				actual, _ := metadata["labels"].(map[string]any)
				set := labels.Set{}
				for name, value := range actual {
					if text, ok := value.(string); ok {
						set[name] = text
					}
				}
				if strings.Contains(key, "/secrets/") && selector.Matches(set) {
					items = append(items, obj)
				}
			}
			write(map[string]any{"apiVersion": "v1", "kind": "SecretList", "items": items})
			return
		}
		if obj := a.objects[r.URL.Path]; obj != nil {
			write(obj)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/app/configmaps/") || strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/app/secrets/") || r.URL.Path == "/apis/batch/v1/namespaces/app/jobs/fixture-hook" {
			notFound()
			return
		}
	}
	secret := strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/app/secrets")
	config := strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/app/configmaps")
	job := r.URL.Path == "/apis/batch/v1/namespaces/app/jobs" || r.URL.Path == "/apis/batch/v1/namespaces/app/jobs/fixture-hook"
	if job && r.Method == "DELETE" {
		var options map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&options) != nil {
			a.reject(r, nil, "delete_options")
			w.WriteHeader(400)
			return
		}
		object := a.objects["/apis/batch/v1/namespaces/app/jobs/fixture-hook"]
		metadata := mapValue(object["metadata"])
		pre := mapValue(options["preconditions"])
		if object == nil || pre["uid"] != metadata["uid"] || pre["resourceVersion"] != metadata["resourceVersion"] {
			a.reject(r, nil, "delete_CAS")
			w.WriteHeader(409)
			return
		}
		delete(a.objects, "/apis/batch/v1/namespaces/app/jobs/fixture-hook")
		a.jobDeletes++
		write(map[string]any{"apiVersion": "v1", "kind": "Status", "status": "Success"})
		return
	}
	if (secret || config || job) && (r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH") {
		body, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		if err != nil || len(body) > 64<<10 {
			a.reject(r, body, "body_limit")
			w.WriteHeader(413)
			return
		}
		var obj map[string]any
		// Helm JSON marshaling also has YAML-compatible input semantics.
		if yaml.Unmarshal(body, &obj) != nil {
			a.reject(r, body, "body_decode")
			w.WriteHeader(400)
			return
		}
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if name == "" {
			a.reject(r, body, "metadata_name")
			w.WriteHeader(400)
			return
		}
		meta["uid"] = "fixture-" + name
		meta["resourceVersion"] = "1"
		key := r.URL.Path
		if r.Method == "POST" {
			key += "/" + name
		}
		if job {
			meta["namespace"] = "app"
			obj["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			if a.jobWatchMode != "" {
				obj["status"] = map[string]any{"active": 1}
			}
			a.jobWrites++
		}
		a.objects[key] = obj
		a.writes++
		if config {
			a.businessWrites++
		}
		if r.Method == "POST" {
			w.WriteHeader(201)
		}
		write(obj)
		return
	}
	a.reject(r, nil, "unhandled_route")
	notFound()
}
func (a *helmCLIValidationAPI) reject(r *http.Request, body []byte, reason string) {
	a.unexpected++
	// Closed request categories only; never echo path names, query or bodies.
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
	category := "other"
	switch r.URL.Path {
	case "/api/v1/namespaces/app":
		category = "namespace_named"
	case "/api/v1/namespaces":
		category = "namespace_collection"
	case "/api/v1/namespaces/app/configmaps":
		category = "configmap_collection"
	case "/api/v1/namespaces/app/secrets":
		category = "secret_collection"
	case "/openapi/v3":
		category = "openapi_v3"
	case "/openapi/v3/api/v1":
		category = "openapi_v3_core"
	case "/api/v1/namespaces/app/events":
		category = "events_collection"
	case "/api/v1/namespaces/app/configmaps/config":
		category = "configmap_named"
	case "/apis/batch/v1/namespaces/app/jobs":
		category = "hook_collection"
	case "/apis/batch/v1/namespaces/app/jobs/fixture-hook":
		category = "hook_named"
	}
	content := "other"
	switch r.Header.Get("Content-Type") {
	case "application/json":
		content = "json"
	case "application/vnd.kubernetes.protobuf":
		content = "protobuf"
	case "application/apply-patch+yaml":
		content = "apply"
	}
	magic := "none"
	if len(body) > 0 {
		magic = "other"
		if json.Valid(body) {
			magic = "json"
		}
		if len(body) >= 4 && string(body[:4]) == "k8s\x00" {
			magic = "protobuf"
		}
	}
	a.unexpectedCategory = method + "_" + category + "_" + content + "_" + magic + "_" + reason
}

package kubeagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

func hookWatchScope(name string) kubeprotocol.Resource {
	s := hookDeleteScope()
	s.Name = name
	s.Actions = []string{"get", "watch", "helm-hook"}
	return s
}
func hookWatchEvent(name, condition string) map[string]any {
	return map[string]any{"type": "MODIFIED", "object": map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": name, "namespace": "app", "uid": "uid-" + name, "resourceVersion": "opaque-" + name}, "status": map[string]any{"conditions": []any{map[string]any{"type": condition, "status": "True"}}}}}
}
func TestPrivateHelmHookWatchFansOutExactNamesAndPreservesRealEvents(t *testing.T) {
	var calls atomic.Int32
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		name := strings.TrimPrefix(q.Get("fieldSelector"), "metadata.name=")
		if r.Method != "GET" || r.URL.Path != "/apis/batch/v1/namespaces/app/jobs" || name != "first" && name != "second" || q.Get("timeoutSeconds") != "1" || q.Get("resourceVersion") != "opaque-start" || r.Header.Get("Authorization") != "Bearer fixture-private-token" {
			t.Error("broad or unpinned upstream watch")
			w.WriteHeader(403)
			return
		}
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(hookWatchEvent("foreign", "Complete"))
		condition := "Complete"
		if name == "second" {
			condition = "Failed"
		}
		_ = json.NewEncoder(w).Encode(hookWatchEvent(name, condition))
	}), []kubeprotocol.Resource{hookWatchScope("first"), hookWatchScope("second")})
	gateway := httptest.NewServer(http.HandlerFunc(a.proxy))
	defer gateway.Close()
	a.gateway = gateway.URL
	response, err := gateway.Client().Get(gateway.URL + "/apis/batch/v1/namespaces/app/jobs?watch=true&resourceVersion=opaque-start")
	if err != nil {
		t.Fatal("private watch failed")
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || calls.Load() != 2 || strings.Contains(string(body), "foreign") || !strings.Contains(string(body), "opaque-first") || !strings.Contains(string(body), "opaque-second") || !strings.Contains(string(body), "Failed") || !strings.Contains(string(body), "Complete") {
		t.Fatal("multiple hook real late events lost or foreign object leaked")
	}
	before := calls.Load()
	if _, err := a.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/apis/batch/v1/namespaces/app/jobs", Query: "watch=true&resourceVersion=opaque-start"}); err == nil || calls.Load() != before {
		t.Fatal("RPC gained unscoped collection watch")
	}
}
func TestPrivateHelmHookWatchRejectsAmbiguousOrUnauthorizedInput(t *testing.T) {
	for _, scenario := range []string{"no-hook", "no-watch", "no-get", "namespace", "group", "plain", "field-selector", "duplicate", "foreign-query", "body", "expired", "too-many"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }), []kubeprotocol.Resource{hookWatchScope("first")})
			in := kubeprotocol.Request{Method: "GET", Path: "/apis/batch/v1/namespaces/app/jobs", Query: "watch=true&resourceVersion=opaque"}
			switch scenario {
			case "no-hook":
				a.resources[0].Actions = []string{"get", "watch"}
			case "no-watch":
				a.resources[0].Actions = []string{"get", "helm-hook"}
			case "no-get":
				a.resources[0].Actions = []string{"watch", "helm-hook"}
			case "namespace":
				in.Path = "/apis/batch/v1/namespaces/other/jobs"
			case "group":
				in.Path = "/apis/other/v1/namespaces/app/jobs"
			case "plain":
				in.Query = "resourceVersion=opaque"
			case "field-selector":
				in.Query += "&fieldSelector=metadata.name%3Dforeign"
			case "duplicate":
				in.Query += "&watch=true"
			case "foreign-query":
				in.Query += "&labelSelector=private"
			case "body":
				in.Body = []byte("private-watch-canary")
			case "expired":
				a.deadline = time.Now().Add(-time.Second)
			case "too-many":
				for i := 0; i < 129; i++ {
					s := hookWatchScope("hook-" + string(rune('a'+i)))
					a.resources = append(a.resources, s)
				}
			}
			result, err := a.readHelmHookWatch(context.Background(), in)
			if err == nil || len(result.Body) != 0 || calls.Load() != 0 {
				t.Fatal("invalid watch reached upstream or returned content")
			}
		})
	}
}
func TestPrivateHelmHookWatchBudgetFailuresAndCancellationAreAtomic(t *testing.T) {
	for _, scenario := range []string{"upstream-error", "stream-error", "invalid-json", "events", "bytes", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			var active atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				active.Add(1)
				defer active.Add(-1)
				name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
				if scenario == "cancel" {
					<-r.Context().Done()
					return
				}
				if scenario == "upstream-error" && name == "second" {
					w.WriteHeader(403)
					return
				}
				if scenario == "stream-error" {
					_ = json.NewEncoder(w).Encode(map[string]any{"type": "ERROR", "object": map[string]any{"apiVersion": "v1", "kind": "Status", "reason": "Forbidden", "code": 403}})
					return
				}
				if scenario == "invalid-json" {
					_, _ = w.Write([]byte("invalid"))
					return
				}
				event := hookWatchEvent(name, "Complete")
				count := 1
				if scenario == "events" {
					count = 2050
				}
				if scenario == "bytes" {
					event["padding"] = strings.Repeat("x", 2200<<10)
				}
				for i := 0; i < count; i++ {
					_ = json.NewEncoder(w).Encode(event)
				}
			}), []kubeprotocol.Resource{hookWatchScope("first"), hookWatchScope("second")})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if scenario != "cancel" {
				ctx = context.Background()
			}
			result, err := a.readHelmHookWatch(ctx, kubeprotocol.Request{Method: "GET", Path: "/apis/batch/v1/namespaces/app/jobs", Query: url.Values{"watch": {"true"}}.Encode()})
			if err == nil || len(result.Body) != 0 {
				t.Fatal("partial/overbudget watch returned success")
			}
			deadline := time.Now().Add(time.Second)
			for active.Load() != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if active.Load() != 0 {
				t.Fatal("canceled watch transport not cleaned")
			}
		})
	}
}

package kubeagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

func hookDeleteScope() kubeprotocol.Resource {
	return kubeprotocol.Resource{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "hook", Actions: []string{"get", "delete", "helm-hook"}}
}
func TestMinimalHelmHookDeleteOnlyNormalizesExactGrantedJSONOptions(t *testing.T) {
	for _, scenario := range []string{"valid", "no-hook", "no-get", "no-delete", "foreign-name", "foreign-namespace", "wrong-kind", "query", "foreign-field", "wrong-policy", "duplicate", "wrong-type-meta", "protobuf"} {
		t.Run(scenario, func(t *testing.T) {
			a := New()
			a.initialized = true
			a.deadline = time.Now().Add(time.Minute)
			a.resources = []kubeprotocol.Resource{hookDeleteScope()}
			in := kubeprotocol.Request{Method: "DELETE", Path: "/apis/batch/v1/namespaces/app/jobs/hook", ContentType: "application/json", Body: []byte(`{"propagationPolicy":"Background"}`)}
			switch scenario {
			case "no-hook":
				a.resources[0].Actions = []string{"get", "delete"}
			case "no-get":
				a.resources[0].Actions = []string{"delete", "helm-hook"}
			case "no-delete":
				a.resources[0].Actions = []string{"get", "helm-hook"}
			case "foreign-name":
				in.Path += "-other"
			case "foreign-namespace":
				in.Path = "/apis/batch/v1/namespaces/other/jobs/hook"
			case "wrong-kind":
				a.resources[0].Kind = "ConfigMap"
			case "query":
				in.Query = "dryRun=All"
			case "foreign-field":
				in.Body = []byte(`{"propagationPolicy":"Background","private":"private-delete-canary"}`)
			case "wrong-policy":
				in.Body = []byte(`{"propagationPolicy":"Foreground"}`)
			case "duplicate":
				in.Body = []byte(`{"propagationPolicy":"Foreground","propagationPolicy":"Background"}`)
			case "wrong-type-meta":
				in.Body = []byte(`{"apiVersion":"other/v1","kind":"DeleteOptions","propagationPolicy":"Background"}`)
			case "protobuf":
				in.ContentType = helmStorageProtobufType
			}
			out, err := a.normalizeHelmHookDeleteRequest(in)
			var object map[string]any
			_ = json.Unmarshal(out.Body, &object)
			normalized := object["apiVersion"] == "v1" && object["kind"] == "DeleteOptions"
			if scenario == "valid" {
				if err != nil || !normalized || releaseHasPreconditions(object) {
					t.Fatal("minimal exact hook options not safely normalized")
				}
			} else if normalized {
				t.Fatal("foreign or ambiguous delete normalized")
			}
			if err != nil && err.Error() != errDenied.Error() {
				t.Fatal("raw hook error exposed")
			}
			if len(a.PendingIDs()) != 0 {
				t.Fatal("normalization generated action")
			}
		})
	}
}
func releaseHasPreconditions(object map[string]any) bool { return object["preconditions"] != nil }
func TestMinimalHelmHookDeleteAbsentOrSingleCASPermit(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent-no-write", true: "present-CAS-permit"}[present], func(t *testing.T) {
			var reads, writes atomic.Int32
			a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					reads.Add(1)
					if !present {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","reason":"NotFound","code":404}`))
						return
					}
					_, _ = w.Write([]byte(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"hook","namespace":"app","uid":"owned-hook","resourceVersion":"7"}}`))
					return
				}
				writes.Add(1)
				var options map[string]any
				_ = json.NewDecoder(r.Body).Decode(&options)
				pre := mapValue(options["preconditions"])
				if r.Method != "DELETE" || pre["uid"] != "owned-hook" || pre["resourceVersion"] != "7" {
					t.Error("hook delete did not use host CAS replacement")
				}
				_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Success"}`))
			}), []kubeprotocol.Resource{hookDeleteScope()})
			gateway := httptest.NewServer(http.HandlerFunc(a.proxy))
			defer gateway.Close()
			a.gateway = gateway.URL
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan int, 1)
			go func() {
				request, _ := http.NewRequestWithContext(ctx, "DELETE", gateway.URL+"/apis/batch/v1/namespaces/app/jobs/hook", strings.NewReader(`{"propagationPolicy":"Background"}`))
				request.Header.Set("Content-Type", "application/json")
				response, err := gateway.Client().Do(request)
				if err != nil {
					done <- 0
					return
				}
				defer response.Body.Close()
				_, _ = io.Copy(io.Discard, response.Body)
				done <- response.StatusCode
			}()
			if !present {
				if <-done != 404 || writes.Load() != 0 || reads.Load() != 1 || len(a.PendingIDs()) != 0 {
					t.Fatal("absent hook was deleted or fenced as a new write")
				}
				return
			}
			var pending *kubeprotocol.PendingWrite
			for pending == nil && ctx.Err() == nil {
				pending = a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "poll"}).Pending
				if pending == nil {
					time.Sleep(time.Millisecond)
				}
			}
			if pending == nil || writes.Load() != 0 {
				t.Fatal("present hook bypassed host fence")
			}
			cas := []byte(`{"apiVersion":"v1","kind":"DeleteOptions","propagationPolicy":"Foreground","preconditions":{"uid":"owned-hook","resourceVersion":"7"}}`)
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "upload", StreamID: "hook-cas", Index: 0, Count: 1, Size: len(cas), Digest: privateDigest(cas), Data: cas}).Code != "ok" {
				t.Fatal("CAS fixture upload failed")
			}
			permit := &kubeprotocol.Permit{RequestID: pending.ID, ActionID: "hook-delete-action", Digest: pending.Digest, Expires: time.Now().Add(time.Second).UnixMilli(), Allow: true, BodyStream: "hook-cas"}
			if a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}).Code != "ok" {
				t.Fatal("hook CAS permit rejected")
			}
			if <-done != 200 || writes.Load() != 1 || a.Handle(ctx, kubeprotocol.Frame{Version: 2, Command: "permit", Permit: permit}).Code == "ok" {
				t.Fatal("hook CAS not single-use")
			}
			// The raw RPC path remains strict: no TypeMeta is added there.
			before := reads.Load()
			if _, err := a.Do(ctx, kubeprotocol.Request{Method: "DELETE", Path: "/apis/batch/v1/namespaces/app/jobs/hook", ContentType: "application/json", Body: []byte(`{"propagationPolicy":"Background"}`)}); err == nil || reads.Load() != before || writes.Load() != 1 {
				t.Fatal("raw RPC gained unapproved options normalization")
			}
		})
	}
}

package kubeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReview106HookWatchWindowEndsWithStandardRelistSignal(t *testing.T) {
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fieldSelector") != "metadata.name=hook" {
			t.Error("watch was not exact name")
		}
		_ = json.NewEncoder(w).Encode(hookWatchEvent("hook", "Complete"))
	}), []kubeprotocol.Resource{hookWatchScope("hook")})
	result, e := a.readHelmHookWatch(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/apis/batch/v1/namespaces/app/jobs", Query: "watch=true&resourceVersion=opaque"})
	if e != nil {
		t.Fatal(e)
	}
	decoder := json.NewDecoder(strings.NewReader(string(result.Body)))
	types := []string{}
	code := 0
	for {
		var event struct {
			Type   string `json:"type"`
			Object struct {
				Kind   string `json:"kind"`
				Code   int    `json:"code"`
				Reason string `json:"reason"`
			} `json:"object"`
		}
		e := decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		types = append(types, event.Type)
		if event.Type == "ERROR" {
			if event.Object.Kind != "Status" || event.Object.Reason != "Expired" {
				t.Fatal("not a standard expiration event")
			}
			code = event.Object.Code
		}
	}
	if len(types) != 2 || types[0] != "MODIFIED" || types[1] != "ERROR" || code != 410 {
		t.Fatal("closed independent name windows were exposed as one continuous collection cursor")
	}
}

func TestReview106HookWatch128ExactNamesAndBoundedConcurrency(t *testing.T) {
	var active, peak atomic.Int32
	var mu sync.Mutex
	seen := map[string]bool{}
	scopes := []kubeprotocol.Resource{}
	for i := 0; i < 128; i++ {
		scopes = append(scopes, hookWatchScope(fmt.Sprintf("hook-%03d", i)))
	}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); count > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, count) {
				break
			}
		}
		name := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
		if r.URL.Query().Get("timeoutSeconds") != "1" || !strings.HasPrefix(name, "hook-") {
			t.Error("scope/window expanded")
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		seen[name] = true
		mu.Unlock()
		time.Sleep(time.Millisecond)
		_ = json.NewEncoder(w).Encode(hookWatchEvent(name, "Complete"))
	}), scopes)
	result, e := a.readHelmHookWatch(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/apis/batch/v1/namespaces/app/jobs", Query: "watch=true&resourceVersion=opaque"})
	if e != nil {
		t.Fatal(e)
	}
	if len(seen) != 128 || peak.Load() > 8 || active.Load() != 0 {
		t.Fatal("frozen name coverage or worker bound changed", len(seen), peak.Load(), active.Load())
	}
	dec := json.NewDecoder(strings.NewReader(string(result.Body)))
	events, expired := 0, 0
	for {
		var row struct {
			Type string `json:"type"`
		}
		e := dec.Decode(&row)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		events++
		if row.Type == "ERROR" {
			expired++
		}
	}
	if events != 129 || expired != 1 {
		t.Fatal("window omitted names or its terminal resync signal", events, expired)
	}
}

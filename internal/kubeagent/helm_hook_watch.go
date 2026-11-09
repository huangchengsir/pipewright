package kubeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

// client-go v0.37.1 exposes this non-locked beta gate via its official envvar
// feature gates. Keep the CLI on ordinary list/watch; WatchList's initial-events
// negotiation is deliberately outside this gateway's protocol.
const helmWatchListEnvironment = "KUBE_FEATURE_WatchListClient=false"

// Helm 4's hookOnly informer watches a collection without a field selector.
// This compatibility is private-proxy-only: it never sends that broad request.
func helmHookCollectionWatch(in kubeprotocol.Request) bool {
	q, err := url.ParseQuery(in.Query)
	return err == nil && in.Method == http.MethodGet && q.Get("watch") == "true" && q.Get("fieldSelector") == ""
}
func (a *Agent) readHelmHookWatch(ctx context.Context, in kubeprotocol.Request) (kubeprotocol.APIResult, error) {
	result := kubeprotocol.APIResult{}
	q, err := url.ParseQuery(in.Query)
	if err != nil || len(in.Body) != 0 || validateTransportTimeout(q) != nil {
		return result, errDenied
	}
	for key, values := range q {
		if len(values) != 1 {
			return result, errDenied
		}
		switch key {
		case "watch", "resourceVersion", "allowWatchBookmarks", "timeoutSeconds", "timeout":
		default:
			return result, errDenied
		}
	}
	if q.Get("watch") != "true" || q.Get("allowWatchBookmarks") != "" && q.Get("allowWatchBookmarks") != "true" && q.Get("allowWatchBookmarks") != "false" {
		return result, errDenied
	}
	a.mu.Lock()
	if !a.initialized || time.Now().After(a.deadline) {
		a.mu.Unlock()
		return result, errDenied
	}
	deadline := a.deadline
	scopes := []kubeprotocol.Resource{}
	seen := map[string]bool{}
	for _, scope := range a.resources {
		if resourcePath(scope, true) != in.Path || scope.Namespace == "" || !has(scope.Actions, "helm-hook") || !has(scope.Actions, "get") || !has(scope.Actions, "watch") {
			continue
		}
		if !(scope.Group == "batch" && scope.Version == "v1" && scope.Resource == "jobs" && scope.Kind == "Job" || scope.Group == "" && scope.Version == "v1" && scope.Resource == "pods" && scope.Kind == "Pod") {
			continue
		}
		if !seen[scope.Name] {
			scopes = append(scopes, scope)
			seen[scope.Name] = true
		}
	}
	a.mu.Unlock()
	// The product freezes at most 128 resources; this is a transport budget, not a
	// namespace list grant. Every frozen hook name participates, including absent ones.
	if len(scopes) == 0 || len(scopes) > 128 {
		return result, errDenied
	}
	if bound := time.Now().Add(25 * time.Second); bound.Before(deadline) {
		deadline = bound
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	type answer struct {
		body []byte
		err  error
	}
	answers := make(chan answer)
	jobs := make(chan kubeprotocol.Resource)
	workers := min(8, len(scopes))
	var pending sync.WaitGroup
	defer func() { cancel(); pending.Wait() }()
	for i := 0; i < workers; i++ {
		pending.Add(1)
		go func() {
			defer pending.Done()
			for scope := range jobs {
				query := url.Values{}
				for key, values := range q {
					query[key] = append([]string(nil), values...)
				}
				query.Set("fieldSelector", "metadata.name="+scope.Name)
				child := in
				child.Query = query.Encode()
				received, e := a.do(work, child, 1)
				if e == nil && received.Status != http.StatusOK {
					e = errDenied
				}
				select {
				case answers <- answer{received.Body, e}:
				case <-work.Done():
					clear(received.Body)
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, scope := range scopes {
			select {
			case jobs <- scope:
			case <-work.Done():
				return
			}
		}
	}()
	var out bytes.Buffer
	count := 0
	for range scopes {
		select {
		case <-work.Done():
			clear(out.Bytes())
			return result, errDenied
		case got := <-answers:
			if got.err != nil {
				clear(got.body)
				clear(out.Bytes())
				return result, errDenied
			}
			dec := json.NewDecoder(bytes.NewReader(got.body))
			for {
				var event json.RawMessage
				e := dec.Decode(&event)
				if e == io.EOF {
					break
				}
				if e != nil {
					clear(got.body)
					clear(out.Bytes())
					return result, errDenied
				}
				count++
				if count > 4096 || out.Len()+len(event)+1 > kubeprotocol.MaxResponseBytes {
					clear(got.body)
					clear(out.Bytes())
					return result, errDenied
				}
				out.Write(event)
				out.WriteByte('\n')
			}
			clear(got.body)
		}
	}
	// This private virtual collection is assembled from independently closed
	// exact-name windows. No single native RV can represent continuous coverage
	// of every name. Explicitly expire the virtual cursor at each complete
	// window so official list/watch clients obtain current authorized objects
	// before another watch. Never sort/advance opaque RVs or hide partial errors.
	expired := []byte(`{"type":"ERROR","object":{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Expired","message":"approved hook watch window closed; relist required","code":410}}`)
	if work.Err() != nil || count+1 > 4096 || out.Len()+len(expired)+1 > kubeprotocol.MaxResponseBytes {
		clear(out.Bytes())
		return result, errDenied
	}
	out.Write(expired)
	out.WriteByte('\n')
	result.Status = http.StatusOK
	result.Body = out.Bytes()
	return result, nil
}

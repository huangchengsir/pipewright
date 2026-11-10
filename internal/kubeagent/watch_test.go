package kubeagent

import (
	"context"
	"fmt"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"testing"
)

func TestExactHookWatchRejectsBroadStreamsAndFiltersForeignObjects(t *testing.T) {
	scope := kubeprotocol.Resource{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespace: "app", Name: "hook", Actions: []string{"get", "watch", "helm-hook"}}
	agent, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("timeoutSeconds") != "5" {
			t.Error("unbounded watch")
		}
		fmt.Fprintln(w, `{"type":"MODIFIED","object":{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"hook","namespace":"app"},"status":{"conditions":[{"type":"Complete","status":"True"}]}}}`)
		fmt.Fprintln(w, `{"type":"MODIFIED","object":{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"other","namespace":"app"},"spec":{"secret":"must-not-cross"}}}`)
	}), []kubeprotocol.Resource{scope})
	path := "/apis/batch/v1/namespaces/app/jobs"
	for _, query := range []string{"watch=true", "watch=true&fieldSelector=metadata.name%3Dother"} {
		if _, e := agent.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path, Query: query}); e == nil {
			t.Fatal("broad watch allowed", query)
		}
	}
	result, e := agent.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path, Query: "watch=true&fieldSelector=metadata.name%3Dhook&timeoutSeconds=99999"})
	if e != nil || strings.Contains(string(result.Body), "must-not-cross") || !strings.Contains(string(result.Body), "Complete") {
		t.Fatal("exact hook watch failed", string(result.Body), e)
	}
	scope.Actions = []string{"get"}
	agent.resources = []kubeprotocol.Resource{scope}
	if _, e = agent.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: path, Query: "watch=true&fieldSelector=metadata.name%3Dhook"}); e == nil {
		t.Fatal("old get grant acquired watch")
	}
}

package kubeagent

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestReadRPCDeniedNamespaceNeverCallsUpstream(t *testing.T) {
	var calls atomic.Int32
	scope := kubeprotocol.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespace: "team-a", Name: "app", Actions: []string{"get"}}
	a, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"team-a"}}`))
	}), []kubeprotocol.Resource{scope})
	denied := a.Handle(context.Background(), kubeprotocol.Frame{Version: 2, Command: "read", Request: &kubeprotocol.Request{Method: "GET", Path: "/apis/apps/v1/namespaces/team-b/deployments/app"}})
	if denied.Code != "request_unverified" || calls.Load() != 0 {
		t.Fatalf("denied read reached upstream: code=%s calls=%d", denied.Code, calls.Load())
	}
	allowed := a.Handle(context.Background(), kubeprotocol.Frame{Version: 2, Command: "read", Request: &kubeprotocol.Request{Method: "GET", Path: "/apis/apps/v1/namespaces/team-a/deployments/app"}})
	if allowed.Code != "ok" || allowed.Result == nil || allowed.Result.Status != 200 || calls.Load() != 1 {
		t.Fatalf("allowed read failed: code=%s calls=%d", allowed.Code, calls.Load())
	}
}

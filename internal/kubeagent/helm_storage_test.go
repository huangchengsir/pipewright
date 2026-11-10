package kubeagent

import (
	"context"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
	"strings"
	"testing"
)

func TestHelmStorageListFiltersOtherReleasesAndOldGrantStaysExact(t *testing.T) {
	scope := kubeprotocol.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app", Actions: []string{"get", "helm-storage"}}
	agent, _ := agentFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"apiVersion":"v1","kind":"SecretList","items":[{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.app.v1","namespace":"app"},"data":{"release":"mine"}},{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.other.v1","namespace":"app"},"data":{"release":"must-not-cross"}}]}`))
	}), []kubeprotocol.Resource{scope})
	result, e := agent.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/secrets"})
	if e != nil || strings.Contains(string(result.Body), "must-not-cross") || !strings.Contains(string(result.Body), "mine") {
		t.Fatal("storage read scope escaped", e, string(result.Body))
	}
	scope.Actions = []string{"get"}
	agent.resources = []kubeprotocol.Resource{scope}
	result, e = agent.Do(context.Background(), kubeprotocol.Request{Method: "GET", Path: "/api/v1/namespaces/app/secrets"})
	if e == nil && strings.Contains(string(result.Body), "mine") {
		t.Fatal("old exact Secret grant expanded to revision names")
	}
}
func TestHelmStorageWriteCannotUseAnotherReleaseOrUnapprovedAction(t *testing.T) {
	scope := kubeprotocol.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "app", Name: "sh.helm.release.v1.app", Actions: []string{"get", "create", "helm-storage"}}
	agent := New()
	agent.resources = []kubeprotocol.Resource{scope}
	request := kubeprotocol.Request{Method: "POST", Path: "/api/v1/namespaces/app/secrets", Body: []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.app.v1","namespace":"app"}}`)}
	if !agent.writePermission(request) {
		t.Fatal("explicit storage create blocked")
	}
	request.Body = []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.other.v1","namespace":"app"}}`)
	if agent.writePermission(request) {
		t.Fatal("storage create widened")
	}
	scope.Actions = []string{"get", "helm-storage"}
	agent.resources = []kubeprotocol.Resource{scope}
	request.Body = []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"sh.helm.release.v1.app.v1","namespace":"app"}}`)
	if agent.writePermission(request) {
		t.Fatal("read-only storage preview can mutate")
	}
}

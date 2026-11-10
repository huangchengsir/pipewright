package kubeagent

import (
	"encoding/json"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

// The pinned Helm resource helper sends only propagationPolicy for its hook
// DeleteOptions. Add standard TypeMeta only for the exact already-granted hook;
// this grants nothing, performs no API call and never fills UID/RV preconditions.
// Do still observes absence or waits for the host's receipt-bound CAS permit.
func (a *Agent) normalizeHelmHookDeleteRequest(in kubeprotocol.Request) (kubeprotocol.Request, error) {
	if in.Method != "DELETE" || in.ContentType != "application/json" {
		return in, nil
	}
	value, err := decodeUniqueJSON(in.Body)
	if err != nil {
		return in, errDenied
	}
	object, ok := value.(map[string]any)
	if !ok {
		return in, errDenied
	}
	if object["apiVersion"] != nil || object["kind"] != nil {
		return in, nil
	}
	if in.Query != "" || len(object) != 1 || object["propagationPolicy"] != "Background" {
		return in, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.initialized || time.Now().After(a.deadline) {
		return in, errDenied
	}
	for _, scope := range a.resources {
		hook := scope.Group == "batch" && scope.Version == "v1" && scope.Resource == "jobs" && scope.Kind == "Job" || scope.Group == "" && scope.Version == "v1" && scope.Resource == "pods" && scope.Kind == "Pod"
		if !hook || scope.Namespace == "" || resourcePath(scope, false) != in.Path || !has(scope.Actions, "helm-hook") || !has(scope.Actions, "get") || !has(scope.Actions, "delete") {
			continue
		}
		body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Background"})
		in.Body = body
		return in, nil
	}
	return in, nil
}

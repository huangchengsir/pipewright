package kubeagent

import (
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"strings"
)

func (a *Agent) writePermission(in kubeprotocol.Request) bool {
	verb := ""
	switch in.Method {
	case "POST":
		verb = "create"
	case "PUT":
		verb = "update"
	case "PATCH":
		verb = "patch"
	case "DELETE":
		verb = "delete"
	default:
		return false
	}
	for _, scope := range a.resources {
		if has(scope.Actions, "helm-storage") && strings.HasPrefix(in.Path, resourcePath(scope, true)+"/") {
			name := strings.TrimPrefix(in.Path, resourcePath(scope, true)+"/")
			if kubeprotocol.HelmStorageResource(scope, name) {
				scope.Name = name
			}
		}
		if in.Method == "POST" && has(scope.Actions, "helm-storage") {
			value, e := decodeUniqueJSON(in.Body)
			if e == nil {
				name := stringValue(mapValue(mapValue(value)["metadata"])["name"])
				if kubeprotocol.HelmStorageResource(scope, name) {
					scope.Name = name
				}
			}
		}
		target := resourcePath(scope, false)
		if in.Method == "POST" {
			target = resourcePath(scope, true)
		}
		if target == in.Path && has(scope.Actions, verb) && namedWriteAllowed(in, scope) {
			return true
		}
	}
	return false
}

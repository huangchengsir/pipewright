package kubeagent

import (
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
)

// Helm 4 creates with SSA PATCH. The host may convert its single approved
// creation to POST so a concurrent same-name object cannot be adopted. Both
// create and patch must be explicitly granted; arbitrary patch creation is not
// permitted, and the original pending request digest remains the permit key.
func (a *Agent) createOnlyRequest(in kubeprotocol.Request, p kubeprotocol.Permit) (kubeprotocol.Request, error) {
	if !p.Allow || p.BodyStream == "" || in.Method != http.MethodPatch || (in.ContentType != "" && in.ContentType != "application/apply-patch+yaml") {
		return in, errDenied
	}
	value, e := decodeUniqueJSON(in.Body)
	if e != nil {
		return in, errDenied
	}
	meta := mapValue(mapValue(value)["metadata"])
	if stringValue(meta["uid"]) != "" || stringValue(meta["resourceVersion"]) != "" {
		return in, errDenied
	}
	for _, scope := range a.resources {
		if resourcePath(scope, false) != in.Path || !has(scope.Actions, "create") || !has(scope.Actions, "patch") || !namedWriteAllowed(in, scope) {
			continue
		}
		next := in
		next.Method = http.MethodPost
		next.Path = resourcePath(scope, true)
		next.ContentType = "application/json"
		if !a.writePermission(next) {
			return in, errDenied
		}
		return next, nil
	}
	return in, errDenied
}

package kubeagent

import (
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"net/http"
)

// A host permit may narrow the exact approved SSA request to its sealed CAS
// patch. The original pending digest remains the single-use permission key.
func (a *Agent) permitMutationRequest(in kubeprotocol.Request, p kubeprotocol.Permit) (kubeprotocol.Request, error) {
	if !p.Allow || p.BodyStream == "" || p.FieldManager != "pipewright-release-v2" {
		return in, errDenied
	}
	if p.ContentType == "" {
		if in.Method != http.MethodPost && !(p.CreateOnly && in.Method == http.MethodPatch && (in.ContentType == "" || in.ContentType == "application/apply-patch+yaml")) {
			return in, errDenied
		}
		return in, nil
	}
	if p.ContentType != "application/merge-patch+json" || p.CreateOnly || in.Method != http.MethodPatch || (in.ContentType != "" && in.ContentType != "application/apply-patch+yaml") {
		return in, errDenied
	}
	value, e := decodeUniqueJSON(in.Body)
	if e != nil {
		return in, errDenied
	}
	meta := mapValue(mapValue(value)["metadata"])
	if stringValue(meta["uid"]) == "" || stringValue(meta["resourceVersion"]) == "" {
		return in, errDenied
	}
	next := in
	next.ContentType = p.ContentType
	if !a.writePermission(next) {
		return in, errDenied
	}
	return next, nil
}

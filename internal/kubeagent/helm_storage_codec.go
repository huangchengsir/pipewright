package kubeagent

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/protobuf"
)

const helmStorageProtobufType = "application/vnd.kubernetes.protobuf"

var helmStorageSecretGVK = schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
var helmStorageSecretCodec = func() *protobuf.Serializer {
	scheme := runtime.NewScheme()
	// Deliberately do not register the rest of core/v1 or a client-go scheme.
	scheme.AddKnownTypeWithName(helmStorageSecretGVK, &corev1.Secret{})
	return protobuf.NewSerializer(scheme, scheme)
}()

// normalizeHelmStorageRequest is private CLI-proxy transport adaptation only.
// Nothing is sent to Kubernetes here. The resulting bounded JSON still crosses
// normal identity/grant checks, pending digest, durable host fence and one permit.
func (a *Agent) normalizeHelmStorageRequest(in kubeprotocol.Request) (kubeprotocol.Request, error) {
	if in.ContentType != helmStorageProtobufType || (in.Method != http.MethodPost && in.Method != http.MethodPut) || len(in.Body) == 0 || len(in.Body) > 1<<20 {
		return in, errDenied
	}
	a.mu.Lock()
	if !a.initialized || time.Now().After(a.deadline) {
		a.mu.Unlock()
		return in, errDenied
	}
	scopes := append([]kubeprotocol.Resource(nil), a.resources...)
	a.mu.Unlock()
	allowed := []kubeprotocol.Resource{}
	for _, scope := range scopes {
		if scope.Group != "" || scope.Version != "v1" || scope.Resource != "secrets" || scope.Kind != "Secret" || scope.Namespace == "" || !has(scope.Actions, "helm-storage") {
			continue
		}
		if in.Method == http.MethodPost && has(scope.Actions, "create") && in.Path == resourcePath(scope, true) {
			allowed = append(allowed, scope)
		}
		if in.Method == http.MethodPut && has(scope.Actions, "update") && strings.HasPrefix(in.Path, resourcePath(scope, true)+"/") && kubeprotocol.HelmStorageResource(scope, strings.TrimPrefix(in.Path, resourcePath(scope, true)+"/")) {
			allowed = append(allowed, scope)
		}
	}
	if len(allowed) == 0 {
		return in, errDenied
	}
	envelope := &runtime.Unknown{}
	_, gvk, err := helmStorageSecretCodec.Decode(in.Body, nil, envelope)
	defer clear(envelope.Raw)
	if err != nil || gvk == nil || *gvk != helmStorageSecretGVK || envelope.ContentEncoding != "" || envelope.ContentType != "" {
		return in, errDenied
	}
	object, gvk, err := helmStorageSecretCodec.Decode(in.Body, nil, nil)
	if err != nil || gvk == nil || *gvk != helmStorageSecretGVK {
		return in, errDenied
	}
	secret, ok := object.(*corev1.Secret)
	if !ok {
		return in, errDenied
	}
	defer func() {
		for _, value := range secret.Data {
			clear(value)
		}
	}()
	if secret.Type != "helm.sh/release.v1" || len(secret.StringData) != 0 || len(secret.Data) != 1 || len(secret.Data["release"]) == 0 {
		return in, errDenied
	}
	for _, scope := range allowed {
		if !kubeprotocol.HelmStorageResource(scope, secret.Name) || (secret.Namespace != "" && secret.Namespace != scope.Namespace) {
			continue
		}
		scope.Name = secret.Name
		if in.Method == http.MethodPut && in.Path != resourcePath(scope, false) {
			continue
		}
		// The pinned Helm storage driver omits body.namespace; typed clients
		// use the URL namespace. Bind it to the already-authorized exact path.
		secret.Namespace = scope.Namespace
		secret.SetGroupVersionKind(helmStorageSecretGVK)
		body, err := json.Marshal(secret)
		if err != nil || len(body) > 1<<20 {
			clear(body)
			return in, errDenied
		}
		out := in
		out.Body = body
		out.ContentType = "application/json"
		if !namedWriteAllowed(out, scope) {
			clear(body)
			return in, errDenied
		}
		return out, nil
	}
	return in, errDenied
}

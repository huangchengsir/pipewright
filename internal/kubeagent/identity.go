package kubeagent

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
)

// decodeUniqueJSON retains Kubernetes map key case-sensitivity while rejecting
// duplicate interpretation and unbounded nesting before identity authorization.
func decodeUniqueJSON(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return nil, errDenied
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	var walk func(int) (any, error)
	walk = func(depth int) (any, error) {
		nodes++
		if depth > 48 || nodes > 100000 {
			return nil, errDenied
		}
		token, e := d.Token()
		if e != nil {
			return nil, errDenied
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return token, nil
		}
		switch delimiter {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, errDenied
				}
				s, ok := key.(string)
				if !ok {
					return nil, errDenied
				}
				if _, ok = m[s]; ok {
					return nil, errDenied
				}
				v, e := walk(depth + 1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, errDenied
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := walk(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, errDenied
			}
			return a, nil
		}
		return nil, errDenied
	}
	v, e := walk(0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errDenied
	}
	return v, nil
}
func exactIdentityKeys(m map[string]any, keys ...string) bool {
	for key := range m {
		for _, reserved := range keys {
			if strings.EqualFold(key, reserved) && key != reserved {
				return false
			}
		}
	}
	return true
}
func namedWriteAllowed(request kubeprotocol.Request, scope kubeprotocol.Resource) bool {
	value, e := decodeUniqueJSON(request.Body)
	if e != nil {
		return false
	}
	if changes, ok := value.([]any); ok {
		if request.Method != "PATCH" || request.ContentType != "application/json-patch+json" {
			return false
		}
		for _, change := range changes {
			op := mapValue(change)
			if len(op) == 0 {
				return false
			}
			p := stringValue(op["path"])
			if p == "/metadata/name" || p == "/metadata/namespace" || p == "/apiVersion" || p == "/kind" {
				if op["op"] != "test" {
					return false
				}
			}
		}
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok || !exactIdentityKeys(obj, "apiVersion", "kind", "metadata") {
		return false
	}
	api := scope.Version
	if scope.Group != "" {
		api = scope.Group + "/" + api
	}
	if request.Method == "DELETE" {
		if obj["apiVersion"] != "v1" || obj["kind"] != "DeleteOptions" {
			return false
		}
		pre := mapValue(obj["preconditions"])
		return stringValue(pre["uid"]) != "" && stringValue(pre["resourceVersion"]) != "" || has(scope.Actions, "helm-hook") && request.Query != "dryRun=All"
	}
	metadata := mapValue(obj["metadata"])
	if !exactIdentityKeys(metadata, "name", "namespace", "uid", "resourceVersion") {
		return false
	}
	full := request.Method == "POST" || request.Method == "PUT" || request.ContentType == "application/apply-patch+yaml" || request.Method == "PATCH" && request.ContentType == ""
	if full && (obj["apiVersion"] != api || obj["kind"] != scope.Kind || metadata["name"] != scope.Name) {
		return false
	}
	if v, ok := obj["apiVersion"]; ok && v != api {
		return false
	}
	if v, ok := obj["kind"]; ok && v != scope.Kind {
		return false
	}
	if v, ok := metadata["name"]; ok && v != scope.Name {
		return false
	}
	if v, ok := metadata["namespace"]; ok && v != scope.Namespace {
		return false
	}
	return true
}

package kubeagent

import (
	"bytes"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"io"
	"net/url"
)

func (a *Agent) watchPermission(path string, query url.Values) (kubeprotocol.Resource, bool) {
	for _, scope := range a.resources {
		if resourcePath(scope, true) == path && has(scope.Actions, "get") && has(scope.Actions, "watch") && query.Get("fieldSelector") == "metadata.name="+scope.Name {
			return scope, true
		}
	}
	return kubeprotocol.Resource{}, false
}
func filterNamedWatch(body []byte, scope kubeprotocol.Resource) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var out bytes.Buffer
	count := 0
	for {
		var event map[string]any
		e := decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errDenied
		}
		count++
		if count > 4096 {
			return nil, errDenied
		}
		if event["type"] == "ERROR" {
			return nil, errDenied
		}
		object := mapValue(event["object"])
		meta := mapValue(object["metadata"])
		if object["kind"] != scope.Kind || meta["name"] != scope.Name || meta["namespace"] != scope.Namespace {
			continue
		}
		switch event["type"] {
		case "ADDED", "MODIFIED", "DELETED", "BOOKMARK":
		default:
			continue
		}
		if e = json.NewEncoder(&out).Encode(event); e != nil {
			return nil, e
		}
	}
	return out.Bytes(), nil
}

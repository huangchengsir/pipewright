package kubeagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"github.com/huangchengsir/pipewright/internal/kubeprotocol"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// chartDiscovery is a CLI-planning-only catalog. It describes newly packaged
// CRDs with a positive upstream NotFound and exact explicit resource grants.
// It never represents an observed cluster resource or an Established CRD.
func (a *Agent) chartDiscovery(ctx context.Context, chart []byte) (map[string][]map[string]any, error) {
	out := map[string][]map[string]any{}
	count, budget := 0, int64(0)
	var walk func([]byte, int) error
	walk = func(raw []byte, depth int) error {
		if depth > 8 {
			return errDenied
		}
		z, e := gzip.NewReader(bytes.NewReader(raw))
		if e != nil {
			return errDenied
		}
		defer z.Close()
		r := tar.NewReader(z)
		for {
			h, e := r.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return errDenied
			}
			count++
			budget += h.Size
			if count > 4096 || budget > kubeprotocol.MaxInputBytes || h.Size < 0 || h.Size > 1<<20 {
				return errDenied
			}
			if h.Typeflag == tar.TypeDir {
				continue
			}
			if h.Typeflag != tar.TypeReg {
				return errDenied
			}
			data, e := io.ReadAll(io.LimitReader(r, h.Size+1))
			if e != nil || int64(len(data)) != h.Size {
				return errDenied
			}
			if strings.HasSuffix(h.Name, ".tgz") {
				if e = walk(data, depth+1); e != nil {
					return e
				}
				continue
			}
			if !strings.Contains(h.Name, "/crds/") || !(strings.HasSuffix(h.Name, ".yaml") || strings.HasSuffix(h.Name, ".yml") || strings.HasSuffix(h.Name, ".json")) {
				continue
			}
			d := yaml.NewDecoder(bytes.NewReader(data))
			for {
				var doc yaml.Node
				e = d.Decode(&doc)
				if e == io.EOF {
					break
				}
				if e != nil {
					return errDenied
				}
				nodes := 0
				var safe func(*yaml.Node, int) bool
				safe = func(n *yaml.Node, depth int) bool {
					nodes++
					if nodes > 100000 || depth > 48 || n.Anchor != "" || n.Alias != nil {
						return false
					}
					for _, c := range n.Content {
						if !safe(c, depth+1) {
							return false
						}
					}
					return true
				}
				if !safe(&doc, 0) {
					return errDenied
				}
				var object map[string]any
				if doc.Decode(&object) != nil || object["apiVersion"] != "apiextensions.k8s.io/v1" || object["kind"] != "CustomResourceDefinition" {
					return errDenied
				}
				name := stringValue(mapValue(object["metadata"])["name"])
				spec := mapValue(object["spec"])
				group := stringValue(spec["group"])
				names := mapValue(spec["names"])
				plural, kind := stringValue(names["plural"]), stringValue(names["kind"])
				if !namePattern.MatchString(group) || !namePattern.MatchString(plural) || !idPattern.MatchString(kind) || name != plural+"."+group || (spec["scope"] != "Namespaced" && spec["scope"] != "Cluster") {
					return errDenied
				}
				current, e := a.Do(ctx, kubeprotocol.Request{Method: http.MethodGet, Path: "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + name})
				if e != nil {
					return errDenied
				}
				status := current.Status
				clear(current.Body)
				if status == http.StatusOK {
					continue
				}
				if status != http.StatusNotFound {
					return errDenied
				}
				for _, v := range helmItems(spec["versions"]) {
					version := mapValue(v)
					if version["served"] != true {
						continue
					}
					ver := stringValue(version["name"])
					if !idPattern.MatchString(ver) {
						return errDenied
					}
					allowed := false
					a.mu.Lock()
					for _, scope := range a.resources {
						if scope.Group == group && scope.Version == ver && scope.Resource == plural && scope.Kind == kind && (scope.Namespace != "") == (spec["scope"] == "Namespaced") {
							allowed = true
						}
					}
					a.mu.Unlock()
					if !allowed {
						continue
					}
					gv := group + "/" + ver
					for _, old := range out[gv] {
						if old["name"] == plural {
							return errDenied
						}
					}
					out[gv] = append(out[gv], map[string]any{"name": plural, "singularName": names["singular"], "namespaced": spec["scope"] == "Namespaced", "kind": kind, "verbs": []string{"get", "list", "create", "patch", "update", "delete", "watch"}})
				}
			}
		}
		return nil
	}
	if !validChartArchive(chart, 0) {
		return nil, errDenied
	}
	if e := walk(chart, 0); e != nil {
		return nil, e
	}
	return out, nil
}

// CLI preview discovery supplements are isolated from Do/RPC reads, so runtime
// verification always observes the real API and cannot accept this projection.
func (a *Agent) planningRequest(ctx context.Context, in kubeprotocol.Request) (kubeprotocol.APIResult, error) {
	a.mu.Lock()
	overlay := a.planningDiscovery
	a.mu.Unlock()
	if len(overlay) == 0 || in.Method != http.MethodGet {
		return a.Do(ctx, in)
	}
	query, e := url.ParseQuery(in.Query)
	if e != nil || validateTransportTimeout(query) != nil {
		return kubeprotocol.APIResult{}, errDenied
	}
	// Normalize only this proven transport deadline when selecting the CLI-only
	// catalog. Actual API requests retain it and all normal authorization checks.
	query.Del("timeout")
	if len(query) != 0 {
		return a.Do(ctx, in)
	}
	matched := in.Path == "/apis"
	for gv := range overlay {
		group := strings.SplitN(gv, "/", 2)[0]
		matched = matched || in.Path == "/apis/"+gv || in.Path == "/apis/"+group
	}
	result, e := a.Do(ctx, in)
	if !matched || e != nil || (result.Status != 200 && result.Status != 404) {
		return result, e
	}
	object := map[string]any{}
	if result.Status == 200 && json.Unmarshal(result.Body, &object) != nil {
		clear(result.Body)
		return kubeprotocol.APIResult{}, errDenied
	}
	clear(result.Body)
	versions := map[string][]map[string]any{}
	for gv := range overlay {
		parts := strings.SplitN(gv, "/", 2)
		versions[parts[0]] = append(versions[parts[0]], map[string]any{"groupVersion": gv, "version": parts[1]})
	}
	groupObject := func(group string, versions []map[string]any) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "APIGroup", "name": group, "versions": versions, "preferredVersion": versions[0]}
	}
	mergeVersions := func(existing []any, want []map[string]any) []any {
		for _, v := range want {
			found := false
			for _, old := range existing {
				found = found || mapValue(old)["groupVersion"] == v["groupVersion"]
			}
			if !found {
				existing = append(existing, v)
			}
		}
		return existing
	}
	if in.Path == "/apis" {
		object["kind"] = "APIGroupList"
		object["apiVersion"] = "v1"
		groups := helmItems(object["groups"])
		for group, vs := range versions {
			found := false
			for _, old := range groups {
				m := mapValue(old)
				if m["name"] == group {
					found = true
					m["versions"] = mergeVersions(helmItems(m["versions"]), vs)
				}
			}
			if !found {
				groups = append(groups, groupObject(group, vs))
			}
		}
		object["groups"] = groups
	} else if resources, ok := overlay[strings.TrimPrefix(in.Path, "/apis/")]; ok {
		object["apiVersion"] = "v1"
		object["kind"] = "APIResourceList"
		object["groupVersion"] = strings.TrimPrefix(in.Path, "/apis/")
		rs := helmItems(object["resources"])
		for _, r := range resources {
			found := false
			for _, old := range rs {
				if mapValue(old)["name"] == r["name"] {
					found = true
				}
			}
			if !found {
				rs = append(rs, r)
			}
		}
		object["resources"] = rs
	} else {
		group := strings.TrimPrefix(in.Path, "/apis/")
		if result.Status == 404 {
			object = groupObject(group, versions[group])
		} else {
			object["versions"] = mergeVersions(helmItems(object["versions"]), versions[group])
		}
	}
	result.Body, e = json.Marshal(object)
	if e != nil || len(result.Body) > kubeprotocol.MaxResponseBytes {
		clear(result.Body)
		return kubeprotocol.APIResult{}, errDenied
	}
	result.Status = 200
	return result, nil
}

func helmItems(v any) []any { rows, _ := v.([]any); return rows }

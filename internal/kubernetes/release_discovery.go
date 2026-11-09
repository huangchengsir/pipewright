package kubernetes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strings"
)

type ReleaseAPIResource struct {
	Group      string   `json:"group"`
	Version    string   `json:"version"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
}
type ReleaseDiscovery struct {
	resources map[string]ReleaseAPIResource
	proposed  map[string]ReleaseResourceID
}

func NewReleaseDiscovery() *ReleaseDiscovery {
	return &ReleaseDiscovery{resources: map[string]ReleaseAPIResource{}}
}

// Add consumes the actual APIResourceList, never an English pluralization rule.
// A new discovery snapshot is built independently and swapped only when complete.
func (d *ReleaseDiscovery) Add(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxReleaseManifestBytes {
		return ErrDiscovery
	}
	var list struct {
		Kind         string `json:"kind"`
		GroupVersion string `json:"groupVersion"`
		Resources    []struct {
			Name       string   `json:"name"`
			Kind       string   `json:"kind"`
			Namespaced bool     `json:"namespaced"`
			Verbs      []string `json:"verbs"`
		} `json:"resources"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(&list) != nil || dec.Decode(new(any)) != io.EOF || list.Kind != "APIResourceList" || len(list.Resources) > 4096 {
		return ErrDiscovery
	}
	group, version, ok := releaseGroupVersion(list.GroupVersion)
	if !ok {
		return ErrDiscovery
	}
	if d.resources == nil {
		d.resources = map[string]ReleaseAPIResource{}
	}
	pending := map[string]ReleaseAPIResource{}
	for _, r := range list.Resources {
		if strings.Contains(r.Name, "/") {
			continue
		}
		if !validDeploymentName(r.Name) || r.Kind == "" || strings.ContainsAny(r.Kind, "/\x00\r\n") {
			return ErrDiscovery
		}
		key := list.GroupVersion + "/" + r.Kind
		if _, ok := pending[key]; ok {
			return ErrDiscovery
		}
		verbs := append([]string(nil), r.Verbs...)
		sort.Strings(verbs)
		pending[key] = ReleaseAPIResource{Group: group, Version: version, Name: r.Name, Kind: r.Kind, Namespaced: r.Namespaced, Verbs: verbs}
	}
	for k, r := range pending {
		if old, ok := d.resources[k]; ok && (old.Name != r.Name || old.Namespaced != r.Namespaced) {
			return ErrDiscovery
		}
		d.resources[k] = r
	}
	return nil
}
func releaseGroupVersion(api string) (string, string, bool) {
	parts := strings.Split(api, "/")
	if len(parts) == 1 && parts[0] == "v1" {
		return "", "v1", true
	}
	if len(parts) != 2 || !validDeploymentName(parts[0]) || !validID(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func (d *ReleaseDiscovery) Resolve(api, kind string) (ReleaseAPIResource, error) {
	if d == nil {
		return ReleaseAPIResource{}, ErrDiscovery
	}
	v, ok := d.resources[api+"/"+kind]
	if !ok {
		return v, ErrDiscovery
	}
	return v, nil
}
func (r ReleaseAPIResource) Allows(verb string) bool {
	for _, v := range r.Verbs {
		if v == verb {
			return true
		}
	}
	return false
}
func (r ReleaseAPIResource) Path(namespace, name string) (string, error) {
	if !validDeploymentName(r.Name) || r.Version == "" || strings.Contains(r.Name, "/") {
		return "", ErrDiscovery
	}
	prefix := "/api/" + url.PathEscape(r.Version)
	if r.Group != "" {
		prefix = "/apis/" + url.PathEscape(r.Group) + "/" + url.PathEscape(r.Version)
	}
	if r.Namespaced {
		if !validName(namespace, 63) {
			return "", ErrDiscovery
		}
		prefix += "/namespaces/" + url.PathEscape(namespace)
	} else if namespace != "" {
		return "", ErrDiscovery
	}
	prefix += "/" + url.PathEscape(r.Name)
	if name != "" {
		if !validDeploymentName(name) {
			return "", ErrDiscovery
		}
		prefix += "/" + url.PathEscape(name)
	}
	return prefix, nil
}

type ReleaseAdapter struct {
	Phase              int    `json:"phase"`
	Health             string `json:"health"`
	Implemented        bool   `json:"implemented"`
	ClusterMaintenance bool   `json:"clusterMaintenance"`
}

func ReleaseAdapterFor(group, kind string) ReleaseAdapter {
	a := ReleaseAdapter{Phase: 2, Health: "custom"}
	switch group + "/" + kind {
	case "/Namespace":
		a.Phase = 0
		a.Implemented = true
		a.ClusterMaintenance = true
	case "rbac.authorization.k8s.io/ClusterRole", "rbac.authorization.k8s.io/ClusterRoleBinding", "scheduling.k8s.io/PriorityClass":
		a.Phase = 0
		a.Implemented = true
		a.ClusterMaintenance = true
	case "/ServiceAccount", "rbac.authorization.k8s.io/Role", "rbac.authorization.k8s.io/RoleBinding":
		a.Phase = 0
		a.Implemented = true
	case "/ConfigMap", "/Secret", "/ResourceQuota", "/LimitRange", "networking.k8s.io/NetworkPolicy":
		a.Phase = 1
		a.Implemented = true
	case "/PersistentVolume", "storage.k8s.io/StorageClass":
		a.Phase = 1
		a.Implemented = true
		a.ClusterMaintenance = true
	case "/PersistentVolumeClaim":
		a.Phase = 1
		a.Health = "pvc"
		a.Implemented = true
	case "apps/Deployment", "apps/StatefulSet", "apps/DaemonSet", "/Pod", "batch/Job":
		a.Health = "workload"
		a.Implemented = true
	case "batch/CronJob":
		a.Implemented = true
	case "/Service", "networking.k8s.io/Ingress":
		a.Phase = 3
		a.Implemented = true
	case "autoscaling/HorizontalPodAutoscaler":
		a.Phase = 3
		a.Health = "hpa"
		a.Implemented = true
	case "policy/PodDisruptionBudget":
		a.Phase = 3
		a.Health = "pdb"
		a.Implemented = true
	case "apiextensions.k8s.io/CustomResourceDefinition":
		a.Phase = 0
		a.Health = "crd"
		a.Implemented = true
		a.ClusterMaintenance = true
	}
	if a.Implemented && a.Health == "custom" {
		a.Health = "declaration"
	}
	return a
}

type ReleaseCapability struct {
	Resource   ReleaseAPIResource `json:"resource"`
	Adapter    ReleaseAdapter     `json:"adapter"`
	Authorized bool               `json:"authorized"`
}

func (d *ReleaseDiscovery) Capabilities(allowed func(ReleaseAPIResource) bool) []ReleaseCapability {
	out := []ReleaseCapability{}
	if d == nil {
		return out
	}
	for _, r := range d.resources {
		out = append(out, ReleaseCapability{Resource: r, Adapter: ReleaseAdapterFor(r.Group, r.Kind), Authorized: allowed != nil && allowed(r)})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Resource.Group+"/"+out[i].Resource.Kind < out[j].Resource.Group+"/"+out[j].Resource.Kind
	})
	return out
}

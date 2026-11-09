package kubernetes

import (
	"regexp"
)

const releaseSATokenMount = "/var/run/secrets/kubernetes.io/serviceaccount"

var releaseSAVolumeName = regexp.MustCompile(`^kube-api-access-[a-z0-9]{5}$`)

func releaseExactObject(a, b any) bool {
	return ReleaseObjectMatches(a, b) && ReleaseObjectMatches(b, a)
}
func releaseOnlyKeys(m map[string]any, keys ...string) bool {
	if m == nil {
		return false
	}
	for key := range m {
		found := false
		for _, allowed := range keys {
			found = found || key == allowed
		}
		if !found {
			return false
		}
	}
	return true
}

// Recognize the native three-source service-account bundle, never an arbitrary
// projected volume. Every value is still compared exactly to the sealed target.
func releaseSATokenBundle(v map[string]any) bool {
	if !releaseOnlyKeys(v, "name", "projected") {
		return false
	}
	p := releaseMap(v["projected"])
	if !releaseOnlyKeys(p, "defaultMode", "sources") {
		return false
	}
	mode, ok := releaseAdmissionInteger(p["defaultMode"])
	if !ok || mode < 0 || mode > 511 {
		return false
	}
	sources, ok := p["sources"].([]any)
	if !ok || len(sources) != 3 {
		return false
	}
	token := releaseMap(sources[0])
	if !releaseOnlyKeys(token, "serviceAccountToken") {
		return false
	}
	t := releaseMap(token["serviceAccountToken"])
	if !releaseOnlyKeys(t, "path", "expirationSeconds", "audience") || t["path"] != "token" {
		return false
	}
	seconds, ok := releaseAdmissionInteger(t["expirationSeconds"])
	if !ok || seconds <= 0 {
		return false
	}
	if audience, exists := t["audience"]; exists {
		if _, ok := audience.(string); !ok {
			return false
		}
	}
	ca := releaseMap(sources[1])
	if !releaseOnlyKeys(ca, "configMap") {
		return false
	}
	cm := releaseMap(ca["configMap"])
	if !releaseOnlyKeys(cm, "name", "items", "optional") || cm["name"] != "kube-root-ca.crt" {
		return false
	}
	if optional, exists := cm["optional"]; exists {
		if _, ok := optional.(bool); !ok {
			return false
		}
	}
	if !releaseExactObject(cm["items"], []any{map[string]any{"key": "ca.crt", "path": "ca.crt"}}) {
		return false
	}
	down := releaseMap(sources[2])
	if !releaseOnlyKeys(down, "downwardAPI") {
		return false
	}
	d := releaseMap(down["downwardAPI"])
	return releaseOnlyKeys(d, "items") && releaseExactObject(d["items"], []any{map[string]any{"path": "namespace", "fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": "metadata.namespace"}}})
}
func releaseInjectedSAVolume(original, admitted map[string]any) (map[string]any, bool) {
	if original["automountServiceAccountToken"] == false {
		return nil, false
	}
	for _, raw := range releaseItems(original["volumes"]) {
		if name := releaseString(releaseMap(raw)["name"]); len(name) >= 16 && name[:16] == "kube-api-access-" {
			return nil, false
		}
	}
	var selected map[string]any
	names := map[string]bool{}
	for _, raw := range releaseItems(admitted["volumes"]) {
		v := releaseMap(raw)
		name := releaseString(v["name"])
		if name == "" || names[name] {
			return nil, false
		}
		names[name] = true
		if len(name) >= 16 && name[:16] == "kube-api-access-" {
			if selected != nil || !releaseSAVolumeName.MatchString(name) || !releaseSATokenBundle(v) {
				return nil, false
			}
			selected = v
		}
	}
	return selected, selected != nil
}
func releaseRenameSAMounts(original, spec map[string]any, from, to string) bool {
	used := false
	for _, list := range []string{"containers", "initContainers", "ephemeralContainers"} {
		declared := map[string]map[string]any{}
		for _, raw := range releaseItems(original[list]) {
			c := releaseMap(raw)
			name := releaseString(c["name"])
			if name == "" || declared[name] != nil {
				return false
			}
			declared[name] = c
			for _, raw := range releaseItems(c["volumeMounts"]) {
				m := releaseMap(raw)
				if releaseSAVolumeName.MatchString(releaseString(m["name"])) {
					return false
				}
			}
		}
		seen := map[string]bool{}
		for _, raw := range releaseItems(spec[list]) {
			c := releaseMap(raw)
			name := releaseString(c["name"])
			if name == "" || seen[name] {
				return false
			}
			seen[name] = true
			blocked := false
			for _, raw := range releaseItems(declared[name]["volumeMounts"]) {
				blocked = blocked || releaseMap(raw)["mountPath"] == releaseSATokenMount
			}
			count := 0
			for _, raw := range releaseItems(c["volumeMounts"]) {
				m := releaseMap(raw)
				if m["name"] != from {
					continue
				}
				if list == "ephemeralContainers" || blocked || !releaseExactObject(m, map[string]any{"name": from, "mountPath": releaseSATokenMount, "readOnly": true}) {
					return false
				}
				count++
				m["name"] = to
				used = true
			}
			if count > 1 || declared[name] != nil && !blocked && count != 1 {
				return false
			}
			for _, raw := range releaseItems(c["volumeDevices"]) {
				if releaseMap(raw)["name"] == from {
					return false
				}
			}
		}
		for name := range declared {
			if !seen[name] {
				return false
			}
		}
	}
	return used
}

// Alpha-renaming is comparison-only. Original intent and actual receipt objects
// retain their full bytes/names, including all token projection and mount data.
func releasePodAdmissionObservation(resource ReleaseResource, expected, observed map[string]any) (map[string]any, bool) {
	if resource.ID.Group != "" || resource.ID.Version != "v1" || resource.ID.Resource != "pods" || resource.ID.Kind != "Pod" || resource.Expected == nil {
		return observed, true
	}
	source := releaseMap(resource.Object["spec"])
	want := releaseMap(expected["spec"])
	actual := releaseMap(observed["spec"])
	dry, ok := releaseInjectedSAVolume(source, want)
	if !ok {
		return observed, true
	}
	live, ok := releaseInjectedSAVolume(source, actual)
	if !ok {
		return observed, true
	}
	from, to := releaseString(live["name"]), releaseString(dry["name"])
	if from == to {
		return observed, true
	}
	left, right := releaseObjectCopy(dry), releaseObjectCopy(live)
	delete(left, "name")
	delete(right, "name")
	if !releaseExactObject(left, right) {
		return nil, false
	}
	expectedCopy := releaseObjectCopy(want)
	if !releaseRenameSAMounts(source, expectedCopy, to, to) {
		return nil, false
	}
	out := releaseObjectCopy(observed)
	spec := releaseMap(out["spec"])
	if !releaseRenameSAMounts(source, spec, from, to) {
		return nil, false
	}
	for _, raw := range releaseItems(spec["volumes"]) {
		v := releaseMap(raw)
		if v["name"] == from {
			v["name"] = to
		}
	}
	return out, true
}

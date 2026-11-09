package kubernetes

import (
	"reflect"
	"strings"
)

// Only supported native GVKs establish exact PodSpec/Service list contexts. A CRD field
// called containers/env (or an arbitrary map with a name member) has no such
// authority. All rooted participants must agree on the GVK.
type releaseListSchema struct {
	pod     bool
	podPath string
	service bool
	native  reflect.Type
}

func releaseNativeListSchema(objects ...any) releaseListSchema {
	version, kind := "", ""
	for _, v := range objects {
		m := releaseMap(v)
		if m == nil {
			return releaseListSchema{}
		}
		if version == "" {
			version, kind = releaseString(m["apiVersion"]), releaseString(m["kind"])
		}
		if version == "" || m["apiVersion"] != version || m["kind"] != kind {
			return releaseListSchema{}
		}
	}
	schema := releaseListSchema{native: releaseNativeTypes[version+"/"+kind]}
	switch version + "/" + kind {
	case "v1/Service":
		schema.service = true
	case "v1/Pod":
		schema.pod = true
		schema.podPath = "/spec"
	case "apps/v1/Deployment", "apps/v1/ReplicaSet", "apps/v1/StatefulSet", "apps/v1/DaemonSet", "batch/v1/Job":
		schema.pod = true
		schema.podPath = "/spec/template/spec"
	case "batch/v1/CronJob":
		schema.pod = true
		schema.podPath = "/spec/jobTemplate/spec/template/spec"
	}
	return schema
}
func releaseFieldPath(path, key string) string {
	// Reserve * for an actual list item, even if an untrusted map key contains it.
	key = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), "*", "~2")
	return path + "/" + key
}
func releaseNativeListField(path string, schema releaseListSchema) string {
	if schema.service && path == "/spec/ports" {
		return "servicePorts"
	}
	if !schema.pod {
		return ""
	}
	for _, field := range []string{"containers", "initContainers", "ephemeralContainers", "volumes", "imagePullSecrets"} {
		if path == schema.podPath+"/"+field {
			return field
		}
	}
	for _, container := range []string{"containers", "initContainers", "ephemeralContainers"} {
		for _, field := range []string{"env", "volumeMounts", "volumeDevices", "ports"} {
			if path == schema.podPath+"/"+container+"/*/"+field {
				return field
			}
		}
	}
	return ""
}
func releaseNativeObjectMatches(want, actual map[string]any) bool {
	return releaseTemplateMatchesAt(want, actual, "", releaseNativeListSchema(want, actual), false)
}

// An explicit authorization/resource identity takes precedence over object
// metadata when selecting native semantics. Historical partial comparison
// fragments may omit TypeMeta, but can never contradict the bound GVK.
func releaseResourceGVKMatches(id ReleaseResourceID, objects ...map[string]any) bool {
	version := id.Version
	if id.Group != "" {
		version = id.Group + "/" + version
	}
	for _, object := range objects {
		if kind, present := object["kind"]; present && kind != id.Kind {
			return false
		}
		if raw, present := object["apiVersion"]; present {
			api, valid := raw.(string)
			if !valid || api == "" {
				return false
			}
			if id.Version != "" && api != version {
				return false
			}
			if id.Version == "" && id.Group != "" {
				parts := strings.Split(api, "/")
				if len(parts) != 2 || parts[0] != id.Group {
					return false
				}
			}
		}
	}
	return true
}

package kubernetes

import (
	"encoding/json"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	"reflect"
	"strings"
	"sync"
)

var releaseNativeTypes = map[string]reflect.Type{
	"v1/Pod": reflect.TypeFor[corev1.Pod](), "v1/Service": reflect.TypeFor[corev1.Service](), "v1/ConfigMap": reflect.TypeFor[corev1.ConfigMap](), "v1/Secret": reflect.TypeFor[corev1.Secret](), "v1/Namespace": reflect.TypeFor[corev1.Namespace](), "v1/ServiceAccount": reflect.TypeFor[corev1.ServiceAccount](), "v1/PersistentVolume": reflect.TypeFor[corev1.PersistentVolume](), "v1/PersistentVolumeClaim": reflect.TypeFor[corev1.PersistentVolumeClaim](), "v1/ResourceQuota": reflect.TypeFor[corev1.ResourceQuota](), "v1/LimitRange": reflect.TypeFor[corev1.LimitRange](),
	"apps/v1/Deployment": reflect.TypeFor[appsv1.Deployment](), "apps/v1/ReplicaSet": reflect.TypeFor[appsv1.ReplicaSet](), "apps/v1/StatefulSet": reflect.TypeFor[appsv1.StatefulSet](), "apps/v1/DaemonSet": reflect.TypeFor[appsv1.DaemonSet](), "apps/v1/ControllerRevision": reflect.TypeFor[appsv1.ControllerRevision](), "batch/v1/Job": reflect.TypeFor[batchv1.Job](), "batch/v1/CronJob": reflect.TypeFor[batchv1.CronJob](), "networking.k8s.io/v1/Ingress": reflect.TypeFor[networkingv1.Ingress](), "networking.k8s.io/v1/NetworkPolicy": reflect.TypeFor[networkingv1.NetworkPolicy](), "autoscaling/v1/HorizontalPodAutoscaler": reflect.TypeFor[autoscalingv1.HorizontalPodAutoscaler](), "autoscaling/v2/HorizontalPodAutoscaler": reflect.TypeFor[autoscalingv2.HorizontalPodAutoscaler](), "policy/v1/PodDisruptionBudget": reflect.TypeFor[policyv1.PodDisruptionBudget](), "storage.k8s.io/v1/StorageClass": reflect.TypeFor[storagev1.StorageClass](), "scheduling.k8s.io/v1/PriorityClass": reflect.TypeFor[schedulingv1.PriorityClass](), "rbac.authorization.k8s.io/v1/Role": reflect.TypeFor[rbacv1.Role](), "rbac.authorization.k8s.io/v1/RoleBinding": reflect.TypeFor[rbacv1.RoleBinding](), "rbac.authorization.k8s.io/v1/ClusterRole": reflect.TypeFor[rbacv1.ClusterRole](), "rbac.authorization.k8s.io/v1/ClusterRoleBinding": reflect.TypeFor[rbacv1.ClusterRoleBinding](),
}

type releaseJSONField struct {
	typ  reflect.Type
	omit bool
}

var releaseJSONFieldCache sync.Map
var releaseJSONMarshaler = reflect.TypeFor[json.Marshaler]()

func releaseCustomJSON(t reflect.Type) bool {
	return t.Implements(releaseJSONMarshaler) || t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(releaseJSONMarshaler)
}
func releaseJSONFields(t reflect.Type) map[string]releaseJSONField {
	if v, ok := releaseJSONFieldCache.Load(t); ok {
		return v.(map[string]releaseJSONField)
	}
	fields := map[string]releaseJSONField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		parts := strings.Split(f.Tag.Get("json"), ",")
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" && f.Anonymous && f.Type.Kind() == reflect.Struct && !releaseCustomJSON(f.Type) {
			for k, v := range releaseJSONFields(f.Type) {
				fields[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		omit := false
		for _, option := range parts[1:] {
			omit = omit || option == "omitempty"
		}
		fields[name] = releaseJSONField{f.Type, omit}
	}
	actual, _ := releaseJSONFieldCache.LoadOrStore(t, fields)
	return actual.(map[string]releaseJSONField)
}

// Walk official JSON field metadata only. Map entries have no field-level
// omitempty, pointers retain presence, and custom JSON wrappers are opaque.
// No typed roundtrip or mutation of approved/live objects occurs.
func releaseNativeField(schema releaseListSchema, path string) (releaseJSONField, bool) {
	t := schema.native
	if t == nil || path == "" {
		return releaseJSONField{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, raw := range parts {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if releaseCustomJSON(t) {
			return releaseJSONField{}, false
		}
		last := i == len(parts)-1
		if raw == "*" {
			if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
				return releaseJSONField{}, false
			}
			t = t.Elem()
			if last {
				return releaseJSONField{}, false
			}
			continue
		}
		key := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(raw, "~2", "*"), "~1", "/"), "~0", "~")
		if t.Kind() == reflect.Map {
			t = t.Elem()
			if last {
				return releaseJSONField{}, false
			}
			continue
		}
		if t.Kind() != reflect.Struct {
			return releaseJSONField{}, false
		}
		field, ok := releaseJSONFields(t)[key]
		if !ok {
			return releaseJSONField{}, false
		}
		if last {
			return field, true
		}
		t = field.typ
	}
	return releaseJSONField{}, false
}
func releaseNativeZeroAt(schema releaseListSchema, path string, value any) bool {
	f, ok := releaseNativeField(schema, path)
	if !ok || !f.omit || f.typ.Kind() == reflect.Pointer || releaseCustomJSON(f.typ) {
		return false
	}
	switch f.typ.Kind() {
	case reflect.Map:
		if value == nil {
			return true
		}
		v := reflect.ValueOf(value)
		return v.Kind() == reflect.Map && v.Len() == 0
	case reflect.Slice:
		if value == nil {
			return true
		}
		v := reflect.ValueOf(value)
		return (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Len() == 0
	case reflect.String:
		s, ok := value.(string)
		return ok && s == ""
	case reflect.Bool:
		b, ok := value.(bool)
		return ok && !b
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		n, ok := releaseJSONNumber(value)
		return ok && n.Sign() == 0
	}
	return false
}
func releasePodSpecSchema() releaseListSchema {
	return releaseListSchema{pod: true, native: reflect.TypeFor[corev1.PodSpec]()}
}

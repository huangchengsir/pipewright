package mcp

// kubernetesConfigSchema describes the write-only v2 input. Cross-field rules,
// private references and exact node bindings are still checked by the domain.
func kubernetesConfigSchema() map[string]any {
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	array := func(item map[string]any, max int) map[string]any {
		return map[string]any{"type": "array", "items": item, "maxItems": max}
	}
	// Only optional pointer fields can be explicitly cleared. Required objects
	// and scalars keep their original schema; domain mode validation remains strict.
	nullable := func(value map[string]any) map[string]any {
		return map[string]any{"anyOf": []any{value, map[string]any{"type": "null"}}}
	}
	boolean := map[string]any{"type": "boolean"}
	artifact := schema(map[string]any{"stageId": stringSchema(128), "jobId": stringSchema(128), "declarationIndex": integerSchema(0, 1023)}, "stageId", "jobId", "declarationIndex")
	source := schema(map[string]any{"kind": enum("inline", "git", "artifact"), "path": stringSchema(1024), "content": stringSchema(16384), "artifact": nullable(artifact)}, "kind")
	executor := schema(map[string]any{"kind": enum("local", "ssh"), "serverId": stringSchema(128)}, "kind")
	resource := schema(map[string]any{"group": stringSchema(253), "version": stringSchema(128), "resource": stringSchema(253), "kind": stringSchema(128), "namespace": stringSchema(63), "name": stringSchema(253)}, "group", "version", "resource", "kind", "name")
	imageSource := schema(map[string]any{"kind": enum("external", "artifact"), "reference": stringSchema(512), "credentialId": stringSchema(128), "artifact": nullable(artifact)}, "kind")
	image := schema(map[string]any{"container": stringSchema(63), "source": imageSource}, "container", "source")
	variable := schema(map[string]any{"name": stringSchema(128), "type": enum("string", "number", "boolean", "object", "array", "null"), "source": enum("fixed", "parameter", "environment", "output", "secret"), "ref": stringSchema(512), "value": map[string]any{}}, "name", "type", "source")
	check := schema(map[string]any{"kind": enum("workload", "condition", "http", "dns", "tcp", "network-allow", "network-deny", "storage"), "name": stringSchema(253), "namespace": stringSchema(63), "target": stringSchema(2048), "server": stringSchema(253), "protocol": enum("udp", "tcp"), "positiveControl": stringSchema(253), "serviceAccount": stringSchema(63), "labels": map[string]any{"type": "object", "additionalProperties": stringSchema(63), "maxProperties": 32}, "imagePullSecrets": array(stringSchema(253), 16), "resource": nullable(resource), "restart": boolean, "waitSeconds": integerSchema(0, 600), "predicate": stringSchema(256), "expected": stringSchema(4096)}, "kind", "name")
	coreDNS := schema(map[string]any{"configMap": stringSchema(253), "key": stringSchema(128), "deployment": stringSchema(253), "container": stringSchema(63), "service": stringSchema(253), "candidatePrefix": stringSchema(30), "candidateServiceIP": stringSchema(64), "dnsPort": integerSchema(1, 65535), "serviceDomain": stringSchema(253), "serviceResource": resource, "records": array(schema(map[string]any{"name": stringSchema(253), "expected": stringSchema(253)}, "name", "expected"), 4)}, "configMap", "key", "deployment", "container", "service", "candidatePrefix", "candidateServiceIP", "dnsPort", "serviceDomain", "serviceResource", "records")
	out := schema(map[string]any{"version": map[string]any{"type": "integer", "const": 2}, "privateRef": stringSchema(128), "mode": enum("image", "manifest", "helm", "progressive"), "connectionId": stringSchema(128), "credentialId": stringSchema(128), "context": stringSchema(128), "namespace": stringSchema(63), "executor": executor, "timeoutSeconds": integerSchema(30, 1800), "observeSeconds": nullable(integerSchema(0, 600)), "workload": nullable(schema(map[string]any{"kind": enum("Deployment", "StatefulSet", "DaemonSet"), "name": stringSchema(253)}, "kind", "name")), "images": array(image, 32), "source": nullable(source), "variables": array(variable, 256), "helm": nullable(schema(map[string]any{"release": stringSchema(53), "values": nullable(source), "hooks": enum("disabled", "approved-jobs")}, "release")), "progressive": nullable(schema(map[string]any{"service": stringSchema(253), "candidate": stringSchema(253), "trackKey": stringSchema(253), "trackValue": stringSchema(63), "batches": array(integerSchema(1, 10000), 32)}, "service", "candidate", "trackKey", "trackValue", "batches")), "checks": array(check, 128), "coreDNS": nullable(coreDNS)})
	out["description"] = "Kubernetes v2 write-only configuration. Add needs a complete config; update replaces supplied top-level fields and preserves omitted fields. Explicit null clears optional workload/source/helm/progressive/coreDNS objects (and optional nested pointers); clear obsolete mode objects when switching modes. Required objects and scalar identities cannot be null. Inline YAML and fixed/probe values are encrypted before draft persistence. Total tool arguments <=16384 bytes; use Git or frozen artifacts for larger YAML. DaemonSet publication additionally requires exact-anchor related-controllerrevisions, native ControllerRevision read permission and the daemonset-controllerrevision-read-v1 helper capability. No Node reads or implicit grants. Protected Kubernetes credentials are references only."
	return out
}

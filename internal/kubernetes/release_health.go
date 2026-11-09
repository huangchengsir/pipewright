package kubernetes

import (
	"encoding/json"
	"strings"

	"time"
)

type ReleaseHealth struct {
	State      string                   `json:"state"`
	Reason     string                   `json:"reason"`
	Desired    int64                    `json:"desired,omitempty"`
	Available  int64                    `json:"available,omitempty"`
	Containers []ReleaseContainerHealth `json:"containers,omitempty"`
}
type ReleaseContainerHealth struct {
	Pod      string `json:"pod,omitempty"`
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason,omitempty"`
	Restarts int64  `json:"restarts,omitempty"`
}

func releaseMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func releaseString(v any) string      { s, _ := v.(string); return s }
func releaseInt(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return i
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
func releaseBool(v any) bool   { b, _ := v.(bool); return b }
func releaseItems(v any) []any { a, _ := v.([]any); return a }
func releaseCondition(obj map[string]any, name string) string {
	for _, v := range releaseItems(releaseMap(obj["status"])["conditions"]) {
		c := releaseMap(v)
		if c["type"] == name {
			return releaseString(c["status"])
		}
	}
	return ""
}

// ReleaseObjectMatches checks only approved declarative fields. Defaults and
// controller bookkeeping may be added by the API server, never replace intent.
func ReleaseObjectMatches(want, actual any) bool {
	return releaseObjectMatchesAt(want, actual, "", releaseNativeListSchema(want, actual))
}
func releaseObjectMatchesAt(want, actual any, path string, schema releaseListSchema) bool {
	if releaseNativeZeroAt(schema, path, want) && releaseNativeZeroAt(schema, path, actual) {
		return true
	}
	switch w := want.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			value, present := a[k]
			child := releaseFieldPath(path, k)
			if !present {
				if releaseNativeZeroAt(schema, child, v) {
					continue
				}
				return false
			}
			if !releaseObjectMatchesAt(v, value, child, schema) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(w) {
			return false
		}
		for i, v := range w {
			if !releaseObjectMatchesAt(v, a[i], path+"/*", schema) {
				return false
			}
		}
		return true
	default:
		return releaseScalarMatches(want, actual)
	}
}
func releaseOwner(obj map[string]any) string {
	for _, v := range releaseItems(releaseMap(obj["metadata"])["ownerReferences"]) {
		o := releaseMap(v)
		if releaseBool(o["controller"]) {
			return releaseString(o["uid"])
		}
	}
	return ""
}
func releasePodHealth(p map[string]any, task bool) ReleaseHealth {
	h := ReleaseHealth{State: "pending", Reason: "pod_not_ready"}
	meta := releaseMap(p["metadata"])
	if meta["deletionTimestamp"] != nil {
		h.Reason = "pod_terminating"
		return h
	}
	status := releaseMap(p["status"])
	phase := releaseString(status["phase"])
	if phase == "Failed" {
		h.State = "failed"
		h.Reason = "pod_failed"
		return h
	}
	taskSucceeded := task && phase == "Succeeded"
	spec := releaseMap(p["spec"])
	initTypes := map[string]bool{}
	for _, v := range releaseItems(spec["initContainers"]) {
		c := releaseMap(v)
		initTypes[releaseString(c["name"])] = c["restartPolicy"] == "Always"
	}
	ready := taskSucceeded || phase == "Running" && releaseCondition(p, "Ready") == "True" && releaseCondition(p, "ContainersReady") == "True" && releaseCondition(p, "Initialized") == "True"
	for _, gate := range releaseItems(spec["readinessGates"]) {
		if !taskSucceeded && releaseCondition(p, releaseString(releaseMap(gate)["conditionType"])) != "True" {
			ready = false
			h.Reason = "readiness_gate_not_ready"
		}
	}
	expectedNames := map[string]bool{}
	expectedImages := map[string]string{}
	for _, key := range []string{"containers", "initContainers"} {
		for _, v := range releaseItems(spec[key]) {
			expectedNames[releaseString(releaseMap(v)["name"])] = true
			expectedImages[releaseString(releaseMap(v)["name"])] = releaseString(releaseMap(v)["image"])
		}
	}
	seenNames := map[string]bool{}
	collect := func(key string, init bool) {
		for _, v := range releaseItems(status[key]) {
			c := releaseMap(v)
			name := releaseString(c["name"])
			state := releaseMap(c["state"])
			if !expectedNames[name] || seenNames[name] {
				ready = false
				h.Reason = "container_status_missing"
			}
			seenNames[name] = true
			r := ReleaseContainerHealth{Pod: releaseString(meta["name"]), Name: name, Ready: releaseBool(c["ready"]), Restarts: releaseInt(c["restartCount"])}
			wait := releaseMap(state["waiting"])
			terminated := releaseMap(state["terminated"])
			reason := releaseString(wait["reason"])
			switch reason {
			case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "CreateContainerConfigError", "CreateContainerError":
				r.Reason = reason
			default:
				if len(wait) > 0 {
					r.Reason = "container_waiting"
				}
			}
			if len(terminated) > 0 && releaseInt(terminated["exitCode"]) != 0 {
				r.Reason = "container_exit_nonzero"
				if terminated["reason"] == "OOMKilled" {
					r.Reason = "OOMKilled"
				}
			}
			if init && !initTypes[name] {
				r.Ready = len(terminated) > 0 && releaseInt(terminated["exitCode"]) == 0
			} else {
				r.Ready = r.Ready && len(releaseMap(state["running"])) > 0 && (c["started"] == nil || releaseBool(c["started"]))
			}
			if taskSucceeded {
				r.Ready = len(terminated) > 0 && (releaseInt(terminated["exitCode"]) == 0 || init && initTypes[name])
			}
			if !releaseContainerStatusImageMatches(expectedImages[name], releaseString(c["image"]), releaseString(c["imageID"])) {
				r.Ready = false
				r.Reason = "container_image_not_observed"
			}
			if !r.Ready {
				ready = false
				if r.Reason != "" {
					h.Reason = r.Reason
				}
			}
			h.Containers = append(h.Containers, r)
		}
	}
	collect("initContainerStatuses", true)
	collect("containerStatuses", false)
	if len(releaseItems(status["containerStatuses"])) != len(releaseItems(spec["containers"])) || len(releaseItems(status["initContainerStatuses"])) != len(releaseItems(spec["initContainers"])) {
		ready = false
		h.Reason = "container_status_missing"
	}
	if phase == "Pending" && releaseCondition(p, "PodScheduled") == "False" {
		h.Reason = "pod_unschedulable"
	}
	if ready {
		h.State = "ready"
		h.Reason = "pod_ready"
		if taskSucceeded {
			h.Reason = "pod_completed"
		}
	}
	return h
}

// EvaluateReleaseHealth receives bounded, authorization-checked objects. Pod
// membership follows controller owner UIDs and the current approved template;
// selector matches alone never make another workload's pods reusable evidence.
func EvaluateReleaseHealth(r ReleaseResource, actual map[string]any, related []map[string]any) ReleaseHealth {
	h := ReleaseHealth{State: "pending", Reason: "resource_not_observed"}
	meta := releaseMap(actual["metadata"])
	if actual == nil || meta["uid"] == nil || meta["deletionTimestamp"] != nil {
		return h
	}
	if r.ExpectedUID != "" && meta["uid"] != r.ExpectedUID {
		return ReleaseHealth{State: "failed", Reason: "resource_identity_changed"}
	}
	if actual["kind"] != r.ID.Kind || releaseString(meta["name"]) != r.ID.Name || releaseString(meta["namespace"]) != r.ID.Namespace {
		return ReleaseHealth{State: "failed", Reason: "resource_identity_changed"}
	}
	if !releaseResourceExpectedMatches(r, actual) {

		return ReleaseHealth{State: "pending", Reason: "approved_fields_not_observed"}
	}
	if len(r.Predicates) > 0 && r.Health != "custom" {
		predicate := evaluateReleasePredicates(r, actual)
		if predicate.State != "ready" {
			return predicate
		}
	}
	kind := r.ID.Kind
	spec, status := releaseMap(actual["spec"]), releaseMap(actual["status"])
	generation := releaseInt(meta["generation"])
	switch r.Health {
	case "custom":
		return evaluateReleasePredicates(r, actual)
	case "declaration":
		return ReleaseHealth{State: "declared", Reason: "configuration_verified_no_application_health"}
	case "crd":
		if releaseCondition(actual, "NamesAccepted") == "False" {
			return ReleaseHealth{State: "failed", Reason: "crd_names_rejected"}
		}
		if releaseCondition(actual, "NamesAccepted") == "True" && releaseCondition(actual, "Established") == "True" {
			return ReleaseHealth{State: "ready", Reason: "crd_established"}
		}
		h.Reason = "crd_not_established"
		return h
	case "pvc":
		if status["phase"] == "Bound" && spec["volumeName"] != "" {
			return ReleaseHealth{State: "ready", Reason: "pvc_bound"}
		}
		if status["phase"] == "Lost" {
			return ReleaseHealth{State: "failed", Reason: "pvc_lost"}
		}
		h.Reason = "pvc_waiting_for_binding"
		return h
	case "hpa":
		if releaseInt(status["observedGeneration"]) < generation {
			h.Reason = "hpa_generation_not_observed"
			return h
		}
		if releaseCondition(actual, "AbleToScale") == "True" && releaseCondition(actual, "ScalingActive") == "True" {
			return ReleaseHealth{State: "ready", Reason: "hpa_metrics_verified"}
		}
		h.Reason = "hpa_metrics_unavailable"
		return h
	case "pdb":
		if releaseInt(status["observedGeneration"]) >= generation && releaseInt(status["currentHealthy"]) >= releaseInt(status["desiredHealthy"]) {
			return ReleaseHealth{State: "ready", Reason: "pdb_observed"}
		}
		h.Reason = "pdb_not_observed"
		return h
	case "workload":
	default:
		return ReleaseHealth{State: "unverified", Reason: "custom_health_predicate_required"}
	}
	if kind == "Pod" {
		return releasePodHealth(actual, spec["restartPolicy"] != "Always")
	}
	if kind == "Job" {
		if releaseCondition(actual, "Failed") == "True" {
			return ReleaseHealth{State: "failed", Reason: "job_failed"}
		}
		if releaseCondition(actual, "Complete") == "True" {
			desired := int64(1)
			if spec["completions"] != nil {
				var valid bool
				desired, valid = releaseAdmissionInteger(spec["completions"])
				if !valid || desired < 0 {
					h.Reason = "job_not_completed"
					return h
				}
			}
			if desired == 0 {
				if !releaseZeroJobIdle(actual, related) {
					h.Reason = "job_not_completed"
					return h
				}
				return ReleaseHealth{State: "ready", Reason: "job_completed", Containers: []ReleaseContainerHealth{}}
			}
			completed := int64(0)
			containers := []ReleaseContainerHealth{}
			template := releaseMap(releaseMap(spec["template"])["spec"])
			for _, pod := range related {
				if pod["kind"] != "Pod" || releaseString(releaseMap(pod["metadata"])["namespace"]) != r.ID.Namespace || releaseOwner(pod) != releaseString(meta["uid"]) || !releasePodSpecMatches(template, pod["spec"]) {
					continue
				}
				if releaseMap(pod["status"])["phase"] == "Succeeded" {
					health := releasePodHealth(pod, true)
					if health.State != "ready" {
						return health
					}
					completed++
					containers = append(containers, health.Containers...)
				}
			}
			if completed < desired {
				return ReleaseHealth{State: "pending", Reason: "job_pod_evidence_missing", Desired: desired, Available: completed}
			}
			return ReleaseHealth{State: "ready", Reason: "job_completed", Desired: desired, Available: completed, Containers: containers}
		}
		h.Reason = "job_not_completed"
		return h
	}
	if releaseInt(status["observedGeneration"]) < generation {
		h.Reason = "workload_generation_not_observed"
		return h
	}
	desired := releaseInt(spec["replicas"])
	if spec["replicas"] == nil {
		desired = 1
	}
	switch kind {
	case "Deployment":
		if releaseBool(spec["paused"]) {
			return ReleaseHealth{State: "failed", Reason: "deployment_paused"}
		}
		if releaseInt(status["updatedReplicas"]) != desired || releaseInt(status["replicas"]) != desired || releaseInt(status["availableReplicas"]) < desired {
			h.Reason = "deployment_rollout_pending"
			return h
		}
	case "StatefulSet":
		strategy := releaseMap(spec["updateStrategy"])
		if strategy["type"] != "RollingUpdate" && strategy["type"] != nil || releaseInt(releaseMap(strategy["rollingUpdate"])["partition"]) != 0 {
			return ReleaseHealth{State: "failed", Reason: "statefulset_strategy_requires_manual_update"}
		}
		if releaseInt(status["updatedReplicas"]) != desired || releaseInt(status["readyReplicas"]) < desired || releaseInt(spec["minReadySeconds"]) > 0 && releaseInt(status["availableReplicas"]) < desired || (desired > 0 && (status["currentRevision"] == nil || status["currentRevision"] != status["updateRevision"])) {
			h.Reason = "statefulset_rollout_pending"
			return h
		}
	case "DaemonSet":
		strategy := releaseMap(spec["updateStrategy"])
		if strategy["type"] != "RollingUpdate" && strategy["type"] != nil {
			return ReleaseHealth{State: "failed", Reason: "daemonset_strategy_requires_manual_update"}
		}
		desired = releaseInt(status["desiredNumberScheduled"])
		if releaseInt(status["updatedNumberScheduled"]) != desired || releaseInt(status["numberReady"]) < desired || releaseInt(status["numberMisscheduled"]) > 0 || releaseInt(spec["minReadySeconds"]) > 0 && releaseInt(status["numberAvailable"]) < desired {
			h.Reason = "daemonset_rollout_pending"
			return h
		}
	default:
		return ReleaseHealth{State: "unverified", Reason: "workload_adapter_missing"}
	}
	h.Desired = desired
	if desired == 0 {
		h.State = "declared"
		h.Reason = "configuration_verified_zero_replicas"
		return h
	}
	pods, valid := releaseCurrentWorkloadPods(r, actual, related)
	if !valid {
		h.Reason = "current_template_pods_not_observed"
		return h
	}
	matched := int64(0)
	for _, p := range pods {
		matched++
		ph := releasePodHealth(p, false)
		h.Containers = append(h.Containers, ph.Containers...)
		if ph.State != "ready" {
			h.Reason = ph.Reason
			return h
		}
		h.Available++
	}
	if matched != desired {
		h.Reason = "current_template_pods_not_observed"
		return h
	}
	h.State = "ready"
	h.Reason = "workload_and_containers_ready"
	return h
}

type ReleaseHealthWindow struct{ Since time.Time }

func (w *ReleaseHealthWindow) Observe(h ReleaseHealth, now time.Time, seconds int) bool {
	if h.State != "ready" && h.State != "declared" {
		w.Since = time.Time{}
		return false
	}
	if w.Since.IsZero() {
		w.Since = now
	}
	return !now.Before(w.Since.Add(time.Duration(seconds) * time.Second))
}

// ReleasePodTemplateMatches permits native list additions only at exact schema
// paths rooted in a supported native GVK. Other objects/arrays stay positional.
// Child PodSpec callers must establish owner/current-template identity before
// using the explicit PodSpec entrypoint. All actual containers are health-gated.
func ReleasePodTemplateMatches(want, actual any) bool {
	return releaseTemplateMatchesAt(want, actual, "", releaseNativeListSchema(want, actual), true)
}

// This entrypoint is only for already owner/current-template-proved native Pods.
// Arbitrary objects and resource subtrees must use the rooted GVK entrypoint.
func releasePodSpecMatches(want, actual any) bool {
	return releaseTemplateMatchesAt(want, actual, "", releasePodSpecSchema(), true)
}
func releaseTemplateListKey(field string, value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	keys := []string{}
	switch field {
	case "containers", "initContainers", "ephemeralContainers", "env", "volumes", "imagePullSecrets":
		keys = []string{"name"}
	case "volumeMounts":
		keys = []string{"mountPath"}
	case "volumeDevices":
		keys = []string{"devicePath"}
	case "ports":
		keys = []string{"containerPort", "protocol"}
	case "servicePorts":
		keys = []string{"port", "protocol"}
	default:
		return "", false
	}
	values := []any{}
	for _, key := range keys {
		value := object[key]
		if key == "protocol" && value == nil {
			value = "TCP"
		}
		if value == nil || value == "" {
			return "", false
		}
		values = append(values, value)
	}
	encoded, err := json.Marshal(values)
	return string(encoded), err == nil
}
func releaseTemplateMatchesAt(want, actual any, path string, schema releaseListSchema, additions bool) bool {
	if releaseNativeZeroAt(schema, path, want) && releaseNativeZeroAt(schema, path, actual) {
		return true
	}
	switch w := want.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			actualValue, present := a[key]
			child := releaseFieldPath(path, key)
			if !present {
				if releaseNativeZeroAt(schema, child, value) {
					continue
				}
				return false
			}
			if !releaseTemplateMatchesAt(value, actualValue, child, schema, additions) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return false
		}
		if additions && schema.pod && path == schema.podPath+"/tolerations" {
			return releasePodTolerationsMatch(w, a)
		}
		field := releaseNativeListField(path, schema)
		if field == "" {
			if len(w) != len(a) {
				return false
			}
			for i, v := range w {
				if !releaseTemplateMatchesAt(v, a[i], path+"/*", schema, additions) {
					return false
				}
			}
			return true
		}
		if !additions && len(w) != len(a) {
			return false
		}
		indexed := map[string]any{}
		positions := map[string]int{}
		for i, v := range a {
			key, valid := releaseTemplateListKey(field, v)
			if !valid || indexed[key] != nil {
				return false
			}
			indexed[key] = v
			positions[key] = i
		}
		seen := map[string]bool{}
		previous := -1
		for _, v := range w {
			key, valid := releaseTemplateListKey(field, v)
			if !valid || seen[key] || indexed[key] == nil || !releaseTemplateMatchesAt(v, indexed[key], path+"/*", schema, additions) {
				return false
			}
			if (field == "env" || field == "initContainers") && positions[key] <= previous {
				return false
			}
			previous = positions[key]
			seen[key] = true
		}
		return true
	default:
		return ReleaseObjectMatches(want, actual)
	}
}

// Never echo an API reason merely because it looks like an identifier. An
// admission webhook can put a secret into any free-form server string.
func ReleaseSafeReason(s string) string {
	switch s {
	case "ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "OOMKilled", "CreateContainerConfigError", "CreateContainerError", "release_ready", "release_failed", "runtime_preflight_failed", "run_settled_before_completion", "run_canceled", "worker_interrupted", "parallel_branch_failed", "stage_not_dispatched", "rollback_complete", "rollback_partial", "rollback_started", "resource_reconciled":
		return s
	}
	return "resource_check_failed"
}

// Kubelet may qualify Docker Hub names or add the implicit latest tag.
// A changed Pod spec alone cannot reuse status from the previously running image.
func releaseContainerImageMatches(want, actual string) bool {
	normalize := func(v string) string {
		v = strings.TrimPrefix(v, "docker.io/")
		v = strings.TrimPrefix(v, "index.docker.io/")
		v = strings.TrimPrefix(v, "library/")
		if !strings.Contains(v, "@") && strings.LastIndex(v, ":") <= strings.LastIndex(v, "/") {
			v += ":latest"
		}
		return v
	}
	return want != "" && actual != "" && normalize(want) == normalize(actual)
}

// Containerd may display a config ID in status.image. Only a pinned manifest
// digest can use imageID as alternative evidence; mutable tags retain their
// existing display-name check. Repository-qualified IDs must name that repository.
func releaseContainerStatusImageMatches(want, display, observedID string) bool {
	if releaseContainerImageMatches(want, display) {
		return true
	}
	parts := strings.Split(want, "@sha256:")
	if len(parts) != 2 || !fullHash.MatchString(parts[1]) || !ValidImageReference(want) {
		return false
	}
	id := observedID
	for _, prefix := range []string{"docker-pullable://", "containerd://", "docker://", "cri-o://"} {
		if strings.HasPrefix(id, prefix) {
			id = strings.TrimPrefix(id, prefix)
			break
		}
	}
	digest := "sha256:" + parts[1]
	if id == digest {
		return true
	}
	observed := strings.Split(id, "@sha256:")
	return len(observed) == 2 && fullHash.MatchString(observed[1]) && observed[1] == parts[1] && ValidImageReference(id) && releaseContainerImageMatches(parts[0]+"@"+digest, observed[0]+"@"+digest)
}

// Explicit zero completion is native success without a child execution only
// after Complete=True. Contradictory active/suspended work is not fabricated as
// completed, and separately configured predicates are evaluated before here.
func releaseZeroJobIdle(job map[string]any, related []map[string]any) bool {
	if releaseBool(releaseMap(job["spec"])["suspend"]) || releaseCondition(job, "Suspended") == "True" {
		return false
	}
	for _, field := range []string{"active", "terminating"} {
		if v := releaseMap(job["status"])[field]; v != nil {
			n, ok := releaseAdmissionInteger(v)
			if !ok || n != 0 {
				return false
			}
		}
	}
	meta := releaseMap(job["metadata"])
	for _, pod := range related {
		if pod["kind"] != "Pod" || releaseMap(pod["metadata"])["namespace"] != meta["namespace"] || releaseOwner(pod) != meta["uid"] {
			continue
		}
		phase := releaseString(releaseMap(pod["status"])["phase"])
		if phase != "Failed" && phase != "Succeeded" {
			return false
		}
	}
	return true
}

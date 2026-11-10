package kubernetes

import (
	"context"
	"errors"
	"time"
)

type releaseHealthLogKey struct{}

func releaseHealthLog(kind string, health ReleaseHealth) string {
	switch kind {
	case "Pod", "Deployment", "DaemonSet", "StatefulSet", "Job", "CronJob", "Service", "Ingress", "ConfigMap", "Secret", "Namespace", "ServiceAccount", "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding", "PersistentVolume", "PersistentVolumeClaim", "StorageClass", "HorizontalPodAutoscaler", "PodDisruptionBudget", "ResourceQuota", "LimitRange", "NetworkPolicy", "CustomResourceDefinition":
	default:
		kind = "CustomResource"
	}
	state := health.State
	switch state {
	case "ready", "declared", "pending", "failed", "unverified":
	default:
		state = "unverified"
	}
	reason := health.Reason
	switch reason {
	case "OOMKilled", "approved_fields_not_observed", "configuration_verified_no_application_health", "configuration_verified_zero_replicas", "container_exit_nonzero", "container_image_not_observed", "container_status_missing", "container_waiting", "crd_established", "crd_names_rejected", "crd_not_established", "current_template_pods_not_observed", "custom_condition_not_observed", "custom_condition_not_ready", "custom_conditions_verified", "custom_generation_not_observed", "custom_health_predicate_required", "daemonset_rollout_pending", "daemonset_strategy_requires_manual_update", "deployment_paused", "deployment_rollout_pending", "hpa_generation_not_observed", "hpa_metrics_unavailable", "hpa_metrics_verified", "job_completed", "job_failed", "job_not_completed", "job_pod_evidence_missing", "pdb_not_observed", "pdb_observed", "pod_completed", "pod_failed", "pod_not_ready", "pod_ready", "pod_terminating", "pod_unschedulable", "pvc_bound", "pvc_lost", "pvc_waiting_for_binding", "readiness_gate_not_ready", "resource_check_failed", "resource_identity_changed", "resource_not_observed", "statefulset_rollout_pending", "statefulset_strategy_requires_manual_update", "workload_adapter_missing", "workload_and_containers_ready", "workload_generation_not_observed":
	default:
		reason = ReleaseSafeReason(reason)
	}
	return "kind=" + kind + " state=" + state + " reason=" + reason
}

// An expired rollout budget must not discard the final known observation. This
// bounded DB-only write retains all owner, UID, approved-body and revision CAS checks.
func persistReleaseActionHealth(ctx context.Context, store *Service, id, owner string, expected int64, receipt ReleaseActionReceipt) (ReleaseAction, error) {
	if log, ok := ctx.Value(releaseHealthLogKey{}).(PhaseLog); ok {
		phase(log, "observe", releaseHealthLog(receipt.Resource.Kind, receipt.Health))
	}
	clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return store.UpdateReleaseActionHealth(clean, id, owner, expected, receipt)
}

// Observation failures expose only a closed stage and errors.Is classification.
// Preserve the original error for owner/unknown/cancellation decisions, never its text.
type releaseObservationError struct {
	stage string
	cause error
}

func (e *releaseObservationError) Error() string { return "kubernetes_observation_failed" }
func (e *releaseObservationError) Unwrap() error { return e.cause }
func withReleaseObservationStage(err error, stage string) error {
	if err == nil {
		return nil
	}
	return &releaseObservationError{stage: releaseObservationStage(stage), cause: err}
}
func releaseObservationStage(stage string) string {
	switch stage {
	case "resource_read", "related_list_read", "related_list_decode", "related_anchor_read", "related_filter", "health_evaluation", "observation_wait":
		return stage
	}
	return "observation"
}
func releaseObservationFailure(ctx context.Context, stage string, err error) {
	if err == nil {
		return
	}
	log, ok := ctx.Value(releaseHealthLogKey{}).(PhaseLog)
	if !ok {
		return
	}
	var wrapped *releaseObservationError
	if errors.As(err, &wrapped) {
		stage = wrapped.stage
	}
	reason := releasePreparationReason(err)
	switch {
	case errors.Is(err, ErrRollout):
		reason = "kubernetes_workload_not_ready"
	case errors.Is(err, ErrSuperseded):
		reason = "kubernetes_target_superseded"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "observation_timeout"
	case errors.Is(err, context.Canceled):
		reason = "observation_canceled"
	}
	if reason == "preparation_failed" {
		reason = "observation_failed"
	}
	phase(log, "observe", "stage="+releaseObservationStage(stage)+" reason="+reason)
}

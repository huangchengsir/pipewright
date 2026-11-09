import type { RetryPlannedNode } from "../../api/runRetry";
import type { KubeOperation } from "../../api/kubernetes";
export const kubePhases = [
  "preflight",
  "update",
  "rollout",
  "cleanup",
] as const;
export type KubePhase = (typeof kubePhases)[number];
const codes: Record<KubePhase, string[]> = {
  preflight: [
    "preflight_started",
    "resource_identity_check",
    "patch_not_authorized",
    "permission_check_unverified",
  ],
  update: ["image_update_dispatched", "resource_write_fenced"],
  rollout: ["rollout_observing", "rollout_ready", "resource_health_check", "release_ready", "batch_ready_waiting_action"],
  cleanup: ["cleanup_started", "cleanup_verified", "cleanup_unverified"],
};
export function kubePhaseEvidence(
  texts: string[],
): Record<KubePhase, string[]> {
  const out: Record<KubePhase, string[]> = {
    preflight: [],
    update: [],
    rollout: [],
    cleanup: [],
  };
  for (const text of texts) {
    const entry = kubeLogEvidence(text);
    if (entry?.code && !out[entry.phase].includes(entry.code)) out[entry.phase].push(entry.code);
  }
  return out;
}

const logPhases: Record<string, KubePhase> = {
  preflight: "preflight", update: "update", dispatch: "update",
  rollout: "rollout", observe: "rollout", pause: "rollout", complete: "rollout", cleanup: "cleanup",
};
const healthKinds = new Set(["Pod", "Deployment", "DaemonSet", "StatefulSet", "Job", "CronJob", "Service", "Ingress", "ConfigMap", "Secret", "Namespace", "ServiceAccount", "Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding", "PersistentVolume", "PersistentVolumeClaim", "StorageClass", "HorizontalPodAutoscaler", "PodDisruptionBudget", "ResourceQuota", "LimitRange", "NetworkPolicy", "CustomResourceDefinition", "CustomResource"]);
const healthReasons = new Set(["CrashLoopBackOff", "CreateContainerConfigError", "CreateContainerError", "ErrImagePull", "ImagePullBackOff", "OOMKilled", "approved_fields_not_observed", "configuration_verified_no_application_health", "configuration_verified_zero_replicas", "container_exit_nonzero", "container_image_not_observed", "container_status_missing", "container_waiting", "crd_established", "crd_names_rejected", "crd_not_established", "current_template_pods_not_observed", "custom_condition_not_observed", "custom_condition_not_ready", "custom_conditions_verified", "custom_generation_not_observed", "custom_health_predicate_required", "daemonset_rollout_pending", "daemonset_strategy_requires_manual_update", "deployment_paused", "deployment_rollout_pending", "hpa_generation_not_observed", "hpa_metrics_unavailable", "hpa_metrics_verified", "job_completed", "job_failed", "job_not_completed", "job_pod_evidence_missing", "parallel_branch_failed", "pdb_not_observed", "pdb_observed", "pod_completed", "pod_failed", "pod_not_ready", "pod_ready", "pod_terminating", "pod_unschedulable", "pvc_bound", "pvc_lost", "pvc_waiting_for_binding", "readiness_gate_not_ready", "release_failed", "release_ready", "resource_check_failed", "resource_identity_changed", "resource_not_observed", "resource_reconciled", "rollback_complete", "rollback_partial", "rollback_started", "run_settled_before_completion", "runtime_preflight_failed", "stage_not_dispatched", "statefulset_rollout_pending", "statefulset_strategy_requires_manual_update", "worker_interrupted", "workload_adapter_missing", "workload_and_containers_ready", "workload_generation_not_observed"]);
export function kubeLogPhase(text: string): KubePhase | undefined {
  const match = /^kubernetes ([a-z]+): /.exec(text);
  return match ? logPhases[match[1]] : undefined;
}
export function kubeLogEvidence(text: string): {phase: KubePhase; code?: string; kind?: string; state?: string} | undefined {
  const phase = kubeLogPhase(text);
  if (!phase) return;
  const body = text.slice(text.indexOf(": ") + 2).trim();
  if (codes[phase].includes(body)) return {phase, code: body};
  if (phase !== "rollout") return;
  const health = /^kind=([A-Za-z]+) state=(ready|declared|pending|failed|unverified) reason=([A-Za-z_]+)$/.exec(body);
  if (health && healthKinds.has(health[1]) && healthReasons.has(health[3])) return {phase, kind: health[1], state: health[2]};
}

export function exactKubeOperation(
  rows: KubeOperation[],
  runId: string,
  nodeId: string,
  operationId?: string,
): KubeOperation | undefined {
  const matches = rows.filter(
    (row) =>
      row.runId === runId &&
      row.nodeId === nodeId &&
      (!operationId || row.id === operationId),
  );
  // A single logical node/attempt cannot select an arbitrary first operation.
  return matches.length === 1 ? matches[0] : undefined;
}
export function kubeBlocksRetry(row?: KubeOperation) {
  return (
    !row ||
    row.retired ||
    !row.canReconcile ||
    !["ready", "unchanged", "failed"].includes(row.business) ||
    row.cleanup !== "verified"
  );
}

/** An adopted result is stored in the child's checkpoint, but names the original operation. */
export function kubeOperationLocation(
  node: RetryPlannedNode | undefined,
  runId: string | undefined,
): {
  runId: string;
  nodeId: string;
  operationId?: string;
  releaseId?: string;
  version?: number;
} | null {
  if (!node || node.node.type !== "deploy_kubernetes") return null;
  const ref = node.kubernetes;
  if (ref?.version === 2)
    return ref.releaseId && ref.sourceAttemptId && ref.nodeKey === node.node.key
      ? {
          runId: ref.sourceAttemptId,
          nodeId: ref.nodeKey,
          releaseId: ref.releaseId,
          version: 2,
        }
      : null;

  if (ref)
    return ref.operationId &&
      ref.sourceAttemptId &&
      ref.nodeKey === node.node.key
      ? {
          runId: ref.sourceAttemptId,
          nodeId: ref.nodeKey,
          operationId: ref.operationId,
        }
      : null;
  if (node.decision.action === "reuse" || node.reasonCode?.includes("adopt"))
    return null;
  return runId ? { runId, nodeId: node.node.key } : null;
}

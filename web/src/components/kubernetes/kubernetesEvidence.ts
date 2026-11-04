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
    "patch_not_authorized",
    "permission_check_unverified",
  ],
  update: ["image_update_dispatched"],
  rollout: ["rollout_observing", "rollout_ready"],
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
    const match =
      /^kubernetes (preflight|update|rollout|cleanup): ([a-z_]+)$/.exec(
        text.trim(),
      );
    if (!match) continue;
    const phase = match[1] as KubePhase;
    if (codes[phase].includes(match[2]) && !out[phase].includes(match[2]))
      out[phase].push(match[2]);
  }
  return out;
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
): { runId: string; nodeId: string; operationId?: string } | null {
  if (!node || node.node.type !== "deploy_kubernetes") return null;
  const ref = node.kubernetes;
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

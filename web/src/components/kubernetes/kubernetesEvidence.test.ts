import { describe, expect, it } from "vitest";
import {
  kubePhaseEvidence,
  exactKubeOperation,
  kubeBlocksRetry,
  kubeOperationLocation,
} from "./kubernetesEvidence";
import type { KubeOperation } from "../../api/kubernetes";
const op = (extra: Partial<KubeOperation> = {}): KubeOperation =>
  ({
    id: "op",
    runId: "source",
    nodeId: "node",
    business: "ready",
    cleanup: "verified",
    retired: false,
    canReconcile: true,
    ...extra,
  }) as KubeOperation;
describe("Kubernetes evidence", () => {
  it("requires one exact source attempt and node association", () => {
    expect(exactKubeOperation([op()], "child", "node")).toBeUndefined();
    expect(exactKubeOperation([op()], "source", "other")).toBeUndefined();
    expect(
      exactKubeOperation([op(), op({ id: "duplicate" })], "source", "node"),
    ).toBeUndefined();
    expect(exactKubeOperation([op()], "source", "node")?.id).toBe("op");
  });
  it("does not equate ready business with cleanup verified and rejects unknown retry", () => {
    expect(kubeBlocksRetry(op())).toBe(false);
    expect(kubeBlocksRetry(op({ cleanup: "blocked" }))).toBe(true);
    expect(kubeBlocksRetry(op({ business: "outcome_unknown" }))).toBe(true);
    expect(kubeBlocksRetry()).toBe(true);
  });
  it("never invents missing steps or accept arbitrary diagnostic text as evidence", () => {
    const r = kubePhaseEvidence([
      "kubernetes preflight: preflight_started",
      "kubernetes rollout: rollout_ready",
      "kubernetes cleanup: SECRET_CANARY",
      "image_update_dispatched",
      "kubernetes update: rollout_ready",
    ]);
    expect(r).toEqual({
      preflight: ["preflight_started"],
      update: [],
      rollout: ["rollout_ready"],
      cleanup: [],
    });
  });
});

import type { RetryPlannedNode } from "../../api/runRetry";
describe("Kubernetes Context DTO to UI source mapping", () => {
  it("uses the sealed original operation for an execute/adopt node whose decision source is the child", () => {
    const node = {
      node: { key: "node", type: "deploy_kubernetes" },
      decision: { action: "execute", sourceAttemptId: "child" },
      reasonCode: "kubernetes_adopted_verified",
      kubernetes: {
        operationId: "op",
        sourceAttemptId: "source",
        nodeKey: "node",
      },
    } as RetryPlannedNode;
    const target = kubeOperationLocation(node, "child");
    expect(target).toEqual({
      operationId: "op",
      runId: "source",
      nodeId: "node",
    });
    expect(
      exactKubeOperation(
        [op(), op({ id: "other" })],
        target!.runId,
        target!.nodeId,
        target!.operationId,
      )?.id,
    ).toBe("op");
    expect(
      kubeOperationLocation({ ...node, kubernetes: undefined }, "child"),
    ).toBeNull();
    expect(
      kubeOperationLocation(
        { ...node, kubernetes: { ...node.kubernetes!, nodeKey: "other" } },
        "child",
      ),
    ).toBeNull();
  });
  it.each([
    "not_dispatched",
    "submitted",
    "outcome_unknown",
    "superseded",
  ] as const)("blocks retry for %s even after cleanup", (business) => {
    expect(kubeBlocksRetry(op({ business }))).toBe(true);
  });
  it("blocks a known failure until owner exit is confirmed", () => {
    expect(
      kubeBlocksRetry(op({ business: "failed", canReconcile: false })),
    ).toBe(true);
    expect(kubeBlocksRetry(op({ business: "failed" }))).toBe(false);
  });
});

describe("Kubernetes permission evidence", () => {
  it("accepts only known permission results in the preflight phase", () => {
    expect(
      kubePhaseEvidence([
        "kubernetes preflight: patch_not_authorized",
        "kubernetes preflight: permission_check_unverified",
        "kubernetes update: patch_not_authorized",
        "kubernetes preflight: untrusted_diagnostic",
        "kubernetes preflight: patch_not_authorized",
      ]),
    ).toEqual({
      preflight: ["patch_not_authorized", "permission_check_unverified"],
      update: [],
      rollout: [],
      cleanup: [],
    });
  });
});

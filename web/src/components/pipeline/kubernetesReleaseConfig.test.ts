import { describe, it, expect } from "vitest";
import {
  newReleaseConfig,
  selectReleaseMode,
  releaseJSON,
} from "./kubernetesReleaseConfig";
import { splitConfig } from "./jobConfigSchema";
import { i18n, SUPPORTED_LOCALES } from "../../i18n";
import { deriveStageStatus } from "../run/runDag";
import type { RunStep } from "../../api/runs";
import { kubeOperationLocation } from "../kubernetes/kubernetesEvidence";
import type { RetryPlannedNode } from "../../api/runRetry";
describe("Kubernetes release suite configuration", () => {
  it("keeps protected references and mappings while choosing all four valid shapes", () => {
    const original = {
      ...newReleaseConfig(),
      privateRef: "kcfg_node_bound",
      variables:
        '[{"name":"VALUE","type":"string","source":"fixed","value":"pwkeep_preserve"}]',
    };
    for (const mode of ["image", "manifest", "helm", "progressive"]) {
      const next = selectReleaseMode(original, mode);
      expect(next.privateRef).toBe(original.privateRef);
      expect(next.variables).toBe(original.variables);
      expect(next.version).toBe("2");
      expect(splitConfig("deploy_kubernetes", next).extras).toEqual([]);
      if (mode === "image" || mode === "progressive") {
        expect(next.source).toBeUndefined();
        expect(releaseJSON(next.workload, {})).toMatchObject({
          kind: "Deployment",
        });
      } else expect(releaseJSON(next.source, {})).toHaveProperty("kind");
      if (mode === "helm") expect(next.helm).toBeDefined();
      if (mode === "progressive") expect(next.progressive).toBeDefined();
    }
  });
  it("preserves typed server roundtrips and runtime variable literals", () => {
    const source = {
      kind: "inline",
      content: "command: echo $" + "$" + "{HOSTNAME}",
    };
    expect(releaseJSON(source, {})).toBe(source);
    expect(releaseJSON(JSON.stringify(source), {})).toEqual(source);
    expect(releaseJSON("[broken]", [])).toEqual([]);
  });
  it("renders variable literals in all eight languages without swallowing braces", () => {
    for (const { code } of SUPPORTED_LOCALES) {
      const text = i18n.global.t(
        "kubernetes.release.variableHint",
        { variable: "$" + "{NAME}", literal: "$" + "$" + "{NAME}" },
        { locale: code },
      );
      expect(text).toContain("$" + "{NAME}");
      expect(text).toContain("$" + "$" + "{NAME}");
      expect(i18n.global.te("runStatus.waiting_action", code)).toBe(true);
      expect(i18n.global.te("onboardingFlow.states.waiting_action", code)).toBe(
        true,
      );
    }
  });
  it("keeps a paused stage nonterminal and resolves only the exact sealed release source", () => {
    expect(
      deriveStageStatus([
        { status: "success" },
        { status: "waiting_action" },
      ] as RunStep[]),
    ).toBe("waiting_action");
    const node = {
      node: { key: "logical", type: "deploy_kubernetes" },
      decision: { action: "reuse" },
      kubernetes: {
        version: 2,
        releaseId: "original-release",
        sourceAttemptId: "original-run",
        nodeKey: "logical",
      },
    } as RetryPlannedNode;
    expect(kubeOperationLocation(node, "current")).toEqual({
      runId: "original-run",
      nodeId: "logical",
      releaseId: "original-release",
      version: 2,
    });
    expect(
      kubeOperationLocation(
        { ...node, kubernetes: { ...node.kubernetes!, nodeKey: "other" } },
        "current",
      ),
    ).toBeNull();
  });
});

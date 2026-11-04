import { describe, expect, it } from "vitest";
import type { PipelineStage, PipelineJob } from "../../api/pipeline";
import {
  insertKubernetesStage,
  kubernetesSources,
  isKubernetesStage,
} from "./kubernetesConfig";
const job = (id: string, type: string): PipelineJob => ({
  id,
  name: id,
  type,
  summary: "",
  config: {},
});
const stage = (id: string, type: string): PipelineStage => ({
  id,
  name: id,
  kind: "build",
  jobs: [job(id + "job", type)],
});
describe("Kubernetes stage and source rules", () => {
  it("preserves every old implicit ordering edge when adding an explicit K8s stage", () => {
    const original = [
      stage("source", "git_source"),
      stage("build", "build_image"),
      stage("push", "push_image"),
      stage("notify", "notify"),
    ];
    const before = JSON.stringify(original);
    const next = insertKubernetesStage(
      original,
      "push",
      job("deploy", "deploy_kubernetes"),
      "k8s",
      "Deploy",
    );
    expect(next.map((s) => [s.id, s.needs])).toEqual([
      ["source", []],
      ["build", ["source"]],
      ["push", ["build"]],
      ["k8s", ["push"]],
      ["notify", ["k8s"]],
    ]);
    expect(JSON.stringify(original)).toBe(before);
    expect(isKubernetesStage(next[3])).toBe(true);
    expect(
      kubernetesSources(next, next[3], next[3].jobs[0]).map((s) => s.job.type),
    ).toEqual(["push_image"]);
  });
  it("does not offer build outputs or parallel/nonancestor pushes", () => {
    const stages = [
      { ...stage("push", "push_image"), needs: [] },
      { ...stage("other", "push_image"), needs: [] },
      { ...stage("build", "build_image"), needs: ["push"] },
      {
        ...stage("k8s", "deploy_kubernetes"),
        kind: "deploy" as const,
        needs: ["build"],
      },
    ];
    expect(
      kubernetesSources(stages, stages[3], stages[3].jobs[0]).map(
        (s) => s.stage.id,
      ),
    ).toEqual(["push"]);
  });
  it("rejects matrices, extra tasks, services and multiple jobs", () => {
    const s = { ...stage("k8s", "deploy_kubernetes"), kind: "deploy" as const };
    expect(isKubernetesStage(s)).toBe(true);
    for (const patch of [
      { matrix: { x: ["a"] } },
      { allowFailure: true },
      { jobs: [...s.jobs, job("x", "script")] },
      { services: [{ name: "db", image: "db" }] },
      {
        post: [
          { condition: "always" as const, image: "tool", commands: ["echo"] },
        ],
      },
    ])
      expect(isKubernetesStage({ ...s, ...patch })).toBe(false);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ReleaseGuardReport from "./ReleaseGuardReport.vue";
import { guardReport } from "../test/releaseGuardFixtures";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  evidence: vi.fn(),
  stop: vi.fn(),
  push: vi.fn(),
  analyses: vi.fn(),
}));
vi.mock("../api/releaseGuard", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/releaseGuard")>()),
  getReleaseGuard: api.get,
  listGuardEvidence: api.evidence,
  stopReleaseGuard: api.stop,
  listGuardAnalyses: api.analyses,
}));
vi.mock("vue-router", () => ({
  useRoute: () => ({ params: { projectId: "project-1", guardId: "guard-1" } }),
  useRouter: () => ({ push: api.push }),
}));
function page() {
  return mount(ReleaseGuardReport, {
    global: {
      stubs: { RouterLink: { props: ["to"], template: "<a><slot /></a>" } },
    },
  });
}

describe("release guard report", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockResolvedValue(guardReport());
    api.evidence.mockResolvedValue({
      items: [],
      nextCursor: 0,
      hasMore: false,
    });
    api.stop.mockResolvedValue(guardReport({ executionStatus: "stopping" }));
    api.analyses.mockResolvedValue({ items: [] });
  });
  afterEach(() => {
    vi.useRealTimers();
  });
  it("keeps a confirmed failure separate from missing coverage and unknown attribution", async () => {
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(wrapper.get(".guard-verdict").text()).toContain("已结束");
    expect(wrapper.get(".guard-verdict").text()).toContain("未定");
    expect(wrapper.text()).toContain("覆盖存在缺口");
    expect(wrapper.text()).toContain("历史报告");
    expect(wrapper.text()).toContain("不覆盖发布结束至此刻");
    expect(wrapper.text()).toContain("未校验主机密钥");
    wrapper.unmount();
  });
  it("does not claim stopped until the worker confirms it and never restarts on polling", async () => {
    vi.useFakeTimers();
    api.get.mockResolvedValue(
      guardReport({ executionStatus: "observing", finishedAt: undefined }),
    );
    const wrapper = page();
    await flushPromises();
    const button = wrapper
      .findAll("button")
      .find((button) => button.text() === "停止观察")!;
    await button.trigger("click");
    await flushPromises();
    expect(api.stop).toHaveBeenCalledTimes(1);
    expect(wrapper.get(".guard-verdict").text()).toContain("正在停止");
    expect(wrapper.get(".guard-verdict").text()).not.toContain("已停止");
    api.get.mockResolvedValue(
      guardReport({ executionStatus: "stopped", verificationResult: "failed" }),
    );
    await vi.advanceTimersByTimeAsync(4000);
    expect(wrapper.get(".guard-verdict").text()).toContain("已停止");
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    const reads = api.get.mock.calls.length;
    const request = api.get.mock.calls.at(-1)![2] as { signal: AbortSignal };
    wrapper.unmount();
    expect(request.signal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(12000);
    expect(api.get).toHaveBeenCalledTimes(reads);
    expect(api.stop).toHaveBeenCalledTimes(1);
  });
  it("shows the pinned local image on observed targets while preserving the original pending plan", async () => {
    const report = guardReport();
    const pending = { reference: "", repository: "", digest: "", digestKind: "config_id", identitySource: "script_pre_activation" };
    report.plan.targets[0].expectedImage = pending;
    report.targets[0].expectedImage = { ...pending, digest: `sha256:${"b".repeat(64)}` };
    report.targets[0].conditions = [{ name: "version", result: "passed", reason: "local_config_id_matched", evidenceIds: [] }];
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-target").text()).toContain(`sha256:${"b".repeat(64)}`);
    expect(wrapper.get(".guard-target").text()).toContain("启动前固定的本地 Image ID");
    expect(wrapper.get(".guard-plan-targets").text()).toContain("构建后、启动前固定 Image ID");
    expect(wrapper.get(".guard-plan-targets").text()).not.toContain("registry.test/app@");
    wrapper.unmount();
  });
  it("explains incomplete deployment and recorded deployment health failure separately from runtime health", async () => {
    const report = guardReport();
    report.targets[0].conditions = [
      { name: "deployment_completion", result: "pending", reason: "deployment_completion_pending", evidenceIds: [] },
      { name: "deployment_health_gate", result: "failed", reason: "deployment_health_gate_failed", evidenceIds: [] },
    ];
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-target").text()).toContain("部署最终结果");
    expect(wrapper.get(".guard-target").text()).toContain("等待本次部署的最终回执");
    expect(wrapper.get(".guard-target").text()).toContain("部署时健康门控");
    expect(wrapper.get(".guard-target").text()).toContain("持续 Docker 健康状态分别判断");
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe("failed");
    wrapper.unmount();
  });
  it("never fetches expired evidence or substitutes summaries as evidence", async () => {
    api.get.mockResolvedValue(guardReport({ evidenceExpired: true }));
    const wrapper = page();
    await flushPromises();
    expect(wrapper.text()).toContain("证据已过期");
    expect(api.evidence).not.toHaveBeenCalled();
    expect(wrapper.findAll(".guard-evidence")).toHaveLength(0);
    wrapper.unmount();
  });
  it("discloses a removed health condition instead of calling revalidation unchanged", async () => {
    api.get.mockResolvedValue(
      guardReport({
        parentGuardId: "original-failed-guard",
        verificationResult: "passed",
        comparison: {
          sameConditions: false,
          changes: ["docker_health_condition_changed"],
        },
      }),
    );
    const wrapper = page();
    await flushPromises();
    expect(wrapper.text()).toContain("检查条件已变化");
    expect(wrapper.text()).toContain("Docker 健康检查条件已变化");
    expect(wrapper.text()).not.toContain("本次沿用相同条件");
    wrapper.unmount();
  });
  it("escapes untrusted evidence and follows the persisted pagination cursor", async () => {
    api.evidence.mockResolvedValueOnce({
      items: [
        {
          id: "evidence-1",
          sequence: 1,
          targetId: "target-1",
          kind: "snapshot",
          eventAt: "2026-10-22T10:00:00Z",
          collectedAt: "2026-10-22T10:00:01Z",
          reason: "",
          truncated: true,
          summary: "<img src=x onerror=alert(1)>",
          value: { Running: false },
        },
      ],
      nextCursor: 9,
      hasMore: true,
    });
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-evidence").text()).toContain(
      "<img src=x onerror=alert(1)>",
    );
    expect(wrapper.findAll(".guard-evidence img")).toHaveLength(0);
    const button = wrapper
      .findAll("button")
      .find((button) => button.text() === "读取更多证据")!;
    await button.trigger("click");
    await flushPromises();
    expect(api.evidence).toHaveBeenLastCalledWith(
      "project-1",
      "guard-1",
      9,
      expect.any(Object),
    );
    wrapper.unmount();
  });
});

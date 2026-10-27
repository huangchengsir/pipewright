import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ReleaseGuardReport from "./ReleaseGuardReport.vue";
import { guardReport } from "../test/releaseGuardFixtures";
import { reactive } from "vue";
import { i18n, setLocale } from "../i18n";
import type { GuardEvidence, GuardReport } from "../api/releaseGuard";
import { HttpError } from "../api/http";
import ReleaseGuardAnalysis from "../components/run/ReleaseGuardAnalysis.vue";
import ReleaseGuardInvestigation from "../components/run/ReleaseGuardInvestigation.vue";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  evidence: vi.fn(),
  stop: vi.fn(),
  push: vi.fn(),
  analyses: vi.fn(),
  preview: vi.fn(),
  send: vi.fn(),
  investigate: vi.fn(),
}));
const route = reactive({
  params: { projectId: "project-1", guardId: "guard-1" },
});
vi.mock("../api/releaseGuard", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/releaseGuard")>()),
  getReleaseGuard: api.get,
  listGuardEvidence: api.evidence,
  stopReleaseGuard: api.stop,
  listGuardAnalyses: api.analyses,
  previewGuardAnalysis: api.preview,
  sendGuardAnalysis: api.send,
  investigateReleaseGuard: api.investigate,
}));
vi.mock("vue-router", () => ({
  useRoute: () => route,
  useRouter: () => ({ push: api.push }),
}));
const mountedPages: Array<{
  unmount: () => void;
  vm: { $: { isUnmounted: boolean } };
}> = [];
function page() {
  const wrapper = mount(ReleaseGuardReport, {
    attachTo: document.body,
    global: {
      stubs: { RouterLink: { props: ["to"], template: "<a><slot /></a>" } },
    },
  });
  mountedPages.push(wrapper);
  return wrapper;
}

describe("release guard report", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setLocale("zh-CN");
    route.params = { projectId: "project-1", guardId: "guard-1" };
    api.get.mockResolvedValue(guardReport());
    api.evidence.mockResolvedValue({
      items: [],
      nextCursor: 0,
      hasMore: false,
    });
    api.stop.mockResolvedValue(guardReport({ executionStatus: "stopping" }));
    api.analyses.mockResolvedValue({ items: [] });
    api.investigate.mockResolvedValue(guardReport());
  });
  afterEach(() => {
    for (const wrapper of mountedPages) {
      if (!wrapper.vm.$.isUnmounted) wrapper.unmount();
    }
    mountedPages.length = 0;
    vi.useRealTimers();
    setLocale("zh-CN");
  });
  it("retranslates an existing evidence error without refetching or changing the report result", async () => {
    api.evidence.mockRejectedValueOnce(
      new HttpError(
        500,
        { code: "unavailable", message: "后端原始中文错误" },
        "unavailable",
      ),
    );
    const wrapper = page();
    await flushPromises();
    const chinese = wrapper.get(".guard-evidence-section .guard-error").text();
    setLocale("de");
    await flushPromises();
    expect(wrapper.get(".guard-evidence-section .guard-error").text()).toBe(
      i18n.global.t("releaseGuard.requestFailed"),
    );
    expect(wrapper.get(".guard-evidence-section .guard-error").text()).not.toBe(
      chinese,
    );
    expect(
      wrapper.get(".guard-evidence-section .guard-error").text(),
    ).not.toContain("后端原始中文错误");
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(api.get).toHaveBeenCalledTimes(1);
    expect(api.evidence).toHaveBeenCalledTimes(1);
    expect(api.preview).not.toHaveBeenCalled();
    expect(api.send).not.toHaveBeenCalled();
    wrapper.unmount();
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
    const pending = {
      reference: "",
      repository: "",
      digest: "",
      digestKind: "config_id",
      identitySource: "script_pre_activation",
    };
    report.plan.targets[0].expectedImage = pending;
    report.targets[0].expectedImage = {
      ...pending,
      digest: `sha256:${"b".repeat(64)}`,
    };
    report.targets[0].conditions = [
      {
        name: "version",
        result: "passed",
        reason: "local_config_id_matched",
        evidenceIds: [],
      },
    ];
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-target").text()).toContain(
      `sha256:${"b".repeat(64)}`,
    );
    expect(wrapper.get(".guard-target").text()).toContain(
      "启动前固定的本地 Image ID",
    );
    const identity = wrapper.get(".guard-target .guard-image-identity");
    expect(identity.get("details").attributes("open")).toBeUndefined();
    expect(identity.get("details code").text()).toBe(
      `sha256:${"b".repeat(64)}`,
    );
    expect(identity.get(".guard-image-identity__kind").text()).toBe(
      "本地 Docker Image ID",
    );
    expect(identity.get(".guard-image-identity__hint").text()).toContain(
      "主机",
    );
    expect(wrapper.get(".guard-plan-targets").text()).toContain(
      "构建后、启动前固定 Image ID",
    );
    expect(wrapper.get(".guard-plan-targets").text()).not.toContain(
      "registry.test/app@",
    );
    wrapper.unmount();
  });
  it("explains incomplete deployment and recorded deployment health failure separately from runtime health", async () => {
    const report = guardReport();
    report.targets[0].conditions = [
      {
        name: "deployment_completion",
        result: "pending",
        reason: "deployment_completion_pending",
        evidenceIds: [],
      },
      {
        name: "deployment_health_gate",
        result: "failed",
        reason: "deployment_health_gate_failed",
        evidenceIds: [],
      },
    ];
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-target").text()).toContain("部署最终结果");
    expect(wrapper.get(".guard-target").text()).toContain(
      "等待本次部署的最终回执",
    );
    expect(wrapper.get(".guard-target").text()).toContain("部署时健康门控");
    expect(wrapper.get(".guard-target").text()).toContain(
      "持续 Docker 健康状态分别判断",
    );
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
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
  function sample(
    id: string,
    value: Record<string, unknown> = { outcome: "ok", running: true },
  ): GuardEvidence {
    return {
      id,
      sequence: 1,
      targetId: "target-1",
      kind: "runtime",
      eventAt: "2026-10-25T10:00:00Z",
      collectedAt: "2026-10-25T10:00:01Z",
      reason: "",
      summary: "typed_runtime_sample",
      truncated: false,
      value,
    };
  }
  it("uses the current report runId for its unified return button after report navigation", async () => {
    const wrapper = page();
    await flushPromises();
    await wrapper.get("button.guard-back").trigger("click");
    expect(api.push).toHaveBeenLastCalledWith({
      name: "run-detail",
      params: { id: "run-1" },
    });
    api.get.mockResolvedValue(guardReport({ id: "guard-2", runId: "run-2" }));
    route.params.guardId = "guard-2";
    await flushPromises();
    await wrapper.get("button.guard-back").trigger("click");
    expect(api.push).toHaveBeenLastCalledWith({
      name: "run-detail",
      params: { id: "run-2" },
    });
    wrapper.unmount();
  });
  it("keeps rule failures and their reasons ahead of the completion deadline and later running samples", async () => {
    const report = guardReport({ reason: "observation_window_ended" });
    report.targets[0].conditions[0].reason = "container_exited_or_oom";
    api.get.mockResolvedValue(report);
    api.evidence.mockResolvedValue({
      items: [sample("later-running")],
      nextCursor: 2,
      hasMore: false,
    });
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-verdict").text()).toContain(
      "已绑定容器已退出或发生 OOM",
    );
    expect(wrapper.get(".guard-verdict").text()).not.toContain(
      "观察窗口已结束",
    );
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(wrapper.get(".guard-evidence h3").text()).toContain("正在运行");
    wrapper.unmount();
  });
  it("groups identical failed conditions without repeating their explanation and keeps coverage and limits beside it", async () => {
    const report = guardReport();
    report.targets[0].conditions[0].reason = "container_exited_or_oom";
    report.targets.push({
      ...report.targets[0],
      id: "target-2",
      serverName: "Web 2",
      conditions: [
        {
          ...report.targets[0].conditions[0],
          evidenceIds: ["evidence-1", "evidence-2"],
        },
      ],
    });
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(
      wrapper.findAll(".guard-summary-primary .guard-failure-summary li"),
    ).toHaveLength(1);
    const failure = wrapper.get(".guard-failure-summary");
    expect(failure.text()).toContain("Web 1");
    expect(failure.text()).toContain("Web 2");
    expect(failure.findAll("button")).toHaveLength(2);
    expect(
      wrapper
        .get(".guard-summary-primary")
        .text()
        .match(/已绑定容器已退出或发生 OOM/g),
    ).toHaveLength(1);
    expect(
      wrapper.findAll(".guard-summary-secondary .guard-summary-metrics > div"),
    ).toHaveLength(2);
    expect(wrapper.get(".guard-summary-secondary").text()).toContain(
      "完整根因仍需独立证据",
    );
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    wrapper.unmount();
  });
  it("shows a readable timeline label once and retains unknown event kinds only in technical data", async () => {
    setLocale("en");
    api.get.mockResolvedValue(
      guardReport({
        timeline: [
          {
            id: "timeline-1",
            at: "2026-10-25T10:00:00Z",
            kind: "runtime_sample",
            targetId: "target-1",
            summary: "runtime_sample",
            evidenceIds: [],
          },
          {
            id: "timeline-2",
            at: "2026-10-25T10:00:01Z",
            kind: "future_internal_event",
            targetId: "target-1",
            summary: "future_internal_event",
            evidenceIds: [],
          },
        ],
      }),
    );
    const wrapper = page();
    await flushPromises();
    const events = wrapper.findAll(".guard-timeline li p");
    expect(events[0].text()).toBe("Runtime sample");
    expect(events[1].text()).toContain("Unconfirmed information");
    expect(wrapper.get(".guard-timeline").text()).not.toContain(
      "future_internal_event",
    );
    expect(wrapper.get(".guard-report-technical pre").text()).toContain(
      "future_internal_event",
    );
    wrapper.unmount();
  });
  it("locates cross-page citations by actual cursor, deduplicates and focuses the same visible card", async () => {
    api.evidence.mockResolvedValueOnce({
      items: [sample("first"), sample("first")],
      nextCursor: 9,
      hasMore: true,
    });
    api.evidence.mockResolvedValueOnce({
      items: [
        sample("first"),
        sample("evidence-1", { outcome: "ok", running: false }),
      ],
      nextCursor: 27,
      hasMore: false,
    });
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    expect(wrapper.get("#guard-reference-status").text()).toContain("尚未加载");
    expect(api.evidence).toHaveBeenCalledTimes(1);
    await wrapper.get("#guard-reference-status button").trigger("click");
    await flushPromises();
    expect(api.evidence).toHaveBeenLastCalledWith(
      "project-1",
      "guard-1",
      9,
      expect.any(Object),
    );
    expect(wrapper.findAll(".guard-evidence")).toHaveLength(2);
    expect(wrapper.get("#guard-reference-status").text()).toContain("已定位");
    expect(document.activeElement?.id).toBe("guard-evidence-evidence-1");
    expect(wrapper.get("#guard-evidence-evidence-1").classes()).toContain(
      "is-highlighted",
    );
    wrapper.unmount();
  });
  it("stops explicitly when a pagination cursor fails to advance", async () => {
    api.evidence.mockResolvedValueOnce({
      items: [sample("first")],
      nextCursor: 9,
      hasMore: true,
    });
    api.evidence.mockResolvedValueOnce({
      items: [sample("first")],
      nextCursor: 9,
      hasMore: true,
    });
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    await wrapper.get("#guard-reference-status button").trigger("click");
    await flushPromises();
    expect(wrapper.get("#guard-reference-status").text()).toContain("未推进");
    expect(wrapper.get("#guard-reference-status").find("button").exists()).toBe(
      false,
    );
    expect(api.evidence).toHaveBeenCalledTimes(2);
    expect(wrapper.findAll(".guard-evidence")).toHaveLength(1);
    wrapper.unmount();
  });
  it("reports missing, failed and expired citations without silent hash links", async () => {
    api.evidence.mockRejectedValueOnce(
      new HttpError(
        500,
        { code: "failed", message: "服务器中文错误" },
        "failed",
      ),
    );
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    expect(wrapper.get("#guard-reference-status").text()).toContain("读取失败");
    await wrapper.get("#guard-reference-status button").trigger("click");
    await flushPromises();
    expect(wrapper.get("#guard-reference-status").text()).toContain("未找到");
    api.get.mockResolvedValue(guardReport({ evidenceExpired: true }));
    await wrapper
      .get(".guard-header-actions button:last-child")
      .trigger("click");
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    expect(wrapper.get("#guard-reference-status").text()).toContain("已过期");
    expect(api.evidence).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });
  it("allows only one evidence request and rejects a late page after switching reports", async () => {
    let finish!: (page: {
      items: GuardEvidence[];
      nextCursor: number;
      hasMore: boolean;
    }) => void;
    api.evidence.mockResolvedValueOnce({
      items: [],
      nextCursor: 9,
      hasMore: true,
    });
    api.evidence.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    await wrapper.get("#guard-reference-status button").trigger("click");
    const pendingSignal = api.evidence.mock.calls[1][3].signal as AbortSignal;
    const more = wrapper
      .findAll("button")
      .find((button) => button.text() === "读取更多证据")!;
    await more.trigger("click");
    expect(api.evidence).toHaveBeenCalledTimes(2);
    api.get.mockResolvedValue(guardReport({ id: "guard-2", runId: "run-2" }));
    api.evidence.mockResolvedValueOnce({
      items: [sample("new-report")],
      nextCursor: 1,
      hasMore: false,
    });
    route.params.guardId = "guard-2";
    await flushPromises();
    expect(pendingSignal.aborted).toBe(true);
    finish({ items: [sample("old-report")], nextCursor: 99, hasMore: true });
    await flushPromises();
    expect(wrapper.find("#guard-evidence-old-report").exists()).toBe(false);
    expect(wrapper.find("#guard-evidence-new-report").exists()).toBe(true);
    expect(wrapper.find("#guard-reference-status").exists()).toBe(false);
    wrapper.unmount();
  });
  it("ignores an old report response that arrives after a route change", async () => {
    let finish!: (report: GuardReport) => void;
    api.get.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const wrapper = page();
    await flushPromises();
    const signal = api.get.mock.calls[0][2].signal as AbortSignal;
    api.get.mockResolvedValueOnce(
      guardReport({ id: "guard-2", runId: "run-2" }),
    );
    route.params.guardId = "guard-2";
    await flushPromises();
    finish(guardReport());
    await flushPromises();
    expect(signal.aborted).toBe(true);
    await wrapper.get("button.guard-back").trigger("click");
    expect(api.push).toHaveBeenLastCalledWith({
      name: "run-detail",
      params: { id: "run-2" },
    });
    wrapper.unmount();
  });
  it("opens a separate AI tab without preview or send and preserves the same component and scope", async () => {
    const wrapper = page();
    await flushPromises();
    const original = wrapper.getComponent(ReleaseGuardAnalysis).vm.$.uid;
    await wrapper.get(".guard-header-actions .app-btn--ai").trigger("click");
    expect(wrapper.get("#guard-tab-ai").attributes("aria-selected")).toBe(
      "true",
    );
    expect(wrapper.get("#guard-tabpanel-ai").isVisible()).toBe(true);
    await wrapper.get(".guard-analysis-scope input").setValue(true);
    await wrapper.get("#guard-tab-verification").trigger("click");
    await wrapper.get("#guard-tab-ai").trigger("click");
    expect(wrapper.findAllComponents(ReleaseGuardAnalysis)).toHaveLength(1);
    expect(wrapper.getComponent(ReleaseGuardAnalysis).vm.$.uid).toBe(original);
    expect(
      (wrapper.get(".guard-analysis-scope input").element as HTMLInputElement)
        .checked,
    ).toBe(true);
    expect(api.preview).not.toHaveBeenCalled();
    expect(api.send).not.toHaveBeenCalled();
    await wrapper
      .get("#guard-tab-ai")
      .trigger("keydown", { key: "ArrowRight" });
    expect(wrapper.get("#guard-tab-plan").attributes("aria-selected")).toBe(
      "true",
    );
    await wrapper.get("#guard-tab-plan").trigger("keydown", { key: "Home" });
    expect(
      wrapper.get("#guard-tab-verification").attributes("aria-selected"),
    ).toBe("true");
    wrapper.unmount();
  });
  it("uses localized generic UI for unknown codes and server errors without changing user target names", async () => {
    setLocale("en");
    const report = guardReport();
    report.targets[0].serverName = "用户服务器";
    report.targets[0].conditions[0] = {
      name: "internal_future_code",
      result: "failed",
      reason: "内部中文原因",
      evidenceIds: [],
    };
    api.get.mockResolvedValue(report);
    api.evidence.mockRejectedValueOnce(
      new HttpError(
        500,
        { code: "failed", message: "服务器中文错误" },
        "failed",
      ),
    );
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-verdict").text()).toContain(
      "Unrecognized check condition",
    );
    expect(wrapper.get(".guard-verdict").text()).toContain("用户服务器");
    expect(wrapper.get(".guard-verdict").text()).not.toContain("内部中文原因");
    expect(wrapper.get(".guard-evidence-section").text()).not.toContain(
      "服务器中文错误",
    );
    expect(wrapper.get(".guard-report-technical pre").text()).toContain(
      "内部中文原因",
    );
    expect(wrapper.get(".guard-header-actions").text()).toContain(
      "AI interpretation",
    );
    wrapper.unmount();
  });
  it("keeps the same in-flight AI preview while switching tabs and focuses the selected tab", async () => {
    let fail!: (cause: Error) => void;
    api.preview.mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          fail = reject;
        }),
    );
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-header-actions .app-btn--ai").trigger("click");
    const uid = wrapper.getComponent(ReleaseGuardAnalysis).vm.$.uid;
    await wrapper.get(".guard-analysis-scope input").setValue(true);
    await wrapper
      .findAll(".guard-analysis button")
      .find((button) => button.text() === "预览本次快照")!
      .trigger("click");
    await wrapper.get("#guard-tab-verification").trigger("click");
    await wrapper.get(".guard-header-actions .app-btn--ai").trigger("click");
    expect(document.activeElement?.id).toBe("guard-tab-ai");
    expect(wrapper.getComponent(ReleaseGuardAnalysis).vm.$.uid).toBe(uid);
    expect(
      wrapper.get(".guard-analysis-scope input").attributes("disabled"),
    ).toBeDefined();
    fail(new HttpError(400, null, "fixture failure"));
    await flushPromises();
    expect(wrapper.get(".guard-analysis-error").text()).toContain("请求失败");
    expect(api.preview).toHaveBeenCalledTimes(1);
    expect(api.send).not.toHaveBeenCalled();
    wrapper.unmount();
  });
  it("turns an expired evidence response into an explicit stopped lookup and keeps the rule failure", async () => {
    api.evidence.mockRejectedValueOnce(new HttpError(410, null, "expired"));
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    expect(wrapper.get("#guard-reference-status").text()).toContain("已过期");
    expect(wrapper.get("#guard-reference-status").find("button").exists()).toBe(
      false,
    );
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(api.evidence).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
  it("waits for the initial evidence request before classifying a citation and focuses it only once", async () => {
    vi.useFakeTimers();
    let finish!: (value: {
      items: GuardEvidence[];
      nextCursor: number;
      hasMore: boolean;
    }) => void;
    api.evidence.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    api.get.mockResolvedValue(
      guardReport({ executionStatus: "observing", finishedAt: undefined }),
    );
    const wrapper = page();
    await flushPromises();
    await wrapper.get(".guard-failure-summary button").trigger("click");
    expect(wrapper.get("#guard-reference-status").text()).toContain(
      "正在读取并查找",
    );
    expect(wrapper.get("#guard-reference-status").text()).not.toContain(
      "未找到",
    );
    finish({ items: [sample("evidence-1")], nextCursor: 1, hasMore: false });
    await flushPromises();
    expect(document.activeElement?.id).toBe("guard-evidence-evidence-1");
    const back = wrapper.get("button.guard-back").element as HTMLButtonElement;
    back.focus();
    await vi.advanceTimersByTimeAsync(4000);
    await flushPromises();
    expect(document.activeElement).toBe(back);
    expect(api.get).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });
  it.each(["click", "keyboard"])(
    "cancels a pending citation focus when the user switches tabs by %s",
    async (method) => {
      let finish!: (value: {
        items: GuardEvidence[];
        nextCursor: number;
        hasMore: boolean;
      }) => void;
      api.evidence.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );
      const wrapper = page();
      await flushPromises();
      await wrapper.get(".guard-failure-summary button").trigger("click");
      if (method === "click")
        await wrapper.get("#guard-tab-ai").trigger("click");
      else
        await wrapper
          .get("#guard-tab-verification")
          .trigger("keydown", { key: "ArrowRight" });
      const expected =
        method === "click" ? "guard-tab-ai" : "guard-tab-investigation";
      expect(document.activeElement?.id).toBe(expected);
      finish({ items: [sample("evidence-1")], nextCursor: 1, hasMore: false });
      await flushPromises();
      expect(document.activeElement?.id).toBe(expected);
      expect(wrapper.find("#guard-reference-status").exists()).toBe(false);
      expect(api.preview).not.toHaveBeenCalled();
      expect(api.send).not.toHaveBeenCalled();
      wrapper.unmount();
    },
  );
  it("keeps a newer polled failure when an older same-report investigation completes", async () => {
    vi.useFakeTimers();
    const initial = guardReport({
      executionStatus: "observing",
      eventsCursor: 1,
      verificationResult: null,
      finishedAt: undefined,
    });
    initial.targets[0].verificationResult = null;
    initial.targets[0].conditions = [];
    api.get.mockResolvedValue(initial);
    let finish!: (value: GuardReport) => void;
    api.investigate.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const wrapper = page();
    await flushPromises();
    await wrapper.get("#guard-tab-investigation").trigger("click");
    await wrapper
      .getComponent(ReleaseGuardInvestigation)
      .get("button")
      .trigger("click");
    const signal = api.investigate.mock.calls[0][2].signal as AbortSignal;
    api.get.mockResolvedValue(
      guardReport({
        executionStatus: "observing",
        eventsCursor: 2,
        verificationResult: "failed",
        finishedAt: undefined,
      }),
    );
    await vi.advanceTimersByTimeAsync(4000);
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(signal.aborted).toBe(false);
    finish({ ...initial, verificationResult: "passed" });
    await flushPromises();
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(wrapper.get(".guard-investigation-error").text()).toContain(
      "请先刷新报告",
    );
    wrapper.unmount();
  });
  it("also rejects an older investigation cursor at the parent boundary", async () => {
    api.get.mockResolvedValue(
      guardReport({ eventsCursor: 2, verificationResult: "failed" }),
    );
    const wrapper = page();
    await flushPromises();
    wrapper
      .getComponent(ReleaseGuardInvestigation)
      .vm.$emit(
        "updated",
        guardReport({ eventsCursor: 1, verificationResult: "passed" }),
      );
    await flushPromises();
    expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
      "failed",
    );
    expect(
      wrapper.get("#guard-tabpanel-investigation .guard-error").text(),
    ).toContain("请先刷新报告");
    wrapper.unmount();
  });
  it.each([
    {
      executionStatus: "blocked",
      reason: "project_configuration_changed",
      expected: "目标身份或配置已变化",
    },
    {
      executionStatus: "interrupted",
      reason: "service_restarted",
      expected: "护航服务重启中断了观察",
    },
  ])(
    "explains a non-failed abnormal ending for $executionStatus next to the execution state",
    async (entry) => {
      api.get.mockResolvedValue(
        guardReport({
          executionStatus: entry.executionStatus,
          reason: entry.reason,
          verificationResult: "insufficient",
        }),
      );
      const wrapper = page();
      await flushPromises();
      expect(wrapper.get(".guard-ending-reason").text()).toContain(
        entry.expected,
      );
      expect(wrapper.get(".guard-ending-reason").isVisible()).toBe(true);
      expect(wrapper.get(".guard-verdict").attributes("data-result")).toBe(
        "insufficient",
      );
      wrapper.unmount();
    },
  );
  it("distinguishes candidate and support/counter contexts in investigation citation names", async () => {
    const report = guardReport();
    report.investigation.candidates = [
      "container_exited_or_oom",
      "docker_unhealthy",
    ].map((title, index) => ({
      id: `candidate-${index}`,
      title,
      supportingEvidenceIds: ["support"],
      counterEvidenceIds: ["counter"],
      missingEvidence: [],
      nextReadOnlyCheck: "",
    }));
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    const labels = wrapper
      .findAll(".guard-candidate a")
      .map((link) => link.attributes("aria-label"));
    expect(new Set(labels).size).toBe(4);
    expect(labels[0]).toContain(
      i18n.global.t("releaseGuard.supportingEvidence"),
    );
    expect(labels[1]).toContain(i18n.global.t("releaseGuard.counterEvidence"));
    expect(labels[0]).toContain(
      wrapper.findAll(".guard-candidate h3")[0].text(),
    );
    expect(labels[2]).toContain(
      wrapper.findAll(".guard-candidate h3")[1].text(),
    );
    wrapper.unmount();
  });
  it("identifies separate containers on the same server in failure groups and timeline events", async () => {
    const report = guardReport();
    report.targets[0].serverName = "Shared server";
    report.targets.push({ ...report.targets[0], id: "target-2" });
    report.plan.targets[0].serverName = "Shared server";
    report.plan.targets.push({
      ...report.plan.targets[0],
      id: "target-2",
      containerName: "worker",
    });
    report.timeline = ["target-1", "target-2"].map((targetId, index) => ({
      id: `timeline-${index}`,
      targetId,
      at: "2026-10-25T10:00:00Z",
      kind: "runtime_sample",
      summary: "runtime_sample",
      evidenceIds: [],
    }));
    api.get.mockResolvedValue(report);
    const wrapper = page();
    await flushPromises();
    expect(wrapper.get(".guard-failure-summary").text()).toContain(
      "Shared server / app",
    );
    expect(wrapper.get(".guard-failure-summary").text()).toContain(
      "Shared server / worker",
    );
    expect(
      wrapper.findAll(".guard-timeline-target").map((target) => target.text()),
    ).toEqual(["· Shared server / app", "· Shared server / worker"]);
    wrapper.unmount();
  });
});

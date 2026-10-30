import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import Dashboard from "./Dashboard.vue";
import { HttpError } from "../api/http";
const api = vi.hoisted(() => ({
  runs: vi.fn(),
  projects: vi.fn(),
  servers: vi.fn(),
  metrics: vi.fn(),
  dora: vi.fn(),
  alerts: vi.fn(),
  diagnosis: vi.fn(),
  env: vi.fn(),
}));
vi.mock("vue-router", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("../api/runs", () => ({ listRuns: api.runs }));
vi.mock("../api/projects", () => ({ listProjects: api.projects }));
vi.mock("../api/servers", () => ({
  listServers: api.servers,
  getAllServerMetrics: api.metrics,
}));
vi.mock("../api/environments", () => ({ listEnvironmentDeployments: api.env }));
vi.mock("../api/metrics", async (original) => ({
  ...(await original<typeof import("../api/metrics")>()),
  getDoraMetrics: api.dora,
}));
vi.mock("../api/anomaly", () => ({ listAnomalyAlerts: api.alerts }));
vi.mock("../api/settings", () => ({ getDiagnosisStats: api.diagnosis }));
const stubs = {
  DashRecentRuns: true,
  DashServers: true,
  DashEnvironments: true,
  DoraMetricCard: true,
};
const alert = {
  id: "a",
  message: "Real alert retained",
  serverName: "Test",
  metric: "cpu",
  at: "2026-10-30T00:00:00Z",
};
describe("dashboard independent data states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.runs.mockResolvedValue({ items: [], page: 1, total: 0 });
    api.projects.mockResolvedValue([]);
    api.servers.mockResolvedValue([]);
    api.metrics.mockResolvedValue([]);
    api.dora.mockResolvedValue(null);
    api.alerts.mockResolvedValue([]);
    api.diagnosis.mockResolvedValue(null);
    api.env.mockResolvedValue([]);
  });
  it("does not label a failed alerts request as no anomalies and retries only that block", async () => {
    api.alerts.mockRejectedValueOnce(new Error("offline"));
    const w = mount(Dashboard, { global: { stubs } });
    await flushPromises();
    const alerts = w.find(".bento-alerts .dash-data");
    expect(alerts.attributes("data-state")).toBe("error");
    expect(w.text()).not.toContain("暂无异常");
    const runCalls = api.runs.mock.calls.length;
    await alerts.find("button").trigger("click");
    await flushPromises();
    expect(w.find(".bento-alerts .dash-data").attributes("data-state")).toBe(
      "empty",
    );
    expect(api.runs).toHaveBeenCalledTimes(runCalls);
    w.unmount();
  });
  it("retains previous alerts with stale warning when refresh fails and local retry recovers", async () => {
    api.alerts
      .mockResolvedValueOnce([alert])
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce([]);
    const w = mount(Dashboard, { global: { stubs } });
    await flushPromises();
    expect(w.text()).toContain("Real alert retained");
    await w.find(".dash-ghost").trigger("click");
    await flushPromises();
    expect(w.find(".bento-alerts .dash-data").attributes("data-state")).toBe(
      "stale",
    );
    expect(w.text()).toContain("Real alert retained");
    expect(w.text()).toContain("最后成功更新");
    await w.find(".bento-alerts .data-notice button").trigger("click");
    await flushPromises();
    expect(w.find(".bento-alerts .dash-data").attributes("data-state")).toBe(
      "empty",
    );
    expect(w.text()).not.toContain("Real alert retained");
    w.unmount();
  });
  it("keeps forbidden distinct from empty and does not invent a failed KPI value", async () => {
    api.projects.mockRejectedValue(new HttpError(403, null, "denied"));
    api.metrics.mockRejectedValue(new Error("offline"));
    const w = mount(Dashboard, { global: { stubs } });
    await flushPromises();
    expect(w.text()).toContain("暂无权限");
    expect(w.find(".kpi-value").text()).toBe("—");
    expect(w.find(".bento-env").attributes("data-state")).toBe("error");
    w.unmount();
  });
  it("reports a server without a metrics sample as unknown instead of offline", async () => {
    api.servers.mockResolvedValue([{ id: "missing", name: "Missing metrics" }]);
    const w = mount(Dashboard, { global: { stubs } });
    await flushPromises();
    const kpis = w.findAll(".kpi");
    expect(kpis[4]!.text()).toContain("—/1");
    expect(kpis[4]!.text()).toContain("1 台状态未知");
    w.unmount();
  });
  it("does not turn one failed environment query into an empty deployment list", async () => {
    api.projects.mockResolvedValue([{ id: "p", name: "P" }]);
    api.env.mockRejectedValue(new Error("deployment unavailable"));
    const w = mount(Dashboard, { global: { stubs } });
    await flushPromises();
    expect(w.find(".bento-env").attributes("data-state")).toBe("error");
    expect(w.find(".bento-runs").attributes("data-state")).toBe("empty");
    w.unmount();
  });
});

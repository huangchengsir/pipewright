import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { reactive } from "vue";
import DoraDashboard from "./DoraDashboard.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
import type { DoraMetrics } from "../api/metrics";
const api = vi.hoisted(() => ({
  dora: vi.fn(),
  projects: vi.fn(),
  replace: vi.fn(),
}));
let query: Record<string, string>;
vi.mock("vue-router", () => ({
  useRoute: () => ({
    get query() {
      return query;
    },
  }),
  useRouter: () => ({ replace: api.replace }),
}));
vi.mock("../api/metrics", async (original) => ({
  ...(await original<typeof import("../api/metrics")>()),
  getDoraMetrics: api.dora,
  getDoraProjects: api.projects,
}));
let wrapper: VueWrapper | undefined;
const metrics = (count: number): DoraMetrics => {
  const metric = { value: 0, band: "none" as const, sampleCount: 0 };
  return {
    window: "30d",
    windowDays: 30,
    projectId: "",
    totalDeployments: count,
    successfulDeployments: count,
    failedDeployments: 0,
    deploymentFrequency: 0,
    deploymentFrequencyPerWeek: 0,
    leadTimeSeconds: 0,
    changeFailureRate: 0,
    mttrSeconds: 0,
    generatedAt: "",
    metrics: {
      deploymentFrequency: metric,
      leadTime: metric,
      changeFailureRate: metric,
      mttr: metric,
    },
    trend: [],
  };
};
function render() {
  wrapper = mount(DoraDashboard, {
    global: {
      stubs: {
        DoraMetricCard: true,
        AppSelect: {
          props: ["options"],
          template:
            '<select><option v-for="p in options" :key="p.value">{{p.label}}</option></select>',
        },
      },
    },
  });
  return wrapper;
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  query = reactive({});
  useSessionStore().user = {
    username: "alice",
    userId: "alice",
    multiTenant: true,
  };
  usePermissionStore().setTeam("a");
  api.projects.mockResolvedValue([]);
  api.dora.mockResolvedValue(metrics(5));
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("binds both reads to the team and ignores late old-team metrics and names", async () => {
  const mr: ((v: DoraMetrics) => void)[] = [];
  const pr: ((v: { id: string; name: string }[]) => void)[] = [];
  api.dora.mockImplementation(
    () => new Promise<DoraMetrics>((r) => mr.push(r)),
  );
  api.projects.mockImplementation(() => new Promise((r) => pr.push(r)));
  render();
  await flushPromises();
  const signal = api.dora.mock.calls[0]![1].signal as AbortSignal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect(api.dora.mock.calls[1]![1].teamId).toBe("b");
  expect(api.projects.mock.calls[1]![0].teamId).toBe("b");
  mr[1]!(metrics(20));
  pr[1]!([{ id: "b", name: "Team B" }]);
  await flushPromises();
  mr[0]!(metrics(99));
  pr[0]!([{ id: "a", name: "Team A" }]);
  await flushPromises();
  expect(wrapper!.get(".dora-summary__num").text()).toBe("20");
  expect(wrapper!.text()).toContain("Team B");
  expect(wrapper!.text()).not.toContain("Team A");
});
it("clears private data on logout without another request", async () => {
  api.projects.mockResolvedValue([{ id: "a", name: "Private project" }]);
  render();
  await flushPromises();
  useSessionStore().user = null;
  await flushPromises();
  expect(wrapper!.find(".dora-summary").exists()).toBe(false);
  expect(wrapper!.text()).not.toContain("Private project");
  expect(api.dora).toHaveBeenCalledTimes(1);
});
it.each(["unknown", "no-team", "forced-password"] as const)(
  "does not query while %s",
  async (state) => {
    if (state === "unknown") useSessionStore().user = undefined;
    if (state === "no-team") usePermissionStore().setTeam("");
    if (state === "forced-password")
      useSessionStore().user = {
        username: "alice",
        multiTenant: true,
        mustChangePassword: true,
      };
    render();
    await flushPromises();
    expect(api.dora).not.toHaveBeenCalled();
    expect(api.projects).not.toHaveBeenCalled();
  },
);
it("refresh removes old figures and rejects late picker results after revocation", async () => {
  render();
  await flushPromises();
  let picker!: (v: { id: string; name: string }[]) => void;
  api.projects.mockImplementationOnce(() => new Promise((r) => (picker = r)));
  api.dora.mockRejectedValueOnce(
    new HttpError(403, { code: "forbidden", message: "denied" }, "denied"),
  );
  await wrapper!.get(".view-header button").trigger("click");
  await flushPromises();
  picker([{ id: "a", name: "Revoked project" }]);
  await flushPromises();
  expect(wrapper!.find(".dora-summary").exists()).toBe(false);
  expect(wrapper!.text()).not.toContain("Revoked project");
  expect(wrapper!.text()).toContain("denied");
});
it("uses latest URL filter and rejects an old response", async () => {
  let old!: (v: DoraMetrics) => void;
  api.dora
    .mockImplementationOnce(() => new Promise<DoraMetrics>((r) => (old = r)))
    .mockResolvedValueOnce(metrics(3));
  render();
  await flushPromises();
  query.projectId = "second";
  await flushPromises();
  expect(api.dora.mock.calls[1]![0].projectId).toBe("second");
  old(metrics(99));
  await flushPromises();
  expect(wrapper!.get(".dora-summary__num").text()).toBe("3");
});
it("aborts both requests on unmount", async () => {
  api.dora.mockImplementation(() => new Promise(() => {}));
  render();
  await flushPromises();
  const signal = api.dora.mock.calls[0]![1].signal as AbortSignal;
  expect(api.projects.mock.calls[0]![0].signal).toBe(signal);
  wrapper!.unmount();
  wrapper = undefined;
  expect(signal.aborted).toBe(true);
});
it("preserves single-admin requests and display", async () => {
  useSessionStore().user = { username: "admin", multiTenant: false };
  render();
  await flushPromises();
  expect(api.dora.mock.calls[0]![1].teamId).toBeUndefined();
  expect(wrapper!.get(".dora-summary__num").text()).toBe("5");
});
it("all three window buttons update URL state", async () => {
  render();
  await flushPromises();
  const buttons = wrapper!.findAll(".dora-segmented__btn");
  expect(buttons).toHaveLength(3);
  for (let i = 0; i < buttons.length; i++) {
    await buttons[i]!.trigger("click");
    expect(api.replace).toHaveBeenLastCalledWith({
      query: { window: ["7d", "30d", "90d"][i] },
    });
  }
});

it.each([401, 403])(
  "rejects late metrics when the picker reports authorization failure %s",
  async (status) => {
    let finish!: (v: DoraMetrics) => void;
    api.dora.mockImplementationOnce(
      () => new Promise<DoraMetrics>((r) => (finish = r)),
    );
    api.projects.mockRejectedValueOnce(
      new HttpError(status, { code: "forbidden", message: "denied" }, "denied"),
    );
    render();
    await flushPromises();
    finish(metrics(99));
    await flushPromises();
    expect(wrapper!.find(".dora-summary").exists()).toBe(false);
    expect(wrapper!.text()).toContain("denied");
  },
);

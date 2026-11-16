import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import Dashboard from "./Dashboard.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
const api = vi.hoisted(() => ({
  section: vi.fn(),
  oldProjects: vi.fn(),
  oldRuns: vi.fn(),
}));
vi.mock("vue-router", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("../api/dashboard", () => ({ getDashboardSection: api.section }));
vi.mock("../api/projects", () => ({ listProjects: api.oldProjects }));
vi.mock("../api/runs", () => ({ listRuns: api.oldRuns }));
let wrapper: VueWrapper | undefined;
const row = (name: string) => ({
  id: name,
  projectId: name,
  projectName: name,
  status: "success",
  createdAt: "2026-11-13T00:00:00Z",
  durationMs: 100,
  trigger: { branch: "main" },
});
const result = (section: string) =>
  section === "recent"
    ? { items: [row("Current team")], page: 1, total: 1 }
    : section === "running"
      ? { items: [], page: 1, total: 0 }
      : section === "dora" || section === "diagnosis"
        ? null
        : [];
function render() {
  wrapper = mount(Dashboard, { global: { stubs: { DoraMetricCard: true } } });
  return wrapper;
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  useSessionStore().user = {
    username: "alice",
    userId: "alice",
    multiTenant: true,
  };
  usePermissionStore().setTeam("a");
  api.section.mockImplementation(async (section) => result(section));
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("uses only minimal overview endpoints bound to the selected team", async () => {
  render();
  await flushPromises();
  expect(api.section).toHaveBeenCalledTimes(9);
  for (const call of api.section.mock.calls) {
    expect(new Headers(call[1].headers).get("X-Pipewright-Team")).toBe("a");
    expect(call[1].signal).toBeInstanceOf(AbortSignal);
  }
  expect(api.oldProjects).not.toHaveBeenCalled();
  expect(api.oldRuns).not.toHaveBeenCalled();
  expect(wrapper!.text()).toContain("Current team");
});
it("does not commit old-team responses after switching team", async () => {
  let old!: (v: unknown) => void;
  api.section.mockImplementation((section, options) =>
    section === "recent" &&
    new Headers(options.headers).get("X-Pipewright-Team") === "a"
      ? new Promise((r) => (old = r))
      : Promise.resolve(result(section)),
  );
  render();
  await flushPromises();
  const oldSignal = api.section.mock.calls.find((c) => c[0] === "recent")![1]
    .signal as AbortSignal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  expect(oldSignal.aborted).toBe(true);
  old({ items: [row("Secret old team")], total: 55, page: 1 });
  await flushPromises();
  expect(wrapper!.text()).not.toContain("Secret old team");
  expect(wrapper!.text()).toContain("Current team");
  expect(api.section).toHaveBeenCalledTimes(18);
});
it("clears all sections and rejects pending results on logout", async () => {
  let old!: (v: unknown) => void;
  api.section.mockImplementation((section) =>
    section === "recent"
      ? new Promise((r) => (old = r))
      : Promise.resolve(result(section)),
  );
  render();
  await flushPromises();
  useSessionStore().user = null;
  await flushPromises();
  old({ items: [row("Secret old user")], total: 55, page: 1 });
  await flushPromises();
  expect(wrapper!.text()).not.toContain("Secret old user");
  expect(api.section).toHaveBeenCalledTimes(9);
});
it.each(["unknown", "no-team", "forced-change"] as const)(
  "does not request data with %s identity",
  async (state) => {
    if (state === "unknown") useSessionStore().user = undefined;
    if (state === "no-team") usePermissionStore().setTeam("");
    if (state === "forced-change")
      useSessionStore().user = {
        username: "alice",
        multiTenant: true,
        mustChangePassword: true,
      };
    render();
    await flushPromises();
    expect(api.section).not.toHaveBeenCalled();
    expect(api.oldProjects).not.toHaveBeenCalled();
  },
);
it("refresh authorization failure clears prior content rather than presenting it as stale", async () => {
  render();
  await flushPromises();
  expect(wrapper!.text()).toContain("Current team");
  api.section.mockRejectedValue(
    new HttpError(403, { code: "forbidden", message: "denied" }, "denied"),
  );
  await wrapper!.get(".dash-ghost").trigger("click");
  await flushPromises();
  expect(wrapper!.text()).not.toContain("Current team");
  expect(wrapper!.text()).toContain("暂无权限");
});
it("retries only the failed section and uses current team identity", async () => {
  api.section.mockImplementation(async (section) => {
    if (section === "alerts") throw new Error("offline");
    return result(section);
  });
  render();
  await flushPromises();
  expect(
    wrapper!.get(".bento-alerts .dash-data").attributes("data-state"),
  ).toBe("error");
  api.section.mockClear();
  api.section.mockImplementation(async (section) => result(section));
  await wrapper!.get(".bento-alerts .dash-data button").trigger("click");
  await flushPromises();
  expect(api.section.mock.calls.map((c) => c[0])).toEqual(["alerts"]);
  expect(
    new Headers(api.section.mock.calls[0]![1].headers).get("X-Pipewright-Team"),
  ).toBe("a");
});
it("aborts every outstanding section request on unmount", async () => {
  api.section.mockImplementation(() => new Promise(() => {}));
  render();
  await flushPromises();
  const signals = api.section.mock.calls.map((c) => c[1].signal as AbortSignal);
  wrapper!.unmount();
  wrapper = undefined;
  expect(signals).toHaveLength(9);
  expect(signals.every((s) => s.aborted)).toBe(true);
});

it("clears mounted data when the same user object becomes password-restricted", async () => {
  render();
  await flushPromises();
  expect(wrapper!.text()).toContain("Current team");
  useSessionStore().user!.mustChangePassword = true;
  await flushPromises();
  expect(wrapper!.text()).not.toContain("Current team");
  expect(api.section).toHaveBeenCalledTimes(9);
});

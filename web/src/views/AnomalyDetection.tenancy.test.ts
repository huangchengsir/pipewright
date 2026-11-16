import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import Page from "./AnomalyDetection.vue";
import Content from "./AnomalyDetectionContent.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
const api = vi.hoisted(() => ({
  rules: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  check: vi.fn(),
  alerts: vi.fn(),
  config: vi.fn(),
  metrics: vi.fn(),
  servers: vi.fn(),
  decisions: vi.fn(),
}));
vi.mock("../api/anomaly", () => ({
  listAnomalyRules: api.rules,
  createAnomalyRule: api.create,
  updateAnomalyRule: api.update,
  deleteAnomalyRule: api.remove,
  checkAnomaly: api.check,
  listAnomalyAlerts: api.alerts,
  getAnomalyConfig: api.config,
  getMetricsHistory: api.metrics,
}));
vi.mock("../api/servers", () => ({ listServers: api.servers }));
vi.mock("../api/access", () => ({ access: { decisions: api.decisions } }));
let wrapper: VueWrapper | undefined;
let allowed: Set<string>;
const row = {
  id: "rule",
  serverId: "s1",
  metric: "cpu",
  operator: "gt",
  threshold: 80,
  enabled: true,
  aiAnalysisEnabled: false,
  createdAt: "2026-01-01T00:00:00Z",
};
function vm() {
  return wrapper!.getComponent(Content).vm as unknown as {
    form: typeof row;
    editForm: typeof row;
    submitRule: () => Promise<void>;
    runCheck: () => Promise<void>;
    startEdit: (r: typeof row) => void;
    saveEdit: (r: typeof row) => Promise<void>;
    removeRule: (r: typeof row) => Promise<void>;
    load: () => Promise<void>;
  };
}
async function render() {
  wrapper = mount(Page, { attachTo: document.body, global: { stubs: { MetricTrendChart: true } } });
  await flushPromises();
  return wrapper;
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  useSessionStore().setUser({ username: "u", userId: "u", multiTenant: true });
  usePermissionStore().setTeam("a");
  allowed = new Set();
  api.rules.mockResolvedValue([row]);
  api.servers.mockResolvedValue([
    { id: "s1", name: "Allowed" },
    { id: "s2", name: "Read only" },
  ]);
  api.alerts.mockResolvedValue([]);
  api.metrics.mockResolvedValue([]);
  api.config.mockResolvedValue({ intervalSeconds: 0, cooldownSeconds: 300 });
  api.create.mockImplementation(async (input) => ({
    ...input,
    id: "new",
    createdAt: row.createdAt,
  }));
  api.update.mockImplementation(async (id, input) => ({
    ...input,
    id,
    createdAt: row.createdAt,
  }));
  api.remove.mockResolvedValue(undefined);
  api.decisions.mockImplementation(
    async (team: string, qs: { action: string; resourceId: string }[]) => ({
      userId: "u",
      teamId: team,
      modeEpoch: 1,
      revision: 1,
      catalogVersion: 1,
      allowed: qs.map((q) => allowed.has(q.action + ":" + q.resourceId)),
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("hides mutation controls and rejects direct form/function calls for a reader", async () => {
  const w = await render();
  expect(w.findAll(".rule-item__actions button")).toHaveLength(0);
  expect(w.find(".rule-form button[type=submit]").exists()).toBe(false);
  expect(w.find('#rule-server option[value=""]').exists()).toBe(false);
  expect(
    (
      w.findAll(".rule-form input[type=checkbox]")[1]!
        .element as HTMLInputElement
    ).disabled,
  ).toBe(true);
  await w.get(".rule-form").trigger("submit");
  await vm().removeRule(row);
  vm().startEdit(row);
  await vm().runCheck();
  await flushPromises();
  expect(api.create).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
  expect(api.check).not.toHaveBeenCalled();
  expect(w.find(".rule-edit").exists()).toBe(false);
});
it("allows selected-server management without granting AI or global detection", async () => {
  allowed.add("anomaly.manage:s1");
  const w = await render();
  expect(w.findAll(".rule-item__actions button")).toHaveLength(2);
  await w.get(".rule-form").trigger("submit");
  await flushPromises();
  expect(api.create).toHaveBeenCalledWith(
    expect.objectContaining({ serverId: "s1", aiAnalysisEnabled: false }),
    expect.objectContaining({ headers: { "X-Pipewright-Team": "a" } }),
  );
  expect(vm().form.serverId).toBe("s1");
  vm().form.aiAnalysisEnabled = true;
  await vm().submitRule();
  expect(api.create).toHaveBeenCalledTimes(1);
  await vm().runCheck();
  expect(api.check).not.toHaveBeenCalled();
  await vm().removeRule(row);
  expect(api.remove).toHaveBeenCalledWith("rule", expect.anything());
});
it("requires both the source and destination grants when editing", async () => {
  allowed.add("anomaly.manage:s1");
  await render();
  vm().startEdit(row);
  await flushPromises();
  vm().editForm.serverId = "s2";
  await flushPromises();
  await vm().saveEdit(row);
  expect(api.update).not.toHaveBeenCalled();
  vm().editForm.serverId = "s1";
  await flushPromises();
  await vm().saveEdit(row);
  expect(api.update).toHaveBeenCalledTimes(1);
});
it("permits explicit AI opt-in only after the separate permission resolves", async () => {
  allowed.add("anomaly.manage:s1");
  allowed.add("anomaly.analyze:s1");
  await render();
  vm().form.aiAnalysisEnabled = true;
  await flushPromises();
  await vm().submitRule();
  expect(api.create).toHaveBeenCalledWith(
    expect.objectContaining({ aiAnalysisEnabled: true }),
    expect.anything(),
  );
});
it("can disable an existing AI rule after AI permission is removed", async () => {
  const aiRow = { ...row, aiAnalysisEnabled: true };
  api.rules.mockResolvedValue([aiRow]);
  allowed.add("anomaly.manage:s1");
  await render();
  vm().startEdit(aiRow);
  await flushPromises();
  vm().editForm.enabled = false;
  await flushPromises();
  await vm().saveEdit(aiRow);
  expect(api.update).toHaveBeenCalledWith(
    "rule",
    expect.objectContaining({ enabled: false, aiAnalysisEnabled: true }),
    expect.anything(),
  );
});
it("blocks direct submit immediately when cached permissions are invalidated", async () => {
  allowed.add("anomaly.manage:s1");
  await render();
  allowed.clear();
  usePermissionStore().invalidate();
  await vm().submitRule();
  await vm().removeRule(row);
  expect(api.create).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
});
it("clears loaded data after forbidden refresh", async () => {
  await render();
  api.rules.mockRejectedValue(new HttpError(403, null, "Denied"));
  await vm().load();
  await flushPromises();
  expect(wrapper!.findAll(".rule-item")).toHaveLength(0);
});
it("aborts old-team requests and ignores their late response", async () => {
  let resolve!: (value: unknown) => void;
  api.rules.mockImplementationOnce(
    () =>
      new Promise((r) => {
        resolve = r;
      }),
  );
  await render();
  const options = api.rules.mock.calls[0]![0];
  usePermissionStore().setTeam("b");
  api.rules.mockResolvedValue([]);
  await flushPromises();
  resolve([{ ...row, id: "old-team-secret" }]);
  await flushPromises();
  expect(options.signal.aborted).toBe(true);
  expect(wrapper!.findAll(".rule-item")).toHaveLength(0);
});
it("clears the page on logout and does not fetch for unknown identity", async () => {
  await render();
  useSessionStore().clearSession();
  await flushPromises();
  expect(wrapper!.findComponent(Content).exists()).toBe(false);
  api.rules.mockClear();
  await flushPromises();
  expect(api.rules).not.toHaveBeenCalled();
});

describe("actual anomaly permissions by instance and server", () => {
  const requestScope = () => expect.objectContaining({
    headers: { "X-Pipewright-Team": "a" }, signal: expect.any(AbortSignal)
  });

  it("uses the actual instance-wide check button only while the instance action remains granted", async () => {
    allowed.add("anomaly.manage:s1");
    const w = await render();
    expect(w.find(".view-header button").exists()).toBe(false);
    allowed.add("system.manage:instance");
    usePermissionStore().invalidate();
    await flushPromises();
    api.check.mockResolvedValue([]);
    const check = w.get(".view-header button");
    await check.trigger("click");
    await flushPromises();
    expect(api.check).toHaveBeenCalledExactlyOnceWith(requestScope());
    expect(w.get(".check-banner").text()).toContain("检测完成");
    allowed.delete("system.manage:instance");
    usePermissionStore().invalidate();
    await flushPromises();
    await check.trigger("click");
    await flushPromises();
    expect(api.check).toHaveBeenCalledOnce();
    expect(w.find(".view-header button").exists()).toBe(false);
    expect(w.findAll(".rule-item__actions button")).toHaveLength(2);
  });

  it("discards the instance-wide check outcome after the action is revoked while awaiting the backend", async () => {
    allowed.add("system.manage:instance");
    let done!: (value: unknown[]) => void;
    api.check.mockImplementation(() => new Promise(resolve => { done = resolve; }));
    const w = await render();
    await w.get(".view-header button").trigger("click");
    await flushPromises();
    expect(api.check).toHaveBeenCalledOnce();
    const readsBefore = api.alerts.mock.calls.length;
    allowed.delete("system.manage:instance");
    usePermissionStore().invalidate();
    await flushPromises();
    done([{ id: "sensitive-instance-result" }]);
    await flushPromises();
    expect(w.find(".check-banner").exists()).toBe(false);
    expect(api.alerts).toHaveBeenCalledTimes(readsBefore);
  });

  it("deletes only the selected authorized server rule using its actual button", async () => {
    allowed.add("anomaly.manage:s1");
    api.rules.mockResolvedValue([row, { ...row, id: "sibling", serverId: "s2", threshold: 42 }]);
    const w = await render();
    const items = w.findAll(".rule-item");
    expect(items[1]!.find(".rule-item__actions button").exists()).toBe(false);
    const remove = items[0]!.findAll(".rule-item__actions button")[1]!;
    await remove.trigger("click");
    await flushPromises();
    expect(api.remove).toHaveBeenCalledExactlyOnceWith("rule", requestScope());
    expect(w.findAll(".rule-item")).toHaveLength(1);
    expect(w.get(".rule-item").text()).toContain("42%");
    allowed.delete("anomaly.manage:s1");
    usePermissionStore().invalidate();
    await flushPromises();
    await remove.trigger("click");
    await flushPromises();
    expect(api.remove).toHaveBeenCalledOnce();
  });

  it("uses the native rule creation button for a specific permitted server without broader actions", async () => {
    allowed.add("anomaly.manage:s1");
    const w = await render();
    await w.get("#rule-threshold").setValue("71");
    const save = w.get<HTMLButtonElement>(".rule-form button[type=submit]");
    save.element.click();
    await flushPromises();
    expect(api.create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ serverId:"s1",threshold:71, aiAnalysisEnabled:false }), requestScope());
    allowed.delete("anomaly.manage:s1");
    usePermissionStore().invalidate();
    await flushPromises();
    expect(w.find(".rule-form button[type=submit]").exists()).toBe(false);
    save.element.click();
    await flushPromises();
    expect(api.create).toHaveBeenCalledOnce();
    expect(api.check).not.toHaveBeenCalled();
  });

  it("uses the native edit save button only for the exact authorized rule and server", async () => {
    allowed.add("anomaly.manage:s1");
    const w = await render();
    await w.findAll(".rule-item__actions button")[0]!.trigger("click");
    await flushPromises();
    await w.get(".rule-edit__threshold").setValue("77");
    w.get<HTMLButtonElement>(".rule-edit button[type=submit]").element.click();
    await flushPromises();
    expect(api.update).toHaveBeenCalledExactlyOnceWith("rule",expect.objectContaining({serverId:"s1",threshold:77}),requestScope());
    expect(w.find(".rule-edit").exists()).toBe(false);
  });

  it("rejects a retained native rule save event after cancelling the active edit", async () => {
    allowed.add("anomaly.manage:s1");
    const w = await render();
    await w.findAll(".rule-item__actions button")[0]!.trigger("click");
    await flushPromises();
    await w.get(".rule-edit__threshold").setValue("79");
    const form = w.get(".rule-edit");
    await form.get("button[type=button]").trigger("click");
    await flushPromises();
    expect(w.find(".rule-edit").exists()).toBe(false);
    await form.trigger("submit");
    await flushPromises();
    expect(api.update).not.toHaveBeenCalled();
  });

  it("uses each trend range in the selected server read and rejects the retained button after logout", async () => {
    const w = await render();
    await w.get(".trend-controls__server").setValue("s2");
    await flushPromises();
    const controls = w.findAll(".trend-range__btn");
    for (const [index, hours] of [1,6,24].entries()) {
      await controls[index]!.trigger("click");
      await flushPromises();
      expect(api.metrics).toHaveBeenLastCalledWith("s2",hours,requestScope());
    }
    const count = api.metrics.mock.calls.length;
    useSessionStore().clearSession();
    await flushPromises();
    await controls[0]!.trigger("click");
    await flushPromises();
    expect(api.metrics).toHaveBeenCalledTimes(count);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import ProxyOverview from "./ProxyOverview.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
const api = vi.hoisted(() => ({
  list: vi.fn(),
  refresh: vi.fn(),
  decisions: vi.fn(),
  push: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));
vi.mock("../api/reverseProxy", () => ({
  getProxyOverview: api.list,
  refreshProxyRoute: api.refresh,
}));
vi.mock("../api/access", () => ({ access: { decisions: api.decisions } }));
vi.mock("vue-router", () => ({ useRouter: () => ({ push: api.push }) }));
vi.mock("../composables/useToast", () => ({
  useToast: () => ({ success: api.success, error: api.error }),
}));
let wrapper: VueWrapper | undefined;
let allowed: Set<string>;
const row = {
  id: "route",
  serverId: "host",
  serverName: "Visible host",
  domain: "visible.invalid",
  upstreamContainer: "app",
  upstreamPort: 80,
  tlsMode: "auto",
  enabled: true,
  certStatus: "issued",
  certDetail: "expires 2030-01-01",
  config: { aliases: [] },
  createdAt: "",
  updatedAt: "",
};
function render() {
  wrapper = mount(ProxyOverview);
  return wrapper;
}
function vm() {
  return wrapper!.vm as unknown as {
    load: () => Promise<void>;
    refreshRow: (r: unknown) => Promise<void>;
    gotoHost: (r: unknown) => void;
    rows: unknown[];
  };
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  useSessionStore().setUser({ username: "u", userId: "u", multiTenant: true });
  usePermissionStore().setTeam("a");
  allowed = new Set();
  api.list.mockResolvedValue([row]);
  api.refresh.mockResolvedValue({ ...row, certDetail: "refreshed" });
  api.decisions.mockImplementation(
    async (team: string, qs: { action: string }[]) => ({
      userId: "u",
      teamId: team,
      modeEpoch: 1,
      revision: 1,
      catalogVersion: 1,
      allowed: qs.map((q) => allowed.has(q.action)),
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("shows scoped data but hides refresh and host buttons without their separate actions", async () => {
  render();
  await flushPromises();
  expect(wrapper!.text()).toContain("visible.invalid");
  expect(wrapper!.findAll(".rowbtn")).toHaveLength(0);
  await vm().refreshRow({ route: row });
  vm().gotoHost({ route: row });
  expect(api.refresh).not.toHaveBeenCalled();
  expect(api.push).not.toHaveBeenCalled();
  expect(api.list.mock.calls[0]![0].headers["X-Pipewright-Team"]).toBe("a");
});
it("permits only the granted refresh button and uses the captured team", async () => {
  allowed.add("certificate.refresh");
  render();
  await flushPromises();
  expect(wrapper!.findAll(".rowbtn")).toHaveLength(1);
  await wrapper!.get(".rowbtn").trigger("click");
  await flushPromises();
  expect(api.refresh).toHaveBeenCalledWith(
    "route",
    expect.objectContaining({ headers: { "X-Pipewright-Team": "a" } }),
  );
  expect(wrapper!.text()).toContain("refreshed");
  expect(api.push).not.toHaveBeenCalled();
});
it("requires container inspect for the host link independently", async () => {
  allowed.add("container.inspect");
  render();
  await flushPromises();
  await wrapper!.get(".rowbtn").trigger("click");
  expect(api.push).toHaveBeenCalledWith({
    name: "containers",
    query: { server: "host" },
  });
  expect(api.refresh).not.toHaveBeenCalled();
});
it("preserves both old buttons and refresh when tenancy is off", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  render();
  await flushPromises();
  expect(wrapper!.findAll(".rowbtn")).toHaveLength(2);
  await wrapper!.findAll(".rowbtn")[0]!.trigger("click");
  await flushPromises();
  expect(api.refresh).toHaveBeenCalledTimes(1);
  expect(api.decisions).not.toHaveBeenCalled();
});
it("aborts and discards old-team responses", async () => {
  let done!: (value: unknown) => void;
  api.list.mockImplementationOnce(
    () =>
      new Promise((r) => {
        done = r;
      }),
  );
  render();
  await flushPromises();
  const signal = api.list.mock.calls[0]![0].signal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  done([{ ...row, domain: "foreign.invalid" }]);
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect(wrapper!.text()).not.toContain("foreign.invalid");
  expect(wrapper!.text()).toContain("visible.invalid");
});
it("clears data immediately on force-password mutation and blocks retained handlers", async () => {
  allowed.add("certificate.refresh");
  render();
  await flushPromises();
  useSessionStore().user!.mustChangePassword = true;
  await flushPromises();
  expect(vm().rows).toEqual([]);
  await vm().refreshRow({ route: row });
  expect(api.refresh).not.toHaveBeenCalled();
});
it("clears rows after a server-denied list refresh", async () => {
  render();
  await flushPromises();
  api.list.mockRejectedValueOnce(new HttpError(403, null, "Denied"));
  await vm().load();
  expect(vm().rows).toEqual([]);
  expect(wrapper!.text()).not.toContain("visible.invalid");
});
it("ignores a late refresh result after switching teams", async () => {
  allowed.add("certificate.refresh");
  let done!: (value: unknown) => void;
  api.refresh.mockImplementationOnce(
    () =>
      new Promise((r) => {
        done = r;
      }),
  );
  render();
  await flushPromises();
  await wrapper!.get(".rowbtn").trigger("click");
  await flushPromises();
  expect(api.refresh).toHaveBeenCalledOnce();
  usePermissionStore().setTeam("b");
  await flushPromises();
  done({ ...row, domain: "old-result.invalid" });
  await flushPromises();
  expect(wrapper!.text()).not.toContain("old-result.invalid");
  expect(api.success).not.toHaveBeenCalled();
});
it("does not query while the session is unverified", async () => {
  useSessionStore().user = undefined;
  render();
  await flushPromises();
  expect(api.list).not.toHaveBeenCalled();
});

describe("certificate overview host-group controls", () => {
  const sibling = { ...row, id: "sibling", domain: "sibling.invalid" };
  const other = { ...row, id:"other", serverId: "other-host", serverName: "Other host", domain:"other.invalid" };
  function exactGrants(actions: Set<string>) {
    api.decisions.mockImplementation(async (team: string, qs: { action:string;resourceId:string }[]) => ({
      userId: "u", teamId: team, modeEpoch: 1, revision:1, catalogVersion:1,
      allowed: qs.map(q => actions.has(q.action+":"+q.resourceId)),
    }));
  }

  it("switches host/expiry grouping and only dispatches the exact certificate refresh and host location", async () => {
    const grants = new Set(["certificate.refresh:route","container.inspect:other-host"]);
    exactGrants(grants);
    api.list.mockResolvedValue([row,sibling,other]);
    const w = render();
    await flushPromises();
    const [expiry,host] = w.findAll('[role="tab"]');
    await host!.trigger("click");
    await flushPromises();
    expect(host!.attributes("aria-selected")).toBe("true");
    expect(w.findAll(".grouprow")).toHaveLength(2);
    const certRow = w.findAll(".datarow").find(el=>el.text().includes("visible.invalid"))!;
    const siblingRow = w.findAll(".datarow").find(el=>el.text().includes("sibling.invalid"))!;
    const otherRow = w.findAll(".datarow").find(el=>el.text().includes("other.invalid"))!;
    expect(siblingRow.find(".rowbtn").exists()).toBe(false);
    expect(certRow.findAll(".rowbtn")).toHaveLength(1);
    expect(otherRow.findAll(".rowbtn")).toHaveLength(1);
    const refresh = certRow.get(".rowbtn");
    const locate = otherRow.get(".rowbtn");
    await refresh.trigger("click");
    await locate.trigger("click");
    await flushPromises();
    expect(api.refresh).toHaveBeenCalledExactlyOnceWith("route",expect.objectContaining({headers:{"X-Pipewright-Team":"a"},signal:expect.any(AbortSignal)}));
    expect(api.push).toHaveBeenCalledExactlyOnceWith({name:"containers",query:{server:"other-host"}});
    grants.clear();
    usePermissionStore().invalidate();
    await flushPromises();
    await refresh.trigger("click");
    await locate.trigger("click");
    await expiry!.trigger("click");
    await flushPromises();
    expect(w.find(".grouprow").exists()).toBe(false);
    expect(expiry!.attributes("aria-selected")).toBe("true");
    expect(api.refresh).toHaveBeenCalledOnce();
    expect(api.push).toHaveBeenCalledOnce();
  });

  it("uses the displayed overview reload for the current team and stops when its session is unavailable", async () => {
    api.list.mockRejectedValueOnce(new Error("temporary"));
    const w = render();
    await flushPromises();
    const refresh = w.get(".ovw__refresh");
    await refresh.trigger("click");
    await flushPromises();
    expect(api.list).toHaveBeenCalledTimes(2);
    expect(api.list).toHaveBeenLastCalledWith(expect.objectContaining({headers:{"X-Pipewright-Team":"a"},signal:expect.any(AbortSignal)}));
    expect(w.text()).toContain("visible.invalid");
    useSessionStore().clearSession();
    await flushPromises();
    await refresh.trigger("click");
    await flushPromises();
    expect(api.list).toHaveBeenCalledTimes(2);
    expect(w.text()).not.toContain("visible.invalid");
  });

  it("does not use retained host-group controls from a prior team with the same visible resource IDs", async () => {
    exactGrants(new Set(["certificate.refresh:route","container.inspect:host"]));
    api.list.mockResolvedValueOnce([row]).mockResolvedValue([{...row, domain:"current-team.invalid"}]);
    const w = render();
    await flushPromises();
    await w.findAll('[role="tab"]')[1]!.trigger("click");
    await flushPromises();
    const [oldRefresh,oldLocate] = w.findAll(".datarow .rowbtn");
    const request = api.list.mock.calls[0]![0];
    usePermissionStore().setTeam("next");
    await flushPromises();
    expect(w.text()).toContain("current-team.invalid");
    expect(request.signal.aborted).toBe(true);
    await oldRefresh!.trigger("click");
    await oldLocate!.trigger("click");
    await flushPromises();
    expect(api.refresh).not.toHaveBeenCalled();
    expect(api.push).not.toHaveBeenCalled();
    const [refresh,locate] = w.findAll(".datarow .rowbtn");
    api.refresh.mockResolvedValueOnce({...row,domain:"current-team.invalid"});
    await refresh!.trigger("click");
    await locate!.trigger("click");
    await flushPromises();
    expect(api.refresh).toHaveBeenCalledExactlyOnceWith("route",expect.objectContaining({headers:{"X-Pipewright-Team":"next"}}));
    expect(api.push).toHaveBeenCalledExactlyOnceWith({name:"containers",query:{server:"host"}});
  });
});

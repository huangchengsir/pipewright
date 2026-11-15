import { afterEach, beforeEach, it, expect, vi } from "vitest";
import { DOMWrapper, mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import SettingsDnsProviders from "./SettingsDnsProviders.vue";
import Content from "./SettingsDnsProvidersContent.vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import { HttpError } from "../../api/http";
const api = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  remove: vi.fn(),
  verify: vi.fn(),
  decisions: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));
vi.mock("../../api/dnsProviders", () => ({
  listDnsProviders: api.list,
  createDnsProvider: api.create,
  deleteDnsProvider: api.remove,
  verifyDnsProvider: api.verify,
}));
vi.mock("../../api/access", () => ({ access: { decisions: api.decisions } }));
vi.mock("../../composables/useToast", () => ({
  useToast: () => ({ success: api.success, error: api.error }),
}));
let wrapper: VueWrapper | undefined;
const provider = {
  id: "dns",
  type: "cloudflare",
  name: "Current DNS",
  baseDomain: "example.invalid",
  credentialConfigured: true,
  createdAt: "",
};
function render() {
  wrapper = mount(SettingsDnsProviders, { attachTo: document.body });
  return wrapper;
}
function vm() {
  return wrapper!.getComponent(Content).vm as unknown as {
    form: { name: string; baseDomain: string; token: string };
    providers: unknown[];
    openAdd: () => void;
    submit: () => Promise<void>;
    verify: (p: unknown) => Promise<void>;
    openDelete: (p: unknown) => void;
    confirmDelete: () => Promise<void>;
  };
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  useSessionStore().setUser({
    username: "admin",
    userId: "admin",
    multiTenant: true,
    instanceAdmin: true,
  });
  usePermissionStore().setTeam("a");
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin",
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => true),
  }));
  api.list.mockResolvedValue([provider]);
  api.create.mockResolvedValue({ ...provider, id: "new" });
  api.remove.mockResolvedValue({ ok: true });
  api.verify.mockResolvedValue({ ok: true });
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("never mounts or queries DNS configuration when the common permission is denied", async () => {
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin",
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => false),
  }));
  render();
  await flushPromises();
  expect(wrapper!.findComponent(Content).exists()).toBe(false);
  expect(api.list).not.toHaveBeenCalled();
  expect(api.create).not.toHaveBeenCalled();
});
it("preserves creation, verification and confirmed deletion in default-off mode", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  render();
  await flushPromises();
  const page = vm();
  page.openAdd();
  page.form.name = "New DNS";
  page.form.baseDomain = "example.invalid";
  page.form.token = "synthetic-token";
  await page.submit();
  expect(api.create).toHaveBeenCalledWith(
    expect.objectContaining({ name: "New DNS", token: "synthetic-token" }),
    expect.anything(),
  );
  expect(page.form.token).toBe("");
  await page.verify(provider);
  expect(api.verify).toHaveBeenCalledTimes(1);
  page.openDelete(provider);
  await page.confirmDelete();
  expect(api.remove).toHaveBeenCalledTimes(1);
  expect(api.decisions).not.toHaveBeenCalled();
});
it("captures the current team in requests", async () => {
  render();
  await flushPromises();
  expect(api.list.mock.calls[0]![0].headers["X-Pipewright-Team"]).toBe("a");
  await vm().verify(provider);
  expect(api.verify.mock.calls[0]![1].headers["X-Pipewright-Team"]).toBe("a");
});
it("clears typed token on team change and ignores late old-team list", async () => {
  render();
  await flushPromises();
  const page = vm();
  page.openAdd();
  page.form.token = "synthetic-private";
  let done!: (v: unknown) => void;
  api.list.mockImplementationOnce(
    () =>
      new Promise((r) => {
        done = r;
      }),
  );
  usePermissionStore().setTeam("b");
  await flushPromises();
  expect(page.form.token).toBe("");
  const signal = api.list.mock.calls.at(-1)![0].signal;
  usePermissionStore().setTeam("c");
  await flushPromises();
  done([{ ...provider, name: "Foreign DNS" }]);
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect(wrapper!.text()).not.toContain("Foreign DNS");
});
it("rejects all retained write handlers after logout", async () => {
  render();
  await flushPromises();
  const page = vm();
  page.openAdd();
  page.form.token = "synthetic-private";
  page.openDelete(provider);
  useSessionStore().user = null;
  await flushPromises();
  await page.submit();
  await page.verify(provider);
  await page.confirmDelete();
  expect(page.form.token).toBe("");
  expect(api.create).not.toHaveBeenCalled();
  expect(api.verify).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
});
it("does not restore state or emit a toast after in-flight verification loses scope", async () => {
  let done!: (v: unknown) => void;
  api.verify.mockImplementationOnce(
    () =>
      new Promise((r) => {
        done = r;
      }),
  );
  render();
  await flushPromises();
  const pending = vm().verify(provider);
  usePermissionStore().setTeam("b");
  await flushPromises();
  done({ ok: true, message: "Old private zone" });
  await pending;
  expect(api.success).not.toHaveBeenCalled();
  expect(wrapper!.text()).not.toContain("Old private zone");
});
it("clears visible configuration immediately on backend denial", async () => {
  render();
  await flushPromises();
  const page = vm();
  page.openAdd();
  page.form.token = "synthetic-private";
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin",
    teamId: team,
    modeEpoch: 1,
    revision: 2,
    catalogVersion: 1,
    allowed: qs.map(() => false),
  }));
  api.verify.mockRejectedValueOnce(new HttpError(403, null, "Denied"));
  await page.verify(provider);
  await flushPromises();
  expect(page.form.token).toBe("");
  expect(page.providers).toEqual([]);
  expect(wrapper!.findComponent(Content).exists()).toBe(false);
});

const dnsRequestOptions = () => expect.objectContaining({
  headers: { "X-Pipewright-Team": "a" },
  signal: expect.any(AbortSignal),
});

function dialog() {
  const root = document.querySelector<HTMLElement>(".settings-dialog[role=dialog]");
  expect(root).not.toBeNull();
  return new DOMWrapper(root!);
}

async function fillDnsForm() {
  await dialog().get("#dns-name").setValue("New DNS");
  await dialog().get("#dns-domain").setValue("example.invalid");
  await dialog().get("#dns-token").setValue("synthetic-token");
}

it.each(["header", "empty"] as const)("creates a DNS provider from the %s entry and native submit", async (entry) => {
  if (entry === "empty") api.list.mockResolvedValue([]);
  render();
  await flushPromises();
  await wrapper!.get(entry === "header" ? ".section-head button.btn-primary" : ".empty-state button.btn-primary").trigger("click");
  await fillDnsForm();
  (dialog().get("button[type=submit]").element as HTMLButtonElement).click();
  await flushPromises();
  expect(api.create).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ name: "New DNS", token: "synthetic-token" }),
    dnsRequestOptions(),
  );
});

it("verifies only the clicked provider and passes the fixed team through the request", async () => {
  api.list.mockResolvedValue([provider, { ...provider, id: "second", name: "Second provider" }]);
  render();
  await flushPromises();
  const rows = wrapper!.findAll(".dns-row").filter((row) => row.find(".dns-ops").exists());
  await rows[1]!.get(".op-btn:not(.op-btn--danger)").trigger("click");
  await flushPromises();
  expect(api.verify).toHaveBeenCalledExactlyOnceWith("second", dnsRequestOptions());
});

it("deletes only the selected provider after entering and confirming through row controls", async () => {
  api.list.mockResolvedValue([provider, { ...provider, id: "second", name: "Second provider" }]);
  render();
  await flushPromises();
  await wrapper!.findAll(".op-btn--danger")[1]!.trigger("click");
  expect(dialog().get(".delete-name").text()).toBe("Second provider");
  expect(api.remove).not.toHaveBeenCalled();
  await dialog().get(".btn-danger").trigger("click");
  await flushPromises();
  expect(api.remove).toHaveBeenCalledExactlyOnceWith("second", dnsRequestOptions());
  expect(wrapper!.text()).toContain("Current DNS");
  expect(wrapper!.text()).not.toContain("Second provider");
});

it.each([
  ["create", ".modal-close"],
  ["create", ".modal-footer .btn-secondary"],
  ["delete", ".modal-close"],
  ["delete", ".modal-footer .btn-secondary"],
] as const)("closes the DNS %s form with %s without a mutation", async (action, selector) => {
  render();
  await flushPromises();
  if (action === "create") await wrapper!.get(".section-head button.btn-primary").trigger("click");
  else await wrapper!.get(".op-btn--danger").trigger("click");
  await dialog().get(selector).trigger("click");
  expect(document.querySelector(".settings-dialog[role=dialog]")).toBeNull();
  expect(api.create).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
});

it.each(["cloudflare", "dnspod", "alidns"] as const)("updates the %s draft through its own type button without writing", async (type) => {
  render();
  await flushPromises();
  await wrapper!.get(".section-head button.btn-primary").trigger("click");
  const index = ["cloudflare", "dnspod", "alidns"].indexOf(type);
  const selected = dialog().findAll(".seg-item")[index]!;
  await selected.trigger("click");
  expect(selected.classes()).toContain("seg-item--active");
  expect(dialog().findAll(".seg-item--active")).toHaveLength(1);
  expect(api.create).not.toHaveBeenCalled();
});

it("retries a failed DNS list through the displayed retry button", async () => {
  api.list.mockRejectedValueOnce(new Error("synthetic read failure"));
  render();
  await flushPromises();
  await wrapper!.get("button.banner-retry").trigger("click");
  await flushPromises();
  expect(api.list).toHaveBeenCalledTimes(2);
  expect(api.list).toHaveBeenLastCalledWith(dnsRequestOptions());
  expect(wrapper!.text()).toContain("Current DNS");
});

it.each(["create", "verify", "delete"] as const)("blocks the old DNS %s button immediately after losing the instance decision", async (action) => {
  render();
  await flushPromises();
  let button: HTMLButtonElement;
  if (action === "create") {
    await wrapper!.get(".section-head button.btn-primary").trigger("click");
    await fillDnsForm();
    button = dialog().get("button[type=submit]").element as HTMLButtonElement;
  } else if (action === "delete") {
    await wrapper!.get(".op-btn--danger").trigger("click");
    button = dialog().get(".btn-danger").element as HTMLButtonElement;
  } else {
    button = wrapper!.get(".op-btn:not(.op-btn--danger)").element as HTMLButtonElement;
  }
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin", teamId: team, modeEpoch: 1, revision: 2,
    catalogVersion: 1, allowed: qs.map(() => false),
  }));
  usePermissionStore().invalidate();
  button.click();
  await flushPromises();
  expect(api.create).not.toHaveBeenCalled();
  expect(api.verify).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
  expect(wrapper!.findComponent(Content).exists()).toBe(false);
});

it.each([
  ["create", "backdrop"], ["create", "escape"],
  ["delete", "backdrop"], ["delete", "escape"],
] as const)("closes the DNS %s form using %s without a mutation", async (action, path) => {
  render();
  await flushPromises();
  if (action === "create") await wrapper!.get(".section-head button.btn-primary").trigger("click");
  else await wrapper!.get(".op-btn--danger").trigger("click");
  await dialog().trigger(path === "backdrop" ? "click" : "keydown", { key: "Escape" });
  expect(document.querySelector(".settings-dialog[role=dialog]")).toBeNull();
  expect(api.create).not.toHaveBeenCalled();
  expect(api.remove).not.toHaveBeenCalled();
});

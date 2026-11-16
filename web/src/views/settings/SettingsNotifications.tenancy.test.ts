import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { HttpError } from "../../api/http";
import SettingsNotifications from "./SettingsNotifications.vue";
import SettingsNotificationsContent from "./SettingsNotificationsContent.vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
const mocks = vi.hoisted(() => ({
  decisions: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  projects: vi.fn(),
  api: Object.fromEntries(
    [
      "listChannels",
      "createChannel",
      "updateChannel",
      "deleteChannel",
      "testChannel",
      "listRoutes",
      "createRoute",
      "deleteRoute",
      "listTemplates",
      "createTemplate",
      "updateTemplate",
      "deleteTemplate",
      "getNotifyConfig",
      "setNotifyConfig",
    ].map((k) => [k, vi.fn()]),
  ),
}));
vi.mock("../../api/notifications", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/notifications")>()),
  ...mocks.api,
}));
vi.mock("../../api/projects", () => ({ listProjects: mocks.projects }));
vi.mock("../../api/access", () => ({ access: { decisions: mocks.decisions } }));
vi.mock("../../composables/useToast", () => ({
  useToast: () => ({ success: mocks.success, error: mocks.error }),
}));
let wrapper: VueWrapper | undefined;
const channel = {
  id: "ch",
  name: "Current channel",
  type: "webhook",
  enabled: true,
  config: { url: "https://fixture.invalid/hook" },
  createdAt: "",
  updatedAt: "",
};
function render() {
  wrapper = mount(SettingsNotifications, { attachTo: document.body });
  return wrapper;
}
function state() {
  return wrapper!.getComponent(SettingsNotificationsContent).vm as unknown as {
    form: { name: string; url: string; password: string };
    openCreate: () => void;
    handleSave: () => Promise<void>;
    handleDelete: (c: unknown) => Promise<void>;
    handleTest: (c: unknown) => Promise<void>;
    channels: unknown[];
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
  mocks.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: useSessionStore().user?.userId,
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => true),
  }));
  for (const fn of Object.values(mocks.api)) fn.mockResolvedValue([]);
  mocks.api.listChannels!.mockResolvedValue([channel]);
  mocks.api.getNotifyConfig!.mockResolvedValue({ language: "zh-CN" });
  mocks.projects.mockResolvedValue([]);
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
});
it("does not mount configuration or call APIs when the shared action is denied", async () => {
  mocks.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin",
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => false),
  }));
  render();
  await flushPromises();
  expect(wrapper!.findComponent(SettingsNotificationsContent).exists()).toBe(
    false,
  );
  for (const fn of Object.values(mocks.api)) expect(fn).not.toHaveBeenCalled();
  expect(mocks.projects).not.toHaveBeenCalled();
});
it("retains default-off configuration and create behavior", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  render();
  await flushPromises();
  expect(wrapper!.text()).toContain(channel.name);
  const vm = state();
  vm.openCreate();
  vm.form.name = "New channel";
  vm.form.url = "https://fixture.invalid/new";
  await vm.handleSave();
  expect(mocks.api.createChannel).toHaveBeenCalledTimes(1);
  expect(mocks.api.createChannel!.mock.calls[0]![0].name).toBe("New channel");
  expect(mocks.decisions).not.toHaveBeenCalled();
});
it("captures the team in every initial request", async () => {
  render();
  await flushPromises();
  for (const key of [
    "listChannels",
    "listRoutes",
    "listTemplates",
    "getNotifyConfig",
  ]) {
    const args = mocks.api[key]!.mock.calls[0]!;
    expect(args.at(-1).headers["X-Pipewright-Team"]).toBe("a");
  }
  expect(mocks.projects.mock.calls[0]![0].headers["X-Pipewright-Team"]).toBe(
    "a",
  );
});
it("aborts old requests and drops a late old-team response", async () => {
  let finish!: (v: unknown) => void;
  mocks.api.listChannels!.mockImplementationOnce(
    () =>
      new Promise((r) => {
        finish = r;
      }),
  );
  render();
  await flushPromises();
  const signal = mocks.api.listChannels!.mock.calls[0]![0].signal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  finish([{ ...channel, name: "Old private channel" }]);
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect(wrapper!.text()).not.toContain("Old private channel");
  expect(wrapper!.text()).toContain(channel.name);
});
it.each(["logout", "password", "network"])(
  "unmounts secrets and rejects retained handlers after %s",
  async (reason) => {
    render();
    await flushPromises();
    const vm = state();
    vm.openCreate();
    vm.form.password = "synthetic-private";
    vm.form.url = "https://fixture.invalid/private";
    if (reason === "logout") useSessionStore().user = null;
    else if (reason === "password")
      useSessionStore().user!.mustChangePassword = true;
    else useSessionStore().isNetworkError = true;
    await flushPromises();
    expect(wrapper!.findComponent(SettingsNotificationsContent).exists()).toBe(
      false,
    );
    expect(vm.form.password).toBe("");
    expect(vm.form.url).toBe("");
    await vm.handleTest(channel);
    await vm.handleDelete(channel);
    await vm.handleSave();
    expect(mocks.api.testChannel).not.toHaveBeenCalled();
    expect(mocks.api.deleteChannel).not.toHaveBeenCalled();
    expect(mocks.api.createChannel).not.toHaveBeenCalled();
  },
);
it("checks the live scope again after delete confirmation", async () => {
  render();
  await flushPromises();
  const vm = state();
  vi.spyOn(window, "confirm").mockImplementation(() => {
    usePermissionStore().setTeam("b");
    return true;
  });
  await vm.handleDelete(channel);
  expect(mocks.api.deleteChannel).not.toHaveBeenCalled();
});
it("discards a completed write toast when identity changes in flight", async () => {
  let finish!: (v: unknown) => void;
  mocks.api.createChannel!.mockImplementationOnce(
    () =>
      new Promise((r) => {
        finish = r;
      }),
  );
  render();
  await flushPromises();
  const vm = state();
  vm.openCreate();
  vm.form.name = "New";
  vm.form.url = "https://fixture.invalid/new";
  const pending = vm.handleSave();
  await flushPromises();
  useSessionStore().user = null;
  await flushPromises();
  finish(channel);
  await pending;
  expect(mocks.success).not.toHaveBeenCalled();
  expect(mocks.error).not.toHaveBeenCalled();
});

it("clears rendered configuration immediately on a server permission denial", async () => {
  render();
  await flushPromises();
  const vm = state();
  vm.openCreate();
  vm.form.password = "synthetic-private";
  mocks.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "admin",
    teamId: team,
    modeEpoch: 1,
    revision: 2,
    catalogVersion: 1,
    allowed: qs.map(() => false),
  }));
  mocks.api.testChannel!.mockRejectedValueOnce(
    new HttpError(403, { code: "forbidden", message: "Denied" }, "Denied"),
  );
  await vm.handleTest(channel);
  await flushPromises();
  expect(vm.channels).toEqual([]);
  expect(vm.form.password).toBe("");
  expect(wrapper!.findComponent(SettingsNotificationsContent).exists()).toBe(
    false,
  );
  expect(mocks.error).not.toHaveBeenCalled();
});

function routeRow(id: string, projectId?: string, channelId = "ch") {
  return {
    id,
    projectId,
    channelId,
    event: "build_succeeded",
    enabled: true,
    createdAt: "",
  };
}

function setupProjectRoutes() {
  mocks.projects.mockResolvedValue([
    { id: "project-a", name: "Project A" },
    { id: "project-b", name: "Project B" },
  ]);
  mocks.api.listChannels!.mockResolvedValue([
    channel,
    { ...channel, id: "ch-a", name: "Only project A route" },
    { ...channel, id: "ch-b", name: "Only project B route" },
  ]);
}

const requestOptions = () => expect.objectContaining({
  headers: { "X-Pipewright-Team": "a" },
  signal: expect.any(AbortSignal),
});

async function click(selector: string) {
  (wrapper!.get(selector).element as HTMLButtonElement).click();
  await flushPromises();
}

function eventCard(section: string, event = "build_succeeded") {
  const card = wrapper!.findAll(section + " .event-card").find(
    (candidate) => candidate.get(".event-code").text() === event,
  );
  expect(card).toBeDefined();
  return card!;
}

it("retains only the selected project's latest route response and removes only that row", async () => {
  setupProjectRoutes();
  let completeProjectA!: (rows: ReturnType<typeof routeRow>[]) => void;
  mocks.api.listRoutes!.mockImplementation((projectId) => {
    if (projectId === "project-a") return new Promise((resolve) => { completeProjectA = resolve; });
    return Promise.resolve(projectId === "project-b"
      ? [routeRow("route-b", "project-b", "ch-b")]
      : []);
  });
  render();
  await flushPromises();
  await wrapper!.get("#override-project").setValue("project-a");
  await wrapper!.get("#override-project").setValue("project-b");
  await flushPromises();
  completeProjectA([routeRow("route-a", "project-a", "ch-a")]);
  await flushPromises();

  const card = eventCard(".override-section");
  expect(card.get(".route-chip").text()).toContain("Only project B route");
  expect(card.text()).not.toContain("Only project A route");
  await card.get(".route-chip-x").trigger("click");
  await flushPromises();
  expect(mocks.api.deleteRoute).toHaveBeenCalledExactlyOnceWith("route-b", requestOptions());
});

it("blocks the previously rendered project route remove button immediately on project selection change", async () => {
  setupProjectRoutes();
  mocks.api.listRoutes!.mockImplementation(async (projectId) => projectId
    ? [routeRow("route-" + projectId, projectId)]
    : []);
  render();
  await flushPromises();
  const project = wrapper!.get("#override-project");
  await project.setValue("project-a");
  await flushPromises();
  const staleRemove = eventCard(".override-section").get(".route-chip-x").element as HTMLButtonElement;

  (project.element as HTMLSelectElement).value = "project-b";
  project.element.dispatchEvent(new Event("change", { bubbles: true }));
  staleRemove.click();

  await flushPromises();
  expect(mocks.api.deleteRoute).not.toHaveBeenCalled();
  await eventCard(".override-section").get(".route-chip-x").trigger("click");
  await flushPromises();
  expect(mocks.api.deleteRoute).toHaveBeenCalledExactlyOnceWith("route-project-b", requestOptions());
});

it("ignores an older route load when switching away from and back to the same project", async () => {
  setupProjectRoutes();
  let completeFirst!: (rows: ReturnType<typeof routeRow>[]) => void;
  let projectACalls = 0;
  mocks.api.listRoutes!.mockImplementation((projectId) => {
    if (projectId === "project-a" && ++projectACalls === 1) {
      return new Promise((resolve) => { completeFirst = resolve; });
    }
    return Promise.resolve(projectId === "project-a"
      ? [routeRow("latest", "project-a", "ch-a")]
      : []);
  });
  render();
  await flushPromises();
  const project = wrapper!.get("#override-project");
  await project.setValue("project-a");
  await project.setValue("project-b");
  await project.setValue("project-a");
  await flushPromises();
  completeFirst([]);
  await flushPromises();
  expect(eventCard(".override-section").text()).toContain("Only project A route");
});

it.each(["create", "edit"] as const)("uses the native channel %s submit and binds the exact team and row", async (action) => {
  render();
  await flushPromises();
  await click(action === "create"
    ? ".nt-root > .section-head button"
    : ".ch-actions .icon-btn:not(.icon-btn--danger)");
  await wrapper!.get("#nt-name").setValue("Edited channel");
  await wrapper!.get("#nt-url").setValue("https://fixture.invalid/edited");
  await click(".config-form .toggle-track");
  await click(".config-form button[type=submit]");

  const payload = expect.objectContaining({ name: "Edited channel", enabled: false });
  if (action === "create") {
    expect(mocks.api.createChannel).toHaveBeenCalledExactlyOnceWith(payload, requestOptions());
    expect(mocks.api.updateChannel).not.toHaveBeenCalled();
  } else {
    expect(mocks.api.updateChannel).toHaveBeenCalledExactlyOnceWith("ch", payload, requestOptions());
    expect(mocks.api.createChannel).not.toHaveBeenCalled();
  }
});

it.each([".editor-close", ".editor-actions button[type=button]"])("closes the channel editor with %s without sending writes", async (selector) => {
  render();
  await flushPromises();
  await click(".nt-root > .section-head button");
  await click(selector);
  expect(wrapper!.find(".config-form").exists()).toBe(false);
  expect(mocks.api.createChannel).not.toHaveBeenCalled();
  expect(mocks.api.updateChannel).not.toHaveBeenCalled();
});

it("opens the empty-state channel editor independently", async () => {
  mocks.api.listChannels!.mockResolvedValue([]);
  render();
  await flushPromises();
  await click(".nt-root .empty-state button");
  expect(wrapper!.find(".config-form").exists()).toBe(true);
  expect(mocks.api.createChannel).not.toHaveBeenCalled();
});

it("tests and deletes only the displayed channel by actual row controls", async () => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render();
  await flushPromises();
  await click(".ch-actions button:not(.icon-btn)");
  await click(".ch-actions .icon-btn--danger");
  expect(mocks.api.testChannel).toHaveBeenCalledExactlyOnceWith("ch", requestOptions());
  expect(mocks.api.deleteChannel).toHaveBeenCalledExactlyOnceWith("ch", requestOptions());
});

it("drops a stale native channel submit immediately after logout", async () => {
  render();
  await flushPromises();
  await click(".nt-root > .section-head button");
  await wrapper!.get("#nt-name").setValue("Before logout");
  await wrapper!.get("#nt-url").setValue("https://fixture.invalid/edited");
  const save = wrapper!.get(".config-form button[type=submit]").element as HTMLButtonElement;
  useSessionStore().clearSession();
  save.click();
  await flushPromises();
  expect(mocks.api.createChannel).not.toHaveBeenCalled();
});

it.each(["global", "project"] as const)("clicks each %s notification route add, cancel, and remove action", async (scope) => {
  setupProjectRoutes();
  const projectId = scope === "project" ? "project-b" : undefined;
  const select = scope === "project" ? ".override-section" : ".routes-section";
  const id = scope + "-route";
  mocks.api.listRoutes!.mockImplementation(async (project) =>
    project === projectId ? [routeRow(id, projectId)] : []);
  render();
  await flushPromises();
  if (projectId) {
    await wrapper!.get("#override-project").setValue(projectId);
    await flushPromises();
  }
  await eventCard(select).get(".event-head button").trigger("click");
  await eventCard(select).get(".route-add button.app-btn--ghost").trigger("click");
  expect(eventCard(select).find(".route-add").exists()).toBe(false);
  expect(mocks.api.createRoute).not.toHaveBeenCalled();
  await eventCard(select).get(".event-head button").trigger("click");
  await eventCard(select).get(".route-select").setValue("ch-b");
  await eventCard(select).get(".route-add button.app-btn--primary").trigger("click");
  await flushPromises();
  expect(mocks.api.createRoute).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ ...(projectId ? { projectId } : {}), event: "build_succeeded", channelId: "ch-b", enabled: true }),
    requestOptions(),
  );
  expect((mocks.api.createRoute!.mock.calls[0]![0] as {projectId?: string}).projectId).toBe(projectId);
  await eventCard(select).get(".route-chip-x").trigger("click");
  await flushPromises();
  expect(mocks.api.deleteRoute).toHaveBeenCalledExactlyOnceWith(id, requestOptions());
});

it("ignores unexpected project route rows even in the current project's completed response", async () => {
  setupProjectRoutes();
  mocks.api.listRoutes!.mockImplementation(async (projectId) =>
    projectId === "project-b"
      ? [routeRow("route-b", "project-b", "ch-b"), routeRow("route-a", "project-a", "ch-a")]
      : []);
  render();
  await flushPromises();
  await wrapper!.get("#override-project").setValue("project-b");
  await flushPromises();
  expect(eventCard(".override-section").findAll(".route-chip")).toHaveLength(1);
  expect(eventCard(".override-section").get(".route-chip").text()).toContain("Only project B route");
});

it("keeps a pending notification route write fixed to its selected project", async () => {
  setupProjectRoutes();
  let resolveRoute!: (value: ReturnType<typeof routeRow>) => void;
  mocks.api.createRoute!.mockImplementationOnce(() => new Promise((resolve) => { resolveRoute = resolve; }));
  render();
  await flushPromises();
  const project = wrapper!.get("#override-project");
  await project.setValue("project-a");
  await flushPromises();
  await eventCard(".override-section").get(".event-head button").trigger("click");
  await eventCard(".override-section").get(".route-add button.app-btn--primary").trigger("click");
  expect(mocks.api.createRoute).toHaveBeenCalledWith(expect.objectContaining({ projectId: "project-a" }), requestOptions());
  await project.setValue("project-b");
  await flushPromises();
  await eventCard(".override-section").get(".event-head button").trigger("click");
  resolveRoute(routeRow("created", "project-a"));
  await flushPromises();
  expect(mocks.success).not.toHaveBeenCalled();
  expect(eventCard(".override-section").find(".route-add").exists()).toBe(true);
  expect(mocks.api.createRoute).toHaveBeenCalledTimes(1);
});

it("creates, edits, cancels, and deletes templates through the displayed controls", async () => {
  mocks.api.listTemplates!.mockResolvedValue([{
    id: "template-original",
    titleTemplate: "Original",
    bodyTemplate: "Original body",
    event: "build_succeeded",
    createdAt: "",
  }]);
  render();
  await flushPromises();
  await click(".templates-section .section-head button");
  await click(".tpl-form-actions button[type=button]");
  expect(wrapper!.find(".tpl-form").exists()).toBe(false);
  expect(mocks.api.createTemplate).not.toHaveBeenCalled();
  await click(".templates-section .section-head button");
  await wrapper!.get("#tpl-title").setValue("New");
  await wrapper!.get("#tpl-body").setValue("New body");
  await click(".tpl-form button[type=submit]");
  expect(mocks.api.createTemplate).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ titleTemplate: "New", bodyTemplate: "New body" }),
    requestOptions(),
  );
  await click(".tpl-card-actions .tpl-icon-btn:not(.tpl-icon-btn--danger)");
  await wrapper!.get("#tpl-title").setValue("Updated");
  await click(".tpl-form button[type=submit]");
  expect(mocks.api.updateTemplate).toHaveBeenCalledExactlyOnceWith(
    "template-original",
    expect.objectContaining({ titleTemplate: "Updated" }),
    requestOptions(),
  );
  await click(".tpl-icon-btn--danger");
  expect(mocks.api.deleteTemplate).toHaveBeenCalledExactlyOnceWith("template-original", requestOptions());
});

it("sets notification language from the actual picker with fixed team", async () => {
  render();
  await flushPromises();
  await wrapper!.get(".nt-lang-select").setValue("en");
  await flushPromises();
  expect(mocks.api.setNotifyConfig).toHaveBeenCalledExactlyOnceWith("en", requestOptions());
});

it("clicks each visible channel type choice without sending a request", async () => {
  render();
  await flushPromises();
  await click(".nt-root > .section-head button");
  const types = wrapper!.findAll(".type-grid button");
  expect(types.length).toBeGreaterThanOrEqual(4);
  for (const button of types) {
    await button.trigger("click");
    expect(button.attributes("aria-pressed")).toBe("true");
    expect(wrapper!.findAll(".type-grid button[aria-pressed=true]")).toHaveLength(1);
  }
  expect(mocks.api.createChannel).not.toHaveBeenCalled();
  expect(mocks.api.updateChannel).not.toHaveBeenCalled();
});

it.each([
  ["channels", ".nt-root > .banner--error button.banner-retry"],
  ["global-routes", ".routes-section button.banner-retry"],
  ["projects", ".override-section button.banner-retry"],
  ["project-routes", ".override-section button.banner-retry"],
  ["templates", ".templates-section button.banner-retry"],
] as const)("retries the %s read with its original scope and no writes", async (kind, selector) => {
  setupProjectRoutes();
  const load = kind === "channels"
    ? mocks.api.listChannels!
    : kind === "templates"
      ? mocks.api.listTemplates!
      : kind === "projects"
        ? mocks.projects
        : mocks.api.listRoutes!;
  if (kind !== "project-routes") load.mockRejectedValueOnce(new Error("synthetic read failure"));
  render();
  await flushPromises();
  if (kind === "project-routes") {
    load.mockRejectedValueOnce(new Error("synthetic read failure"));
    await wrapper!.get("#override-project").setValue("project-b");
    await flushPromises();
  }
  const before = load.mock.calls.length;
  await click(selector);
  expect(load).toHaveBeenCalledTimes(before + 1);
  const args = load.mock.calls.at(-1)!;
  expect(args.at(-1)).toEqual(requestOptions());
  if (kind === "project-routes") expect(args[0]).toBe("project-b");
  if (kind === "global-routes") expect(args[0]).toBeUndefined();
  for (const method of ["createChannel", "updateChannel", "deleteChannel", "testChannel", "createRoute", "deleteRoute", "createTemplate", "updateTemplate", "deleteTemplate", "setNotifyConfig"]) {
    expect(mocks.api[method]).not.toHaveBeenCalled();
  }
});

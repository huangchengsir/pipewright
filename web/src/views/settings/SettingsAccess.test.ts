import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { useSessionStore } from "../../stores/session";
import { setLocale } from "../../i18n";
import { access, type Role } from "../../api/access";
import SettingsAccess from "./SettingsAccess.vue";
vi.mock("vue-router", () => ({ onBeforeRouteLeave: vi.fn() }));
vi.mock("../../api/access", () => ({
  access: Object.fromEntries(
    [
      "preview",
      "decisions",
      "teams",
      "catalog",
      "roles",
      "members",
      "choices",
      "capabilities",
      "createUser",
      "saveRole",
      "saveMember",
      "lookup",
      "users",
      "updateUser",
      "resetPassword",
      "setUserEnabled",
    ].map((key) => [key, vi.fn()]),
  ),
}));
const role: Role = {
  canManage: true,
  id: "viewer",
  tenantId: "team",
  nameKey: "access.roles.viewer",
  presetKey: "viewer",
  revision: 1,
  pages: ["page.projects"],
  grants: [
    { action: "project.read", scope: { kind: "team" } },
    { action: "run.start", scope: { kind: "team", environmentPolicy: "test" } },
  ],
  requiresSelectedBinding: true,
};
const wrappers: ReturnType<typeof mount>[] = [];
function render(admin = true) {
  const pinia = createPinia();
  setActivePinia(pinia);
  useSessionStore().setUser({
    username: "admin",
    userId: "admin",
    multiTenant: true,
    instanceAdmin: admin,
  });
  const wrapper = mount(SettingsAccess, {
    global: { plugins: [pinia] },
    attachTo: document.body,
  });
  wrappers.push(wrapper);
  return wrapper;
}
function button(wrapper: ReturnType<typeof mount>, text: string) {
  const found = wrapper.findAll("button").find((item) => item.text() === text);
  if (!found) throw new Error(`Missing button: ${text}`);
  return found;
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(access.decisions).mockImplementation(async(team, queries) => ({userId:"admin",teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(()=>true)}));
  setLocale("zh-CN");
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
    this.dispatchEvent(new Event("close"));
  };
  vi.mocked(access.teams).mockResolvedValue([
    { id: "team", name: "研发团队", revision: 1 },
  ]);
  vi.mocked(access.catalog).mockResolvedValue({
    version: 1,
    actions: [
      {
        id: "run.start",
        page: "page.runs",
        resourceKind: "run",
        projectScoped: true,
        environmentRequired: true,
        sensitive: false,
      },
      {
        id: "project.read",
        page: "page.projects",
        resourceKind: "project",
        projectScoped: true,
        environmentRequired: false,
        sensitive: false,
      },
    ],
  });
  vi.mocked(access.roles).mockResolvedValue([role]);
  vi.mocked(access.members).mockResolvedValue([]);
  vi.mocked(access.choices).mockResolvedValue({
    projects: [{ id: "p", name: "项目 A" }],
    environments: [
      { id: "test", name: "测试 A", projectId: "p", purpose: "test" },
      { id: "prod", name: "生产 A", projectId: "p", purpose: "production" },
    ],
  });
  vi.mocked(access.capabilities).mockResolvedValue({
    "role.read": true,
    "member.read": true,
    "choices.read": true,
    "role.manage": true,
    "member.manage": true,
  });
  vi.mocked(access.users).mockResolvedValue({
    users: [
      {
        id: "orphan",
        username: "orphan",
        displayName: "未分配员工",
        enabled: false,
      },
    ],
    nextCursor: "",
  });
});
afterEach(() => {
  for (const wrapper of wrappers.splice(0)) wrapper.unmount();
  setLocale("zh-CN");
});
describe("team access product interactions", () => {
  it("loads a role-only manager without requesting inaccessible member data and permits selected-row editing", async () => {
    vi.mocked(access.capabilities).mockResolvedValue({
      "role.read": true,
      "choices.read": true,
      "role.manage": false,
      "member.read": false,
    });
    const wrapper = render(false);
    await flushPromises();
    expect(access.members).not.toHaveBeenCalled();
    expect(
      wrapper.find(".role-editor input").attributes("disabled"),
    ).toBeUndefined();
    expect(
      wrapper.findAll("button").some((item) => item.text() === "新增"),
    ).toBe(false);
    expect(button(wrapper, "保存").exists()).toBe(true);
  });
  it("uses typed resource references when selecting a server sharing another resource ID", async () => {
    vi.mocked(access.catalog).mockResolvedValue({
      version: 1,
      actions: [
        {
          id: "server.inspect",
          page: "page.servers",
          resourceKind: "server",
          projectScoped: false,
          environmentRequired: false,
          sensitive: false,
        },
      ],
    });
    const serverRole: Role = {
      ...role,
      pages: ["page.servers"],
      grants: [{ action: "server.inspect", scope: { kind: "selected" } }],
    };
    vi.mocked(access.roles).mockResolvedValue([serverRole]);
    vi.mocked(access.choices).mockResolvedValue({
      projects: [],
      environments: [],
      resources: [
        { kind: "server", id: "same", name: "测试服务器" },
        { kind: "credential", id: "same", name: "不可混入凭据" },
      ],
    });
    vi.mocked(access.saveRole).mockResolvedValue({
      ...serverRole,
      revision: 2,
    });
    const wrapper = render();
    await flushPromises();
    await wrapper.findAll(".access-tabs button").find(item => item.text().startsWith("角色"))!.trigger("click");
    await flushPromises();
    const label = wrapper
      .findAll("label")
      .find((item) => item.text() === "测试服务器")!;
    await label.get("input").setValue(true);
    expect(wrapper.text()).not.toContain("不可混入凭据");
    await button(wrapper, "保存").trigger("click");
    await flushPromises();
    const request = vi.mocked(access.saveRole).mock.calls[0]![2];
    expect(request.grants[0]!.scope.resourceRefs).toEqual([
      { kind: "server", id: "same" },
    ]);
  });
  it("offers an explicitly authorized production environment for a custom role binding", async () => {
    vi.mocked(access.roles).mockResolvedValue([
      {
        ...role,
        grants: [
          ...role.grants.filter((grant) => grant.action !== "run.start"),
          {
            action: "run.start",
            scope: {
              kind: "team",
              environmentPolicy: "selected",
              environmentIds: ["prod"],
            },
          },
        ],
      },
    ]);
    const wrapper = render();
    await flushPromises();
    await button(wrapper, "创建员工").trigger("click");
    await flushPromises();
    const dialog = wrapper.get('dialog[aria-labelledby="access-form-title"]');
    await dialog
      .findAll("label")
      .find((item) => item.text() === "项目 A")!
      .get("input")
      .setValue(true);
    expect(dialog.text()).toContain("生产 A");
    expect(dialog.text()).not.toContain("测试 A");
  });
  it("previews current and draft access without saving role or member changes", async () => {
    vi.mocked(access.members).mockResolvedValue([{id:"member",teamId:"team",userId:"u",username:"user",displayName:"Preview member",enabled:true,accountEnabled:true,revision:1,bindings:[]}]);
    vi.mocked(access.preview).mockResolvedValue({currentAllowed:false,draftAllowed:true,revision:8});
    const wrapper=render();await flushPromises();
    await wrapper.findAll(".access-tabs button").find(item=>item.text().startsWith("角色"))!.trigger("click");await flushPromises();
    await wrapper.get(".role-editor input[maxlength='80']").setValue("Draft role");
    const section=wrapper.get('section[aria-label="权限效果预览"]');
    const select = async (index: number, optionName: string) => {
      await section.findAll("[role=combobox]")[index]!.trigger("click");
      const option = [...document.querySelectorAll<HTMLButtonElement>("[role=option]")]
        .find((item) => item.textContent?.trim() === optionName);
      expect(option).toBeDefined();
      option!.click();
      await flushPromises();
    };
    expect(section.get("button.app-btn").attributes("disabled")).toBeDefined();
    await select(0, "Preview member");
    await select(1, "查看");
    await select(2, "项目 A");
    await section.get("button.app-btn").trigger("click");await flushPromises();
    expect(access.preview).toHaveBeenCalledWith("team",expect.objectContaining({memberId:"member",action:"project.read",resourceId:"p",draftRoleId:"viewer",draft:expect.objectContaining({name:"Draft role"})}));
    expect(section.text()).toContain("当前生效 · 不允许");expect(section.text()).toContain("未保存草稿 · 允许");
    expect(access.saveRole).not.toHaveBeenCalled();expect(access.saveMember).not.toHaveBeenCalled();
  });
  it("creates an employee with explicit project and test environment and clears one-time credentials on close", async () => {
    vi.mocked(access.createUser).mockResolvedValue({
      user: { id: "u", username: "alice", displayName: "Alice", enabled: true },
      temporaryPassword: "one-time-test-value",
      expiresAt: 9999999999,
    });
    const wrapper = render();
    await flushPromises();
    await button(wrapper, "创建员工").trigger("click");
    await flushPromises();
    const dialog = wrapper.get('dialog[aria-labelledby="access-form-title"]');
    const inputs = dialog.findAll("input:not([type=checkbox])");
    await inputs[0]!.setValue("alice");
    await inputs[1]!.setValue("Alice");
    const projectLabel = dialog
      .findAll("label")
      .find((item) => item.text() === "项目 A")!;
    await projectLabel.get("input").setValue(true);
    const environmentLabel = dialog
      .findAll("label")
      .find((item) => item.text() === "测试 A")!;
    await environmentLabel.get("input").setValue(true);
    expect(dialog.text()).not.toContain("生产 A");
    (dialog.get("button[type=submit]").element as HTMLButtonElement).click();
    await flushPromises();
    expect(access.createUser).toHaveBeenCalledWith("alice", "Alice", "team", [
      {
        roleId: "viewer",
        scope: {
          kind: "selected",
          projectIds: ["p"],
          environmentIds: ["test"],
        },
      },
    ]);
    const credential = wrapper.get(
      'dialog[aria-labelledby="access-credential-title"]',
    );
    expect(credential.text()).toContain("one-time-test-value");
    await credential
      .findAll("button")
      .find((item) => item.text() === "我已妥善保存")!
      .trigger("click");
    await flushPromises();
    expect(wrapper.text()).not.toContain("one-time-test-value");
  });
  it("provides a global administrator directory for unassigned and disabled accounts", async () => {
    const wrapper = render();
    await flushPromises();
    await button(wrapper, "全部员工").trigger("click");
    await flushPromises();
    expect(access.users).toHaveBeenCalledWith("");
    expect(wrapper.text()).toContain("未分配员工");
    expect(wrapper.text()).toContain("已停用");
  });
  it("does not show global account creation or directory to a team manager", async () => {
    const wrapper = render(false);
    await flushPromises();
    expect(
      wrapper.findAll("button").some((item) => item.text() === "创建员工"),
    ).toBe(false);
    expect(wrapper.text()).not.toContain("全部员工");
    expect(button(wrapper, "添加已有账号").exists()).toBe(true);
  });
  it("reuses AppSelect expanded state for role selection and keyboard close", async () => {
    const wrapper = render();
    await flushPromises();
    await button(wrapper, "创建员工").trigger("click");
    await flushPromises();
    const picker = wrapper
      .get('dialog[aria-labelledby="access-form-title"]')
      .findAll("[role=combobox]")[0]!;
    expect(picker.attributes("aria-expanded")).toBe("false");
    await picker.trigger("click");
    expect(picker.attributes("aria-expanded")).toBe("true");
    expect(wrapper.get('dialog[aria-labelledby="access-form-title"]').element.querySelector('[role=listbox]')).not.toBeNull();
    expect(wrapper.findAllComponents({name:"AppSelect"}).every(select=>select.props("portal"))).toBe(true);
    await picker.trigger("keydown", { key: "Escape" });
    expect(picker.attributes("aria-expanded")).toBe("false");
  });
  it.each(["zh-CN", "zh-TW", "en", "ja", "ko", "es", "fr", "de"] as const)(
    "renders the access controls without raw translation keys in %s",
    async (lang) => {
      setLocale(lang);
      const wrapper = render();
      await flushPromises();
      expect(wrapper.text()).not.toMatch(
        /access\.(title|subtitle|roles|pages|scope)/,
      );
    },
  );
});

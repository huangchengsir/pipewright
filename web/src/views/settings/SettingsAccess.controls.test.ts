import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { usePermissionStore } from "../../stores/permissions";
import { useSessionStore } from "../../stores/session";
import { setLocale } from "../../i18n";
import { access, type Role } from "../../api/access";
import SettingsAccess from "./SettingsAccess.vue";
vi.mock("vue-router", () => ({ onBeforeRouteLeave: vi.fn() }));
vi.mock("../../api/access", () => ({
  access: Object.fromEntries(
    [
      "createTeam", "deleteRole", "preview",
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
const confirmation=vi.hoisted(()=>({open:vi.fn()}));
vi.mock("../../composables/useConfirm",()=>({useConfirm:()=>confirmation}));
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
  vi.resetAllMocks(); confirmation.open.mockResolvedValue(true);
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

const row={id:"member",teamId:"team",userId:"u",username:"alice",displayName:"Alice",enabled:true,accountEnabled:true,canManage:true,revision:1,bindings:[]};
async function ready(){vi.mocked(access.members).mockResolvedValue([row]);const w=render();await flushPromises();return w}
function deferred<T>(){let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>resolve=r);return {promise,resolve}}
describe("missing access control interactions",()=>{
 it("creates a team from its real form",async()=>{vi.mocked(access.createTeam).mockResolvedValue({id:"new",name:"New",revision:1});const w=await ready();await button(w,"创建团队").trigger("click");await flushPromises();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.get("input").setValue("New");(d.get("button[type=submit]").element as HTMLButtonElement).click();await flushPromises();expect(access.createTeam).toHaveBeenCalledWith("New")});
 it("adds an existing account through lookup and member save",async()=>{vi.mocked(access.lookup).mockResolvedValue({id:"u",username:"alice",displayName:"Alice",enabled:true});const w=await ready();await button(w,"添加已有账号").trigger("click");await flushPromises();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.get('input:not([type=checkbox])').setValue("alice");(d.get("button[type=submit]").element as HTMLButtonElement).click();await flushPromises();expect(access.lookup).toHaveBeenCalledWith("team","alice");expect(access.saveMember).toHaveBeenCalledWith("team","",expect.objectContaining({userId:"u"}))});
 it("edits member bindings without looking up a different user",async()=>{const w=await ready();await w.get(".row-actions button").trigger("click");await flushPromises();(w.get('dialog[aria-labelledby="access-form-title"] button[type=submit]').element as HTMLButtonElement).click();await flushPromises();expect(access.lookup).not.toHaveBeenCalled();expect(access.saveMember).toHaveBeenCalledWith("team","member",expect.objectContaining({userId:"u",expectedRevision:1}))});
 it("edits employee profile from row control",async()=>{const w=await ready();await button(w,"编辑个人信息").trigger("click");await flushPromises();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.get("input").setValue("Updated");(d.get("button[type=submit]").element as HTMLButtonElement).click();await flushPromises();expect(access.updateUser).toHaveBeenCalledWith("u","Updated")});
 it("resets a password after confirmation and displays one-time credential",async()=>{vi.mocked(access.resetPassword).mockResolvedValue({user:{id:"u",username:"alice",displayName:"Alice",enabled:true},temporaryPassword:"synthetic-once",expiresAt:9999999999});const w=await ready();await button(w,"重置密码").trigger("click");await flushPromises();expect(access.resetPassword).toHaveBeenCalledWith("u");expect(w.get('dialog[aria-labelledby="access-credential-title"]').text()).toContain("synthetic-once")});
 it("disables the selected employee after confirmation",async()=>{const w=await ready();await button(w,"停用账号").trigger("click");await flushPromises();expect(access.setUserEnabled).toHaveBeenCalledWith("u",false)});
 it("deletes a selected role after confirmation",async()=>{const w=await ready();await w.findAll(".access-tabs button")[1]!.trigger("click");await flushPromises();await button(w,"删除角色").trigger("click");await flushPromises();expect(access.deleteRole).toHaveBeenCalledWith("team",expect.objectContaining({id:"viewer"}))});
 it("blocks a pending password reset after administrator demotion",async()=>{const wait=deferred<boolean>();confirmation.open.mockReturnValue(wait.promise);const w=await ready();await button(w,"重置密码").trigger("click");useSessionStore().setUser({username:"admin",userId:"admin",multiTenant:true,instanceAdmin:false});await flushPromises();wait.resolve(true);await flushPromises();expect(access.resetPassword).not.toHaveBeenCalled()});
 it("blocks a retained team form after administrator demotion",async()=>{const w=await ready();await button(w,"创建团队").trigger("click");await flushPromises();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.get("input").setValue("Stale");useSessionStore().setUser({username:"admin",userId:"admin",multiTenant:true,instanceAdmin:false});await flushPromises();(d.get("button[type=submit]").element as HTMLButtonElement).click();await flushPromises();expect(access.createTeam).not.toHaveBeenCalled()});
});

it("paginates the actual employee directory controls",async()=>{vi.mocked(access.users).mockImplementation(async(cursor)=>({users:[],nextCursor:cursor?'':'next'}));const w=await ready();await button(w,"全部员工").trigger("click");await flushPromises();const footer=w.get('.panel-footer');await footer.findAll('button')[1]!.trigger('click');await flushPromises();expect(access.users).toHaveBeenLastCalledWith('next');await w.get('.panel-footer').findAll('button')[0]!.trigger('click');await flushPromises();expect(access.users).toHaveBeenLastCalledWith('')});
it.each(['重置密码','停用账号','删除角色'])("cancelling %s confirmation has no side effects",async(label)=>{confirmation.open.mockResolvedValue(false);const w=await ready();if(label==='删除角色'){await w.findAll('.access-tabs button')[1]!.trigger('click');await flushPromises()}await button(w,label).trigger('click');await flushPromises();expect(access.resetPassword).not.toHaveBeenCalled();expect(access.setUserEnabled).not.toHaveBeenCalled();expect(access.deleteRole).not.toHaveBeenCalled()});
it("drops a pending employee directory result after identity change",async()=>{const wait=deferred<{users: typeof row[];nextCursor:string}>();vi.mocked(access.users).mockReturnValue(wait.promise);const w=await ready();await button(w,'全部员工').trigger('click');useSessionStore().setUser({username:'other',userId:'other',multiTenant:true,instanceAdmin:false});await flushPromises();wait.resolve({users:[{...row,displayName:'Old directory secret'}],nextCursor:''});await flushPromises();expect(w.text()).not.toContain('Old directory secret')});
it("blocks pending disable after administrator demotion",async()=>{const wait=deferred<boolean>();confirmation.open.mockReturnValue(wait.promise);const w=await ready();await button(w,'停用账号').trigger('click');useSessionStore().setUser({username:'admin',userId:'admin',multiTenant:true,instanceAdmin:false});wait.resolve(true);await flushPromises();expect(access.setUserEnabled).not.toHaveBeenCalled()});

async function rolesTab(w:ReturnType<typeof mount>){await w.findAll('.access-tabs button').find(x=>x.text().startsWith('角色'))!.trigger('click');await flushPromises()}
async function settleControls(){for(let i=0;i<4;i++)await flushPromises()}
it("creates a role using the actual new and save buttons and exact empty role ID",async()=>{vi.mocked(access.saveRole).mockResolvedValue({...role,id:'new',nameKey:'New role'});const w=await ready();await rolesTab(w);await button(w,'新增').trigger('click');await settleControls();await w.get('.role-basics input[maxlength="80"]').setValue('New role');await button(w,'保存').trigger('click');await settleControls();expect(access.saveRole).toHaveBeenCalledWith('team','',expect.objectContaining({name:'New role',grants:[],pages:[]}))});
it("denies a captured new role save after role.manage revocation",async()=>{const w=await ready();await rolesTab(w);await button(w,'新增').trigger('click');await settleControls();await w.get('.role-basics input[maxlength="80"]').setValue('No grant');const save=button(w,'保存');vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'admin',teamId:team,modeEpoch:1,revision:2,catalogVersion:1,allowed:qs.map(()=>false)}));usePermissionStore().invalidate();await settleControls();await save.trigger('click');expect(access.saveRole).not.toHaveBeenCalled()});
it("selects another role and menu page without saving or granting anything",async()=>{vi.mocked(access.roles).mockResolvedValue([role,{...role,id:'second',nameKey:'Second role',pages:['page.runs']}]);const w=await ready();await rolesTab(w);await w.findAll('.role-item')[1]!.trigger('click');await settleControls();expect(w.findAll('.role-item')[1]!.attributes('aria-pressed')).toBe('true');expect(w.get<HTMLInputElement>('.role-basics input[maxlength="80"]').element.value).toBe('Second role');const menus=w.findAll('.menu-item button');for(const menu of menus){await menu.trigger('click');expect(menu.element.parentElement?.classList.contains('selected')).toBe(true)}expect(access.saveRole).not.toHaveBeenCalled()});
it("adds and removes a binding before saving the exact remaining assignments",async()=>{const w=await ready();await w.get('.row-actions button').trigger('click');await settleControls();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.findAll('button').find(x=>x.text()==='添加角色')!.trigger('click');await d.findAll('button').find(x=>x.text()==='添加角色')!.trigger('click');expect(d.findAll('.binding-card')).toHaveLength(2);await d.findAll('.binding-card')[0]!.get('button.app-btn').trigger('click');expect(d.findAll('.binding-card')).toHaveLength(1);(d.get('button[type="submit"]').element as HTMLButtonElement).click();await settleControls();expect(access.saveMember).toHaveBeenCalledWith('team','member',expect.objectContaining({bindings:[{roleId:'viewer',scope:{kind:'selected',projectIds:[],environmentIds:[]}}]}))});
it.each(['team','member','employee','profile'] as const)("closes %s form using its header without writes",async(kind)=>{const w=await ready();const labels={team:'创建团队',member:'添加已有账号',employee:'创建员工',profile:'编辑个人信息'};await button(w,labels[kind]).trigger('click');await settleControls();const d=w.get('dialog[aria-labelledby="access-form-title"]');expect((d.element as HTMLDialogElement).open).toBe(true);await d.get('header button').trigger('click');expect((d.element as HTMLDialogElement).open).toBe(false);expect(access.createTeam).not.toHaveBeenCalled();expect(access.saveMember).not.toHaveBeenCalled();expect(access.createUser).not.toHaveBeenCalled();expect(access.updateUser).not.toHaveBeenCalled()});
it.each(['team','member','employee','profile'] as const)("cancels %s form using footer without writes",async(kind)=>{const w=await ready();const labels={team:'创建团队',member:'添加已有账号',employee:'创建员工',profile:'编辑个人信息'};await button(w,labels[kind]).trigger('click');await settleControls();const d=w.get('dialog[aria-labelledby="access-form-title"]');await d.get('footer button').trigger('click');expect((d.element as HTMLDialogElement).open).toBe(false);expect(access.createTeam).not.toHaveBeenCalled();expect(access.saveMember).not.toHaveBeenCalled();expect(access.createUser).not.toHaveBeenCalled();expect(access.updateUser).not.toHaveBeenCalled()});
it.each(['header','footer'])("copies one-time credential and clears it through %s close",async(where)=>{const writeText=vi.fn().mockResolvedValue(undefined);Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText}});vi.mocked(access.resetPassword).mockResolvedValue({user:{id:'u',username:'alice',displayName:'Alice',enabled:true},temporaryPassword:'synthetic-copy',expiresAt:9999999999});const w=await ready();await button(w,'重置密码').trigger('click');await settleControls();const d=w.get('dialog[aria-labelledby="access-credential-title"]');await d.findAll('button').find(x=>x.text()==='复制')!.trigger('click');await settleControls();expect(writeText).toHaveBeenCalledWith('synthetic-copy');await d.get(where+' button').trigger('click');await settleControls();expect((d.element as HTMLDialogElement).open).toBe(false);expect(w.text()).not.toContain('synthetic-copy')});
it("does not copy a retained credential after administrator demotion",async()=>{const writeText=vi.fn();Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText}});vi.mocked(access.resetPassword).mockResolvedValue({user:{id:'u',username:'alice',displayName:'Alice',enabled:true},temporaryPassword:'synthetic-copy',expiresAt:9999999999});const w=await ready();await button(w,'重置密码').trigger('click');await settleControls();const copy=w.get('.credential-value button');useSessionStore().setUser({username:'admin',userId:'admin',multiTenant:true,instanceAdmin:false});await settleControls();await copy.trigger('click');expect(writeText).not.toHaveBeenCalled();expect(w.text()).not.toContain('synthetic-copy')});
it("enables only the chosen disabled employee",async()=>{vi.mocked(access.members).mockResolvedValue([{...row,accountEnabled:false}]);const w=render();await settleControls();await button(w,'启用账号').trigger('click');await settleControls();expect(access.setUserEnabled).toHaveBeenCalledWith('u',true)});
it("returns to members using the members tab without querying global directory",async()=>{const w=await ready();await rolesTab(w);await w.findAll('.access-tabs button').find(x=>x.text().startsWith('成员'))!.trigger('click');await settleControls();expect(w.get('.access-table-wrap').text()).toContain('Alice');expect(access.users).not.toHaveBeenCalled()});
it("retries failed team reads through refresh without writes",async()=>{vi.mocked(access.capabilities).mockRejectedValueOnce(new Error('temporary')).mockResolvedValue({'role.read':true,'member.read':true,'choices.read':true,'role.manage':true,'member.manage':true});const w=await ready();await button(w,'刷新').trigger('click');await settleControls();expect(access.capabilities).toHaveBeenCalledTimes(2);expect(w.text()).toContain('Alice');expect(access.saveMember).not.toHaveBeenCalled()});


describe("project action scope aliases", () => {
  const scopes = [
    { kind: "selected" as const, resourceIds: ["p"] },
    { kind: "selected" as const, resourceRefs: [{kind:"project",id:"p"}] },
    { kind: "selected" as const, projectIds: ["p"], resourceIds:["p"], resourceRefs:[{kind:"project",id:"p"},{kind:"run",id:"p"}] },
  ];
  async function selected(scope: typeof scopes[number]) {
    const fixture = {...role, grants:[{action:"project.read",scope}]};
    vi.mocked(access.roles).mockResolvedValue([fixture]);
    vi.mocked(access.saveRole).mockResolvedValue(fixture);
    vi.mocked(access.choices).mockResolvedValue({projects:[{id:"p",name:"项目 A"},{id:"b",name:"项目 B"}],environments:[]});
    const w=await ready(); await rolesTab(w); await settleControls(); return w;
  }
  it.each(scopes)("reflects exact project aliases in DOM and preserves untouched save: %j", async(scope)=>{
    const w=await selected(scope);
    const inputs=w.findAll<HTMLInputElement>('.action-row .scope-picker input');
    expect(inputs.map(i=>i.element.checked)).toEqual([true,false]);
    await w.get('.role-basics input[maxlength="80"]').setValue('Renamed');
    await button(w,'保存').trigger('click'); await settleControls();
    expect(access.saveRole).toHaveBeenCalledWith('team','viewer',expect.objectContaining({grants:[{action:'project.read',scope}]}));
  });
  it.each(scopes)("unchecks every equivalent project alias without retaining hidden permission: %j",async(scope)=>{
    const w=await selected(scope);
    const inputs=w.findAll<HTMLInputElement>('.action-row .scope-picker input');
    expect(inputs[0]!.element.checked).toBe(true);
    await inputs[0]!.setValue(false); await inputs[1]!.setValue(true);
    await button(w,'保存').trigger('click'); await settleControls();
    const saved=vi.mocked(access.saveRole).mock.calls[0]![2].grants[0]!.scope;
    expect(saved.projectIds).toEqual(['b']);
    expect(saved.resourceIds??[]).not.toContain('p');
    expect(saved.resourceRefs??[]).not.toContainEqual({kind:'project',id:'p'});
    expect(saved.resourceRefs??[]).toEqual(scope.resourceRefs?.filter(r=>r.kind!=='project')??[]);
  });
  it("does not interpret another action kind's exact resource as an entire project",async()=>{
    const fixture={...role,pages:['page.runs'],grants:[{action:'run.start',scope:{kind:'selected' as const,resourceIds:['p'],resourceRefs:[{kind:'project',id:'p'}],environmentPolicy:'test' as const}}]};
    vi.mocked(access.roles).mockResolvedValue([fixture]); vi.mocked(access.saveRole).mockResolvedValue(fixture);
    const w=await ready(); await rolesTab(w); await settleControls();
    expect(w.get<HTMLInputElement>('.action-row .scope-picker input').element.checked).toBe(false);
    await w.get('.role-basics input[maxlength="80"]').setValue('Renamed'); await button(w,'保存').trigger('click'); await settleControls();
    expect(access.saveRole).toHaveBeenCalledWith('team','viewer',expect.objectContaining({grants:fixture.grants}));
  });
});

describe("team selector confirmation", () => {
  async function selectOtherTeam(accepted: Promise<boolean>) {
    vi.mocked(access.teams).mockResolvedValue([
      { id: "team", name: "研发团队", revision: 1 },
      { id: "other-team", name: "测试团队", revision: 1 },
    ]);
    confirmation.open.mockReturnValue(accepted);
    const wrapper = await ready();
    await rolesTab(wrapper);
    await wrapper.get(".role-basics input[maxlength='80']").setValue("Unsaved role");
    await wrapper.get(".access-toolbar [role=combobox]").trigger("click");
    const option = [...document.querySelectorAll<HTMLButtonElement>("[role=option]")]
      .find((item) => item.textContent?.trim() === "测试团队");
    expect(option).toBeDefined();
    option!.click();
    await settleControls();
    expect(confirmation.open).toHaveBeenCalledTimes(1);
    expect(access.capabilities).toHaveBeenCalledTimes(1);
    return wrapper;
  }

  it("loads only the selected team and discards the old role draft after confirmation", async () => {
    const pending = deferred<boolean>();
    const wrapper = await selectOtherTeam(pending.promise);
    pending.resolve(true);
    await settleControls();

    expect(access.capabilities).toHaveBeenLastCalledWith("other-team");
    expect(access.members).toHaveBeenLastCalledWith("other-team");
    expect(access.roles).toHaveBeenLastCalledWith("other-team");
    expect(access.choices).toHaveBeenLastCalledWith("other-team");
    expect(wrapper.get<HTMLInputElement>(".role-basics input[maxlength='80']").element.value).not.toBe("Unsaved role");
    expect(access.saveRole).not.toHaveBeenCalled();
  });

  it("keeps the current team and role draft after cancelling the selector confirmation", async () => {
    const pending = deferred<boolean>();
    const wrapper = await selectOtherTeam(pending.promise);
    pending.resolve(false);
    await settleControls();

    expect(access.capabilities).toHaveBeenCalledTimes(1);
    expect(access.members).toHaveBeenCalledTimes(1);
    expect(wrapper.get<HTMLInputElement>(".role-basics input[maxlength='80']").element.value).toBe("Unsaved role");
    expect(access.saveRole).not.toHaveBeenCalled();
  });

  it("does not load a team selected by the previous account after confirmation", async () => {
    const pending = deferred<boolean>();
    await selectOtherTeam(pending.promise);
    useSessionStore().setUser({ username: "next", userId: "next", multiTenant: true });
    pending.resolve(true);
    await settleControls();

    expect(access.capabilities).toHaveBeenCalledTimes(1);
    expect(access.members).toHaveBeenCalledTimes(1);
    expect(access.roles).toHaveBeenCalledTimes(1);
    expect(access.choices).toHaveBeenCalledTimes(1);
  });
});

describe("role and member data-scope controls", () => {
  function clickOption(index: number) {
    const option = document.querySelectorAll<HTMLButtonElement>("[role=option]")[index];
    expect(option).toBeDefined();
    option!.click();
  }

  it("sets one menu, action, project and explicit environment via actual controls", async () => {
    vi.mocked(access.saveRole).mockResolvedValue(role);
    const wrapper = await ready();
    await rolesTab(wrapper);
    await button(wrapper, "新增").trigger("click");
    await settleControls();
    await wrapper.get(".role-basics input[maxlength='80']").setValue("Scoped deployer");
    const menus = wrapper.findAll(".menu-item");
    await menus[0]!.get("button").trigger("click");
    expect(wrapper.get(".action-line input").attributes("disabled")).toBeDefined();
    await menus[0]!.get("input").setValue(true);
    await wrapper.get(".action-line input").setValue(true);
    await wrapper.get(".action-line [role=combobox]").trigger("click");
    clickOption(2);
    await settleControls();
    await wrapper.get(".action-row > .scope-picker input").setValue(true);
    await wrapper.get(".environment-scope [role=combobox]").trigger("click");
    clickOption(1);
    await settleControls();
    const production = wrapper.get(".environment-scope").findAll("label")
      .find((node) => node.text() === "生产 A");
    expect(production).toBeDefined();
    await production!.get("input").setValue(true);
    await button(wrapper, "保存").trigger("click");
    await settleControls();

    expect(access.saveRole).toHaveBeenCalledWith("team", "", expect.objectContaining({
      pages: ["page.runs"],
      grants: [{ action: "run.start", scope: {
        kind: "selected", projectIds: ["p"], environmentPolicy: "selected", environmentIds: ["prod"],
      } }],
    }));
  });

  it("removing the menu and disabling the action remove its grant in saved data", async () => {
    vi.mocked(access.saveRole).mockResolvedValue(role);
    const wrapper = await ready();
    await rolesTab(wrapper);
    await wrapper.findAll(".menu-item").find((menu) => menu.get("input").element.checked)!.get("button").trigger("click");
    await wrapper.get(".action-line input").setValue(false);
    const menu = wrapper.findAll(".menu-item input").find((item) => (item.element as HTMLInputElement).checked);
    await menu!.setValue(false);
    expect(wrapper.get(".action-line input").attributes("disabled")).toBeDefined();
    await button(wrapper, "保存").trigger("click");
    await settleControls();

    expect(access.saveRole).toHaveBeenCalledWith("team", "viewer", expect.objectContaining({
      pages: [],
      grants: [expect.objectContaining({ action: "run.start" })],
    }));
  });

  it("submits only the selected member, role binding and typed resource", async () => {
    const serverRole = {
      ...role,
      id: "server-reader",
      nameKey: "Server reader",
      presetKey: "custom",
      grants: [{ action: "server.inspect", scope: { kind: "team" as const } }],
    };
    vi.mocked(access.roles).mockResolvedValue([role, serverRole]);
    vi.mocked(access.catalog).mockResolvedValue({ version: 1, actions: [{
      id: "server.inspect", page: "page.servers", resourceKind: "server",
      projectScoped: false, environmentRequired: false, sensitive: false,
    }] });
    vi.mocked(access.choices).mockResolvedValue({
      projects: [],
      environments: [],
      resources: [
        { kind: "server", id: "shared", name: "Selected server" },
        { kind: "credential", id: "shared", name: "Private credential" },
      ],
    });
    vi.mocked(access.members).mockResolvedValue([{ ...row, id: "other", userId: "other-user", displayName: "Other" }, row]);
    const wrapper = render();
    await settleControls();
    await wrapper.findAll(".row-actions")[1]!.get("button").trigger("click");
    const dialog = wrapper.get('dialog[aria-labelledby="access-form-title"]');
    expect(dialog.get<HTMLInputElement>("input[readonly]").element.value).toBe("alice");
    await dialog.get("input[type=checkbox]").setValue(false);
    await dialog.findAll("button").find((node) => node.text() === "添加角色")!.trigger("click");
    await dialog.get(".binding-card header [role=combobox]").trigger("click");
    clickOption(1);
    await settleControls();
    expect(dialog.text()).toContain("Selected server");
    expect(dialog.text()).not.toContain("Private credential");
    await dialog.findAll("label").find((node) => node.text() === "Selected server")!.get("input").setValue(true);
    (dialog.get("button[type=submit]").element as HTMLButtonElement).click();
    await settleControls();

    expect(access.saveMember).toHaveBeenCalledWith("team", "member", {
      userId: "u", enabled: false, expectedRevision: 1,
      bindings: [{ roleId: "server-reader", scope: {
        kind: "selected", projectIds: [], environmentIds: [], resourceIds: [], resourceRefs: [{ kind: "server", id: "shared" }],
      } }],
    });
  });

  it("blocks the actual editing form save after selected-member permission is revoked", async () => {
    const wrapper = await ready();
    await wrapper.get(".row-actions button").trigger("click");
    const dialog = wrapper.get('dialog[aria-labelledby="access-form-title"]');
    await dialog.get("input[type=checkbox]").setValue(false);
    vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
      userId: "admin", teamId: team, modeEpoch: 1, revision: 2, catalogVersion: 1,
      allowed: queries.map((query) => query.action !== "member.manage"),
    }));
    usePermissionStore().invalidate();
    await settleControls();
    (dialog.get("button[type=submit]").element as HTMLButtonElement).click();
    await settleControls();

    expect(access.saveMember).not.toHaveBeenCalled();
    expect(access.lookup).not.toHaveBeenCalled();
  });
});

it("keeps the current administrator password and enabled state outside employee row actions", async () => {
  vi.mocked(access.members).mockResolvedValue([{ ...row, id: "self", userId: "admin", username: "admin", displayName: "Admin" }]);
  const wrapper = render();
  await settleControls();
  const reset = button(wrapper, "重置密码");
  const disable = button(wrapper, "停用账号");
  expect(reset.attributes("disabled")).toBeDefined();
  expect(disable.attributes("disabled")).toBeDefined();
  (reset.element as HTMLButtonElement).click();
  (disable.element as HTMLButtonElement).click();
  await settleControls();

  expect(confirmation.open).not.toHaveBeenCalled();
  expect(access.resetPassword).not.toHaveBeenCalled();
  expect(access.setUserEnabled).not.toHaveBeenCalled();
});

it("saves one binding scope choice and keeps the role limit in the correct payload", async () => {
  const assignable = { ...role, requiresSelectedBinding: false };
  vi.mocked(access.roles).mockResolvedValue([assignable]);
  vi.mocked(access.saveRole).mockResolvedValue(role);
  const wrapper = await ready();
  await rolesTab(wrapper);
  await wrapper.get(".role-basics input[type=checkbox]").setValue(true);
  await button(wrapper, "保存").trigger("click");
  await settleControls();
  expect(access.saveRole).toHaveBeenCalledWith("team", "viewer", expect.objectContaining({ requiresSelectedBinding: true }));
  await wrapper.findAll(".access-tabs button").find((node) => node.text().startsWith("成员"))!.trigger("click");
  await settleControls();
  await wrapper.get(".row-actions button").trigger("click");
  await settleControls();
  const dialog = wrapper.get('dialog[aria-labelledby="access-form-title"]');
  await dialog.findAll("button").find((node) => node.text() === "添加角色")!.trigger("click");
  await dialog.findAll(".binding-card [role=combobox]")[1]!.trigger("click");
  document.querySelectorAll<HTMLButtonElement>("[role=option]")[0]!.click();
  await settleControls();
  expect(dialog.find(".binding-card > .scope-picker input").exists()).toBe(false);
  (dialog.get("button[type=submit]").element as HTMLButtonElement).click();
  await settleControls();
  expect(access.saveMember).toHaveBeenCalledWith("team", "member", expect.objectContaining({
    bindings: [{ roleId: "viewer", scope: { kind: "none", projectIds: [], environmentIds: [] } }],
  }));
});

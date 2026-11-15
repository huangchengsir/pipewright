import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { useSessionStore } from "../../stores/session";
import { access } from "../../api/access";
import { changePassword, listSessions } from "../../api/account";
import SettingsAccount from "./SettingsAccount.vue";
const confirmation=vi.hoisted(()=>({open:vi.fn()}));
vi.mock("../../composables/useConfirm",()=>({useConfirm:()=>confirmation}));
const navigation = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
vi.mock("vue-router", () => ({ useRouter: () => navigation }));
vi.mock("../../api/access", () => ({
  access: {
    sessions: vi.fn(),
    changePassword: vi.fn(),
    updateSelf: vi.fn(),
    revokeSession: vi.fn(),
  },
}));
vi.mock("../../api/account", () => ({
  listSessions: vi.fn(),
  changePassword: vi.fn(),
  revokeSession: vi.fn(),
}));
const wrappers: ReturnType<typeof mount>[] = [];
function render(mode: "legacy" | "employee" | "restricted") {
  const pinia = createPinia();
  setActivePinia(pinia);
  const store = useSessionStore();
  store.setUser({
    username: "alice",
    displayName: "Alice",
    userId: "alice-id",
    multiTenant: mode !== "legacy",
    instanceAdmin: false,
    mustChangePassword: mode === "restricted",
  });
  const wrapper = mount(SettingsAccount, {
    attachTo: document.body,
    global: { plugins: [pinia], stubs: { SettingsPlatformLogin: true } },
  });
  wrappers.push(wrapper);
  return { wrapper, store };
}
beforeEach(() => {
  vi.resetAllMocks(); confirmation.open.mockResolvedValue(true);
  vi.mocked(access.sessions).mockResolvedValue([]);
  vi.mocked(listSessions).mockResolvedValue([]);
  vi.mocked(access.changePassword).mockResolvedValue(undefined);
  vi.mocked(changePassword).mockResolvedValue(undefined);
});
afterEach(() => {
  for (const wrapper of wrappers.splice(0)) wrapper.unmount();
});

function deferred<T>(){let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>resolve=r);return {promise,resolve}}
const other={id:"other",current:false,createdAt:"2026-01-01",lastSeenAt:"2026-01-01",expiresAt:"2027-01-01"};
describe("account session controls",()=>{
 it("revokes the selected own session after confirmation",async()=>{vi.mocked(access.sessions).mockResolvedValue([other]);const {wrapper}=render("employee");await flushPromises();await wrapper.get('[aria-labelledby="acct-sessions-h"] button').trigger("click");await flushPromises();expect(access.revokeSession).toHaveBeenCalledWith("other");expect(access.sessions).toHaveBeenCalledTimes(2)});
 it("retries a failed session list using the self API",async()=>{vi.mocked(access.sessions).mockRejectedValueOnce(new Error("temporary")).mockResolvedValue([]);const {wrapper}=render("employee");await flushPromises();await wrapper.get('[aria-labelledby="acct-sessions-h"] button').trigger("click");await flushPromises();expect(access.sessions).toHaveBeenCalledTimes(2);expect(wrapper.text()).not.toContain("temporary")});
 it("does not revoke an old employee session after identity switches during confirmation",async()=>{vi.mocked(access.sessions).mockResolvedValue([other]);const wait=deferred<boolean>();confirmation.open.mockReturnValue(wait.promise);const {wrapper,store}=render("employee");await flushPromises();await wrapper.get('[aria-labelledby="acct-sessions-h"] button').trigger("click");store.setUser({username:"bob",userId:"bob",multiTenant:true});await flushPromises();wait.resolve(true);await flushPromises();expect(access.revokeSession).not.toHaveBeenCalled()});
 it("discards a previous employee session response after identity switch",async()=>{const wait=deferred<typeof other[]>();vi.mocked(access.sessions).mockReturnValueOnce(wait.promise).mockResolvedValue([]);const {wrapper,store}=render("employee");await flushPromises();store.setUser({username:"bob",userId:"bob",multiTenant:true});await flushPromises();wait.resolve([other]);await flushPromises();expect(wrapper.text()).not.toContain("other")});
});

it("submits profile through the actual save button with only the current display name",async()=>{const {wrapper,store}=render('employee');vi.spyOn(store,'ensureSession').mockResolvedValue({kind:'ok',user:store.user!});await flushPromises();await wrapper.get('#access-display-name').setValue('Updated self');(wrapper.get('#access-display-name').element.closest('form')!.querySelector('button[type=submit]') as HTMLButtonElement).click();await flushPromises();expect(access.updateSelf).toHaveBeenCalledWith('Updated self')});
it.each(['legacy','employee'] as const)("submits password by its button with %s API",async(mode)=>{const {wrapper,store}=render(mode);vi.spyOn(store,'ensureSession').mockResolvedValue({kind:'ok',user:store.user!});await flushPromises();await wrapper.get('#acct-current').setValue('old-password');await wrapper.get('#acct-new').setValue('new-password');await wrapper.get('#acct-confirm').setValue('new-password');(wrapper.get('[aria-labelledby="acct-pw-h"] button[type=submit]').element as HTMLButtonElement).click();await flushPromises();expect(mode==='legacy'?changePassword:access.changePassword).toHaveBeenCalledWith({currentPassword:'old-password',newPassword:'new-password'});expect(mode==='legacy'?access.changePassword:changePassword).not.toHaveBeenCalled()});
it("cancels session revocation without requests",async()=>{confirmation.open.mockResolvedValue(false);vi.mocked(access.sessions).mockResolvedValue([other]);const {wrapper}=render('employee');await flushPromises();await wrapper.get('[aria-labelledby="acct-sessions-h"] button').trigger('click');await flushPromises();expect(access.revokeSession).not.toHaveBeenCalled()});
it("revokes the current session and clears local identity",async()=>{vi.mocked(access.sessions).mockResolvedValue([{...other,current:true}]);const {wrapper,store}=render('employee');await flushPromises();await wrapper.get('[aria-labelledby="acct-sessions-h"] button').trigger('click');await flushPromises();expect(access.revokeSession).toHaveBeenCalledWith('other');expect(store.user).toBeNull();expect(navigation.push).toHaveBeenCalledWith('/login')});

it("does not send a retained employee profile form after switching to legacy mode", async () => {
  const { wrapper, store } = render("employee");
  await flushPromises();
  const displayName = wrapper.get("#access-display-name");
  await displayName.setValue("Old employee draft");
  const save = displayName.element.closest("form")!.querySelector<HTMLButtonElement>("button[type=submit]")!;
  store.setUser({ username: "alice", userId: "alice-id", displayName: "Alice", multiTenant: false });
  save.click();
  await flushPromises();

  expect(wrapper.find("#access-display-name").exists()).toBe(false);
  expect(access.updateSelf).not.toHaveBeenCalled();
});

it("does not send a retained password or retry action after logout", async () => {
  vi.mocked(access.sessions).mockRejectedValueOnce(new Error("temporary"));
  const { wrapper, store } = render("employee");
  await flushPromises();
  await wrapper.get("#acct-current").setValue("old-password");
  await wrapper.get("#acct-new").setValue("new-password");
  await wrapper.get("#acct-confirm").setValue("new-password");
  const password = wrapper.get<HTMLButtonElement>('[aria-labelledby="acct-pw-h"] button[type=submit]').element;
  const retry = wrapper.get<HTMLButtonElement>('[aria-labelledby="acct-sessions-h"] button').element;
  store.clearSession();
  password.click();
  retry.click();
  await flushPromises();

  expect(access.changePassword).not.toHaveBeenCalled();
  expect(changePassword).not.toHaveBeenCalled();
  expect(access.sessions).toHaveBeenCalledTimes(1);
  expect(listSessions).not.toHaveBeenCalled();
});

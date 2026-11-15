import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { useSessionStore } from "../../stores/session";
import { access } from "../../api/access";
import { changePassword, listSessions } from "../../api/account";
import { HttpError } from "../../api/http";
import SettingsAccount from "./SettingsAccount.vue";
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
    global: { plugins: [pinia], stubs: { SettingsPlatformLogin: true } },
  });
  wrappers.push(wrapper);
  return { wrapper, store };
}
async function enterPassword(wrapper: ReturnType<typeof mount>) {
  await wrapper.get("#acct-current").setValue("current-password");
  await wrapper.get("#acct-new").setValue("next-password");
  await wrapper.get("#acct-confirm").setValue("next-password");
  await wrapper.get('[aria-labelledby="acct-pw-h"] form').trigger("submit");
  await flushPromises();
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(access.sessions).mockResolvedValue([]);
  vi.mocked(listSessions).mockResolvedValue([]);
  vi.mocked(access.changePassword).mockResolvedValue(undefined);
  vi.mocked(changePassword).mockResolvedValue(undefined);
});
afterEach(() => {
  for (const wrapper of wrappers.splice(0)) wrapper.unmount();
});
describe("account settings mode boundaries", () => {
  it("preserves legacy account APIs and platform login section when mode is off", async () => {
    const { wrapper } = render("legacy");
    await flushPromises();
    expect(listSessions).toHaveBeenCalledOnce();
    expect(access.sessions).not.toHaveBeenCalled();
    expect(wrapper.find("settings-platform-login-stub").exists()).toBe(true);
    await enterPassword(wrapper);
    expect(changePassword).toHaveBeenCalledWith({
      currentPassword: "current-password",
      newPassword: "next-password",
    });
    expect(access.changePassword).not.toHaveBeenCalled();
  });
  it("uses only current-user APIs for employee profile and sessions", async () => {
    const { wrapper, store } = render("employee");
    const refresh = vi
      .spyOn(store, "ensureSession")
      .mockResolvedValue({
        kind: "ok",
        user: { username: "alice", multiTenant: true },
      });
    await flushPromises();
    expect(access.sessions).toHaveBeenCalledOnce();
    expect(listSessions).not.toHaveBeenCalled();
    await wrapper.get("#access-display-name").setValue("New name");
    await wrapper
      .get("#access-display-name")
      .element.closest("form")!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await flushPromises();
    expect(access.updateSelf).toHaveBeenCalledWith("New name");
    expect(refresh).toHaveBeenCalledWith(true);
  });
  it("restricts temporary sessions to password change and reloads the authoritative session after rotation", async () => {
    const { wrapper, store } = render("restricted");
    await flushPromises();
    expect(access.sessions).not.toHaveBeenCalled();
    expect(listSessions).not.toHaveBeenCalled();
    expect(wrapper.find("#access-display-name").exists()).toBe(false);
    expect(wrapper.find('[aria-labelledby="acct-sessions-h"]').exists()).toBe(
      false,
    );
    const refresh = vi
      .spyOn(store, "ensureSession")
      .mockImplementation(async () => {
        const user = {
          username: "alice",
          userId: "alice-id",
          multiTenant: true,
          mustChangePassword: false,
        };
        store.setUser(user);
        return { kind: "ok", user };
      });
    await enterPassword(wrapper);
    expect(access.changePassword).toHaveBeenCalledWith({
      currentPassword: "current-password",
      newPassword: "next-password",
    });
    expect(refresh).toHaveBeenCalledWith(true);
    expect(access.sessions).toHaveBeenCalledOnce();
    expect(wrapper.get<HTMLInputElement>("#acct-current").element.value).toBe(
      "",
    );
  });
  it("clears expired temporary credentials and returns to sign-in on 401", async () => {
    vi.mocked(access.changePassword).mockRejectedValue(
      new HttpError(401, null, "expired"),
    );
    const { wrapper, store } = render("restricted");
    await flushPromises();
    await enterPassword(wrapper);
    expect(store.user).toBeNull();
    expect(navigation.replace).toHaveBeenCalledWith("/login");
    expect(wrapper.get<HTMLInputElement>("#acct-current").element.value).toBe(
      "",
    );
    expect(wrapper.get<HTMLInputElement>("#acct-new").element.value).toBe("");
  });
});

it("hides instance setup from employees while preserving self-service account controls",async()=>{
 const {wrapper}=render('employee');await flushPromises();expect(wrapper.find('[aria-labelledby="acct-onboarding-h"]').exists()).toBe(false);expect(wrapper.find('[aria-labelledby="acct-pw-h"]').exists()).toBe(true);expect(wrapper.find('settings-platform-login-stub').exists()).toBe(true)
})
it("preserves administrator re-onboarding and rejects a captured click after demotion",async()=>{
 const {wrapper,store}=render('legacy');await flushPromises();const button=wrapper.get('[aria-labelledby="acct-onboarding-h"] button');await button.trigger('click');expect(navigation.push).toHaveBeenCalledWith('/onboarding');navigation.push.mockClear();store.setUser({username:'employee',userId:'employee',multiTenant:true,instanceAdmin:false});await flushPromises();await button.trigger('click');expect(navigation.push).not.toHaveBeenCalled();expect(wrapper.find('[aria-labelledby="acct-onboarding-h"]').exists()).toBe(false)
})

it("preserves setup for the enabled-mode instance administrator",async()=>{const {wrapper,store}=render('employee');store.setUser({username:'admin',userId:'admin',multiTenant:true,instanceAdmin:true});await flushPromises();await wrapper.get('[aria-labelledby="acct-onboarding-h"] button').trigger('click');expect(navigation.push).toHaveBeenCalledWith('/onboarding')})

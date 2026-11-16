import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import ProjectPreviewConfig from "./ProjectPreviewConfig.vue";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import type { PreviewConfig } from "../../api/previewEnvs";
const api = vi.hoisted(() => ({
  get: vi.fn(),
  set: vi.fn(),
  dns: vi.fn(),
  decisions: vi.fn(),
}));
vi.mock("../../api/previewEnvs", () => ({
  getPreviewConfig: api.get,
  setPreviewConfig: api.set,
  listPreviewProviders:api.dns,
}));
vi.mock("../../api/dnsProviders", () => ({ listDnsProviders: api.dns }));
vi.mock("../../api/access", () => ({ access: { decisions: api.decisions } }));
vi.mock("../../composables/useToast", () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}));
let wrapper: VueWrapper | undefined;
const config = (domain: string): PreviewConfig => ({
  projectId: "p",
  enabled: false,
  dnsProviderId: "",
  baseDomain: domain,
});
function render(disabled = false) {
  wrapper = mount(ProjectPreviewConfig, {
    props: { projectId: "p", disabled },
    global: { stubs: { AppSelect: true } },
  });
  return wrapper;
}
const exposed = () =>
  wrapper!.vm as unknown as { save: () => Promise<boolean>; ready: boolean };
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  useSessionStore().setUser({
    username: "alice",
    userId: "alice",
    multiTenant: true,
  });
  usePermissionStore().setTeam("a");
  api.get.mockResolvedValue(config("scope.invalid"));
  api.dns.mockResolvedValue([]);
  api.set.mockResolvedValue(config("scope.invalid"));
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "alice",
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => true),
  }));
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("lets an explicitly authorized employee save and uses project-scoped minimal DNS choices", async () => {
  render(); await flushPromises();await flushPromises();
  expect(api.get.mock.calls[0]![1].headers["X-Pipewright-Team"]).toBe("a");
  expect(api.dns).toHaveBeenCalledOnce();
  expect(api.dns.mock.calls[0]![1].headers["X-Pipewright-Team"]).toBe("a");
  expect(wrapper!.find(".pvc__save").exists()).toBe(true);
  expect(await exposed().save()).toBe(true);
  expect(api.set).toHaveBeenCalledOnce();
});
it("still requires the shared edit decision for an instance administrator", async () => {
  useSessionStore().user!.instanceAdmin = true;
  api.decisions.mockImplementation(async (team: string, qs: unknown[]) => ({
    userId: "alice",
    teamId: team,
    modeEpoch: 1,
    revision: 1,
    catalogVersion: 1,
    allowed: qs.map(() => false),
  }));
  render();
  await flushPromises();
  expect(wrapper!.find(".pvc__save").exists()).toBe(false);
  expect(await exposed().save()).toBe(false);
  expect(api.set).not.toHaveBeenCalled();
});
it("discards an old-team config response and aborts its request", async () => {
  let complete!: (v: PreviewConfig) => void;
  api.get.mockImplementationOnce(() => new Promise((r) => (complete = r)));
  render();
  await flushPromises();
  const signal = api.get.mock.calls[0]![1].signal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  complete(config("old-secret.invalid"));
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect((wrapper!.get(".pvc__in").element as HTMLInputElement).value).toBe(
    "scope.invalid",
  );
});
it("rejects direct save after forced-password identity mutation", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  render();
  await flushPromises();
  useSessionStore().user!.mustChangePassword = true;
  await flushPromises();
  expect(await exposed().save()).toBe(false);
  expect(api.set).not.toHaveBeenCalled();
  expect((wrapper!.get(".pvc__in").element as HTMLInputElement).value).toBe("");
});
it("preserves coordinator programmatic saves while the original UI is busy", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  render(true);
  await flushPromises();
  expect(wrapper!.get(".pvc__save").attributes("disabled")).toBeDefined();
  expect(await exposed().save()).toBe(true);
  expect(api.set).toHaveBeenCalledTimes(1);
});
it("does not query before session verification", async () => {
  useSessionStore().user = undefined;
  render();
  await flushPromises();
  expect(api.get).not.toHaveBeenCalled();
  expect(api.dns).not.toHaveBeenCalled();
});

async function settle(){for(let i=0;i<5;i++)await flushPromises()}
function decisionsWithout(...denied:string[]){api.decisions.mockImplementation(async(team:string,qs:{action:string}[])=>({userId:"alice",teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>!denied.includes(q.action))}))}
it("permits disabling existing previews without provisioning or DNS use grants",async()=>{
 decisionsWithout("preview.provision","dns.use");api.get.mockResolvedValue({...config("preview.invalid"),enabled:true,dnsProviderId:"dns",authorizationState:"required"});render();await settle();
 expect(wrapper!.text()).toContain("重新");expect(await exposed().save()).toBe(false);
 await wrapper!.get(".pvc__cb").setValue(false);await settle();expect(await exposed().save()).toBe(true);expect(api.set.mock.calls[0][1].enabled).toBe(false);
});
it("blocks enabling when provisioning is missing",async()=>{decisionsWithout("preview.provision");render();await settle();expect(wrapper!.get(".pvc__cb").attributes("disabled")).toBeDefined();expect(await exposed().save()).toBe(true);expect(api.set.mock.calls[0][1].enabled).toBe(false)});
it("refuses enabled configuration without the selected DNS use permission",async()=>{decisionsWithout("dns.use");api.get.mockResolvedValue({...config("preview.invalid"),enabled:true,dnsProviderId:"dns"});render();await settle();expect(await exposed().save()).toBe(false);expect(api.set).not.toHaveBeenCalled()});
it("does not read configuration or DNS metadata without pipeline.read",async()=>{decisionsWithout("pipeline.read");render();await settle();expect(api.get).not.toHaveBeenCalled();expect(api.dns).not.toHaveBeenCalled();expect(await exposed().save()).toBe(false)});
it("clears configuration and stops saves when read is revoked",async()=>{render();await settle();decisionsWithout("pipeline.read");usePermissionStore().invalidate();await settle();expect((wrapper!.get(".pvc__in").element as HTMLInputElement).value).toBe("");expect(await exposed().save()).toBe(false);expect(api.set).not.toHaveBeenCalled()});

it("clicks the preview settings Save for the current project and blocks it after edit revocation", async ()=>{
  render();
  await settle();
  await wrapper!.get(".pvc__in").setValue("changed.invalid");
  const button=wrapper!.get("button.pvc__save");
  await button.trigger("click");
  await settle();
  expect(api.set).toHaveBeenCalledTimes(1);
  const fixed=api.set.mock.calls[0]!;
  expect(fixed[0]).toBe("p");
  expect(fixed.at(-1)?.headers).toMatchObject({"X-Pipewright-Team":"a"});
  usePermissionStore().reject("a",{action:"pipeline.edit",resourceId:"p"});
  button.element.dispatchEvent(new MouseEvent("click",{bubbles:true}));
  await settle();
  expect(api.set).toHaveBeenCalledTimes(1);
});

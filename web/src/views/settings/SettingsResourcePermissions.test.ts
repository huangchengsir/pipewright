import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import { HttpError } from "../../api/http";
import type { PermissionQuery } from "../../api/access";
import SettingsVault from "./SettingsVault.vue";
import VaultContent from "./SettingsVaultContent.vue";
import SettingsKubernetes from "./SettingsKubernetes.vue";
import KubernetesContent from "./SettingsKubernetesContent.vue";
import KubeCredentials from "../../components/kubernetes/KubeCredentials.vue";
import CredentialContent from "../../components/kubernetes/KubeCredentialsContent.vue";
import KubeReleaseSettings from "../../components/kubernetes/KubeReleaseSettings.vue";
import ReleaseContent from "../../components/kubernetes/KubeReleaseSettingsContent.vue";
const m = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  reveal: vi.fn(),
  reconnect: vi.fn(),
  oauth: vi.fn(),
  authorize: vi.fn(),
  decisions: vi.fn(),
  get: vi.fn(),
  confirm: vi.fn(),
  credentials: vi.fn(),
  preview: vi.fn(),
  saveCredential: vi.fn(),
  enableCredential: vi.fn(),
  deleteCredential: vi.fn(),
  connections: vi.fn(),
  saveConnection: vi.fn(),
  deleteConnection: vi.fn(),
  grants: vi.fn(),
  saveGrant: vi.fn(),
  deleteGrant: vi.fn(),
  probe: vi.fn(),
  releaseGrants: vi.fn(),
  releaseProfile: vi.fn(),
  saveReleaseProfile: vi.fn(),
  saveReleaseGrant: vi.fn(),
  deleteReleaseGrant: vi.fn(),
  reauth: vi.fn(),
}));
vi.mock("../../api/access", () => ({ access: { decisions: m.decisions } }));
vi.mock("../../api/credentials", () => ({
  listCredentials: m.list,
  createCredential: m.create,
  updateCredential: m.update,
  deleteCredential: m.remove,
  revealCredential: m.reveal,
  reconnectOAuthCredential: m.reconnect,
}));
vi.mock("../../api/oauth", () => ({
  getOAuthApps: m.oauth,
  authorizeUrl: m.authorize,
}));
vi.mock("../../api/kubernetes", async () => ({
  ...(await vi.importActual<typeof import("../../api/kubernetes")>(
    "../../api/kubernetes",
  )),
  kubeAPI: m,
}));
vi.mock("../../api/http", async () => ({
  ...(await vi.importActual<typeof import("../../api/http")>("../../api/http")),
  http: { get: m.get },
}));
vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
  onBeforeRouteLeave: vi.fn(),
}));
vi.mock("../../composables/useConfirm", () => ({
  useConfirm: () => ({ open: m.confirm }),
}));
vi.mock("../../components/AuditTimeline.vue", () => ({
  default: { render: () => null },
}));
vi.mock("../../composables/useToast", () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}));
const cred = {
  id: "a",
  name: "Allowed credential",
  type: "git_token",
  scope: "",
  username: "",
  createdAt: "2026-11-13T00:00:00Z",
  lastUsedAt: null,
  maskedValue: "***",
};
const kc = {
  id: "ka",
  name: "Allowed kube secret",
  revision: 1,
  mode: "server_token",
  context: "test",
  server: "https://cluster.invalid",
  enabled: true,
};
const connection = {
  id: "ca",
  name: "Allowed cluster",
  revision: 1,
  credentialId: "ka",
  credentialRevision: 1,
  context: "test",
  server: "https://cluster.invalid",
  enabled: true,
  profile: {
    protocol: "kubectl-sh-v1",
    image: "test",
    architecture: "amd64",
    uid: 1000,
    network: "bridge",
    kubectlPath: "/kubectl",
  },
  executors: [{ kind: "ssh", serverId: "sa", hostKeySHA256: "pin" }],
};
const grant = {
  id: "ga",
  connectionId: "ca",
  revision: 1,
  projectId: "pa",
  environment: "dev",
  environmentId: "env-dev",
  namespace: "app",
  deployment: "web",
  containers: ["web"],
  executor: connection.executors[0],
  enabled: true,
  requireApproval: true,
};
const releaseGrant = {
  id: "ra",
  connectionId: "ca",
  revision: 1,
  projectId: "pa",
  environment: "dev",
  executor: connection.executors[0],
  enabled: true,
  requireApproval: true,
  allowPrune: false,
  resources: [
    {
      resource: {
        group: "",
        version: "v1",
        resource: "configmaps",
        kind: "ConfigMap",
        namespace: "app",
        name: "cfg",
      },
      actions: ["get"],
      fields: [],
      containers: [],
    },
  ],
};
let w: VueWrapper | undefined;
let allow: (q: PermissionQuery) => boolean = () => true;
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
async function settle() {
  for (let i = 0; i < 4; i++) await flushPromises();
}
async function render(component: any, props: any = {}) {
  w = mount(component, {
    props,
    attachTo: document.body,
    global: {
      stubs: {
        Teleport: true,
        RouterLink: true,
        ...(component === SettingsVault ? { KubeCredentials: true } : {}),
      },
    },
  });
  await settle();
  return w;
}
function state(component: any): any {
  return (w!.getComponent(component).vm as any).$.setupState;
}
async function verify(s: any) {
  s.gate.password.value = "fixture";
  await s.gate.verify();
  await settle();
}
async function revoke(test: (q: PermissionQuery) => boolean = () => false) {
  allow = test;
  usePermissionStore().invalidate();
  await settle();
}
const options = () =>
  expect.objectContaining({
    headers: { "X-Pipewright-Team": "team-a" },
    signal: expect.any(AbortSignal),
    throwOnUnauthorized: true,
  });
beforeEach(() => {
  setActivePinia(createPinia());
  useSessionStore().setUser({
    username: "employee",
    userId: "u1",
    multiTenant: true,
    instanceAdmin: false,
  });
  usePermissionStore().setTeam("team-a");
  allow = () => true;
  for (const f of Object.values(m)) f.mockReset();
  m.decisions.mockImplementation(
    async (team: string, qs: PermissionQuery[]) => ({
      userId: useSessionStore().user?.userId,
      teamId: team,
      modeEpoch: 1,
      revision: 1,
      catalogVersion: 1,
      allowed: qs.map((q) => (!!q.resourceId || q.create ? allow(q) : false)),
    }),
  );
  m.list.mockResolvedValue([
    cred,
    { ...cred, id: "b", name: "Forbidden credential" },
  ]);
  m.oauth.mockResolvedValue([]);
  m.authorize.mockReturnValue("#fixture-oauth-no-navigation");
  m.create.mockResolvedValue(cred);
  m.update.mockResolvedValue(cred);
  m.reveal.mockResolvedValue("REVEAL_CANARY");
  m.reconnect.mockRejectedValue(new Error("fixture"));
  m.credentials.mockResolvedValue([kc]);
  m.connections.mockResolvedValue([connection]);
  m.grants.mockResolvedValue([grant]);
  m.saveConnection.mockResolvedValue(connection);
  m.releaseGrants.mockResolvedValue([releaseGrant]);
  m.releaseProfile.mockResolvedValue({
    connectionId: "ca",
    revision: 1,
    profile: { runtime: {}, helmVersion: "v4" },
  });
  m.saveReleaseProfile.mockResolvedValue({ profile: { runtime: {} } });
  m.probe.mockResolvedValue({ availableReplicas: 1 });
  m.get.mockImplementation(async (url: string) =>
    url === "/api/servers"
      ? { items: [{ id: "sa", name: "Host" }] }
      : url === "/api/projects"
        ? [{ id: "pa", name: "Project" }]
        : [],
  );
  m.confirm.mockResolvedValue(true);
  m.reauth.mockResolvedValue({
    reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
  });
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value() {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value() {
      this.removeAttribute("open");
    },
  });
});
afterEach(() => {
  w?.unmount();
  w = undefined;
  document.body.innerHTML = "";
  sessionStorage.clear();
  localStorage.clear();
});
describe("vault resource authorization", () => {
  it("filters metadata by credential ID and hides every unauthorized action", async () => {
    allow = (q) => q.action === "credential.metadata" && q.resourceId === "a";
    await render(SettingsVault);
    expect(w!.text()).toContain(cred.name);
    expect(w!.text()).not.toContain("Forbidden credential");
    expect(w!.find(".oauth-reconnect").exists()).toBe(false);
    expect(w!.find('button[aria-label^="编辑凭据"]').exists()).toBe(false);
    expect(w!.find('button[aria-label^="删除凭据"]').exists()).toBe(false);
    expect(m.oauth).not.toHaveBeenCalled();
    const s = state(VaultContent);
    s.openAddModal();
    s.openEditModal(cred);
    s.openDeleteModal(cred);
    s.openReconnect(cred);
    await s.handleFormSubmit();
    await s.confirmDelete();
    await s.confirmReconnect();
    await s.toggleEditReveal();
    expect(s.modalOpen).toBe(false);
    for (const f of [m.create, m.update, m.remove, m.reconnect, m.reveal])
      expect(f).not.toHaveBeenCalled();
  });
  it.each(["create", "edit", "delete", "reveal", "reconnect"])(
    "permits precise %s action with fixed request team",
    async (action) => {
      allow = (q) =>
        q.action === "credential.metadata" ||
        q.action === "credential." + ({ reconnect: "edit" } as any)[action] ||
        q.action === "credential." + action;
      await render(SettingsVault);
      const s = state(VaultContent);
      if (action === "create") {
        s.openAddModal();
        s.form.name = "new";
        s.form.secret = "fixture";
        await s.handleFormSubmit();
        expect(m.create).toHaveBeenCalledWith(
          expect.objectContaining({ name: "new" }),
          options(),
        );
      } else if (action === "delete") {
        s.openDeleteModal(cred);
        await s.confirmDelete();
        expect(m.remove).toHaveBeenCalledWith("a", options());
      } else if (action === "reconnect") {
        s.openReconnect(cred);
        s.reconnectProvider = "github";
        s.adoptOAuth = true;
        await s.confirmReconnect();
        expect(m.reconnect).toHaveBeenCalledWith(
          "a",
          expect.anything(),
          options(),
        );
      } else {
        s.openEditModal(cred);
        await settle();
        if (action === "edit") {
          s.form.name = "changed";
          await s.handleFormSubmit();
          expect(m.update).toHaveBeenCalledWith(
            "a",
            expect.objectContaining({ name: "changed" }),
            options(),
          );
        } else {
          expect(w!.get("#cred-name").attributes("disabled")).toBeDefined();
          await w!.get(".reveal-current-btn").trigger("click");
          await settle();
          expect(w!.text()).toContain("REVEAL_CANARY");
          expect(m.reveal).toHaveBeenCalledWith("a", options());
        }
      }
    },
  );
  it.each(["close-reopen", "revoke", "team", "logout"])(
    "discards a late plaintext response after %s",
    async (change) => {
      const pending = deferred<string>();
      m.reveal.mockReturnValue(pending.promise);
      await render(SettingsVault);
      const s = state(VaultContent);
      s.openEditModal(cred);
      await settle();
      const work = s.toggleEditReveal();
      if (change === "close-reopen") {
        s.closeModal();
        s.openEditModal(cred);
      } else if (change === "revoke")
        await revoke((q) => q.action !== "credential.reveal");
      else if (change === "team") usePermissionStore().setTeam("team-b");
      else useSessionStore().clearSession();
      await settle();
      pending.resolve("LATE_SECRET_CANARY");
      await work;
      await settle();
      expect(w!.text()).not.toContain("LATE_SECRET_CANARY");
      expect(s.editRevealed).toBeNull();
      if (["team", "logout"].includes(change))
        expect(m.reveal.mock.calls[0]![1].signal.aborted).toBe(true);
    },
  );
  it("clears a typed rotation secret when editing is revoked but reveal remains allowed", async () => {
    await render(SettingsVault);
    const s = state(VaultContent);
    s.openEditModal(cred);
    await settle();
    s.form.secret = "TYPED_CANARY";
    await revoke((q) => q.action !== "credential.edit");
    expect(s.form.secret).toBe("");
    await s.handleFormSubmit();
    expect(m.update).not.toHaveBeenCalled();
  });
});
describe("kubernetes credential authorization", () => {
  it("retains read-only metadata while guarding all forced action handlers", async () => {
    allow = (q) => q.action === "kubernetes_credential.read";
    await render(KubeCredentials);
    const s = state(CredentialContent);
    expect(w!.text()).toContain(kc.name);
    s.edit();
    s.edit(kc);
    s.inspect();
    s.save();
    s.toggle(kc);
    await s.remove(kc);
    expect(s.open).toBe(false);
    expect(m.confirm).not.toHaveBeenCalled();
    expect(m.reauth).not.toHaveBeenCalled();
    for (const f of [
      m.preview,
      m.saveCredential,
      m.enableCredential,
      m.deleteCredential,
    ])
      expect(f).not.toHaveBeenCalled();
  });
  it.each(["create", "edit", "preview", "toggle", "delete"])(
    "allows %s through password confirmation and resource-scoped options",
    async (action) => {
      await render(KubeCredentials);
      const s = state(CredentialContent);
      if (action === "delete") await s.remove(kc);
      else if (action === "toggle") s.toggle(kc);
      else {
        s.edit(action === "create" ? undefined : kc);
        await settle();
        s.draft.name = "Fixture";
        s.draft.token = "TOKEN_CANARY";
        if (action === "preview") s.inspect();
        else s.save();
      }
      await verify(s);
      const fn =
        action === "delete"
          ? m.deleteCredential
          : action === "toggle"
            ? m.enableCredential
            : action === "preview"
              ? m.preview
              : m.saveCredential;
      expect(fn).toHaveBeenCalledTimes(1);
      expect(fn.mock.calls[0]!.at(-1)).toEqual(options());
      if (action === "preview")
        expect(m.preview.mock.calls[0]![0].credentialId).toBe("ka");
    },
  );
  it("revocation during password verification prevents a credential write and clears the token", async () => {
    const pending = deferred<any>();
    m.reauth.mockReturnValue(pending.promise);
    await render(KubeCredentials);
    const s = state(CredentialContent);
    s.edit(kc);
    await settle();
    s.draft.token = "TOKEN_CANARY";
    s.save();
    s.gate.password.value = "fixture";
    const work = s.gate.verify();
    await revoke((q) => q.action === "kubernetes_credential.read");
    pending.resolve({
      reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
    });
    await work;
    expect(m.saveCredential).not.toHaveBeenCalled();
    expect(s.draft.token).toBe("");
  });
  it("aborts old-team list response and never displays its metadata", async () => {
    const pending = deferred<any[]>();
    m.credentials.mockReturnValueOnce(pending.promise).mockResolvedValue([]);
    await render(KubeCredentials);
    const opts = m.credentials.mock.calls[0]![1];
    usePermissionStore().setTeam("team-b");
    await settle();
    pending.resolve([{ ...kc, name: "OLD_TEAM_CANARY" }]);
    await settle();
    expect(opts.signal.aborted).toBe(true);
    expect(w!.text()).not.toContain("OLD_TEAM_CANARY");
  });
});
describe("kubernetes connection and release authorization", () => {
  it("guards every connection/grant handler for read-only employees", async () => {
    allow = (q) => ["kubernetes.read", "project.read"].includes(q.action);
    await render(SettingsKubernetes);
    const s = state(KubernetesContent);
    s.selectedId = "ca";
    await settle();
    s.edit();
    s.edit(connection);
    s.saveConnection();
    await s.remove(connection);
    s.editGrant(grant);
    s.saveGrant();
    await s.removeGrant(grant);
    s.probe();
    expect(s.connectionOpen).toBe(false);
    expect(s.grantOpen).toBe(false);
    expect(m.confirm).not.toHaveBeenCalled();
    for (const f of [
      m.saveConnection,
      m.deleteConnection,
      m.saveGrant,
      m.deleteGrant,
      m.probe,
    ])
      expect(f).not.toHaveBeenCalled();
  });
  it.each([
    "create",
    "edit",
    "delete",
    "grant-create",
    "grant-edit",
    "grant-delete",
  ])("allows %s without global system.manage permission", async (action) => {
    allow = (q) => q.action !== "system.manage";
    await render(SettingsKubernetes);
    const s = state(KubernetesContent);
    s.selectedId = "ca";
    await settle();
    if (action === "delete") await s.remove(connection);
    else if (action === "grant-delete") await s.removeGrant(grant);
    else if (action.startsWith("grant")) {
      s.editGrant(action === "grant-edit" ? grant : undefined);
      s.grant.projectId = "pa";
      s.executorIndex = "0";
      s.containerNames = "web";
      await settle();
      s.saveGrant();
    } else {
      s.edit(action === "edit" ? connection : undefined);
      s.draft.credentialId = "ka";
      s.ssh = [{ serverId: "sa", hostKeySHA256: "pin" }];
      s.local = false;
      await settle();
      s.saveConnection();
    }
    await verify(s);
    const fn =
      action === "delete"
        ? m.deleteConnection
        : action === "grant-delete"
          ? m.deleteGrant
          : action.startsWith("grant")
            ? m.saveGrant
            : m.saveConnection;
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn.mock.calls[0]!.at(-1)).toEqual(options());
  });
  it.each(["kubernetes_credential.use", "server.use"])(
    "rejects a connection edit without %s on the referenced resource",
    async (denied) => {
      allow = (q) => q.action !== denied;
      await render(SettingsKubernetes);
      const s = state(KubernetesContent);
      s.edit(connection);
      await settle();
      s.saveConnection();
      expect(s.gate.passwordOpen.value).toBe(false);
      expect(m.saveConnection).not.toHaveBeenCalled();
    },
  );
  it("uses the exact server-derived environment ID for deployment probe", async () => {
    allow = (q) =>
      q.action !== "kubernetes.deploy" || q.environmentId === "env-dev";
    await render(SettingsKubernetes);
    const s = state(KubernetesContent);
    s.selectedId = "ca";
    await settle();
    s.probeGrant = "ga";
    await settle();
    s.probeContainer = "web";
    await settle();
    s.probe();
    await verify(s);
    expect(m.probe).toHaveBeenCalledWith(
      "ca",
      "ga",
      "web",
      expect.any(AbortSignal),
      options(),
    );
    expect(
      m.decisions.mock.calls
        .flatMap((c) => c[1])
        .some(
          (q) =>
            q.action === "kubernetes.deploy" && q.environmentId === "env-dev",
        ),
    ).toBe(true);
    await revoke((q) => q.action !== "kubernetes.deploy");
    s.probe();
    expect(m.probe).toHaveBeenCalledTimes(1);
    expect(s.probeResult).toBeUndefined();
  });
  it("rejects local execution for an employee even when resource decisions allow it", async () => {
    await render(SettingsKubernetes);
    const s = state(KubernetesContent);
    s.edit(connection);
    s.local = true;
    await settle();
    s.saveConnection();
    expect(m.saveConnection).not.toHaveBeenCalled();
    expect(s.gate.passwordOpen.value).toBe(false);
  });
  it("hides grants whose project is unreadable", async () => {
    allow = (q) => q.action !== "project.read";
    await render(SettingsKubernetes);
    const s = state(KubernetesContent);
    s.selectedId = "ca";
    await settle();
    expect(s.probeChoices).toHaveLength(0);
    expect(w!.text()).not.toContain("app / web");
  });
  it.each(["profile", "grant", "delete"])(
    "guards release %s mutations when connection is read-only",
    async (action) => {
      allow = (q) => ["kubernetes.read", "project.read"].includes(q.action);
      await render(KubeReleaseSettings, { connection, projects: [] });
      const s = state(ReleaseContent);
      if (action === "profile") s.saveProfile();
      else if (action === "grant") {
        s.edit(releaseGrant);
        s.resource();
        s.saveGrant();
      } else await s.remove(releaseGrant);
      expect(m.confirm).not.toHaveBeenCalled();
      for (const f of [
        m.saveReleaseProfile,
        m.saveReleaseGrant,
        m.deleteReleaseGrant,
      ])
        expect(f).not.toHaveBeenCalled();
    },
  );
  it.each(["profile", "grant", "delete"])(
    "allows release %s only with precise connection edit permission",
    async (action) => {
      await render(KubeReleaseSettings, {
        connection,
        projects: [{ id: "pa", name: "Project" }],
      });
      const s = state(ReleaseContent);
      if (action === "profile") s.saveProfile();
      else if (action === "grant") {
        s.edit(releaseGrant);
        await settle();
        s.saveGrant();
      } else await s.remove(releaseGrant);
      await verify(s);
      const fn =
        action === "profile"
          ? m.saveReleaseProfile
          : action === "grant"
            ? m.saveReleaseGrant
            : m.deleteReleaseGrant;
      expect(fn).toHaveBeenCalledTimes(1);
      expect(fn.mock.calls[0]!.at(-1)).toEqual(options());
    },
  );
  it("clears release grant metadata on read permission revocation and blocks pending delete", async () => {
    const pending = deferred<boolean>();
    m.confirm.mockReturnValue(pending.promise);
    await render(KubeReleaseSettings, { connection, projects: [] });
    const s = state(ReleaseContent);
    const work = s.remove(releaseGrant);
    await revoke();
    pending.resolve(true);
    await work;
    await settle();
    expect(s.grants).toHaveLength(0);
    expect(m.deleteReleaseGrant).not.toHaveBeenCalled();
  });
});

describe("resource read-back and authorization failure boundaries", () => {
  it("never exposes denied credential metadata in an uncertain-write read-back", async () => {
    m.credentials.mockResolvedValue([
      kc,
      { ...kc, id: "kb", name: "DENIED_READBACK_CANARY" },
    ]);
    allow = (q) => q.resourceId !== "kb";
    await render(KubeCredentials);
    const s = state(CredentialContent);
    s.gate.uncertain.value = true;
    await s.load();
    await settle();
    expect(w!.text()).toContain(kc.name);
    expect(w!.text()).not.toContain("DENIED_READBACK_CANARY");
  });
  it("clears cached credentials after a server-side authorization denial", async () => {
    await render(KubeCredentials);
    const s = state(CredentialContent);
    m.credentials.mockRejectedValue(new HttpError(403, null, "denied"));
    await s.load();
    await settle();
    expect(w!.text()).not.toContain(kc.name);
    expect(s.rows).toHaveLength(0);
  });
  it("discards an old team release profile after team switch", async () => {
    const pending = deferred<any>();
    m.releaseProfile
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValue({ profile: { runtime: {} } });
    await render(KubeReleaseSettings, { connection, projects: [] });
    const old = state(ReleaseContent),
      options = m.releaseProfile.mock.calls[0]![2];
    usePermissionStore().setTeam("team-b");
    await settle();
    pending.resolve({ profile: { runtime: { image: "OLD_PROFILE_CANARY" } } });
    await settle();
    expect(options.signal.aborted).toBe(true);
    expect(old.profile).toBeUndefined();
    expect(w!.html()).not.toContain("OLD_PROFILE_CANARY");
  });
  it("rechecks the captured release project after password confirmation", async () => {
    await render(KubeReleaseSettings, {
      connection,
      projects: [{ id: "pa", name: "Project" }],
    });
    const s = state(ReleaseContent);
    s.edit(releaseGrant);
    await settle();
    s.saveGrant();
    s.grant.projectId = "pb";
    await revoke((q) => q.resourceId !== "pa");
    await verify(s);
    expect(m.saveReleaseGrant).not.toHaveBeenCalled();
  });
});

describe('bounded resource permission pagination',()=>{
 it.each([
  {component:SettingsVault,mock:m.list,fixture:cred,count:160,action:'credential.metadata'},
  {component:KubeCredentials,mock:m.credentials,fixture:kc,count:180,action:'kubernetes_credential.read'},
  {component:SettingsKubernetes,mock:m.connections,fixture:connection,count:140,action:'kubernetes.read'},
 ])('can reach the last $count resources without retaining earlier pages',async ({component,mock,fixture,count,action})=>{
  mock.mockResolvedValue(Array.from({length:count},(_,i)=>({...fixture,id:'many-'+i,name:'Resource '+i})));
  await render(component);await settle();
  const store=usePermissionStore();
  expect(store.allowed('team-a',{action,resourceId:'many-0'})).toBe(true);
  for(let page=1;page<Math.ceil(count/25);page++){
   await w!.get('.resource-pager button:last-child').trigger('click');await settle();
  }
  expect(w!.text()).toContain('Resource '+(count-1));
  expect(store.allowed('team-a',{action,resourceId:'many-0'})).toBe(false);
  expect(store.allowed('team-a',{action,resourceId:'many-'+(count-1)})).toBe(true);
  expect(w!.get('.resource-pager button:last-child').attributes('disabled')).toBeDefined();
  await revoke(q=>q.resourceId!=='many-'+(count-1));
  expect(w!.text()).not.toContain('Resource '+(count-1));
 });
});


describe('release settings actual delete confirmation identity boundaries', () => {
  it.each(['connection', 'employee', 'team'])('does not transfer a delete confirmation after %s changes', async (change) => {
    const pending = deferred<boolean>();
    m.confirm.mockReturnValue(pending.promise);
    await render(KubeReleaseSettings, { connection, projects: [{ id: 'pa', name: 'Project' }] });
    const old = state(ReleaseContent);
    const removeButton = w!.findAll('button').find(button => button.text() === '删除');
    expect(removeButton).toBeDefined();
    await removeButton!.trigger('click');
    expect(m.confirm).toHaveBeenCalledTimes(1);
    if (change === 'connection') await w!.setProps({ connection: { ...connection, id: 'cb' } });
    else if (change === 'employee') useSessionStore().setUser({ username: 'other', userId: 'u2', multiTenant: true, instanceAdmin: false });
    else usePermissionStore().setTeam('team-b');
    await settle();
    pending.resolve(true);
    await settle();
    // Even a stale password continuation must not perform the old deletion.
    await verify(old);
    expect(m.deleteReleaseGrant).not.toHaveBeenCalled();
    expect(m.reauth).not.toHaveBeenCalled();
    expect(state(ReleaseContent)).not.toBe(old);
  });
});

describe('integration actual DOM controls', () => {
  function button(text: string) {
    const result = w!.findAll('button').find(b => b.text() === text);
    expect(result, `button ${text}`).toBeDefined();
    return result!;
  }
  async function passwordClick() {
    const input = w!.get('input[autocomplete="current-password"]');
    await input.setValue('fixture');
    const form = input.element.closest('form')!;
    (form.querySelector('button[type="submit"]') as HTMLButtonElement).click();
    await settle();
  }
  async function credentialDraft() {
    for (const [id,value] of Object.entries({'kc-name':'Fixture','kc-context':'test','kc-server':'https://cluster.invalid','kc-ca':'CA','kc-token':'TOKEN_CANARY'})) await w!.get('#'+id).setValue(value);
  }
  it.each(['create','rotate','preview','disable','enable','delete'])('clicks credential %s and actual password submit with fixed team', async action => {
    if(action==='enable') m.credentials.mockResolvedValue([{...kc,enabled:false}]);
    await render(KubeCredentials);
    if(['create','rotate','preview'].includes(action)) {
      await (action==='create' ? w!.get('.kube-head button') : w!.get('.kube-row .kube-actions button')).trigger('click');
      await settle(); await credentialDraft();
      if(action==='preview') await button('检查格式').trigger('click');
      else await w!.get('#kc-name').element.closest('form')!.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    } else await w!.get('.kube-row .kube-actions').findAll('button')[action==='delete'?2:1]!.trigger('click');
    await settle(); expect(m.reauth).not.toHaveBeenCalled(); await passwordClick();
    const fn=action==='delete'?m.deleteCredential:['disable','enable'].includes(action)?m.enableCredential:action==='preview'?m.preview:m.saveCredential;
    expect(fn).toHaveBeenCalledOnce();expect(fn.mock.calls[0]!.at(-1)).toEqual(options());
    if(['disable','enable'].includes(action)) expect(fn.mock.calls[0]![1]).toBe(action==='enable');
  });
  it('hides all credential mutation buttons for a read-only employee',async()=>{
    allow=q=>q.action==='kubernetes_credential.read'; await render(KubeCredentials);
    expect(w!.find('section[aria-labelledby="kube-vault-title"] > header button').exists()).toBe(false);expect(w!.find('.kube-row .kube-actions button').exists()).toBe(false);
    expect(m.saveCredential).not.toHaveBeenCalled();expect(m.enableCredential).not.toHaveBeenCalled();expect(m.deleteCredential).not.toHaveBeenCalled();
  });
  it.each(['cancel','revoke','team'])('credential delete actual click rejects %s pending confirmation',async mode=>{
    const pending=deferred<boolean>();m.confirm.mockReturnValue(pending.promise);await render(KubeCredentials);
    await w!.get('.kube-row .kube-actions').findAll('button')[2]!.trigger('click');
    if(mode==='revoke') await revoke(q=>q.action==='kubernetes_credential.read');
    if(mode==='team'){usePermissionStore().setTeam('team-b');await settle()}
    pending.resolve(mode!=='cancel');await settle();expect(m.deleteCredential).not.toHaveBeenCalled();expect(m.reauth).not.toHaveBeenCalled();
  });
  it('credential actual password cancel drops the pending toggle',async()=>{
    await render(KubeCredentials);await w!.get('.kube-row .kube-actions').findAll('button')[1]!.trigger('click');await settle();
    const form=w!.get('input[autocomplete="current-password"]').element.closest('form')!;
    (form.querySelector('button[type="button"]') as HTMLButtonElement).click();await settle();expect(m.enableCredential).not.toHaveBeenCalled();expect(m.reauth).not.toHaveBeenCalled();
  });
  it.each(['profile','create-grant','edit-grant','delete-grant'])('clicks release %s and actual password submit with fixed team',async action=>{
    await render(KubeReleaseSettings,{connection,projects:[{id:'pa',name:'Project'}]});
    if(action==='profile'){
      for(const [id,value] of Object.entries({'kr-tool-image':'tool:1','kr-tool-uid':'1000','kr-tool-kubectl':'/kubectl','kr-tool-helper':'/helper','kr-tool-helm':'/helm'}))await w!.get('#'+id).setValue(value);
      await w!.get('details button[type="submit"]').trigger('click');
    }else if(action==='delete-grant')await button('删除').trigger('click');
    else {
      if(action==='create-grant')await w!.findAll('header.kube-head')[1]!.get('button').trigger('click');else await button('编辑').trigger('click');
      await settle();await w!.get('#kr-grant-project').setValue('pa');await w!.get('#kr-grant-env').setValue('dev');
      if(!w!.find('#kr-scope-0-name').exists()){const form=w!.get('#kr-grant-env').element.closest('form')!;(form.querySelector('fieldset > button') as HTMLButtonElement).click();await settle()}
      await w!.get('#kr-scope-0-name').setValue('cfg');
      (w!.get('#kr-grant-env').element.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement).click();
    }
    await settle();await passwordClick();
    const fn=action==='profile'?m.saveReleaseProfile:action==='delete-grant'?m.deleteReleaseGrant:m.saveReleaseGrant;
    expect(fn).toHaveBeenCalledOnce();expect(fn.mock.calls[0]!.at(-1)).toEqual(options());expect(fn.mock.calls[0]![0]).toBe('ca');
  });
  it('blocks actual release controls when the connection is read-only',async()=>{
    allow=q=>['kubernetes.read','project.read'].includes(q.action);await render(KubeReleaseSettings,{connection,projects:[{id:'pa',name:'Project'}]});
    for(const b of w!.findAll('button').filter(b=>['编辑','删除','保存'].includes(b.text()))){expect((b.element as HTMLButtonElement).disabled).toBe(true);await b.trigger('click')}
    expect(m.saveReleaseProfile).not.toHaveBeenCalled();expect(m.saveReleaseGrant).not.toHaveBeenCalled();expect(m.deleteReleaseGrant).not.toHaveBeenCalled();
  });
  it.each(['cancel','revoke'])('release delete actual click rejects %s confirmation',async mode=>{
    const pending=deferred<boolean>();m.confirm.mockReturnValue(pending.promise);await render(KubeReleaseSettings,{connection,projects:[{id:'pa',name:'Project'}]});await button('删除').trigger('click');
    if(mode==='revoke')await revoke(q=>['kubernetes.read','project.read'].includes(q.action));pending.resolve(mode!=='cancel');await settle();expect(m.deleteReleaseGrant).not.toHaveBeenCalled();expect(m.reauth).not.toHaveBeenCalled();
  });
});
it('clicks release refresh and local resource add/remove/cancel controls without writes',async()=>{
 await render(KubeReleaseSettings,{connection,projects:[{id:'pa',name:'Project'}]});const before=m.releaseProfile.mock.calls.length;await w!.get('header button').trigger('click');await settle();expect(m.releaseProfile.mock.calls.length).toBe(before+1);
 await w!.findAll('header.kube-head')[1]!.get('button').trigger('click');await settle();
 const form=w!.get('#kr-grant-env').element.closest('form')!;const add=form.querySelector<HTMLButtonElement>('fieldset > button')!;add.click();await settle();expect(w!.find('#kr-scope-0-name').exists()).toBe(true);
 await w!.get('.kube-action-options input').setValue(false);await w!.get('.kube-action-options input').setValue(true);
 await w!.get('div.kube-surface.kube-form > button').trigger('click');await settle();expect(w!.find('#kr-scope-0-name').exists()).toBe(false);
 const cancel=w!.findAll('button').find(b=>b.text()==='取消');expect(cancel).toBeDefined();await cancel!.trigger('click');await settle();expect(w!.find('#kr-grant-env').exists()).toBe(false);expect(m.saveReleaseGrant).not.toHaveBeenCalled();
});
it('clicks credential failed-list refresh and draft footer cancel',async()=>{
 m.credentials.mockRejectedValueOnce(new Error('transient'));await render(KubeCredentials);await w!.get('.kube-error button').trigger('click');await settle();expect(m.credentials).toHaveBeenCalledTimes(2);
 await w!.get('section[aria-labelledby="kube-vault-title"] > header button').trigger('click');await settle();await w!.get('#kc-token').setValue('DRAFT_SECRET');
 const form=w!.get('#kc-token').element.closest('form')!;(form.querySelector('footer button') as HTMLButtonElement).click();await settle();expect((w!.get('#kc-token').element as HTMLInputElement).value).toBe('');expect(m.saveCredential).not.toHaveBeenCalled();
});

it('clicks release uncertain-delete read-back and acknowledge without repeating the write',async()=>{
 m.deleteReleaseGrant.mockRejectedValue(new HttpError(0,null,'lost'));await render(KubeReleaseSettings,{connection,projects:[{id:'pa',name:'Project'}]});
 await w!.findAll('button').find(b=>b.text()==='删除')!.trigger('click');await settle();const password=w!.get('input[autocomplete="current-password"]');await password.setValue('fixture');(password.element.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement).click();await settle();expect(m.deleteReleaseGrant).toHaveBeenCalledOnce();
 await w!.findAll('button').find(b=>b.text()==='只读核对')!.trigger('click');await settle();await w!.findAll('button').find(b=>b.text()==='已核对，结束本次编辑')!.trigger('click');await settle();expect(m.deleteReleaseGrant).toHaveBeenCalledOnce();expect(w!.findAll('button').some(b=>b.text()==='只读核对')).toBe(false);
});



describe("vault form buttons enforce the current resource permission", () => {
  const opener = {
    create: ".section-header button.btn-primary, .section-head button.btn-primary",
    edit: 'button[aria-label^="编辑凭据"]',
    delete: 'button[aria-label^="删除凭据"]',
  };
  const selector = { create: 'form button[type="submit"]', edit: 'form button[type="submit"]', delete: ".modal-footer .btn-danger" };
  const pick = {
    create: () => m.create,
    edit: () => m.update,
    delete: () => m.remove,
  };
  const open = async (kind: keyof typeof opener) => {
    await render(SettingsVault);
    await w!.get(opener[kind]).trigger("click");
    await settle();
    if (kind !== "delete") {
      await w!.get("#cred-name").setValue("updated fixture");
      if (kind === "create") await w!.get("#cred-secret").setValue("synthetic-value");
    }
    return w!.get(selector[kind]);
  };
  const cases = ["create", "edit", "delete"] as const;
  it.each(cases)("clicks the %s confirmation with the exact resource and team", async (kind) => {
    const action = "credential." + kind;
    allow = (q) => (q.action === "credential.metadata" && q.resourceId === "a") ||
      (q.action === action && q.resourceId === (kind === "create" ? "" : "a"));
    const button = await open(kind);
    (button.element as HTMLButtonElement).click();
    await settle();
    if (kind === "create") expect(m.create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ name: "updated fixture" }), options());
    if (kind === "edit") expect(m.update).toHaveBeenCalledExactlyOnceWith("a", expect.objectContaining({ name: "updated fixture" }), options());
    if (kind === "delete") expect(m.remove).toHaveBeenCalledExactlyOnceWith("a", options());
  });
  it.each(cases)("blocks a stale %s confirmation when only metadata remains allowed", async (kind) => {
    const button = await open(kind);
    await revoke((q) => q.action === "credential.metadata" && q.resourceId === "a");
    await button.trigger("click");
    await settle();
    expect(pick[kind]()).not.toHaveBeenCalled();
    expect(w!.text()).toContain(cred.name);
    expect(w!.text()).not.toContain("Forbidden credential");
  });
});

describe("vault plaintext rendered controls", () => {
  const sibling = { ...cred, id: "b", name: "Read-only second credential" };
  const metadataAndSelectedReveal = (q: PermissionQuery) =>
    q.action === "credential.metadata" ||
    (q.action === "credential.reveal" && q.resourceId === cred.id);

  async function openSelectedCredential() {
    allow = metadataAndSelectedReveal;
    m.list.mockResolvedValue([cred, sibling]);
    const wrapper = await render(SettingsVault);
    const rows = wrapper.findAll(".cred-row");
    const selected = rows.find(row => row.text().includes(cred.name))!;
    const readOnly = rows.find(row => row.text().includes(sibling.name))!;
    expect(selected.exists()).toBe(true);
    expect(readOnly.exists()).toBe(true);
    expect(readOnly.find(".cred-ops button").exists()).toBe(false);
    await selected.get(".cred-ops button").trigger("click");
    await settle();
    expect(wrapper.get("#cred-name").attributes("disabled")).toBeDefined();
    return wrapper;
  }

  it("reveals and hides only the selected same-team credential through its two button states", async () => {
    const wrapper = await openSelectedCredential();
    expect(wrapper.find(".current-secret").exists()).toBe(false);
    const reveal = wrapper.get(".reveal-current-btn");
    await reveal.trigger("click");
    await settle();
    expect(wrapper.get(".current-secret").text()).toBe("REVEAL_CANARY");
    expect(m.reveal).toHaveBeenCalledExactlyOnceWith(cred.id, options());

    await reveal.trigger("click");
    await settle();
    expect(wrapper.find(".current-secret").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("REVEAL_CANARY");
    expect(m.reveal).toHaveBeenCalledTimes(1);
    expect(m.update).not.toHaveBeenCalled();
  });

  it("discards a pending plaintext result and blocks its actual old button after revocation", async () => {
    const wrapper = await openSelectedCredential();
    const pending = deferred<string>();
    m.reveal.mockReturnValueOnce(pending.promise);
    const old = wrapper.get(".reveal-current-btn");
    await old.trigger("click");
    expect(m.reveal).toHaveBeenCalledTimes(1);
    await revoke(q => q.action === "credential.metadata");
    pending.resolve("REVOKED_DOM_CANARY");
    await settle();
    expect(wrapper.find(".reveal-current-btn").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("REVOKED_DOM_CANARY");

    await old.trigger("click");
    await settle();
    expect(m.reveal).toHaveBeenCalledTimes(1);
    expect(m.update).not.toHaveBeenCalled();
  });
});

describe("vault actual save and delete confirmation buttons", () => {
  async function scopedRow(action: "edit" | "delete") {
    const sibling = { ...cred, id: "b", name: "Visible metadata only" };
    allow = q => q.action === "credential.metadata" ||
      (q.action === "credential." + action && q.resourceId === cred.id);
    m.list.mockResolvedValue([cred, sibling]);
    const wrapper = await render(SettingsVault);
    const readOnly = wrapper.findAll(".cred-row").find(row => row.text().includes(sibling.name))!;
    expect(readOnly.find(".cred-ops button").exists()).toBe(false);
    const row = wrapper.findAll(".cred-row").find(candidate => candidate.text().includes(cred.name))!;
    await row.get(action === "delete" ? ".op-btn--danger" : 'button[aria-label^="编辑凭据"]').trigger("click");
    await settle();
    return wrapper;
  }

  it("submits native save only for the selected team credential and never sends its old secret", async () => {
    const wrapper = await scopedRow("edit");
    await wrapper.get("#cred-name").setValue("Edited credential");
    wrapper.get<HTMLButtonElement>(".modal-form button[type=submit]").element.click();
    await settle();
    expect(m.update).toHaveBeenCalledExactlyOnceWith(cred.id, {
      name: "Edited credential", scope: "", username: "",
    }, options());
    expect(m.reveal).not.toHaveBeenCalled();
    expect(wrapper.find(".modal-form").exists()).toBe(false);
  });

  it("rejects a retained native save on the same frame after cancelling the edit", async () => {
    const wrapper = await scopedRow("edit");
    await wrapper.get("#cred-name").setValue("Cancelled edit");
    const save = wrapper.get<HTMLButtonElement>(".modal-form button[type=submit]").element;
    wrapper.get<HTMLButtonElement>(".modal-form .modal-footer .btn-secondary").element.click();
    save.click();
    await settle();

    expect(wrapper.find(".modal-form").exists()).toBe(false);
    expect(m.update).not.toHaveBeenCalled();
  });

  it("confirms deletion only for the selected credential ID and leaves its same-team sibling", async () => {
    const wrapper = await scopedRow("delete");
    expect(m.remove).not.toHaveBeenCalled();
    await wrapper.get(".modal-footer--standalone .btn-danger").trigger("click");
    await settle();
    expect(m.remove).toHaveBeenCalledExactlyOnceWith(cred.id, options());
    expect(wrapper.text()).toContain("Visible metadata only");
    expect(wrapper.text()).not.toContain(cred.name);
  });

  it.each(["footer", "header", "scrim"] as const)("cancels the displayed delete through %s without using its retained confirmation", async (where) => {
    const wrapper = await scopedRow("delete");
    const dialog = wrapper.get(".modal-scrim[role=dialog]");
    const confirm = dialog.get<HTMLButtonElement>(".modal-footer--standalone .btn-danger").element;
    const cancel = where === "scrim" ? dialog.element : dialog.get(where === "header" ? ".modal-close" : ".modal-footer--standalone .btn-secondary").element;
    (cancel as HTMLElement).click();
    confirm.click();
    await settle();

    expect(wrapper.find(".modal-scrim[role=dialog]").exists()).toBe(false);
    expect(wrapper.text()).toContain(cred.name);
    expect(m.remove).not.toHaveBeenCalled();
  });
});

it.each(["the same", "another"] as const)("never transfers a cancelled native save to %s credential reopened in the same frame", async (target) => {
  const first = cred;
  const second = { ...cred, id: "b", name: "Second editable credential" };
  m.list.mockResolvedValue([first, second]);
  allow = q => q.action === "credential.metadata" || q.action === "credential.edit";
  const wrapper = await render(SettingsVault);
  function edit(name: string) {
    return wrapper.findAll(".cred-row").find(row => row.text().includes(name))!
      .get<HTMLButtonElement>('button[aria-label^="编辑凭据"]');
  }
  const selected = target === "the same" ? first : second;
  const openSelected = edit(selected.name).element;
  await edit(first.name).trigger("click");
  await settle();
  await wrapper.get("#cred-name").setValue("Cancelled first draft");
  const oldSave = wrapper.get<HTMLButtonElement>(".modal-form button[type=submit]").element;
  wrapper.get<HTMLButtonElement>(".modal-form .btn-secondary").element.click();
  openSelected.click();
  oldSave.click();
  await settle();

  expect(m.update).not.toHaveBeenCalled();
  expect(wrapper.get<HTMLInputElement>("#cred-name").element.value).toBe(selected.name);
  m.update.mockResolvedValue(selected);
  await wrapper.get("#cred-name").setValue("Confirmed current draft");
  wrapper.get<HTMLFormElement>(".modal-form").element.requestSubmit();
  await settle();
  expect(m.update).toHaveBeenCalledExactlyOnceWith(selected.id, {
    name: "Confirmed current draft", scope: "", username: "",
  }, options());
});
describe("kubernetes connection actual DOM scopes", () => {
  const sibling = { ...connection, id: "cb", name: "Read-only cluster" };
  const byText = (text: string, within: Pick<DOMWrapper<Element>, "findAll"> = w!) => within.findAll("button").find(button => button.text() === text)!;
  const selected = () => w!.get("article.kube-card");
  const formOf = (field: string) => w!.get(field).element.closest("form")!;
  const submitFor = (field: string) => formOf(field).querySelector<HTMLButtonElement>('button[type="submit"]')!;
  async function password() {
    const input = w!.get('dialog[open] input[autocomplete="current-password"]');
    await input.setValue("test-only-password");
    input.element.closest("form")!.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    await settle();
  }
  async function openGrantEditor() {
    await byText("授权目标", selected()).trigger("click");
    await settle();
    await w!.get(".kube-section .kube-head button").trigger("click");
    await settle();
    for (const [id,value] of Object.entries({
      "kg-project":"pa","kg-environment":"dev","kg-namespace":"app",
      "kg-deployment":"web","kg-containers":"web","kg-executor":"0",
    })) await w!.get("#"+id).setValue(value);
    await settle();
  }

  it.each([true, false])("saves connection enabled=%s with native submit and password only for the editable row", async (enabled) => {
    m.connections.mockResolvedValue([{...connection,enabled:!enabled}, sibling]);
    allow = q => !(q.resourceId === sibling.id && ["kubernetes.edit","kubernetes.delete"].includes(q.action));
    await render(SettingsKubernetes);
    const rows = w!.findAll("article.kube-card");
    const siblingButtons = rows[1]!.findAll("button").map(button=>button.text());
    expect(siblingButtons).toEqual(["授权目标"]);
    await byText("编辑", rows[0]!).trigger("click");
    await settle();
    await w!.get("#kn-name").setValue("Selected cluster edit");
    const checks = formOf("#kn-name").querySelectorAll<HTMLInputElement>('input[type="checkbox"]');
    checks[checks.length-1]!.click();
    await settle();
    submitFor("#kn-name").click();
    await settle();
    expect(m.saveConnection).not.toHaveBeenCalled();
    await password();

    expect(m.saveConnection).toHaveBeenCalledExactlyOnceWith(connection.id,connection.revision,expect.objectContaining({
      name:"Selected cluster edit",enabled,
      credentialId:"ka",executors:[{kind:"ssh",serverId:"sa",hostKeySHA256:"pin"}],
    }),expect.any(AbortSignal),options());
  });

  it("adds and removes exact SSH draft entries and creates a new connection after authorized native submit", async () => {
    await render(SettingsKubernetes);
    const header = w!.get(".settings-page > header");
    const calls = m.connections.mock.calls.length;
    await header.findAll("button")[0]!.trigger("click");
    await settle();
    expect(m.connections).toHaveBeenCalledTimes(calls+1);
    await header.findAll("button")[1]!.trigger("click");
    await settle();
    await w!.get("#kn-name").setValue("Created cluster");
    await w!.get("#kn-credential").setValue("ka");
    await w!.get("#kn-image").setValue("tool:1");
    const dialog = w!.get("#kn-name").element.closest("dialog")!;
    const add = [...dialog.querySelectorAll<HTMLButtonElement>("button")].find(button=>button.textContent?.trim()==="添加 SSH 执行节点")!;
    expect(add).toBeDefined();
    add.click(); await settle(); add.click(); await settle();
    expect(w!.find("#kn-ssh-1").exists()).toBe(true);
    const last = w!.get("#kn-ssh-1").element.closest(".kube-card")!;
    last.querySelector<HTMLButtonElement>("button")!.click();
    await settle();
    expect(w!.find("#kn-ssh-1").exists()).toBe(false);
    await w!.get("#kn-ssh-0").setValue("sa");
    await w!.get("#kn-pin-0").setValue("synthetic-pin");
    await settle();
    submitFor("#kn-name").click(); await settle();
    await password();
    expect(m.saveConnection).toHaveBeenCalledExactlyOnceWith(undefined,0,expect.objectContaining({
      name:"Created cluster",credentialId:"ka",enabled:true,
      executors:[{kind:"ssh",serverId:"sa",hostKeySHA256:"synthetic-pin"}],
    }),expect.any(AbortSignal),options());
  });

  it("does not reopen password verification from a retained connection submit after native dialog cancel", async () => {
    await render(SettingsKubernetes);
    await byText("编辑",selected()).trigger("click");
    await settle();
    const form = formOf("#kn-name");
    const old = submitFor("#kn-name");
    form.querySelector<HTMLButtonElement>("footer button[type=button]")!.click();
    old.click(); await settle();
    expect(w!.find('dialog[open] input[autocomplete="current-password"]').exists()).toBe(false);
    expect(m.reauth).not.toHaveBeenCalled();
    expect(m.saveConnection).not.toHaveBeenCalled();
  });

  it("deletes only the selected connection after its independent confirmation and native password", async () => {
    m.connections.mockResolvedValue([connection,sibling]);
    allow = q => q.action !== "kubernetes.delete" || q.resourceId === connection.id;
    await render(SettingsKubernetes);
    const rows = w!.findAll("article.kube-card");
    expect(rows[1]!.findAll("button").some(button=>button.text()==="删除")).toBe(false);
    await byText("删除",rows[0]!).trigger("click"); await settle();
    expect(m.confirm).toHaveBeenCalledTimes(1);
    expect(m.deleteConnection).not.toHaveBeenCalled();
    await password();
    expect(m.deleteConnection).toHaveBeenCalledExactlyOnceWith(connection,expect.any(AbortSignal),options());
  });

  it("clears an old selected connection deletion when its own grant is revoked during confirmation", async () => {
    const pending = deferred<boolean>();
    m.confirm.mockReturnValueOnce(pending.promise);
    await render(SettingsKubernetes);
    const remove = byText("删除",selected());
    await remove.trigger("click");
    await revoke(q=>q.action!=="kubernetes.delete");
    pending.resolve(true); await settle();
    await remove.trigger("click"); await settle();
    expect(w!.find('dialog[open] input[autocomplete="current-password"]').exists()).toBe(false);
    expect(m.reauth).not.toHaveBeenCalled();
    expect(m.deleteConnection).not.toHaveBeenCalled();
  });

  it("selects a connection and submits the exact project and executor in its actual grant form", async () => {
    await render(SettingsKubernetes);
    await openGrantEditor();
    expect(m.grants.mock.calls.some(([id])=>id==="ca")).toBe(true);
    expect(formOf("#kg-project").checkValidity()).toBe(true);
    submitFor("#kg-project").click(); await settle();
    expect(m.saveGrant).not.toHaveBeenCalled();
    await password();
    expect(m.saveGrant).toHaveBeenCalledExactlyOnceWith("ca",undefined,0,{
      projectId:"pa",environment:"dev",namespace:"app",deployment:"web",containers:["web"],
      executor:{kind:"ssh",serverId:"sa",hostKeySHA256:"pin"},requireApproval:true,enabled:true,
    },expect.any(AbortSignal),options());
  });

  it("disables native grant save when the current destination project is withdrawn", async () => {
    await render(SettingsKubernetes);
    await openGrantEditor();
    const save = submitFor("#kg-project");
    expect(save.disabled).toBe(false);
    await revoke(q=>q.action!=="project.read");
    expect(save.disabled).toBe(true);
    save.click(); await settle();
    expect(m.reauth).not.toHaveBeenCalled();
    expect(m.saveGrant).not.toHaveBeenCalled();
  });

  it("uses the actual probe button for the selected environment, grant and container only", async () => {
    allow = q => q.action !== "kubernetes.deploy" || q.environmentId === "env-dev";
    await render(SettingsKubernetes);
    await byText("授权目标",selected()).trigger("click"); await settle();
    const probe = w!.get(".kube-section .kube-form button");
    expect(probe.attributes("disabled")).toBeDefined();
    await w!.get("#kp-grant-ca").setValue("ga"); await settle();
    await w!.get("#kp-container-ca").setValue("web"); await settle();
    expect(probe.attributes("disabled")).toBeUndefined();
    await probe.trigger("click"); await settle();
    expect(m.probe).not.toHaveBeenCalled();
    await password();
    expect(m.probe).toHaveBeenCalledExactlyOnceWith("ca","ga","web",expect.any(AbortSignal),options());
    await revoke(q=>q.action!=="kubernetes.deploy");
    await probe.trigger("click"); await settle();
    expect(m.probe).toHaveBeenCalledTimes(1);
    expect(w!.find(".kube-section .kube-note").exists()).toBe(false);
  });

  it("blocks the retained credential inspect button once its native dialog was cancelled", async () => {
    await render(KubeCredentials);
    await w!.get('[aria-labelledby="kube-vault-title"] header button').trigger("click");
    await settle();
    const form = formOf("#kc-name");
    const inspect = byText("检查格式").element as HTMLButtonElement;
    form.querySelector<HTMLButtonElement>("footer button[type=button]")!.click();
    inspect.click(); await settle();
    expect(w!.find('dialog[open] input[autocomplete="current-password"]').exists()).toBe(false);
    expect(m.reauth).not.toHaveBeenCalled();
    expect(m.preview).not.toHaveBeenCalled();
  });

  it.each(["edit","delete"] as const)("clicks the actual selected deployment grant %s control with fixed scope", async mode => {
    await render(SettingsKubernetes);
    await byText("授权目标",selected()).trigger("click"); await settle();
    const row = w!.get(".kube-section > .kube-row");
    await byText(mode === "edit" ? "编辑" : "删除",row).trigger("click"); await settle();
    if(mode === "edit") {
      await w!.get("#kg-containers").setValue("web, worker");
      submitFor("#kg-project").click(); await settle();
      expect(m.saveGrant).not.toHaveBeenCalled();
      await password();
      expect(m.saveGrant).toHaveBeenCalledExactlyOnceWith("ca","ga",1,expect.objectContaining({
        projectId:"pa",environment:"dev",containers:["web","worker"],executor:grant.executor,
      }),expect.any(AbortSignal),options());
    } else {
      expect(m.confirm).toHaveBeenCalledTimes(1);
      expect(m.deleteGrant).not.toHaveBeenCalled();
      await password();
      expect(m.deleteGrant).toHaveBeenCalledExactlyOnceWith("ca",grant,expect.any(AbortSignal),options());
    }
  });

  it.each(["deployment","release"] as const)("blocks a withdrawn project from opening password or deleting a retained %s grant", async mode => {
    const confirmation = deferred<boolean>();
    m.confirm.mockReturnValueOnce(confirmation.promise);
    if(mode === "deployment") {
      await render(SettingsKubernetes);
      await byText("授权目标",selected()).trigger("click"); await settle();
    } else {
      await render(KubeReleaseSettings,{connection,projects:[{id:"pa",name:"Project"}]});
    }
    const row = w!.get(mode === "deployment" ? ".kube-section > .kube-row" : ".kube-head:has(.kube-actions)");
    const remove = byText("删除",row);
    await remove.trigger("click"); await settle();
    expect(m.confirm).toHaveBeenCalledTimes(1);
    await revoke(q=>q.action!=="project.read");
    confirmation.resolve(true); await settle();
    await remove.trigger("click"); await settle();
    expect(w!.find('dialog[open] input[autocomplete="current-password"]').exists()).toBe(false);
    expect(m.confirm).toHaveBeenCalledTimes(1);
    expect(m.reauth).not.toHaveBeenCalled();
    expect(m.deleteGrant).not.toHaveBeenCalled();
    expect(m.deleteReleaseGrant).not.toHaveBeenCalled();
  });
});



describe("vault remaining rendered controls", () => {
  it("connects only the current instance administrator through the exact provider control", async () => {
    useSessionStore().setUser({username:"admin",userId:"root",multiTenant:true,instanceAdmin:true});
    m.oauth.mockResolvedValue([{provider:"github",enabled:true,configured:true}]);
    await render(SettingsVault);
    const connect=w!.get(".connect-btn");
    await connect.trigger("click");
    expect(m.authorize).toHaveBeenCalledExactlyOnceWith("github","team-a");
    const element=connect.element;
    useSessionStore().setUser({username:"employee",userId:"u2",multiTenant:true,instanceAdmin:false});
    await settle(); element.dispatchEvent(new MouseEvent("click",{bubbles:true})); await settle();
    expect(m.authorize).toHaveBeenCalledTimes(1);
    expect(w!.find(".connect-btn").exists()).toBe(false);
  });
  it("retries only the current vault load from its actual error banner", async () => {
    m.list.mockRejectedValueOnce(new HttpError(0,null,"fixture"));
    await render(SettingsVault); const retry=w!.get(".banner-retry");
    await retry.trigger("click"); await settle();
    expect(m.list).toHaveBeenCalledTimes(2); expect(m.list).toHaveBeenLastCalledWith(options());
    expect(w!.text()).toContain(cred.name); expect(m.create).not.toHaveBeenCalled();
    usePermissionStore().setTeam("team-b"); await settle();
    const currentCalls=m.list.mock.calls.length;
    expect(m.list).toHaveBeenLastCalledWith(expect.objectContaining({headers:{"X-Pipewright-Team":"team-b"}}));
    retry.element.dispatchEvent(new MouseEvent("click",{bubbles:true})); await settle();
    expect(m.list).toHaveBeenCalledTimes(currentCalls);
  });
  it("opens the empty-state create button only with current credential create permission", async () => {
    m.list.mockResolvedValue([]); await render(SettingsVault);
    const add=w!.get(".empty-state .btn-primary"); await add.trigger("click"); await settle();
    expect(w!.find(".modal-scrim").exists()).toBe(true);
    await w!.get(".modal-close").trigger("click"); await settle();
    await revoke(q=>q.action==="credential.metadata");
    add.element.dispatchEvent(new MouseEvent("click",{bubbles:true})); await settle();
    expect(w!.find(".empty-state .btn-primary").exists()).toBe(false);
    expect(w!.find(".modal-scrim").exists()).toBe(false); expect(m.create).not.toHaveBeenCalled();
  });
  it.each(["header","scrim","escape"])("closes the vault editor via its actual %s control and clears plaintext",async variant=>{
    await render(SettingsVault); await w!.get('button[aria-label="编辑凭据 Allowed credential"]').trigger("click"); await settle();
    const editor=w!.get(".modal-scrim"); await editor.get('input[type="password"]').setValue("DISCARD_SECRET_CANARY");
    if(variant==="header") await editor.get(".modal-close").trigger("click");
    else if(variant==="scrim"){await editor.get(".modal-head").trigger("click");expect(w!.find(".modal-scrim").exists()).toBe(true);await editor.trigger("click");}
    else await editor.trigger("keydown",{key:"Escape"});
    await settle();expect(w!.find(".modal-scrim").exists()).toBe(false); expect(state(VaultContent).form.secret).toBe("");
    expect(state(VaultContent).editRevealed).toBeNull(); expect(m.update).not.toHaveBeenCalled();
  });
  it.each(["footer","scrim","escape"])("cancels vault reconnect via its actual %s control without authorization requests",async variant=>{
    await render(SettingsVault); await w!.get(".cred-row:not(.cred-row--head) .oauth-reconnect").trigger("click"); await settle();
    const dialog=w!.get(".modal-scrim");
    if(variant==="footer")await dialog.get(".modal-footer .btn-secondary").trigger("click");
    else if(variant==="scrim"){await dialog.get(".modal-head").trigger("click");expect(w!.find(".modal-scrim").exists()).toBe(true);await dialog.trigger("click");}
    else await dialog.trigger("keydown",{key:"Escape"});
    await settle(); expect(w!.find(".modal-scrim").exists()).toBe(false); expect(m.reconnect).not.toHaveBeenCalled(); expect(m.authorize).not.toHaveBeenCalled();
  });
});

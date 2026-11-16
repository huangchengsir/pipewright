import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { useSessionStore } from "../../stores/session";
import { usePermissionStore } from "../../stores/permissions";
import { i18n } from "../../i18n";
import Release from "./KubeReleasePanel.vue";
import Legacy from "./KubeLegacyOperationPanel.vue";
import Operation from "./KubeOperationPanel.vue";
import Password from "./KubePassword.vue";
import type { KubeRelease, KubeOperation } from "../../api/kubernetes";
const m = vi.hoisted(() => ({
  decisions: vi.fn(),
  release: vi.fn(),
  releases: vi.fn(),
  operations: vi.fn(),
  observe: vi.fn(),
  releaseAction: vi.fn(),
  rollbackRelease: vi.fn(),
  rollbackRequest: vi.fn(),
  reauth: vi.fn(),
  confirm: vi.fn(),
}));
vi.mock("../../api/access", () => ({ access: { decisions: m.decisions } }));
vi.mock("../../api/kubernetes", async () => ({
  ...(await vi.importActual("../../api/kubernetes")),
  kubeAPI: m,
}));
vi.mock("../../composables/useConfirm", () => ({
  useConfirm: () => ({ open: m.confirm }),
}));
let wrapper: VueWrapper | undefined,
  read = true,
  retry = true;
const release = (): KubeRelease => ({
  canManage: true,
  id: "release-a",
  runId: "run-a",
  nodeKey: "node-a",
  revision: 2,
  state: "paused",
  cleanup: "verified",
  ownerFenced: true,
  cursor: 0,
  createdAt: 1,
  updatedAt: 1,
  preview: { planId: "sealed", mode: "image", resources: [] },
  results: [],
});
const operation = (): KubeOperation => ({
  canManage: true,
  id: "op-a",
  runId: "run-a",
  nodeId: "node-a",
  attempt: 1,
  revision: 1,
  connectionId: "cluster-a",
  projectId: "project-a",
  environment: "test",
  namespace: "secret-namespace",
  deployment: "app",
  container: "app",
  executor: { kind: "ssh", serverId: "server-a" },
  image: "image",
  intentHash: "sealed",
  business: "outcome_unknown",
  cleanup: "verified",
  retired: false,
  reasonCode: "",
  canReconcile: true,
  canRetire: true,
});
async function settle() {
  for (let n = 0; n < 4; n++) await flushPromises();
}
const button = (key: string) =>
  wrapper!.findAll("button").find((b) => b.text() === i18n.global.t(key));
function render(
  kind: "release" | "legacy" | "loader" = "release",
  record = release(),
) {
  wrapper =
    kind === "release"
      ? mount(Release, {
          props: { release: record },
          global: { stubs: { KubePassword: true, RouterLink: true } },
        })
      : kind === "legacy"
        ? mount(Legacy, {
            props: { runId: "run-a", nodeId: "node-a" },
            global: { stubs: { KubePassword: true } },
          })
        : mount(Operation, {
            props: { runId: "run-a", nodeId: "node-a", version: 2 },
            global: {
              stubs: { KubeReleasePanel: true, KubeLegacyOperationPanel: true },
            },
          });
  return wrapper;
}
beforeEach(() => {
 Object.defineProperty(HTMLDialogElement.prototype,"close",{configurable:true,value(){this.removeAttribute("open")}});
 Object.defineProperty(HTMLDialogElement.prototype,"showModal",{configurable:true,value(){this.setAttribute("open","")}});
  setActivePinia(createPinia());
  useSessionStore().setUser({
    username: "employee",
    userId: "u1",
    multiTenant: true,
    instanceAdmin: false,
  });
  usePermissionStore().setTeam("team-a");
  read = true;
  retry = true;
  sessionStorage.clear();
  for (const fn of Object.values(m)) fn.mockReset();
  m.decisions.mockImplementation(
    async (team: string, qs: { action: string }[]) => ({
      userId: "u1",
      teamId: team,
      modeEpoch: 1,
      revision: 1,
      catalogVersion: 1,
      allowed: qs.map((q) => (q.action === "run.read" ? read : retry)),
    }),
  );
  m.release.mockImplementation(async () => release());
  m.releases.mockImplementation(async () => [release()]);
  m.operations.mockImplementation(async () => [operation()]);
  m.confirm.mockResolvedValue(true);
  m.reauth.mockResolvedValue({
    reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
  });
  m.releaseAction.mockResolvedValue({});
  m.observe.mockImplementation(async () => operation());
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  vi.restoreAllMocks();
});
it.each(["release", "legacy", "loader"] as const)(
  "%s sends no request or evidence without read permission",
  async (kind) => {
    read = false;
    render(kind);
    await settle();
    expect(wrapper!.find("section").exists()).toBe(false);
    expect(m.release).not.toHaveBeenCalled();
    expect(m.operations).not.toHaveBeenCalled();
    expect(m.releases).not.toHaveBeenCalled();
  },
);
it.each(["release", "legacy"] as const)(
  "%s hides mutations for reader and server-denied target",
  async (kind) => {
    retry = false;
    render(kind);
    await settle();
    expect(
      button(
        kind === "release"
          ? "kubernetes.release.continue"
          : "kubernetes.reconcile",
      ),
    ).toBeUndefined();
    wrapper!.unmount();
    retry = true;
    usePermissionStore().invalidate();
    m.release.mockResolvedValue({ ...release(), canManage: false });
    m.operations.mockResolvedValue([{ ...operation(), canManage: false }]);
    render(kind, { ...release(), canManage: false });
    await settle();
    expect(
      button(
        kind === "release"
          ? "kubernetes.release.continue"
          : "kubernetes.reconcile",
      ),
    ).toBeUndefined();
  },
);
it.each(["release", "legacy"] as const)(
  "%s fixes read, password and mutation requests to the same team",
  async (kind) => {
    render(kind);
    await settle();
    const load = kind === "release" ? m.release : m.operations;
    expect(load.mock.calls[0]![2]).toMatchObject({
      headers: { "X-Pipewright-Team": "team-a" },
    });
    await button(
      kind === "release"
        ? "kubernetes.release.continue"
        : "kubernetes.reconcile",
    )!.trigger("click");
    await settle();
    const gate = wrapper!.getComponent(Password).props("state");
    gate.password.value = "test-password";
    await gate.verify();
    await settle();
    expect(m.reauth.mock.calls[0]![2]).toMatchObject({
      headers: { "X-Pipewright-Team": "team-a" },
    });
    expect(
      (kind === "release" ? m.releaseAction : m.observe).mock.calls[0]![3],
    ).toMatchObject({ headers: { "X-Pipewright-Team": "team-a" } });
  },
);
it.each(["team", "employee", "permission"] as const)(
  "drops late legacy evidence after %s changes",
  async (change) => {
    let done!: (rows: KubeOperation[]) => void;
    m.operations.mockImplementation(
      () => new Promise<KubeOperation[]>((resolve) => (done = resolve)),
    );
    render("legacy");
    await settle();
    const signal = m.operations.mock.calls[0]![1] as AbortSignal;
    if (change === "team") usePermissionStore().setTeam("team-b");
    else if (change === "employee")
      useSessionStore().setUser({
        username: "other",
        userId: "u2",
        multiTenant: true,
        instanceAdmin: false,
      });
    else {
      read = false;
      usePermissionStore().invalidate();
    }
    await settle();
    expect(signal.aborted).toBe(true);
    done([operation()]);
    await settle();
    expect(wrapper!.text()).not.toContain("secret-namespace");
    expect(m.observe).not.toHaveBeenCalled();
  },
);
it("cancels pending password continuation on action revocation", async () => {
  render();
  await settle();
  await button("kubernetes.release.continue")!.trigger("click");
  const gate = wrapper!.getComponent(Password).props("state");
  expect(gate.passwordOpen.value).toBe(true);
  retry = false;
  usePermissionStore().invalidate();
  await settle();
  expect(gate.passwordOpen.value).toBe(false);
  gate.password.value = "test";
  await gate.verify();
  expect(m.releaseAction).not.toHaveBeenCalled();
  expect(m.reauth).not.toHaveBeenCalled();
});
it("does not continue a confirmed rollback after switching team", async () => {
  let confirm!: (ok: boolean) => void;
  m.confirm.mockImplementation(
    () => new Promise<boolean>((resolve) => (confirm = resolve)),
  );
  render();
  await settle();
  await button("kubernetes.release.rollback")!.trigger("click");
  usePermissionStore().setTeam("team-b");
  confirm(true);
  await settle();
  expect(m.rollbackRelease).not.toHaveBeenCalled();
  expect(m.reauth).not.toHaveBeenCalled();
});
it.each([
  ["continue", "paused", "verified", "kubernetes.release.continue"],
  ["reconcile", "ready", "verified", "kubernetes.reconcile"],
  ["cleanup", "failed", "pending", "kubernetes.release.cleanupTool"],
  ["rollback", "ready", "verified", "kubernetes.release.rollback"],
] as const)(
  "authorizes release %s button through fixed-team reauthentication",
  async (action, state, cleanup, label) => {
    const row = { ...release(), state, cleanup };
    m.release.mockResolvedValue(row);
    m.rollbackRelease.mockResolvedValue({
      runId: "rollback-run",
      release: row,
    });
    render("release", row);
    await settle();
    await button(label)!.trigger("click");
    await settle();
    const gate = wrapper!.getComponent(Password).props("state");
    gate.password.value = "test";
    await gate.verify();
    await settle();
    const call = (action === "rollback" ? m.rollbackRelease : m.releaseAction)
      .mock.calls[0]!;
    expect(call[0]).toMatchObject({ id: "release-a", runId: "run-a" });
    expect(call[3]).toMatchObject({
      headers: { "X-Pipewright-Team": "team-a" },
    });
    if (action !== "rollback") expect(call[1]).toBe(action);
  },
);
it("authorizes retire only after confirmation and reauthentication", async () => {
  render("legacy");
  await settle();
  await button("kubernetes.retire")!.trigger("click");
  await settle();
  expect(m.confirm).toHaveBeenCalledOnce();
  expect(m.observe).not.toHaveBeenCalled();
  const gate = wrapper!.getComponent(Password).props("state");
  gate.password.value = "test";
  await gate.verify();
  await settle();
  expect(m.observe.mock.calls[0]![1]).toBe("retire");
  expect(m.observe.mock.calls[0]![3]).toMatchObject({
    headers: { "X-Pipewright-Team": "team-a" },
  });
});
it.each(["release", "loader"] as const)(
  "discards late %s evidence after employee switch",
  async (kind) => {
    let done!: (value: any) => void;
    const mock = kind === "release" ? m.release : m.releases;
    mock.mockImplementation(() => new Promise((resolve) => (done = resolve)));
    render(kind);
    await settle();
    const signal = mock.mock.calls[0]![1] as AbortSignal;
    useSessionStore().setUser({
      username: "other",
      userId: "u2",
      multiTenant: true,
      instanceAdmin: false,
    });
    await settle();
    expect(signal.aborted).toBe(true);
    done(kind === "release" ? release() : [release()]);
    await settle();
    expect(wrapper!.find("section").exists()).toBe(false);
    expect(wrapper!.find("kube-release-panel-stub").exists()).toBe(false);
  },
);
it("keeps uncertain rollback request private to the employee and team", async () => {
  m.rollbackRelease.mockRejectedValue(new Error("lost reply"));
  render();
  await settle();
  await button("kubernetes.release.rollback")!.trigger("click");
  await settle();
  const gate = wrapper!.getComponent(Password).props("state");
  gate.password.value = "test";
  await gate.verify();
  await settle();
  expect(button("kubernetes.release.findRollback")).toBeDefined();
  expect(sessionStorage.length).toBe(1);
  wrapper!.unmount();
  useSessionStore().setUser({
    username: "other",
    userId: "u2",
    multiTenant: true,
    instanceAdmin: false,
  });
  render();
  await settle();
  expect(button("kubernetes.release.findRollback")).toBeUndefined();
  expect(m.rollbackRequest).not.toHaveBeenCalled();
});
it.each(['release','legacy','loader'] as const)('clicks %s receipt refresh without performing a write',async kind=>{
 if(kind==='loader')m.releases.mockRejectedValueOnce(new Error('read unavailable'));
 render(kind);await settle();const readMock=kind==='release'?m.release:kind==='legacy'?m.operations:m.releases;const before=readMock.mock.calls.length;
 await button('common.refresh')!.trigger('click');await settle();expect(readMock.mock.calls.length).toBe(before+1);expect(m.releaseAction).not.toHaveBeenCalled();expect(m.observe).not.toHaveBeenCalled();
});
it.each(['release','legacy'] as const)('clicks every %s phase tab without any mutation',async kind=>{
 wrapper=kind==='release'?mount(Release,{props:{release:release()},slots:{default:'phase output'},global:{stubs:{KubePassword:true,RouterLink:true}}}):mount(Legacy,{props:{runId:'run-a',nodeId:'node-a'},slots:{default:'phase output'},global:{stubs:{KubePassword:true}}});await settle();
 const tabs=wrapper.findAll('[role="tab"]');expect(tabs.length).toBeGreaterThan(1);for(const tab of tabs){await tab.trigger('click');expect(tab.attributes('aria-selected')).toBe('true')}
 expect(m.releaseAction).not.toHaveBeenCalled();expect(m.observe).not.toHaveBeenCalled();
});

it("checks a lost rollback reply without resubmitting the write", async () => {
  m.rollbackRelease.mockRejectedValue(new Error("lost reply"));
  m.rollbackRequest.mockResolvedValue({ runId: "rollback-run", release: release() });
  render();
  await settle();
  await button("kubernetes.release.rollback")!.trigger("click");
  await settle();
  const gate = wrapper!.getComponent(Password).props("state");
  gate.password.value = "test";
  await gate.verify();
  await settle();

  const originalRequest = m.rollbackRelease.mock.calls[0]![1];
  await button("kubernetes.release.findRollback")!.trigger("click");
  await settle();
  expect(m.rollbackRequest).toHaveBeenCalledOnce();
  expect(m.rollbackRequest.mock.calls[0]![0]).toMatchObject({
    id: "release-a",
    runId: "run-a",
  });
  expect(m.rollbackRequest.mock.calls[0]![1]).toBe(originalRequest);
  expect(m.rollbackRequest.mock.calls[0]![3]).toMatchObject({
    headers: { "X-Pipewright-Team": "team-a" },
  });
  expect(m.rollbackRelease).toHaveBeenCalledOnce();
  expect(sessionStorage.length).toBe(0);
  expect(button("kubernetes.release.findRollback")).toBeUndefined();
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import KubeCredentials from "./KubeCredentials.vue";
import KubeOperationPanel from "./KubeOperationPanel.vue";
import SettingsKubernetes from "../../views/settings/SettingsKubernetes.vue";
import KubernetesJobConfig from "../pipeline/KubernetesJobConfig.vue";
import { HttpError } from "../../api/http";
import {
  type KubeConnection,
  type KubeGrant,
  type KubeOperation,
} from "../../api/kubernetes";
import type { PipelineJob, PipelineStage } from "../../api/pipeline";
const m = vi.hoisted(() => ({
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
  operations: vi.fn(),
  observe: vi.fn(),
  reauth: vi.fn(),
  get: vi.fn(),
  confirm: vi.fn(),
  leave: vi.fn(),
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
vi.mock("../../composables/useConfirm", () => ({
  useConfirm: () => ({ open: m.confirm }),
}));
vi.mock("vue-router", async () => ({
  ...(await vi.importActual<typeof import("vue-router")>("vue-router")),
  onBeforeRouteLeave: (...args: unknown[]) => m.leave(...args),
}));
const c: KubeConnection = {
  id: "connection",
  name: "Test cluster",
  revision: 3,
  credentialId: "kcred_fixture",
  credentialRevision: 2,
  context: "test",
  server: "https://cluster.invalid",
  clusterKey: "opaque",
  profileFingerprint: "profile",
  enabled: true,
  profile: {
    protocol: "kubectl-sh-v1",
    image: "tools@sha256:fixture",
    architecture: "amd64",
    kubectlPath: "/usr/bin/kubectl",
    uid: 1000,
    network: "bridge",
  },
  executors: [
    { kind: "local" },
    { kind: "ssh", serverId: "host", hostKeySHA256: "SHA256:pin" },
  ],
};
const g: KubeGrant = {
  id: "grant",
  connectionId: c.id,
  revision: 2,
  projectId: "project",
  environment: "dev",
  namespace: "apps",
  deployment: "web",
  containers: ["web"],
  executor: { kind: "local" },
  requireApproval: true,
  enabled: true,
};
const operation: KubeOperation = {
  id: "op",
  runId: "source",
  nodeId: "node",
  attempt: 1,
  revision: 7,
  connectionId: c.id,
  projectId: "project",
  environment: "dev",
  namespace: "apps",
  deployment: "web",
  container: "web",
  executor: { kind: "local" },
  image: "registry/web@sha256:fixture",
  intentHash: "hash",
  business: "outcome_unknown",
  cleanup: "verified",
  retired: false,
  reasonCode: "boundary",
  canReconcile: true,
  canRetire: true,
};
let w: VueWrapper | undefined;
function button(text: string) {
  const b = w!.findAll("button").find((b) => b.text() === text);
  if (!b) throw new Error("Missing " + text);
  return b;
}
async function mountView(
  component: Parameters<typeof mount>[0],
  props: Record<string, unknown> = {},
) {
  w = mount(component, {
    attachTo: document.body,
    props,
    global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } },
  });
  await flushPromises();
}
async function password() {
  const dialogs = w!.findAll("dialog[open]");
  const dialog = dialogs.at(-1)!;
  await dialog.get("input[type=password]").setValue("fixture-password");
  await dialog.get("form").trigger("submit");
  await flushPromises();
}
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  for (const f of Object.values(m)) f.mockReset();
  m.credentials.mockResolvedValue([]);
  m.connections.mockResolvedValue([c]);
  m.grants.mockResolvedValue([g]);
  m.operations.mockResolvedValue([operation]);
  m.reauth.mockResolvedValue({
    reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
  });
  m.confirm.mockResolvedValue(true);
  m.get.mockImplementation((url: string) =>
    Promise.resolve(
      url === "/api/servers"
        ? { items: [] }
        : url === "/api/projects"
          ? [{ id: "project", name: "Project" }]
          : [],
    ),
  );
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
  localStorage.clear();
  sessionStorage.clear();
});
describe("Kubernetes management forms", () => {
  it("clears credential secrets on cancel and never saves them to browser storage or reveals them", async () => {
    await mountView(KubeCredentials);
    await button("新增 Kubernetes 凭据").trigger("click");
    await flushPromises();
    await w!.get("#kc-token").setValue("SECRET_CANARY");
    expect(w!.text()).not.toContain("SECRET_CANARY");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    await w!.get("dialog[open] .kube-head button").trigger("click");
    await button("新增 Kubernetes 凭据").trigger("click");
    await flushPromises();
    expect((w!.get("#kc-token").element as HTMLInputElement).value).toBe("");
    expect(m.saveCredential).not.toHaveBeenCalled();
    expect(w!.findAll("button").some((b) => b.text().includes("明文"))).toBe(
      false,
    );
  });
  it("does not repeat a lost credential creation and supports explicit read-back and exit", async () => {
    m.saveCredential.mockRejectedValue(new HttpError(0, null, "lost"));
    await mountView(KubeCredentials);
    await button("新增 Kubernetes 凭据").trigger("click");
    await flushPromises();
    await w!.get("#kc-name").setValue("Cluster");
    await w!.get("#kc-context").setValue("test");
    await w!.get("#kc-server").setValue("https://cluster.invalid");
    await w!.get("#kc-ca").setValue("fixture-ca");
    await w!.get("#kc-token").setValue("SECRET_CANARY");
    await w!.get("dialog[open] form").trigger("submit");
    await flushPromises();
    await password();
    expect(m.saveCredential).toHaveBeenCalledTimes(1);
    expect(button("保存").attributes("disabled")).toBeDefined();
    await button("只读核对").trigger("click");
    await flushPromises();
    expect(m.credentials).toHaveBeenCalledTimes(2);
    expect(m.saveCredential).toHaveBeenCalledTimes(1);
    expect(w!.text()).toContain("同名记录不能证明");
    await button("已核对，结束本次编辑").trigger("click");
    await flushPromises();
    expect(w!.findAll("dialog[open]")).toHaveLength(0);
    expect((w!.get("#kc-token").element as HTMLInputElement).value).toBe("");
  });
  it("requires explicit authorized target and container before readonly probe", async () => {
    await mountView(SettingsKubernetes);
    await button("授权目标").trigger("click");
    await flushPromises();
    expect(m.probe).not.toHaveBeenCalled();
    expect(button("检查连接").attributes("disabled")).toBeDefined();
    await w!.get("#kp-grant-connection").setValue("grant");
    expect(button("检查连接").attributes("disabled")).toBeDefined();
    await w!.get("#kp-container-connection").setValue("web");
    await button("检查连接").trigger("click");
    await flushPromises();
    expect(m.probe).not.toHaveBeenCalled();
    m.probe.mockResolvedValue({ availableReplicas: 1, desiredReplicas: 1 });
    await password();
    expect(m.probe).toHaveBeenCalledWith(
      "connection",
      "grant",
      "web",
      expect.any(AbortSignal),
    );
    expect(w!.text()).toContain("连接检查通过");
  });
  it("does not show a probe form when no exact grant exists", async () => {
    m.grants.mockResolvedValue([]);
    await mountView(SettingsKubernetes);
    await button("授权目标").trigger("click");
    await flushPromises();
    expect(w!.text()).toContain("先添加授权");
    expect(w!.find("#kp-grant-connection").exists()).toBe(false);
    expect(m.probe).not.toHaveBeenCalled();
  });
});
describe("Kubernetes node config and operation evidence", () => {
  it("preserves flat config, selects only explicit ancestor pushes and excludes admin secrets/pins", async () => {
    const push: PipelineStage = {
      id: "push",
      name: "Push",
      kind: "build",
      jobs: [
        {
          id: "image",
          name: "Image",
          type: "push_image",
          summary: "",
          config: {},
        },
      ],
    };
    const job: PipelineJob = {
      id: "deploy",
      name: "Deploy",
      type: "deploy_kubernetes",
      summary: "",
      config: {
        connectionId: c.id,
        credentialId: c.credentialId,
        context: "test",
        timeoutSeconds: "300",
      },
    };
    const stage: PipelineStage = {
      id: "deploy",
      name: "Deploy",
      kind: "deploy",
      needs: ["push"],
      jobs: [job],
    };
    await mountView(KubernetesJobConfig, {
      config: job.config,
      stages: [push, stage],
      stage,
      job,
      projectId: "project",
    });
    expect((w!.get("#kj-source").element as HTMLSelectElement).value).toBe("");
    await w!.get("#kj-target").setValue("grant");
    const update = w!.emitted("update")!.at(-1)![0] as Record<string, string>;
    expect(update.executor).toBe('{"kind":"local"}');
    expect(update.namespace).toBe("apps");
    expect(Object.values(update).every((v) => typeof v === "string")).toBe(
      true,
    );
    expect(update).not.toHaveProperty("hostKeySHA256");
    expect(w!.find("input[type=password]").exists()).toBe(false);
    await w!
      .get("#kj-source")
      .setValue('{"stageId":"push","jobId":"image","declarationIndex":0}');
    expect(
      (w!.emitted("update")!.at(-1)![0] as Record<string, string>)
        .artifactSource,
    ).toContain("push");
  });
  it("keeps unknown separate from success and issues no writes until explicit reconcile plus password", async () => {
    await mountView(KubeOperationPanel, {
      runId: "source",
      nodeId: "node",
      operationId: "op",
    });
    expect(w!.text()).toContain("发布结果待确认");
    expect(w!.text()).toContain("清理已完成");
    expect(w!.emitted("blocked")!.at(-1)).toEqual([true]);
    expect(m.observe).not.toHaveBeenCalled();
    m.observe.mockResolvedValue({
      ...operation,
      business: "ready",
      revision: 8,
    });
    await button("核对集群状态").trigger("click");
    await flushPromises();
    await password();
    expect(m.observe).toHaveBeenCalledWith(
      operation,
      "reconcile",
      expect.any(AbortSignal),
    );
    expect(w!.emitted("blocked")!.at(-1)).toEqual([false]);
    expect(w!.text()).toContain("工作负载已就绪");
  });
  it("never makes a ready workload with cleanup blocked retryable", async () => {
    m.operations.mockResolvedValue([
      { ...operation, business: "ready", cleanup: "blocked", canRetire: false },
    ]);
    await mountView(KubeOperationPanel, { runId: "source", nodeId: "node" });
    expect(w!.text()).toContain("工作负载已就绪");
    expect(w!.text()).toContain("清理受阻");
    expect(w!.emitted("blocked")!.at(-1)).toEqual([true]);
    expect(w!.findAll("button").some((b) => b.text() === "结束本地跟踪")).toBe(
      false,
    );
  });
  it("allows a retry preview after an authoritative empty operation list", async () => {
    m.operations.mockResolvedValue([]);
    await mountView(KubeOperationPanel, { runId: "source", nodeId: "node" });
    expect(w!.emitted("blocked")!.at(-1)).toEqual([false]);
    expect(m.observe).not.toHaveBeenCalled();
  });
  it.each(["pinned", "ambiguous", "error"])(
    "keeps %s missing evidence blocked",
    async (kind) => {
      if (kind === "error")
        m.operations.mockRejectedValue(new Error("unavailable"));
      else
        m.operations.mockResolvedValue(
          kind === "ambiguous"
            ? [operation, { ...operation, id: "second" }]
            : [],
        );
      await mountView(KubeOperationPanel, {
        runId: "source",
        nodeId: "node",
        ...(kind === "pinned" ? { operationId: "op" } : {}),
      });
      expect(w!.emitted("blocked")!.at(-1)).toEqual([true]);
      expect(m.observe).not.toHaveBeenCalled();
    },
  );
  it("ignores an old source response after switching attempts", async () => {
    let resolve!: (rows: KubeOperation[]) => void;
    m.operations
      .mockReturnValueOnce(
        new Promise<KubeOperation[]>((r) => {
          resolve = r;
        }),
      )
      .mockResolvedValueOnce([
        { ...operation, id: "new", runId: "next", business: "failed" },
      ]);
    await mountView(KubeOperationPanel, { runId: "source", nodeId: "node" });
    await w!.setProps({ runId: "next" });
    await flushPromises();
    resolve([{ ...operation, business: "ready" }]);
    await flushPromises();
    expect(w!.text()).toContain("发布未完成");
    expect(w!.text()).not.toContain("工作负载已就绪");
  });
});

describe("Kubernetes unsaved navigation", () => {
  it("yields the native dialog before the existing guard and restores the secret draft when canceled", async () => {
    await mountView(KubeCredentials);
    await button("新增 Kubernetes 凭据").trigger("click");
    await flushPromises();
    await w!.get("#kc-token").setValue("DRAFT_SECRET");
    let resolve!: (value: boolean) => void;
    m.confirm.mockReturnValue(
      new Promise<boolean>((r) => {
        resolve = r;
      }),
    );
    const guard = m.leave.mock.calls.at(-1)![0] as () => Promise<boolean>;
    const navigation = guard();
    await flushPromises();
    expect(w!.findAll("dialog[open]")).toHaveLength(0);
    resolve(false);
    expect(await navigation).toBe(false);
    await flushPromises();
    expect((w!.get("#kc-token").element as HTMLInputElement).value).toBe(
      "DRAFT_SECRET",
    );
    expect(w!.findAll("dialog[open]")).toHaveLength(1);
  });
});

describe("Kubernetes permission failure guidance", () => {
  it.each(["patch_not_authorized", "permission_check_unverified"])(
    "explains %s without submitting a request",
    async (reasonCode) => {
      m.operations.mockResolvedValue([
        { ...operation, business: "failed", reasonCode },
      ]);
      await mountView(KubeOperationPanel, { runId: "source", nodeId: "node" });
      expect(w!.get(".kube-note").text()).toContain(
        "\u672c\u6b21\u672a\u53d1\u9001\u66f4\u65b0",
      );
      expect(m.observe).not.toHaveBeenCalled();
    },
  );
  it.each([
    {
      business: "outcome_unknown",
      cleanup: "verified",
      expected: "\u53d1\u5e03\u53ef\u80fd\u5df2\u751f\u6548",
    },
    {
      business: "failed",
      cleanup: "blocked",
      expected:
        "\u5de5\u5177\u5bb9\u5668\u7684\u6e05\u7406\u5c1a\u672a\u786e\u8ba4",
    },
  ])(
    "keeps unresolved $business/$cleanup guidance above a reason code",
    async (state) => {
      m.operations.mockResolvedValue([
        {
          ...operation,
          ...state,
          reasonCode: "patch_not_authorized",
        },
      ]);
      await mountView(KubeOperationPanel, { runId: "source", nodeId: "node" });
      expect(w!.get(".kube-note").text()).toContain(state.expected);
      expect(w!.get(".kube-note").text()).not.toContain(
        "\u672c\u6b21\u672a\u53d1\u9001\u66f4\u65b0",
      );
      expect(w!.emitted("blocked")!.at(-1)).toEqual([true]);
    },
  );
});

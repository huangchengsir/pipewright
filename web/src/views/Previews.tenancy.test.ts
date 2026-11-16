import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { reactive } from "vue";
import Previews from "./Previews.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
import type { PreviewEnv } from "../api/previewEnvs";
const api = vi.hoisted(() => ({
  list: vi.fn(),
  projects: vi.fn(),
  legacy: vi.fn(),
  reclaim: vi.fn(),
  confirm: vi.fn(),
  decisions: vi.fn(),
  toast: vi.fn(),
}));
let query: Record<string, string>;
vi.mock("vue-router", () => ({
  useRoute: () => ({
    get query() {
      return query;
    },
  }),
  useRouter: () => ({ replace: vi.fn() }),
}));
vi.mock("../api/previewEnvs", () => ({
  listPreviewEnvs: api.list,
  listPreviewProjects: api.projects,
  reclaimPreviewEnv: api.reclaim,
}));
vi.mock("../api/projects", () => ({ listProjects: api.legacy }));
vi.mock("../api/access", () => ({ access: { decisions: api.decisions } }));
vi.mock("../composables/useConfirm", () => ({
  useConfirm: () => ({ open: api.confirm }),
}));
vi.mock("../composables/useToast", () => ({
  useToast: () => ({ success: api.toast, error: api.toast }),
}));
const row = (name: string): PreviewEnv => ({
  id: name,
  projectId: "p",
  pipelineId: "",
  prNumber: 1,
  branch: "main",
  serverId: "s",
  subdomain: name + ".invalid",
  routeId: "r",
  status: "active",
  createdAt: "2026-01-01T00:00:00Z",
  reclaimedAt: "",
});
let wrapper: VueWrapper | undefined;
function render() {
  wrapper = mount(Previews, {
    global: {
      stubs: {
        AppSelect: {
          props: ["options"],
          template:
            '<select><option v-for="p in options">{{p.label}}</option></select>',
        },
      },
    },
  });
  return wrapper;
}
beforeEach(() => {
  vi.resetAllMocks();
  setActivePinia(createPinia());
  query = reactive({ projectId: "p" });
  useSessionStore().setUser({
    username: "alice",
    userId: "alice",
    multiTenant: true,
  });
  usePermissionStore().setTeam("a");
  api.projects.mockResolvedValue([{ id: "p", name: "Allowed" }]);
  api.legacy.mockResolvedValue([{ id: "p", name: "Legacy" }]);
  api.list.mockResolvedValue([row("allowed")]);
  api.reclaim.mockResolvedValue({ ok: true });
  api.confirm.mockResolvedValue(true);
  api.decisions.mockImplementation(
    async (team: string, queries: unknown[]) => ({
      userId: "alice",
      teamId: team,
      modeEpoch: 1,
      revision: 1,
      catalogVersion: 1,
      allowed: queries.map(() => true),
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});
it("shows scoped preview data without querying project configuration, hides global DNS mutation for employees", async () => {
  render();
  await flushPromises();
  expect(api.legacy).not.toHaveBeenCalled();
  expect(api.list.mock.calls[0]![1].headers["X-Pipewright-Team"]).toBe("a");
  expect(wrapper!.text()).toContain("allowed.invalid");
  expect(wrapper!.find(".pcard__reclaim").exists()).toBe(false);
});
it("aborts old-team requests and discards their late preview data and names", async () => {
  let complete!: (rows: PreviewEnv[]) => void;
  api.list.mockImplementationOnce(() => new Promise((r) => (complete = r)));
  render();
  await flushPromises();
  const signal = api.list.mock.calls[0]![1].signal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  expect(signal.aborted).toBe(true);
  expect(api.list.mock.calls.at(-1)![1].headers["X-Pipewright-Team"]).toBe("b");
  complete([row("secret-old")]);
  await flushPromises();
  expect(wrapper!.text()).not.toContain("secret-old");
});
it("clears displayed data after forbidden refresh", async () => {
  render();
  await flushPromises();
  api.list.mockRejectedValue(new HttpError(403, null, "forbidden"));
  await wrapper!.find(".pvb__refresh").trigger("click");
  await flushPromises();
  expect(wrapper!.text()).not.toContain("allowed.invalid");
});
it("does not query with unknown identity or a forced password change", async () => {
  useSessionStore().user = undefined;
  render();
  await flushPromises();
  expect(api.list).not.toHaveBeenCalled();
  useSessionStore().setUser({
    username: "alice",
    userId: "alice",
    multiTenant: true,
    mustChangePassword: true,
  });
  await flushPromises();
  expect(api.projects).not.toHaveBeenCalled();
});
it("clears data on in-place forced-password and leaves no active controls", async () => {
  render();
  await flushPromises();
  useSessionStore().user!.mustChangePassword = true;
  await flushPromises();
  expect(wrapper!.text()).not.toContain("allowed.invalid");
  expect(wrapper!.find(".pcard__reclaim").exists()).toBe(false);
});
it("preserves default-off reclaim and cancels confirmation after identity changes", async () => {
  useSessionStore().setUser({ username: "admin", multiTenant: false });
  let confirm!: (value: boolean) => void;
  api.confirm.mockImplementation(() => new Promise((r) => (confirm = r)));
  render();
  await flushPromises();
  expect(api.legacy).toHaveBeenCalled();
  await wrapper!.find(".pcard__reclaim").trigger("click");
  useSessionStore().clearSession();
  confirm(true);
  await flushPromises();
  expect(api.reclaim).not.toHaveBeenCalled();
});
it("allows explicitly authorized instance admin reclaim with the captured team", async () => {
  useSessionStore().setUser({
    username: "alice",
    userId: "alice",
    multiTenant: true,
    instanceAdmin: true,
  });
  render();
  await flushPromises();
  await wrapper!.find(".pcard__reclaim").trigger("click");
  await flushPromises();
  expect(api.reclaim).toHaveBeenCalledTimes(1);
  expect(api.reclaim.mock.calls[0]![1].headers["X-Pipewright-Team"]).toBe("a");
});

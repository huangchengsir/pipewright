import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { mount, flushPromises, type VueWrapper } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { nextTick } from "vue";
import AuditTimeline from "./AuditTimeline.vue";
import { useSessionStore } from "../stores/session";
import { usePermissionStore } from "../stores/permissions";
import { HttpError } from "../api/http";
import type { AuditListResponse } from "../api/audit";
const api = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock("../api/audit", () => ({ listAudit: api.list }));
let wrapper: VueWrapper | undefined;
const response = (
  id: string,
  nextBefore: string | null = null,
): AuditListResponse => ({
  entries: [
    {
      id,
      action: "project_update",
      actor: "user:alice",
      targetType: "project",
      targetId: id,
      detail: { name: id },
      timestamp: new Date().toISOString(),
      ip: "",
    },
  ],
  nextBefore,
});
beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  useSessionStore().user = {
    username: "alice",
    userId: "alice",
    multiTenant: true,
  };
  usePermissionStore().setTeam("a");
});
afterEach(() => wrapper?.unmount());
it("clears a previous team and ignores its late first-page response", async () => {
  const pending: ((v: AuditListResponse) => void)[] = [];
  api.list.mockImplementation(
    () => new Promise<AuditListResponse>((resolve) => pending.push(resolve)),
  );
  wrapper = mount(AuditTimeline);
  await flushPromises();
  const oldSignal = api.list.mock.calls[0]![1].signal as AbortSignal;
  usePermissionStore().setTeam("b");
  await flushPromises();
  expect(oldSignal.aborted).toBe(true);
  expect(api.list.mock.calls[1]![1].teamId).toBe("b");
  pending[1]!(response("team-b"));
  await flushPromises();
  pending[0]!(response("team-a"));
  await flushPromises();
  expect(wrapper.text()).toContain("team-b");
  expect(wrapper.text()).not.toContain("team-a");
});
it("does not append old-team pagination after a team switch", async () => {
  let finish!: (v: AuditListResponse) => void;
  api.list
    .mockResolvedValueOnce(response("a-first", "cursor-a"))
    .mockImplementationOnce(
      () => new Promise<AuditListResponse>((r) => (finish = r)),
    )
    .mockResolvedValueOnce(response("b-first"));
  wrapper = mount(AuditTimeline);
  await flushPromises();
  await wrapper.get("button.viewall").trigger("click");
  expect(api.list.mock.calls[1]![0].before).toBe("cursor-a");
  usePermissionStore().setTeam("b");
  await nextTick();
  expect(wrapper.text()).not.toContain("a-first");
  await flushPromises();
  finish(response("a-second"));
  await flushPromises();
  expect(wrapper.text()).toContain("b-first");
  expect(wrapper.text()).not.toContain("a-second");
});
it("clears cached rows and rejects late responses on logout", async () => {
  let finish!: (v: AuditListResponse) => void;
  api.list.mockImplementation(
    () => new Promise<AuditListResponse>((r) => (finish = r)),
  );
  wrapper = mount(AuditTimeline);
  await flushPromises();
  useSessionStore().user = null;
  await nextTick();
  finish(response("private-row"));
  await flushPromises();
  expect(wrapper.findAll(".aev")).toHaveLength(0);
  expect(api.list).toHaveBeenCalledTimes(1);
});
it("does not request data without a verified session or selected team", async () => {
  useSessionStore().user = undefined;
  wrapper = mount(AuditTimeline);
  await flushPromises();
  expect(api.list).not.toHaveBeenCalled();
  usePermissionStore().setTeam("");
  useSessionStore().user = { username: "alice", multiTenant: true };
  await flushPromises();
  expect(api.list).not.toHaveBeenCalled();
  api.list.mockResolvedValue(response("ready"));
  usePermissionStore().setTeam("a");
  await flushPromises();
  expect(wrapper.text()).toContain("ready");
});
it("clears previously visible rows when pagination authorization is revoked", async () => {
  api.list
    .mockResolvedValueOnce(response("private", "cursor"))
    .mockRejectedValueOnce(
      new HttpError(403, { code: "forbidden", message: "denied" }, "denied"),
    );
  wrapper = mount(AuditTimeline);
  await flushPromises();
  await wrapper.get("button.viewall").trigger("click");
  await flushPromises();
  expect(wrapper.findAll(".aev")).toHaveLength(0);
  expect(wrapper.find("button.viewall").exists()).toBe(false);
});
it("aborts pending requests on unmount", async () => {
  api.list.mockImplementation(() => new Promise(() => {}));
  wrapper = mount(AuditTimeline);
  await flushPromises();
  const signal = api.list.mock.calls[0]![1].signal as AbortSignal;
  wrapper.unmount();
  wrapper = undefined;
  expect(signal.aborted).toBe(true);
});
it("retries a failed read for the currently selected team", async () => {
  api.list
    .mockRejectedValueOnce(
      new HttpError(
        503,
        { code: "unavailable", message: "temporarily unavailable" },
        "temporarily unavailable",
      ),
    )
    .mockResolvedValueOnce(response("retry-success"));
  wrapper = mount(AuditTimeline);
  await flushPromises();
  await wrapper.get("button.audit-retry").trigger("click");
  await flushPromises();
  expect(api.list).toHaveBeenCalledTimes(2);
  expect(api.list.mock.calls[1]![1].teamId).toBe("a");
  expect(wrapper.text()).toContain("retry-success");
  expect(wrapper.find("button.audit-retry").exists()).toBe(false);
});
it("appends an authorized next page once and removes the completed pagination button", async () => {
  let finish!: (v: AuditListResponse) => void;
  api.list
    .mockResolvedValueOnce(response("first", "cursor"))
    .mockImplementationOnce(
      () => new Promise<AuditListResponse>((r) => (finish = r)),
    );
  wrapper = mount(AuditTimeline);
  await flushPromises();
  await wrapper.get("button.viewall").trigger("click");
  expect(wrapper.get("button.viewall").attributes("disabled")).toBeDefined();
  await wrapper.get("button.viewall").trigger("click");
  expect(api.list).toHaveBeenCalledTimes(2);
  finish(response("second"));
  await flushPromises();
  expect(wrapper.findAll(".aev")).toHaveLength(2);
  expect(wrapper.find("button.viewall").exists()).toBe(false);
});

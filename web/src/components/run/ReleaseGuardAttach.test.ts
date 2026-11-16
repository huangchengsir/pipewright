import {beforeEach as beforeEachGuardSession} from 'vitest'
import {legacyPermissionSession} from '../../test/legacyPermissionSession'
beforeEachGuardSession(legacyPermissionSession)
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ReleaseGuardAttach from "./ReleaseGuardAttach.vue";
import { guardPlan } from "../../test/releaseGuardFixtures";
import { access } from "../../api/access";
import { usePermissionStore } from "../../stores/permissions";
import { useSessionStore } from "../../stores/session";

vi.mock("../../api/access", () => ({ access: { decisions: vi.fn() } }));


const input = {
  artifactId: "image-1",
  serverIds: ["server-1"],
  deployConfig: { containerName: "app" },
  strategy: "rolling" as const,
};
function panel() {
  return mount(ReleaseGuardAttach, {
    props: { projectId: "project-1", runId: "run-1", input },
    global: {
      stubs: {
        ReleaseGuardPlan: { name: "ReleaseGuardPlan", template: "<div />" },
      },
    },
  });
}
function lastState(wrapper: ReturnType<typeof panel>) {
  return wrapper.emitted("change")!.at(-1)![0] as {
    enabled: boolean;
    ready: boolean;
    binding?: unknown;
  };
}

describe("attached release guard consent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    vi.useRealTimers();
  });
  it("is disabled by default and requires a reviewed plan before carrying a binding", async () => {
    const wrapper = panel();
    await flushPromises();
    expect(lastState(wrapper)).toEqual({ enabled: false, ready: false });
    await wrapper.get('input[type="checkbox"]').setValue(true);
    expect(lastState(wrapper)).toEqual({ enabled: true, ready: false });
    await wrapper.get("button").trigger("click");
    wrapper
      .findComponent({ name: "ReleaseGuardPlan" })
      .vm.$emit(
        "prepared",
        guardPlan({ mode: "attached", artifactId: "image-1", attemptId: "" }),
      );
    await flushPromises();
    expect(lastState(wrapper)).toEqual({
      enabled: true,
      ready: true,
      binding: {
        planId: "plan-1",
        planHash: "plan-hash",
        idempotencyKey: expect.any(String),
      },
    });
    wrapper.unmount();
  });
  it("invalidates a reviewed plan when any original deployment setting changes", async () => {
    const wrapper = panel();
    await wrapper.get("input").setValue(true);
    await wrapper.get("button").trigger("click");
    wrapper
      .findComponent({ name: "ReleaseGuardPlan" })
      .vm.$emit(
        "prepared",
        guardPlan({ mode: "attached", artifactId: "image-1", attemptId: "" }),
      );
    await flushPromises();
    await wrapper.setProps({ input: { ...input, strategy: "canary" } });
    expect(lastState(wrapper)).toEqual({ enabled: true, ready: false });
    expect(wrapper.text()).toContain("部署设置已变化");
    wrapper.unmount();
  });
  it("replaces the binding when an already ready plan is reviewed again", async () => {
    const wrapper = panel();
    await wrapper.get("input").setValue(true);
    await wrapper.get("button").trigger("click");
    wrapper
      .findComponent({ name: "ReleaseGuardPlan" })
      .vm.$emit("prepared", guardPlan({ mode: "attached", includeLogs: true }));
    await flushPromises();
    const firstBinding = lastState(wrapper).binding;
    await wrapper.get("button").trigger("click");
    wrapper
      .findComponent({ name: "ReleaseGuardPlan" })
      .vm.$emit(
        "prepared",
        guardPlan({
          id: "plan-2",
          hash: "hash-2",
          mode: "attached",
          includeLogs: false,
          durationSeconds: 300,
        }),
      );
    await flushPromises();
    expect(lastState(wrapper)).toEqual({
      enabled: true,
      ready: true,
      binding: {
        planId: "plan-2",
        planHash: "hash-2",
        idempotencyKey: expect.any(String),
      },
    });
    expect(lastState(wrapper).binding).not.toEqual(firstBinding);
    wrapper.unmount();
  });
});

it("revokes an accepted attachment and ignores the late callback from an earlier review", async () => {
  useSessionStore().setUser({username: "employee", userId: "u", multiTenant: true});
  const permissions = usePermissionStore();
  permissions.setTeam("team-a");
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    teamId: team, userId: "u", modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(q => q.action === "guard.observe" && q.resourceId === "run-1"),
  }));
  const wrapper = panel();
  await flushPromises();
  await wrapper.get("input").setValue(true);
  await wrapper.get("button").trigger("click");
  const prepared = guardPlan({mode: "attached", artifactId: "image-1", attemptId: ""});
  wrapper.findComponent({name: "ReleaseGuardPlan"}).vm.$emit("prepared", prepared);
  await flushPromises();
  expect(lastState(wrapper)).toMatchObject({ready: true});
  await wrapper.get("button").trigger("click");
  const obsolete = wrapper.findComponent({name: "ReleaseGuardPlan"}).vm.$.vnode.props?.onPrepared;
  expect(obsolete).toBeTypeOf("function");
  permissions.reject("team-a", {action: "guard.observe", resourceId: "run-1"});
  obsolete(prepared);
  await flushPromises();
  expect(lastState(wrapper)).toEqual({enabled: false, ready: false});
  expect(wrapper.get("input").attributes("disabled")).toBeDefined();
  expect(wrapper.findComponent({name: "ReleaseGuardPlan"}).exists()).toBe(false);
  wrapper.unmount();
});

it("preserves an accepted plan during an in-flight deployment without accepting a new review", async () => {
  const wrapper = panel();
  await wrapper.get("input").setValue(true);
  await wrapper.get("button").trigger("click");
  wrapper.findComponent({name: "ReleaseGuardPlan"}).vm.$emit("prepared", guardPlan({mode:"attached"}));
  await flushPromises();
  const accepted = lastState(wrapper);
  expect(accepted.ready).toBe(true);
  await wrapper.get("button").trigger("click");
  await wrapper.setProps({disabled:true});
  expect(lastState(wrapper)).toEqual(accepted);
  expect(wrapper.get("input").attributes("disabled")).toBeDefined();
  expect(wrapper.findComponent({name: "ReleaseGuardPlan"}).exists()).toBe(false);
  wrapper.unmount();
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import ReleaseGuardAttach from "./ReleaseGuardAttach.vue";
import { guardPlan } from "../../test/releaseGuardFixtures";

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

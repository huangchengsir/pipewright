import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { useKubeMutation } from "./useKubeMutation";
import { HttpError } from "../api/http";
const reauth = vi.hoisted(() => vi.fn());
vi.mock("../api/kubernetes", async () => ({
  ...(await vi.importActual<typeof import("../api/kubernetes")>(
    "../api/kubernetes",
  )),
  kubeAPI: { reauth },
}));
function deferred<T>() {
  let resolve!: (v: T) => void, reject!: (e: unknown) => void;
  const promise = new Promise<T>((r, j) => {
    resolve = r;
    reject = j;
  });
  return { promise, resolve, reject };
}
let w: VueWrapper | undefined, s: ReturnType<typeof useKubeMutation>;
function setup() {
  w = mount(
    defineComponent({
      setup() {
        s = useKubeMutation();
        return () => h("div");
      },
    }),
  );
}
beforeEach(() => reauth.mockReset());
afterEach(() => w?.unmount());
describe("Kubernetes password and mutation lifecycle", () => {
  it("never executes a new target action after an old password response resolves", async () => {
    setup();
    const old = deferred<{ reauthenticatedUntil: string }>();
    reauth.mockReturnValueOnce(old.promise);
    const a = vi.fn(),
      b = vi.fn();
    s.execute(a);
    s.password.value = "private";
    const pending = s.verify();
    s.reset();
    s.execute(b);
    old.resolve({
      reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
    });
    await pending;
    expect(a).not.toHaveBeenCalled();
    expect(b).not.toHaveBeenCalled();
    expect(s.passwordOpen.value).toBe(true);
    expect(s.error.value).toBe("");
    expect(s.password.value).toBe("");
  });
  it("does not let an old rejected mutation replace a new target busy/error state", async () => {
    setup();
    reauth.mockResolvedValue({
      reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
    });
    const old = deferred<void>(),
      next = deferred<void>();
    s.execute(() => old.promise);
    s.password.value = "private";
    void s.verify();
    await flushPromises();
    s.reset();
    s.execute(() => next.promise);
    expect(s.busy.value).toBe(true);
    old.reject(new HttpError(0, null, "old"));
    await flushPromises();
    expect(s.busy.value).toBe(true);
    expect(s.error.value).toBe("");
    expect(s.uncertain.value).toBe(false);
    next.resolve();
    await flushPromises();
    expect(s.busy.value).toBe(false);
  });
  it("blocks duplicate writes after a lost response until an explicit reset and clears secrets on cancel", async () => {
    setup();
    reauth.mockResolvedValue({
      reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
    });
    const action = vi.fn().mockRejectedValue(new HttpError(0, null, "lost"));
    s.execute(action);
    s.password.value = "private";
    await s.verify();
    s.execute(action);
    await flushPromises();
    expect(action).toHaveBeenCalledTimes(1);
    expect(s.uncertain.value).toBe(true);
    expect(s.password.value).toBe("");
    s.password.value = "other";
    s.cancel();
    expect(s.password.value).toBe("");
    expect(s.uncertain.value).toBe(true);
  });
  it("invalidates pending work and secrets on unmount", async () => {
    setup();
    const wait = deferred<{ reauthenticatedUntil: string }>();
    reauth.mockReturnValue(wait.promise);
    const action = vi.fn();
    s.execute(action);
    s.password.value = "private";
    const promise = s.verify();
    w?.unmount();
    w = undefined;
    wait.resolve({
      reauthenticatedUntil: new Date(Date.now() + 300000).toISOString(),
    });
    await promise;
    expect(action).not.toHaveBeenCalled();
    expect(s.password.value).toBe("");
  });
});

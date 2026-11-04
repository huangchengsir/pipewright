import { beforeEach, describe, expect, it, vi } from "vitest";
import { HttpError } from "./http";
import {
  kubeAPI,
  kubeError,
  kubeUnknownWrite,
  type KubeOperation,
} from "./kubernetes";
const calls = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}));
vi.mock("./http", async () => ({
  ...(await vi.importActual<typeof import("./http")>("./http")),
  http: calls,
}));
beforeEach(() => {
  for (const f of Object.values(calls)) {
    f.mockReset();
    f.mockResolvedValue({});
  }
});
describe("Kubernetes API boundaries", () => {
  it("preserves array lists and sends only exact probe grant and container", async () => {
    calls.get.mockResolvedValue([{ id: "c" }]);
    expect(await kubeAPI.connections()).toEqual([{ id: "c" }]);
    await kubeAPI.probe("c/1", "g", "web");
    expect(calls.post).toHaveBeenCalledWith(
      "/api/kubernetes/connections/c%2F1/probe",
      { grantId: "g", container: "web" },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });
  it("sends CAS on deletion and stored run/operation association on reconciliation", async () => {
    await kubeAPI.deleteCredential({
      id: "credential",
      revision: 7,
    } as Parameters<typeof kubeAPI.deleteCredential>[0]);
    expect(calls.delete).toHaveBeenCalledWith(
      "/api/kubernetes/credentials/credential",
      expect.objectContaining({ body: '{"expectedRevision":7}' }),
    );
    await kubeAPI.observe(
      { id: "operation", runId: "source-run", revision: 12 } as KubeOperation,
      "reconcile",
    );
    expect(calls.post).toHaveBeenLastCalledWith(
      "/api/runs/source-run/kubernetes/operations/operation/reconcile",
      { expectedRevision: 12 },
      expect.any(Object),
    );
  });
  it("never exposes arbitrary server messages and keeps transport outcomes uncertain", () => {
    const e = new HttpError(
      409,
      { code: "kubernetes_revision_conflict", message: "SECRET_CANARY" },
      "SECRET_CANARY",
    );
    expect(kubeError(e, true)).toBe("kubernetes.errors.conflict");
    expect(kubeUnknownWrite(e)).toBe(false);
    expect(kubeError(new Error("SECRET_CANARY"), true)).toBe(
      "kubernetes.errors.uncertain",
    );
    expect(kubeUnknownWrite(new HttpError(0, null, "timeout"))).toBe(true);
  });
  it("propagates cancellation without resubmitting", async () => {
    const c = new AbortController();
    calls.post.mockImplementation(
      (_p, _d, o) =>
        new Promise((_r, reject) =>
          o.signal.addEventListener("abort", () =>
            reject(new Error("aborted")),
          ),
        ),
    );
    const promise = kubeAPI.probe("c", "g", "web", c.signal);
    c.abort();
    await expect(promise).rejects.toThrow();
    expect(calls.post).toHaveBeenCalledTimes(1);
  });
});

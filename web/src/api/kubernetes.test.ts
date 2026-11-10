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
  put: vi.fn(),
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

describe("Kubernetes v2 wire contracts", () => {
  it("keeps exact source identity and stable request ID on rollback and read-only lookup", async () => {
    const row = {
      runId: "source/run",
      id: "release/id",
      revision: 9,
      preview: { planId: "cipher-derived-id" },
    } as Parameters<typeof kubeAPI.rollbackRelease>[0];
    await kubeAPI.rollbackRelease(row, "fixed-request");
    expect(calls.post).toHaveBeenLastCalledWith(
      "/api/runs/source%2Frun/kubernetes/releases/release%2Fid/rollback",
      {
        expectedRevision: 9,
        planId: "cipher-derived-id",
        requestId: "fixed-request",
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
    await kubeAPI.rollbackRequest(row, "fixed-request");
    expect(calls.get).toHaveBeenLastCalledWith(
      "/api/runs/source%2Frun/kubernetes/releases/release%2Fid/rollback-requests/fixed-request",
      expect.any(Object),
    );
    expect(calls.post).toHaveBeenCalledTimes(1);
  });
  it("writes only the independent v2 tool profile using PUT with CAS", async () => {
    const profile = {
      revision: 3,
      enabled: true,
      profile: {
        runtime: { protocol: "kube-gateway-v2" },
        helperPath: "/usr/bin/pipewright-kube-tool",
        helmPath: "/usr/bin/helm",
        helmVersion: "v4.3.1",
      },
    } as Parameters<typeof kubeAPI.saveReleaseProfile>[1];
    await kubeAPI.saveReleaseProfile("connection", profile);
    expect(calls.put).toHaveBeenCalledWith(
      "/api/kubernetes/connections/connection/release-profile",
      { expectedRevision: 3, enabled: true, profile: profile.profile },
      expect.any(Object),
    );
    expect(calls.post).not.toHaveBeenCalled();
  });
});

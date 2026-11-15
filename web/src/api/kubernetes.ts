import { http, HttpError, type HttpOptions } from "./http";
import { kubernetesDiagnosticKeys } from "../utils/kubernetesDiagnostic";

export interface KubeExecutor {
  kind: "local" | "ssh";
  serverId?: string;
  hostKeySHA256?: string;
}
export interface KubeCredentialInput {
  name: string;
  mode: "server_token" | "kubeconfig";
  context: string;
  server?: string;
  ca?: string;
  token?: string;
  tlsServerName?: string;
  kubeconfig?: string;
  enabled: boolean;
}
export interface KubeCredential {
  id: string;
  name: string;
  revision: number;
  enabled: boolean;
  mode: "server_token" | "kubeconfig";
  context: string;
  server: string;
  tlsServerName?: string;
  caFingerprint: string;
  clusterKey: string;
}
export type KubePreview = Omit<
  KubeCredential,
  "id" | "name" | "revision" | "enabled"
>;
export interface KubeProfile {
  protocol: "kubectl-sh-v1";
  image: string;
  architecture: "amd64" | "arm64";
  kubectlPath: string;
  uid: number;
  network: "bridge";
  registryCredentialId?: string;
}
export interface KubeConnectionInput {
  name: string;
  credentialId: string;
  enabled: boolean;
  profile: KubeProfile;
  executors: KubeExecutor[];
}
export interface KubeConnection extends KubeConnectionInput {
  id: string;
  revision: number;
  credentialRevision: number;
  context: string;
  server: string;
  clusterKey: string;
  profileFingerprint: string;
}
export interface KubeGrantInput {
  projectId: string;
  environment: string;
  namespace: string;
  deployment: string;
  containers: string[];
  executor: KubeExecutor;
  requireApproval: boolean;
  enabled: boolean;
}
export interface KubeGrant extends KubeGrantInput {
  /** Server-derived authorization scope; never submitted as input. */
  environmentId?: string;
  id: string;
  connectionId: string;
  revision: number;
}
export type KubeBusiness =
  | "not_dispatched"
  | "submitted"
  | "ready"
  | "unchanged"
  | "failed"
  | "superseded"
  | "outcome_unknown";
export type KubeCleanup = "pending" | "verified" | "blocked";
export interface KubeReceipt {
  uid: string;
  generation: number;
  observedGeneration: number;
  revision: string;
  image: string;
  desiredReplicas: number;
  updatedReplicas: number;
  availableReplicas: number;
  paused: boolean;
  reason?: string;
}
export interface KubeOperation {
  canManage: boolean;
  id: string;
  runId: string;
  nodeId: string;
  attempt: number;
  revision: number;
  connectionId: string;
  projectId: string;
  environment: string;
  namespace: string;
  deployment: string;
  container: string;
  executor: KubeExecutor;
  image: string;
  intentHash: string;
  business: KubeBusiness;
  cleanup: KubeCleanup;
  retired: boolean;
  reasonCode: string;
  canReconcile: boolean;
  canRetire: boolean;
  receipt?: KubeReceipt;
}
const base = "/api/kubernetes";
const id = encodeURIComponent;
/** Bounded requests; cancellation never causes a mutation to be replayed. */
async function call<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  context?: HttpOptions,
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  const timer = setTimeout(abort, method === "GET" ? 20000 : 90000);
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  const scopeAbort = () => controller.abort();
  context?.signal?.addEventListener("abort", scopeAbort, { once: true });
  if (context?.signal?.aborted) scopeAbort();
  const options = {
    ...context,
    signal: controller.signal,
    throwOnUnauthorized: true,
  };
  try {
    if (controller.signal.aborted)
      throw new DOMException("Request cancelled", "AbortError");
    if (method === "GET") return await http.get<T>(path, options);
    if (method === "POST") return await http.post<T>(path, body, options);
    if (method === "PATCH") return await http.patch<T>(path, body, options);
    if (method === "PUT") return await http.put<T>(path, body, options);
    return await http.delete<T>(path, {
      ...options,
      body: JSON.stringify(body),
    });
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
    context?.signal?.removeEventListener("abort", scopeAbort);
  }
}
export interface KubeResourceID {
  group: string;
  version: string;
  resource: string;
  kind: string;
  namespace?: string;
  name: string;
}
export interface KubeReleaseAccess {
  resource: KubeResourceID;
  actions: string[];
  containers?: string[];
  fields?: string[];
}
export interface KubeReleaseProfile {
  connectionId: string;
  revision: number;
  enabled: boolean;
  profile: {
    runtime: Omit<KubeProfile, "protocol"> & { protocol: "kube-gateway-v2" };
    helperPath: string;
    helmPath: string;
    helmVersion: "v4.3.1";
  };
}
export interface KubeReleaseGrantInput {
  projectId: string;
  environment: string;
  executor: KubeExecutor;
  resources: KubeReleaseAccess[];
  requireApproval: boolean;
  enabled: boolean;
}
export interface KubeReleaseGrant extends KubeReleaseGrantInput {
  id: string;
  revision: number;
  connectionId: string;
}
export interface KubeReleaseHealth {
  state: string;
  reason: string;
  desired?: number;
  available?: number;
  containers?: {
    pod?: string;
    name: string;
    ready: boolean;
    reason?: string;
    restarts?: number;
  }[];
}
export interface KubeRelease {
  canManage: boolean;
  id: string;
  runId: string;
  nodeKey: string;
  revision: number;
  state: string;
  cleanup: KubeCleanup;
  ownerFenced: boolean;
  cursor: number;
  reasonCode?: string;
  createdAt: number;
  updatedAt: number;
  preview: {
    changes?: {
      resource: KubeResourceID;
      action: string;
      health: string;
      images?: { container: string; image: string }[];
    }[];
    planId: string;
    mode: string;
    rollbackOf?: string;
    resources: KubeResourceID[];
    deletes?: KubeResourceID[];
    retained?: { resource: KubeResourceID; reason: string }[];
    batchReplicas?: number[];
    helmRelease?: string;
    helmRevision?: number;
    coreDNSCanary?: string;
  };
  results: {
    resource: KubeResourceID;
    actionId?: string;
    state: string;
    health: KubeReleaseHealth;
    deleted?: boolean;
    observedAt?: number;
  }[];
}
const connectionRelease = (key: string) => base + "/connections/" + id(key);
const releasePath = (row: Pick<KubeRelease, "runId" | "id">) =>
  "/api/runs/" + id(row.runId) + "/kubernetes/releases/" + id(row.id);
export const kubeAPI = {
  releaseProfile: (key: string, signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeReleaseProfile>(
      "GET",
      connectionRelease(key) + "/release-profile",
      undefined,
      signal,
      context,
    ),
  saveReleaseProfile: (
    key: string,
    profile: KubeReleaseProfile,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeReleaseProfile>(
      "PUT",
      connectionRelease(key) + "/release-profile",
      {
        expectedRevision: profile.revision,
        enabled: profile.enabled,
        profile: profile.profile,
      },
      signal,
      context,
    ),
  releaseGrants: (key: string, signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeReleaseGrant[]>(
      "GET",
      connectionRelease(key) + "/release-grants",
      undefined,
      signal,
      context,
    ),
  saveReleaseGrant: (
    key: string,
    row: KubeReleaseGrantInput,
    grantId?: string,
    revision = 0,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeReleaseGrant>(
      grantId ? "PATCH" : "POST",
      connectionRelease(key) +
        "/release-grants" +
        (grantId ? "/" + id(grantId) : ""),
      { ...row, expectedRevision: revision },
      signal,
      context,
    ),
  deleteReleaseGrant: (
    key: string,
    row: KubeReleaseGrant,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call(
      "DELETE",
      connectionRelease(key) + "/release-grants/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
      context,
    ),
  releaseOptions: (
    key: string,
    input: { projectId: string; environment: string; executor: KubeExecutor },
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<{
      version: 2;
      resources: KubeReleaseAccess[];
      profile: KubeReleaseProfile;
      requireApproval: boolean;
    }>(
      "POST",
      connectionRelease(key) + "/release-options",
      input,
      signal,
      context,
    ),
  releaseCapabilities: (
    key: string,
    input: { projectId: string; environment: string; executor: KubeExecutor },
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<
      {
        resource: {
          group: string;
          version: string;
          kind: string;
          name: string;
          namespaced: boolean;
        };
        adapter: {
          health: string;
          implemented: boolean;
          clusterMaintenance: boolean;
        };
        authorized: boolean;
      }[]
    >(
      "POST",
      connectionRelease(key) + "/release-capabilities",
      input,
      signal,
      context,
    ),
  releases: (run: string, signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeRelease[]>(
      "GET",
      "/api/runs/" + id(run) + "/kubernetes/releases",
      undefined,
      signal,
      context,
    ),
  release: (
    row: Pick<KubeRelease, "runId" | "id">,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) => call<KubeRelease>("GET", releasePath(row), undefined, signal, context),
  releaseAction: (
    row: KubeRelease,
    action: "continue" | "reconcile" | "cleanup",
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<unknown>(
      "POST",
      releasePath(row) + "/" + action,
      { expectedRevision: row.revision, planId: row.preview.planId },
      signal,
      context,
    ),
  rollbackRequest: (
    row: KubeRelease,
    requestId: string,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<{ runId: string; release: KubeRelease }>(
      "GET",
      releasePath(row) + "/rollback-requests/" + id(requestId),
      undefined,
      signal,
      context,
    ),
  rollbackRelease: (
    row: KubeRelease,
    requestId: string,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<{ runId: string; release: KubeRelease }>(
      "POST",
      releasePath(row) + "/rollback",
      { expectedRevision: row.revision, planId: row.preview.planId, requestId },
      signal,
      context,
    ),
  credentials: (signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeCredential[]>(
      "GET",
      base + "/credentials",
      undefined,
      signal,
      context,
    ),
  preview: (
    input: KubeCredentialInput & { credentialId?: string },
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubePreview>(
      "POST",
      base + "/credentials/preview",
      input,
      signal,
      context,
    ),
  saveCredential: (
    key: string | undefined,
    revision: number,
    input: KubeCredentialInput,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeCredential>(
      key ? "PATCH" : "POST",
      base + "/credentials" + (key ? "/" + id(key) : ""),
      { ...input, expectedRevision: revision },
      signal,
      context,
    ),
  enableCredential: (
    row: KubeCredential,
    enabled: boolean,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeCredential>(
      "POST",
      base + "/credentials/" + id(row.id) + "/enabled",
      { expectedRevision: row.revision, enabled },
      signal,
      context,
    ),
  deleteCredential: (
    row: KubeCredential,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call(
      "DELETE",
      base + "/credentials/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
      context,
    ),
  connections: (signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeConnection[]>(
      "GET",
      base + "/connections",
      undefined,
      signal,
      context,
    ),
  saveConnection: (
    key: string | undefined,
    revision: number,
    input: KubeConnectionInput,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeConnection>(
      key ? "PATCH" : "POST",
      base + "/connections" + (key ? "/" + id(key) : ""),
      { ...input, expectedRevision: revision },
      signal,
      context,
    ),
  deleteConnection: (
    row: KubeConnection,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call(
      "DELETE",
      base + "/connections/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
      context,
    ),
  grants: (connection: string, signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeGrant[]>(
      "GET",
      base + "/connections/" + id(connection) + "/grants",
      undefined,
      signal,
      context,
    ),
  saveGrant: (
    connection: string,
    key: string | undefined,
    revision: number,
    input: KubeGrantInput,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeGrant>(
      key ? "PATCH" : "POST",
      base +
        "/connections/" +
        id(connection) +
        "/grants" +
        (key ? "/" + id(key) : ""),
      { ...input, expectedRevision: revision },
      signal,
      context,
    ),
  deleteGrant: (
    connection: string,
    row: KubeGrant,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call(
      "DELETE",
      base + "/connections/" + id(connection) + "/grants/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
      context,
    ),
  probe: (
    connection: string,
    grantId: string,
    container: string,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeReceipt>(
      "POST",
      base + "/connections/" + id(connection) + "/probe",
      { grantId, container },
      signal,
      context,
    ),
  operations: (run: string, signal?: AbortSignal, context?: HttpOptions) =>
    call<KubeOperation[]>(
      "GET",
      "/api/runs/" + id(run) + "/kubernetes/operations",
      undefined,
      signal,
      context,
    ),
  observe: (
    row: KubeOperation,
    action: "reconcile" | "retire",
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<KubeOperation>(
      "POST",
      "/api/runs/" +
        id(row.runId) +
        "/kubernetes/operations/" +
        id(row.id) +
        "/" +
        action,
      { expectedRevision: row.revision },
      signal,
      context,
    ),
  reauth: (
    currentPassword: string,
    signal?: AbortSignal,
    context?: HttpOptions,
  ) =>
    call<{ reauthenticatedUntil: string }>(
      "POST",
      "/api/account/reauth",
      { currentPassword },
      signal,
      context,
    ),
};
const errors: Record<string, string> = {
  kubernetes_invalid_input: "invalid",
  kubernetes_release_invalid: "invalid",
  kubernetes_session_invalid: "session",
  recent_password_required: "passwordRequired",
  kubernetes_not_authorized: "denied",
  kubernetes_not_found: "missing",
  kubernetes_revision_conflict: "conflict",
  kubernetes_in_use: "inUse",
  kubernetes_workload_held: "held",
  kubernetes_owner_unconfirmed: "ownerUnknown",
  secret_runtime_unverified: "runtime",
  kubernetes_credential_unavailable: "credentialUnavailable",
  kubernetes_observation_interrupted: "observation",
  kubernetes_unavailable: "unavailable",
  invalid_current_password: "passwordInvalid",
  locked_out: "passwordInvalid",
};
export function kubeError(error: unknown, mutation = false): string {
  if (
    error instanceof HttpError &&
    error.apiError?.code &&
    kubernetesDiagnosticKeys[error.apiError.code]
  )
    return kubernetesDiagnosticKeys[error.apiError.code];
  if (
    error instanceof HttpError &&
    error.apiError?.code &&
    errors[error.apiError.code]
  )
    return "kubernetes.errors." + errors[error.apiError.code];
  return "kubernetes.errors." + (mutation ? "uncertain" : "load");
}
export function kubeUnknownWrite(error: unknown): boolean {
  return (
    !(error instanceof HttpError) ||
    error.status === 0 ||
    error.status === 408 ||
    error.status >= 500
  );
}

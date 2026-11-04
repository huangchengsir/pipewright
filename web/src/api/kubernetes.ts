import { http, HttpError } from "./http";

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
  method: "GET" | "POST" | "PATCH" | "DELETE",
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  const timer = setTimeout(abort, method === "GET" ? 20000 : 90000);
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  const options = { signal: controller.signal, throwOnUnauthorized: true };
  try {
    if (method === "GET") return await http.get<T>(path, options);
    if (method === "POST") return await http.post<T>(path, body, options);
    if (method === "PATCH") return await http.patch<T>(path, body, options);
    return await http.delete<T>(path, {
      ...options,
      body: JSON.stringify(body),
    });
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}
export const kubeAPI = {
  credentials: (signal?: AbortSignal) =>
    call<KubeCredential[]>("GET", base + "/credentials", undefined, signal),
  preview: (input: KubeCredentialInput, signal?: AbortSignal) =>
    call<KubePreview>("POST", base + "/credentials/preview", input, signal),
  saveCredential: (
    key: string | undefined,
    revision: number,
    input: KubeCredentialInput,
    signal?: AbortSignal,
  ) =>
    call<KubeCredential>(
      key ? "PATCH" : "POST",
      base + "/credentials" + (key ? "/" + id(key) : ""),
      { ...input, expectedRevision: revision },
      signal,
    ),
  enableCredential: (
    row: KubeCredential,
    enabled: boolean,
    signal?: AbortSignal,
  ) =>
    call<KubeCredential>(
      "POST",
      base + "/credentials/" + id(row.id) + "/enabled",
      { expectedRevision: row.revision, enabled },
      signal,
    ),
  deleteCredential: (row: KubeCredential, signal?: AbortSignal) =>
    call(
      "DELETE",
      base + "/credentials/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
    ),
  connections: (signal?: AbortSignal) =>
    call<KubeConnection[]>("GET", base + "/connections", undefined, signal),
  saveConnection: (
    key: string | undefined,
    revision: number,
    input: KubeConnectionInput,
    signal?: AbortSignal,
  ) =>
    call<KubeConnection>(
      key ? "PATCH" : "POST",
      base + "/connections" + (key ? "/" + id(key) : ""),
      { ...input, expectedRevision: revision },
      signal,
    ),
  deleteConnection: (row: KubeConnection, signal?: AbortSignal) =>
    call(
      "DELETE",
      base + "/connections/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
    ),
  grants: (connection: string, signal?: AbortSignal) =>
    call<KubeGrant[]>(
      "GET",
      base + "/connections/" + id(connection) + "/grants",
      undefined,
      signal,
    ),
  saveGrant: (
    connection: string,
    key: string | undefined,
    revision: number,
    input: KubeGrantInput,
    signal?: AbortSignal,
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
    ),
  deleteGrant: (connection: string, row: KubeGrant, signal?: AbortSignal) =>
    call(
      "DELETE",
      base + "/connections/" + id(connection) + "/grants/" + id(row.id),
      { expectedRevision: row.revision },
      signal,
    ),
  probe: (
    connection: string,
    grantId: string,
    container: string,
    signal?: AbortSignal,
  ) =>
    call<KubeReceipt>(
      "POST",
      base + "/connections/" + id(connection) + "/probe",
      { grantId, container },
      signal,
    ),
  operations: (run: string, signal?: AbortSignal) =>
    call<KubeOperation[]>(
      "GET",
      "/api/runs/" + id(run) + "/kubernetes/operations",
      undefined,
      signal,
    ),
  observe: (
    row: KubeOperation,
    action: "reconcile" | "retire",
    signal?: AbortSignal,
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
    ),
  reauth: (currentPassword: string, signal?: AbortSignal) =>
    call<{ reauthenticatedUntil: string }>(
      "POST",
      "/api/account/reauth",
      { currentPassword },
      signal,
    ),
};
const errors: Record<string, string> = {
  kubernetes_invalid_input: "invalid",
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
    errors[error.apiError.code]
  )
    return "kubernetes.errors." + errors[error.apiError.code];
  return "kubernetes.errors." + (mutation ? "uncertain" : "load");
}
export function kubeUnknownWrite(error: unknown): boolean {
  return (
    !(error instanceof HttpError) || error.status === 0 || error.status >= 500
  );
}

/**
 * Container management API client.
 *
 * GET /api/servers/containers       → { items: ServerContainers[] }  (batch aggregate; read-only)
 * GET /api/servers/:id/containers   → ServerContainers               (single host; read-only)
 *
 * Containers are collected over SSH by running `docker ps -a --format {{json .}}`
 * on each registered host — the target machine stays agentless (no Docker API
 * socket exposed). Per-host fault isolation: an unreachable host or a host with
 * no container runtime never 500s and never affects the others.
 *
 * Container LIFECYCLE (start/stop/restart/pause/unpause/kill/rm) reuses the
 * existing `serviceAction(id, { type: 'docker', ... })` endpoint in `servers.ts`
 * — see ServiceAction there. Interactive container terminals reuse
 * `openContainerTerminal` (WebSocket). Container logs reuse the server logs
 * endpoint with `source: 'docker'`.
 */

import { http } from './http'

/** One container as reported by `docker ps -a`. */
export interface ContainerInfo {
  /** Full container ID (the UI shortens it for display). */
  id: string
  /** Primary name (leading `/` already stripped). */
  names: string
  /** Image reference, e.g. `nginx:latest`. */
  image: string
  /** Normalised lifecycle state. */
  state: ContainerState
  /** Human status line, e.g. `Up 2 hours` / `Exited (0) 3 days ago`. */
  status: string
  /** Raw port-mapping string, e.g. `0.0.0.0:80->80/tcp`. */
  ports: string
  /** Raw creation timestamp from the runtime. */
  createdAt: string
}

export type ContainerState =
  | 'running'
  | 'exited'
  | 'paused'
  | 'created'
  | 'restarting'
  | 'dead'
  | 'unknown'

/** Containers for one server, plus reachability + runtime detection. */
export interface ServerContainers {
  serverId: string
  /** False when the host is unreachable / auth failed. */
  reachable: boolean
  /** Detected runtime (`docker`) or empty string when none was found. */
  runtime: string
  /** Human-readable error when reachable is false or no runtime was found. Never a secret. */
  error: string
  containers: ContainerInfo[]
  /** Count of containers in the `running` state. */
  running: number
  /** Total containers (all states). */
  total: number
  collectedAt: string
}

/** Fetch the container inventory for every registered server (parallel, fault-isolated). */
export async function getAllContainers(): Promise<ServerContainers[]> {
  const res = await http.get<{ items: ServerContainers[] }>('/api/servers/containers')
  return res.items ?? []
}

/** Fetch the container inventory for a single server. */
export async function getServerContainers(id: string): Promise<ServerContainers> {
  return http.get<ServerContainers>(`/api/servers/${id}/containers`)
}

/** Restart policy for a new container (docker `--restart`). */
export type RestartPolicy = 'no' | 'always' | 'unless-stopped' | 'on-failure'

/**
 * Spec for creating (and running) a new container — maps to `docker run -d`.
 * Every field is strictly validated server-side (AC-SEC-02) and the command is
 * built as an argv array, never a shell string.
 */
export interface CreateContainerInput {
  /** Image reference, e.g. `nginx:latest`. Required. */
  image: string
  name?: string
  /** Port mappings, e.g. `["8080:80", "127.0.0.1:9090:90/tcp"]`. */
  ports?: string[]
  /** Env vars as `KEY=VALUE`. */
  env?: string[]
  /** Volume mounts, e.g. `["/host:/ctr", "/host:/ctr:ro", "vol:/ctr"]`. */
  volumes?: string[]
  restart?: RestartPolicy
  /** Optional container command; split on whitespace into args (no shell). */
  command?: string
}

export interface CreateContainerResult {
  serverId: string
  ok: boolean
  /** Full container ID on success. */
  containerId: string
  /** Human-readable error when ok is false. Never a secret. */
  error: string
}

/** Create and run a new container on a server (docker run -d). */
export async function createContainer(
  serverId: string,
  input: CreateContainerInput,
): Promise<CreateContainerResult> {
  return http.post<CreateContainerResult>(`/api/servers/${serverId}/containers`, input)
}

// ─── Images ──────────────────────────────────────────────────────────────────

/** One image as reported by `docker images`. */
export interface ImageInfo {
  id: string
  /** `<none>` for dangling images. */
  repository: string
  tag: string
  /** Human-readable size, e.g. `142MB`. */
  size: string
  /** Human-readable age, e.g. `3 weeks ago`. */
  createdSince: string
}

export interface ServerImages {
  serverId: string
  reachable: boolean
  runtime: string
  error: string
  images: ImageInfo[]
  collectedAt: string
}

export interface ImageActionResult {
  serverId: string
  action: string
  image: string
  ok: boolean
  output: string
  error: string
}

/** List images on a server (docker images). */
export async function getServerImages(id: string): Promise<ServerImages> {
  return http.get<ServerImages>(`/api/servers/${id}/images`)
}

/** Pull an image onto a server (docker pull). */
export async function pullImage(id: string, image: string): Promise<ImageActionResult> {
  return http.post<ImageActionResult>(`/api/servers/${id}/images/pull`, { image })
}

/** Remove an image from a server (docker rmi). */
export async function removeImage(id: string, image: string, force = false): Promise<ImageActionResult> {
  return http.post<ImageActionResult>(`/api/servers/${id}/images/remove`, { image, force })
}

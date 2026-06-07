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

// ─── Compose / Stacks ────────────────────────────────────────────────────────

/** One compose project as reported by `docker compose ls`. */
export interface StackInfo {
  name: string
  /** e.g. `running(2)` / `exited(1)`. */
  status: string
  /** Path(s) to the compose file(s). */
  configFiles: string
}

export interface ServerStacks {
  serverId: string
  reachable: boolean
  runtime: string
  error: string
  stacks: StackInfo[]
  collectedAt: string
}

export type StackAction = 'start' | 'stop' | 'restart' | 'down' | 'update'

export interface StackActionResult {
  serverId: string
  name: string
  action: string
  ok: boolean
  output: string
  error: string
}

/** List compose projects on a server (docker compose ls). */
export async function getServerStacks(id: string): Promise<ServerStacks> {
  return http.get<ServerStacks>(`/api/servers/${id}/stacks`)
}

// ─── AI 诊断 / 看日志 ─────────────────────────────────────────────────────────

export interface DiagnosisEvidence {
  line: number
  text: string
  highlight: boolean
}

/** AI 容器诊断结果。status≠ready 时只有 reason 有意义。 */
export interface ContainerDiagnosis {
  status: 'ready' | 'unavailable' | 'pending'
  reason: string
  hypothesis: string
  confidence: 'high' | 'medium' | 'low' | ''
  alternateCauses: string[]
  fixSuggestions: string[]
  /** 可直接粘贴的修复脚本/补丁片段;空串表示模型未给。 */
  fixScript: string
  evidence: DiagnosisEvidence[]
  generatedAt: string
}

/**
 * 让 AI 分析/诊断一个容器:取其最近日志 → 根因假说 + 修复建议 + 修复脚本。
 * AI 未配置 / 失败 → status=unavailable + reason(不报错)。
 */
export async function diagnoseContainer(serverId: string, name: string): Promise<ContainerDiagnosis> {
  return http.post<ContainerDiagnosis>(
    `/api/servers/${serverId}/containers/${encodeURIComponent(name)}/diagnose`,
    {},
  )
}

// ─── 数据卷 ───────────────────────────────────────────────────────────────────

export interface VolumeInfo {
  name: string
  driver: string
}
export interface ServerVolumes {
  serverId: string
  reachable: boolean
  runtime: string
  error: string
  volumes: VolumeInfo[]
  collectedAt: string
}
export interface VolNetActionResult {
  serverId: string
  action: string
  name: string
  ok: boolean
  error: string
}

export async function getServerVolumes(id: string): Promise<ServerVolumes> {
  return http.get<ServerVolumes>(`/api/servers/${id}/volumes`)
}
export async function createVolume(id: string, name: string): Promise<VolNetActionResult> {
  return http.post<VolNetActionResult>(`/api/servers/${id}/volumes/create`, { name })
}
export async function removeVolume(id: string, name: string): Promise<VolNetActionResult> {
  return http.post<VolNetActionResult>(`/api/servers/${id}/volumes/remove`, { name })
}

// ─── 网络 ─────────────────────────────────────────────────────────────────────

export interface NetworkInfo {
  id: string
  name: string
  driver: string
  scope: string
}
export interface ServerNetworks {
  serverId: string
  reachable: boolean
  runtime: string
  error: string
  networks: NetworkInfo[]
  collectedAt: string
}

export async function getServerNetworks(id: string): Promise<ServerNetworks> {
  return http.get<ServerNetworks>(`/api/servers/${id}/networks`)
}
export async function createNetwork(id: string, name: string): Promise<VolNetActionResult> {
  return http.post<VolNetActionResult>(`/api/servers/${id}/networks/create`, { name })
}
export async function removeNetwork(id: string, name: string): Promise<VolNetActionResult> {
  return http.post<VolNetActionResult>(`/api/servers/${id}/networks/remove`, { name })
}

/** Deploy a stack from compose yaml (docker compose up -d). */
export async function deployStack(id: string, name: string, compose: string): Promise<StackActionResult> {
  return http.post<StackActionResult>(`/api/servers/${id}/stacks/deploy`, { name, compose })
}

/**
 * Act on an existing compose project (by project name).
 * `update` re-applies the compose file with `up -d --pull always`(拉新镜像 + 重建 = 升级);
 * it needs `configFile`(来自 stack 列表的 configFiles)。其余动作按项目标签操作,无需文件。
 */
export async function stackAction(
  id: string,
  name: string,
  action: StackAction,
  configFile?: string,
): Promise<StackActionResult> {
  return http.post<StackActionResult>(`/api/servers/${id}/stacks/action`, { name, action, configFile })
}

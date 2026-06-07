/**
 * Servers API — aligns to frozen 4.1 contract (FR-14).
 *
 * GET    /api/servers            → { items: Server[] }
 * POST   /api/servers            → Server          (needs CSRF)
 * GET    /api/servers/:id        → Server
 * PUT    /api/servers/:id        → Server          (needs CSRF)
 * DELETE /api/servers/:id        → 204             (needs CSRF)
 * POST   /api/servers/:id/test   → ServerTestResult (needs CSRF)
 *
 * A server binds a SSH credential by reference only (credentialId); the API
 * never returns the private key or password. Connectivity is proven by the
 * test endpoint, which runs a read-only `uname -a` over SSH and reports
 * latency / output, or a human-readable error — never the secret.
 */

import { http } from './http'

export interface Server {
  id: string
  name: string
  host: string
  port: number
  user: string
  /** Reference to a ssh_key credential — never the key material itself. */
  credentialId: string
  /** Redundant display name joined from credentials, for the list UI. */
  credentialName: string
  createdAt: string
  updatedAt: string
}

export interface CreateServerInput {
  name: string
  host: string
  port: number
  user: string
  credentialId: string
}

export interface UpdateServerInput {
  name?: string
  host?: string
  port?: number
  user?: string
  credentialId?: string
}

export interface ServerTestResult {
  ok: boolean
  latencyMs: number
  /** Truncated `uname -a` output on success; empty on failure. */
  output: string
  /** Human-readable error on failure; null on success. Never contains secrets. */
  error: string | null
}

export async function listServers(): Promise<Server[]> {
  const res = await http.get<{ items: Server[] }>('/api/servers')
  return res.items
}

export async function createServer(input: CreateServerInput): Promise<Server> {
  return http.post<Server>('/api/servers', input)
}

export async function updateServer(id: string, input: UpdateServerInput): Promise<Server> {
  return http.put<Server>(`/api/servers/${id}`, input)
}

export async function deleteServer(id: string): Promise<void> {
  return http.delete<void>(`/api/servers/${id}`)
}

export async function testServer(id: string): Promise<ServerTestResult> {
  return http.post<ServerTestResult>(`/api/servers/${id}/test`)
}

// ─── Service logs (Story 6-2, FR-16) ──────────────────────────────────────────
//
// GET /api/servers/:id/logs         → ServerLogsResponse  (history; read-only)
// GET /api/servers/:id/logs/stream  → SSE `logline` (live tail) + `error`
//
// AC-SEC-02: source ∈ {journald|file|docker}; the target is strictly validated
// server-side (file: absolute path, no `..`, no shell metacharacters; journald
// unit `[\w.@-]+`; docker `[\w.-]+`) → 400 invalid_log_target. Commands are built
// as an argv array and never assembled into a shell string. SSH/command failures
// return 200 + a human `error` field instead of 500 — never the secret.

/** Log source kind. */
export type LogSource = 'journald' | 'file' | 'docker'

/** One log line. `ts` is always null in this contract (no per-line timestamp). */
export interface ServerLogLine {
  text: string
  ts: string | null
}

export interface ServerLogsResponse {
  serverId: string
  source: LogSource
  target: string
  lines: ServerLogLine[]
  truncated: boolean
  /** Human-readable error when SSH/command failed; absent on success. */
  error?: string
}

export interface GetServerLogsParams {
  source: LogSource
  target: string
  lines?: number
}

/** Fetch the most recent N lines of a log (history; one-shot). */
export async function getServerLogs(
  id: string,
  params: GetServerLogsParams,
): Promise<ServerLogsResponse> {
  const qs = new URLSearchParams({ source: params.source, target: params.target })
  if (params.lines != null) qs.set('lines', String(params.lines))
  return http.get<ServerLogsResponse>(`/api/servers/${id}/logs?${qs.toString()}`)
}

export interface ServerLogStreamHandlers {
  /** A new live log line arrived. */
  onLine: (line: string) => void
  /** Backend signalled a human-readable stream error (SSH/command failure). */
  onError?: (message: string) => void
  /** EventSource transport error (connection dropped). */
  onTransportError?: (err: Event) => void
}

/**
 * Subscribe to a live `tail -f` style log stream over SSE.
 * Returns a cleanup function; calling it closes the EventSource, which makes the
 * backend tear down the SSH session (no leak). Same-origin EventSource carries
 * the session cookie automatically.
 */
export function subscribeServerLogs(
  id: string,
  params: GetServerLogsParams,
  handlers: ServerLogStreamHandlers,
): () => void {
  const qs = new URLSearchParams({ source: params.source, target: params.target })
  if (params.lines != null) qs.set('lines', String(params.lines))
  const url = `/api/servers/${id}/logs/stream?${qs.toString()}`

  let es: EventSource | null = null
  let closed = false

  function cleanup(): void {
    closed = true
    if (es) {
      es.close()
      es = null
    }
  }

  try {
    es = new EventSource(url)

    es.addEventListener('logline', (ev: MessageEvent) => {
      if (closed) return
      try {
        const data = JSON.parse(ev.data) as { text: string }
        handlers.onLine(data.text)
      } catch {
        // Malformed JSON — ignore.
      }
    })

    es.addEventListener('error', (ev: MessageEvent) => {
      // Note: this fires for the backend-sent `error` event (named), which
      // carries JSON data. The transport `onerror` (below) has no `.data`.
      if (closed) return
      const data = (ev as MessageEvent).data
      if (typeof data === 'string' && data.length > 0) {
        try {
          const parsed = JSON.parse(data) as { error: string }
          handlers.onError?.(parsed.error)
          // code-review P7:后端发的具名 error 事件是终态业务失败(SSH 认证/连接/命令失败),
          // 随后后端关闭连接会触发 EventSource 默认自动重连 → 对永久失败的服务器形成无限 SSH
          // 重拨风暴。主动 cleanup() 关闭 ES 停止重连;用户可手动重开。
          cleanup()
          return
        } catch {
          // fall through to transport-error handling
        }
      }
    })

    es.onerror = (err) => {
      if (closed) return
      handlers.onTransportError?.(err)
      // EventSource auto-reconnects; we leave it to retry unless caller cleans up.
    }
  } catch {
    // EventSource unsupported / invalid URL — surface as transport error once.
    handlers.onTransportError?.(new Event('error'))
  }

  return cleanup
}

// ─── Server-layer metrics (Story 6-1, FR-15) ─────────────────────────────────
//
// GET /api/servers/:id/metrics  → ServerMetrics      (single host; read-only)
// GET /api/servers/metrics      → { items: ServerMetrics[] }  (batch; parallel)
//
// Metrics are collected over SSH by running a FIXED read-only command whitelist
// (`cat /proc/loadavg`/`uptime`, `nproc`/`getconf`, `free -b`, `df -B1 /`/`df -k /`).
// AC-SEC-02: the commands are static argv arrays and never incorporate any user
// input — no injection surface; metrics carry no secrets.
//
// Fault tolerance: an unreachable / auth-failed host returns reachable:false +
// a human `error` (HTTP 200, never 500), and does not affect other hosts. A
// metric whose command is missing or whose output can't be parsed comes back
// null (cross-platform best-effort: Linux first, macOS partial), independently
// of the other metrics.

/** CPU load + core count. Either sub-field may be null when unparseable. */
export interface CpuMetric {
  loadavg1: number | null
  cores: number | null
}

/** Memory used/total in bytes. */
export interface MemoryMetric {
  /** 进程真实占用,**不含**可回收页缓存(htop/node_exporter 同口径,反映内存压力)。 */
  usedBytes: number
  /** total - free,**含**页缓存;与 cgroup 总用量 / 容器面板的「已用」一致(口径之一,不绑定平台)。 */
  usedWithCacheBytes: number
  /** free 的 MemTotal:内核**可用**总量(已扣固件/内核保留)。 */
  totalBytes: number
  /** 物理/分配总量(dmidecode SMBIOS);0 表示采集不到(非 root / 无 dmidecode / 虚拟化未暴露)。 */
  physicalTotalBytes: number
  /** 交换分区 used/total(free 的 Swap 行);swapTotalBytes 为 0 表示未配置 swap。 */
  swapUsedBytes: number
  swapTotalBytes: number
}

/** Disk used/total in bytes for a mount path (root `/`). */
export interface DiskMetric {
  path: string
  usedBytes: number
  totalBytes: number
}

export interface ServerMetrics {
  serverId: string
  /** False when SSH/auth/connect failed; metrics are null and `error` is human-readable. */
  reachable: boolean
  /** Human-readable error when unreachable; empty otherwise. Never contains secrets. */
  error: string
  /** Null when the host is unreachable or CPU collection failed entirely. */
  cpu: CpuMetric | null
  /** Null on hosts without `free` (e.g. macOS) or on parse failure. */
  memory: MemoryMetric | null
  /** Null on parse failure; `df` is cross-platform so usually present. */
  disk: DiskMetric | null
  /** RFC3339 collection timestamp. */
  collectedAt: string
}

/** Fetch resource metrics for a single registered server (one-shot, read-only). */
export async function getServerMetrics(id: string): Promise<ServerMetrics> {
  return http.get<ServerMetrics>(`/api/servers/${id}/metrics`)
}

/** Fetch metrics for all registered servers (collected in parallel, each independent). */
export async function getAllServerMetrics(): Promise<ServerMetrics[]> {
  const res = await http.get<{ items: ServerMetrics[] }>('/api/servers/metrics')
  return res.items
}

// ─── Service operations (Story 6-3, FR-17) ───────────────────────────────────
//
// POST /api/servers/:id/service/action → ServiceActionResult  (needs CSRF; write)
//
// Restart / stop / start a systemd unit or a docker container on a target host
// over SSH. AC-SEC-02: `type` ∈ {systemd|docker}; `target` is strictly validated
// server-side (first char `[\w]` — never `-`, so it can't be parsed as a flag;
// no shell metacharacters: systemd `^[\w][\w.@-]*$`, docker `^[\w][\w.-]*$`);
// `action` ∈ {restart|stop|start}. Illegal input → 400 invalid_service_target.
// The command is built as an argv array (`systemctl <action> <unit>` /
// `docker <action> <name>`) and never assembled into a shell string. SSH/command
// failures return 200 + ok:false + a human `error` instead of 500 — never the
// secret. Success is audited (append-only; detail scrubbed).

/** Service kind the operation targets. */
export type ServiceType = 'systemd' | 'docker'

/**
 * Lifecycle operation. Destructive (restart/stop/kill/rm) — UI should confirm.
 *
 * systemd accepts only restart/stop/start; docker additionally accepts
 * pause/unpause/kill/rm (container management). The server enforces the
 * per-type whitelist — this widened union is shared by both kinds.
 */
export type ServiceAction =
  | 'restart'
  | 'stop'
  | 'start'
  | 'pause'
  | 'unpause'
  | 'kill'
  | 'rm'

export interface ServiceActionInput {
  type: ServiceType
  target: string
  action: ServiceAction
}

export interface ServiceActionResult {
  serverId: string
  type: ServiceType
  target: string
  action: ServiceAction
  /** True when the remote command exited 0. */
  ok: boolean
  /** Truncated stdout from the command; may be empty. */
  output: string
  /** Human-readable error when ok is false; empty on success. Never contains secrets. */
  error: string
}

/** Execute a restart/stop/start against a systemd unit or docker container. */
export async function serviceAction(
  id: string,
  input: ServiceActionInput,
): Promise<ServiceActionResult> {
  return http.post<ServiceActionResult>(`/api/servers/${id}/service/action`, input)
}

// ─── Container interactive terminal (Story 6-4, FR-18) ───────────────────────
//
// GET /api/servers/:id/containers/:containerId/terminal  → WebSocket upgrade
//
// Per the architecture rule, **WebSocket is used ONLY for the interactive
// container terminal**; every other realtime stream uses SSE. The WS handshake
// is a GET that passes through the same session-cookie auth as the rest of /api
// (not logged in → 401, no upgrade); CSRF is exempt for GET, so the server does
// a same-origin (Origin) check instead to prevent cross-site WS hijacking.
//
// AC-SEC-02: the command is `docker exec -it <containerId> <shell>`. The
// containerId is strictly validated server-side (first char `[\w]`, never `-`,
// so it can't be parsed as a flag; no shell metacharacters: `^[\w][\w.-]*$`) and
// the shell is restricted to an enum whitelist. The command is built as an argv
// array and never assembled into a shell string — no injection surface. The SSH
// credential is taken from the vault, used in-process, and never enters a WS
// frame or a log. Opening a terminal is audited (append-only; detail scrubbed).

/** Shells the backend allows entering a container with. */
export type TerminalShell =
  | '/bin/sh'
  | '/bin/bash'
  | '/bin/ash'
  | '/bin/zsh'
  | 'sh'
  | 'bash'

export interface TerminalHandlers {
  /** A chunk of terminal output (stdout+stderr merged) arrived from the container. */
  onData: (chunk: Uint8Array) => void
  /** The socket opened and the remote PTY is live. */
  onOpen?: () => void
  /** The socket closed (remote session ended or transport dropped). `reason` is human-readable. */
  onClose?: (reason: string) => void
}

/** A live container-terminal connection. */
export interface TerminalConnection {
  /** Send raw keystrokes / input bytes to the container stdin. */
  send: (data: string) => void
  /** Notify the backend of a new terminal size so it issues an SSH WindowChange. */
  resize: (cols: number, rows: number) => void
  /** Close the socket; the backend tears down the SSH PTY (no leak). */
  close: () => void
}

/**
 * Open an interactive terminal into a container on a registered server.
 *
 * Same-origin WebSocket carries the session cookie automatically. `shell`
 * defaults server-side to `/bin/sh` when omitted. Returns a connection handle
 * for input / resize / close. Output is delivered as binary chunks via
 * `handlers.onData`.
 */
export function openContainerTerminal(
  serverId: string,
  containerId: string,
  handlers: TerminalHandlers,
  shell?: TerminalShell,
): TerminalConnection {
  const qs = shell ? `?shell=${encodeURIComponent(shell)}` : ''
  return openTerminalWS(
    `/api/servers/${serverId}/containers/${encodeURIComponent(containerId)}/terminal${qs}`,
    handlers,
  )
}

/**
 * Open an interactive **host shell** on a registered server (SSH → login shell, no container).
 *
 * This is the default target of "open the server's terminal". Same-origin WebSocket carries the
 * session cookie. `shell` defaults server-side to `/bin/sh` when omitted.
 */
export function openServerTerminal(
  serverId: string,
  handlers: TerminalHandlers,
  shell?: TerminalShell,
): TerminalConnection {
  const qs = shell ? `?shell=${encodeURIComponent(shell)}` : ''
  return openTerminalWS(`/api/servers/${serverId}/terminal${qs}`, handlers)
}

/** Shared WS wiring for both host-shell and container terminals (path is the only difference). */
function openTerminalWS(path: string, handlers: TerminalHandlers): TerminalConnection {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const url = `${proto}//${location.host}${path}`

  const ws = new WebSocket(url)
  ws.binaryType = 'arraybuffer'
  let closed = false

  ws.onopen = () => {
    handlers.onOpen?.()
  }
  ws.onmessage = (ev: MessageEvent) => {
    if (ev.data instanceof ArrayBuffer) {
      handlers.onData(new Uint8Array(ev.data))
    } else if (typeof ev.data === 'string') {
      handlers.onData(new TextEncoder().encode(ev.data))
    }
  }
  ws.onclose = (ev: CloseEvent) => {
    if (closed) return
    closed = true
    handlers.onClose?.(ev.reason || '终端会话已结束')
  }
  ws.onerror = () => {
    // The browser fires a generic error before close; defer the human message to onclose.
  }

  return {
    send(data: string): void {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize(cols: number, rows: number): void {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close(): void {
      closed = true
      try {
        ws.close()
      } catch {
        // already closing/closed
      }
    },
  }
}

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

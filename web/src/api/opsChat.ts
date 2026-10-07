import { http, HttpError } from './http'
import { resetOpsAuth } from '../utils/opsAuth'
import { currentLocale } from '../i18n'

export type Status =
  | 'queued'
  | 'planning'
  | 'running'
  | 'awaiting_confirmation'
  | 'awaiting_analysis_consent'
  | 'succeeded'
  | 'partial_failed'
  | 'failed'
  | 'interrupted'
  | 'unknown'
export interface Provider {
  provider: string
  model: string
  configHash: string
}
export interface Capabilities {
  available: boolean
  modelAvailable: boolean
  provider: Provider
  limits: Record<string, number>
}
export interface Session {
  id: string
  revision: number
  title: string
  draft: string
  serverIds: string[] | null
  activeRunId: string
  watermark: number
  createdAt: string
  updatedAt: string
}
export interface Run {
  id: string
  sessionId: string
  clientRequestId: string
  status: Status
  cancelRequested: boolean
  createdAt: string
}
export interface ToolArgs {
  container?: string
  unit?: string
  lines?: number
  action?: string
}
export interface Tool {
  toolId: string
  mutation: boolean
  schema: unknown
}
export interface Resources {
  disks:
    | {
        filesystem: string
        mount: string
        totalBytes: number
        usedBytes: number
        availableBytes: number
        usedPercent: number
      }[]
    | null
  memory: {
    totalBytes: number
    usedBytes: number
    availableBytes: number
    swapTotalBytes: number
    swapUsedBytes: number
  } | null
  load: {
    one: number
    five: number
    fifteen: number
    uptimeSeconds: number
  } | null
  unavailable: string[] | null
}
export interface Call {
  callId: string
  runId: string
  sessionId: string
  serverId: string
  serverName: string
  toolId: string
  args: ToolArgs
  object: string
  status: Status
  argsHash: string
  targetHash: string
  collectedAt?: string
  exitCode?: number
  output: string
  error: string
  truncated: boolean
  resources?: Resources
}
export interface BoundCall {
  callId: string
  serverId: string
  toolId: string
  object: string
  argsHash: string
  targetHash: string
}
export interface Confirmation {
  runId: string
  nonce: string
  expiresAt: string
  calls: BoundCall[]
  valid: boolean
}
export interface Entry {
  sessionId: string
  seq: number
  kind: string
  runId?: string
  callId?: string
  text?: string
  status?: Status
  createdAt: string
}
export interface EntryPage {
  entries: Entry[] | null
  cursor: string
  watermark: number
  reset: boolean
}
export interface SessionPage {
  sessions: Session[] | null
  cursor: string
  activeSessionId: string
}
export interface Snapshot {
  session: Session
  runs: Run[] | null
  calls: Call[] | null
  confirmations: Confirmation[] | null
  entries: EntryPage
  watermark: number
}
export interface Preview {
  callIds: string[]
  items: {
    callId: string
    targetAlias: string
    toolId: string
    output: string
    error: string
    status: Status
  }[]
  hash: string
  provider: Provider
}
export interface Turn {
  clientRequestId: string
  revision: number
  text?: string
  toolId?: string
  args?: ToolArgs
}
export interface Patch {
  revision: number
  title?: string
  draft?: string
  serverIds?: string[]
}
export interface Retry {
  clientRequestId: string
  revision: number
  callIds: string[]
}
export interface Analysis extends Retry {
  previewHash: string
  provider: Provider
  consent: boolean
}
const root = '/api/ai/ops'
const path = (id: string): string =>
  `${root}/sessions/${encodeURIComponent(id)}`
export const opsApi = {
  capabilities: () => http.get<Capabilities>(`${root}/capabilities`),
  tools: () => http.get<Tool[]>(`${root}/tools`),
  list: () => http.get<SessionPage>(`${root}/sessions?limit=100`),
  create: (serverIds: string[]) =>
    http.post<Session>(`${root}/sessions`, { title: '', serverIds }),
  snapshot: (id: string) => http.get<Snapshot>(path(id)),
  patch: (id: string, input: Patch) => http.patch<Session>(path(id), input),
  activate: (id: string) =>
    http.post<{ ok: boolean }>(`${path(id)}/activate`, {}),
  delete: (id: string) => http.delete<void>(path(id)),
  entries: (id: string, before: string) =>
    http.get<EntryPage>(
      `${path(id)}/entries?limit=50&before=${encodeURIComponent(before)}`,
    ),
  call: (id: string, callId: string) =>
    http.get<Call>(`${path(id)}/calls/${encodeURIComponent(callId)}`),
  turn: (id: string, input: Turn) => http.post<Run>(`${path(id)}/turns`, input),
  retry: (id: string, input: Retry) =>
    http.post<Run>(`${path(id)}/retry`, input),
  cancel: (id: string, run: string) =>
    http.post<Run>(`${path(id)}/runs/${encodeURIComponent(run)}/cancel`, {}),
  confirm: (id: string, input: Confirmation) =>
    http.post<Run>(`${path(id)}/calls/confirm`, {
      runId: input.runId,
      nonce: input.nonce,
      calls: input.calls,
    }),
  reissue: (id: string, run: string) =>
    http.post<Confirmation>(
      `${path(id)}/runs/${encodeURIComponent(run)}/confirmation`,
      {},
    ),
  preview: (id: string, callIds: string[]) =>
    http.get<Preview>(
      `${path(id)}/analysis/preview?callIds=${encodeURIComponent(callIds.join(','))}`,
    ),
  analyze: (id: string, input: Analysis) =>
    http.post<Run>(`${path(id)}/analysis`, input),
}

export function requestId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6]! & 15) | 64
  bytes[8] = (bytes[8]! & 63) | 128
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
export const utf8Bytes = (text: string): number =>
  new TextEncoder().encode(text).length
export function errorCode(error: unknown): string {
  if (error instanceof HttpError) {
    if (error.status === 401) return 'unauthorized'
    if (error.status === 403) return 'forbidden'
    if (error.status === 0) return 'network'
    const code = error.apiError?.code
    if (code && /^ops_[a-z_]+$/.test(code)) return code
  }
  return 'network'
}
export interface StreamEvent {
  type: string
  id: string
  data: unknown
}
// SSE framing, including split UTF-8 and CRLF chunks. Limit individual frames.
export function sseParser(
  deliver: (event: StreamEvent) => void,
): (chunk: string) => void {
  let buffer = ''
  const maxFrame = 256 * 1024
  return (chunk) => {
    buffer += chunk
    let match: RegExpExecArray | null
    while ((match = /\r?\n\r?\n/.exec(buffer))) {
      const block = buffer.slice(0, match.index)
      buffer = buffer.slice(match.index + match[0].length)
      if (block.length > maxFrame) throw new Error('SSE frame too large')
      let type = 'message',
        id = ''
      const data: string[] = []
      for (const line of block.split(/\r?\n/)) {
        const colon = line.indexOf(':')
        const key = colon < 0 ? line : line.slice(0, colon)
        const value = colon < 0 ? '' : line.slice(colon + 1).replace(/^ /, '')
        if (key === 'event') type = value
        else if (key === 'id' && !value.includes('\0')) id = value
        else if (key === 'data') data.push(value)
      }
      if (data.length)
        deliver({ type, id, data: JSON.parse(data.join('\n')) as unknown })
    }
    if (buffer.length > maxFrame) throw new Error('SSE frame too large')
  }
}
export async function streamOps(
  id: string,
  after: string,
  signal: AbortSignal,
  deliver: (event: StreamEvent) => void,
): Promise<void> {
  const response = await fetch(
    `${path(id)}/events?after=${encodeURIComponent(after)}`,
    {
      credentials: 'same-origin',
      cache: 'no-store',
      signal,
      headers: {
        Accept: 'text/event-stream',
        'X-Pipewright-Locale': currentLocale(),
      },
    },
  )
  if (response.status === 401) resetOpsAuth()
  if (!response.ok)
    throw new HttpError(response.status, null, 'Stream unavailable')
  if (
    !response.body ||
    !response.headers.get('content-type')?.includes('text/event-stream')
  )
    throw new Error('Invalid stream')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  const parse = sseParser(deliver)
  try {
    while (!signal.aborted) {
      const part = await reader.read()
      if (part.done) break
      parse(decoder.decode(part.value, { stream: true }))
    }
    parse(decoder.decode())
  } finally {
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}

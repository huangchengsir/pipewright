/**
 * Runs API — listRuns / getRun / cancelRun + SSE subscription
 *
 * Types strictly follow the frozen run-detail DTO contract (Story 3.1).
 * targets? / diagnosis? are forward-declared optional blocks (Epic 4 / Epic 7);
 * this client does not render their content — only preserves shape.
 */

import { http } from './http'

// ─── Status vocabulary (fixed six-word set; do not alias) ────────────────────

export type RunStatus =
  | 'queued'
  | 'running'
  | 'success'
  | 'failed'
  | 'partial_failed'
  | 'rolled_back'

// ─── Step ────────────────────────────────────────────────────────────────────

export type StepStatus = 'pending' | 'running' | 'success' | 'failed' | 'skipped'

export interface RunStep {
  id: string
  name: string
  status: StepStatus
  startedAt: string | null
  finishedAt: string | null
  durationMs: number | null
}

// ─── Trigger ─────────────────────────────────────────────────────────────────

export interface RunTrigger {
  type: 'webhook' | 'manual'
  branch: string
  commit: string
  actor: string
}

// ─── targets / diagnosis: forward-declared optional blocks (Epic 4 / Epic 7) ─
// Shape is frozen here; Epic 4/7 fills content — do not modify these types.

export interface TargetRun {
  server: string
  status: RunStatus
  steps: RunStep[]
  rolledBackTo: string | null
}

export interface Diagnosis {
  rootCause: string
  confidence: number
  evidence: string[]
  feedback: 'positive' | 'negative' | null
}

// ─── Run Detail (frozen DTO contract — do not change shape) ─────────────────

export interface RunDetail {
  id: string
  projectId: string
  projectName: string
  status: RunStatus
  trigger: RunTrigger
  steps: RunStep[]
  createdAt: string
  startedAt: string | null
  finishedAt: string | null
  durationMs: number | null
  targets: TargetRun[] | null   // Epic 4 fills — slot owner: Story 4.x
  diagnosis: Diagnosis | null   // Epic 7 fills — slot owner: Story 7.x
}

// ─── Run list item (compact; no steps/targets/diagnosis) ────────────────────

export interface RunListItem {
  id: string
  projectId: string
  projectName: string
  status: RunStatus
  trigger: RunTrigger
  createdAt: string
  durationMs: number | null
}

// ─── List response ───────────────────────────────────────────────────────────

export interface RunListResponse {
  items: RunListItem[]
  page: number
  total: number
}

// ─── List params ─────────────────────────────────────────────────────────────

export interface ListRunsParams {
  projectId?: string
  status?: RunStatus
  page?: number
  pageSize?: number
}

// ─── API calls ───────────────────────────────────────────────────────────────

export function listRuns(params: ListRunsParams = {}): Promise<RunListResponse> {
  const qs = new URLSearchParams()
  if (params.projectId) qs.set('projectId', params.projectId)
  if (params.status) qs.set('status', params.status)
  if (params.page !== undefined) qs.set('page', String(params.page))
  if (params.pageSize !== undefined) qs.set('pageSize', String(params.pageSize))
  const query = qs.toString()
  return http.get<RunListResponse>(`/api/runs${query ? `?${query}` : ''}`)
}

export function getRun(id: string): Promise<RunDetail> {
  return http.get<RunDetail>(`/api/runs/${id}`)
}

export function cancelRun(id: string): Promise<RunDetail> {
  return http.post<RunDetail>(`/api/runs/${id}/cancel`)
}

// ─── Manual trigger ───────────────────────────────────────────────────────────
//
// POST /api/projects/{id}/runs  body { branch, commit? }
// Returns the created RunDetail (3-1 frozen DTO shape).
// Caller uses the returned run.id to navigate to /runs/:id.

export interface TriggerManualInput {
  branch: string
  commit?: string
}

export function triggerManual(projectId: string, input: TriggerManualInput): Promise<RunDetail> {
  return http.post<RunDetail>(`/api/projects/${projectId}/runs`, input)
}

// ─── SSE subscription ────────────────────────────────────────────────────────
//
// Listens to GET /api/runs/:id/events (text/event-stream).
// Two named events: "status" (run status update) and "step" (step status update).
// Same-origin EventSource carries session cookie automatically.
// On connection error, falls back to polling getRun (graceful degradation).

export type SseStatusEvent = { runId: string; status: RunStatus }
export type SseStepEvent   = { runId: string; step: RunStep }

export interface SseHandlers {
  onStatus: (e: SseStatusEvent) => void
  onStep:   (e: SseStepEvent) => void
  onError?: (err: Event) => void
}

/** Returns a cleanup function; call it to unsubscribe. */
export function subscribeRunEvents(runId: string, handlers: SseHandlers): () => void {
  const url = `/api/runs/${runId}/events`

  // We try SSE first and fall back to polling if EventSource fails.
  let es: EventSource | null = null
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let closed = false

  function startPolling(): void {
    if (closed || pollTimer !== null) return
    // Poll every 3s as graceful degradation — no error surfaced to caller
    pollTimer = setInterval(() => {
      if (closed) return
      getRun(runId)
        .then((run) => {
          // Synthesize a status event from polled data
          handlers.onStatus({ runId, status: run.status })
          // Synthesize step events
          for (const step of run.steps) {
            handlers.onStep({ runId, step })
          }
        })
        .catch(() => {
          // Polling failure is silent — backend may not be ready yet
        })
    }, 3000)
  }

  function cleanup(): void {
    closed = true
    if (es) {
      es.close()
      es = null
    }
    if (pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  try {
    es = new EventSource(url)

    es.addEventListener('status', (ev: MessageEvent) => {
      if (closed) return
      try {
        const data = JSON.parse(ev.data) as SseStatusEvent
        handlers.onStatus(data)
      } catch {
        // Malformed JSON — ignore
      }
    })

    es.addEventListener('step', (ev: MessageEvent) => {
      if (closed) return
      try {
        const data = JSON.parse(ev.data) as SseStepEvent
        handlers.onStep(data)
      } catch {
        // Malformed JSON — ignore
      }
    })

    es.onerror = (err) => {
      if (closed) return
      // SSE connection dropped — switch to polling silently
      if (es) {
        es.close()
        es = null
      }
      handlers.onError?.(err)
      startPolling()
    }
  } catch {
    // EventSource not supported or URL invalid — fall back immediately
    startPolling()
  }

  return cleanup
}

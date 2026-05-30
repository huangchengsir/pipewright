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

// ─── targets: SSH deploy execution (Story 4-2 frozen contract — FR-10) ───────
// Per-target deploy result. Shape is frozen here; 4-4 rollback sets status to
// 'rolled_back', 4-5 multi-fanout appends rows — neither changes this shape.
// `message` is human-readable (success summary / failure reason) and NEVER
// contains plaintext secrets. `status` is the fixed five-word target set.

export type TargetStatus =
  | 'pending'
  | 'deploying'
  | 'success'
  | 'failed'
  | 'rolled_back'

export interface DeployTarget {
  serverId: string
  serverName: string
  status: TargetStatus
  message: string
  startedAt: string          // RFC3339
  finishedAt: string | null  // RFC3339; null while not finished
}

// ─── DiagnosisDTO (Story 7-2 frozen contract) ────────────────────────────────
// status=ready: full diagnosis available; unavailable: graceful fallback;
// pending: diagnosis in progress.

export interface DiagnosisEvidence {
  line: number
  text: string
  highlight: boolean
}

export type DiagnosisStatus = 'ready' | 'unavailable' | 'pending'
export type DiagnosisConfidence = 'high' | 'medium' | 'low'

export interface DiagnosisDTO {
  status: DiagnosisStatus
  reason: string              // non-empty when status !== 'ready'
  hypothesis: string          // AI root-cause hypothesis (ready only)
  confidence: DiagnosisConfidence
  alternateCauses: string[]   // populated when confidence='low'
  fixSuggestions: string[]
  evidence: DiagnosisEvidence[]
  generatedAt: string         // RFC3339
}

/** @deprecated Use DiagnosisDTO — removed in 7-2 */
export interface Diagnosis {
  rootCause: string
  confidence: number
  evidence: string[]
  feedback: 'positive' | 'negative' | null
}

// ─── Artifact (Story 3-4 frozen contract — FR-6) ─────────────────────────────
// Build-artifact contract is an Epic 3 first-class deliverable: the `type` enum
// (image|jar|dist|archive) and `reference` (type-addressing) are frozen. Epic 4
// deploy consumes by (type, reference) without knowing build internals. Real
// builds (3-3) feed via the same EmitArtifact interface — shape never changes.

export type ArtifactType = 'image' | 'jar' | 'dist' | 'archive'

export interface ArtifactDTO {
  id: string
  type: ArtifactType
  name: string
  reference: string          // type-addressing ref (Epic 4 consumes this)
  sizeBytes: number          // 0 when unknown
  metadata: Record<string, unknown> // free KV (digest/path/stub…)
  createdAt: string          // RFC3339
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
  artifacts: ArtifactDTO[]          // Story 3-4 fills (FR-6) — empty [] when no artifacts
  targets: DeployTarget[] | null    // Story 4-2 fills (FR-10) — deployed ⇒ array, else null
  diagnosis: DiagnosisDTO | null    // Epic 7 fills — slot owner: Story 7.x (Story 7-2 defines shape)
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

// ─── (Re-)diagnose a failed run ───────────────────────────────────────────────
//
// POST /api/runs/{id}/diagnose
// Triggers (or re-triggers) AI diagnosis for the given run.
// Returns the DiagnosisDTO directly (not a full RunDetail).
// 422 if the run is not in failed state; 404 if run not found.
// Any LLM failure yields 200 + status=unavailable — never 500, never secrets.

export function diagnoseRun(id: string): Promise<DiagnosisDTO> {
  return http.post<DiagnosisDTO>(`/api/runs/${id}/diagnose`)
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

// ─── Run logs (Story 3-6 frozen contract) ─────────────────────────────────────
//
// A single log line. `text` is already secret-masked by the backend ([MASKED]
// substrings present) — the frontend never re-processes secrets, only renders.
// `seq` is monotonic per-run (from 1); the client uses it to de-dupe / order /
// resume. `stream` distinguishes stdout vs stderr for coloring. `stepOrdinal`
// associates the line with a step (-1 ⇒ run-level).

export type LogStream = 'stdout' | 'stderr'

export interface RunLogLine {
  seq: number
  ts: string            // RFC3339
  stream: LogStream
  stepOrdinal: number
  text: string          // single line, already masked, no trailing newline
}

// GET /api/runs/{id}/logs?sinceSeq=<int>  → historical / paginated pull (non-SSE)
// Returns lines after `sinceSeq` (ascending). `nextSeq` = last seq + 1 (resume
// cursor); `complete` = run has reached a terminal state (stop polling). 404
// run_not_found if the run does not exist.

export interface RunLogsResponse {
  lines: RunLogLine[]
  nextSeq: number
  complete: boolean
}

export function getRunLogs(id: string, sinceSeq = 0): Promise<RunLogsResponse> {
  const qs = sinceSeq > 0 ? `?sinceSeq=${sinceSeq}` : ''
  return http.get<RunLogsResponse>(`/api/runs/${id}/logs${qs}`)
}

// ─── Run artifacts (Story 3-4 frozen contract — FR-6) ─────────────────────────
//
// GET /api/runs/{id}/artifacts → { artifacts: ArtifactDTO[] }
// Read-only, authenticated. Epic 4 deploy consumes by (type, reference).
// 404 run_not_found if the run does not exist. The run-detail DTO also carries
// the same artifacts under `run.artifacts`; this endpoint is the standalone view.

export interface RunArtifactsResponse {
  artifacts: ArtifactDTO[]
}

export function getRunArtifacts(id: string): Promise<RunArtifactsResponse> {
  return http.get<RunArtifactsResponse>(`/api/runs/${id}/artifacts`)
}

// ─── SSH deploy execution (Story 4-2 frozen contract — FR-10) ─────────────────
//
// POST /api/runs/{id}/deploy  body { artifactId, serverIds, deployConfig? }
// Deploys the given artifact to the selected target servers over SSH (commands
// are array-ized server-side; AC-SEC-02). Synchronous this story: returns the
// final per-target results once execution completes.
//
// 200 with the filled `targets` array. Per-target execution failure is NOT an
// HTTP error — that target carries status='failed' + human message (never 500,
// never secrets). 404 run_not_found; 422 when the run is not successful, the
// artifact is missing, or a target server does not exist.

export interface DeployRunInput {
  artifactId: string
  serverIds: string[]
  deployConfig?: Record<string, string>
}

export interface DeployRunResponse {
  targets: DeployTarget[]
}

export function deployRun(id: string, input: DeployRunInput): Promise<DeployRunResponse> {
  return http.post<DeployRunResponse>(`/api/runs/${id}/deploy`, input)
}

// ─── SSE subscription ────────────────────────────────────────────────────────
//
// Listens to GET /api/runs/:id/events (text/event-stream).
// Named events: "status" (run status update), "step" (step status update) and
// "log" (Story 3-6: live log line). On connect the backend first replays this
// run's full history of `log` events (seq-ascending) so a refresher receives the
// complete log, then transitions to live tailing — the client de-dupes by seq.
// Same-origin EventSource carries session cookie automatically.
// On connection error, falls back to polling getRun (graceful degradation).

export type SseStatusEvent = { runId: string; status: RunStatus }
export type SseStepEvent   = { runId: string; step: RunStep }
// The "log" event payload IS a RunLogLine (no envelope) — matches the frozen
// contract: {"seq","ts","stream","stepOrdinal","text"}.
export type SseLogEvent    = RunLogLine

export interface SseHandlers {
  onStatus: (e: SseStatusEvent) => void
  onStep:   (e: SseStepEvent) => void
  onLog?:   (e: SseLogEvent) => void
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

    es.addEventListener('log', (ev: MessageEvent) => {
      if (closed) return
      try {
        const data = JSON.parse(ev.data) as SseLogEvent
        handlers.onLog?.(data)
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

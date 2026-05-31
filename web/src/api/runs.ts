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
  | 'waiting_approval'
  | 'success'
  | 'failed'
  | 'partial_failed'
  | 'rolled_back'

// ─── Step ────────────────────────────────────────────────────────────────────

export type StepStatus =
  | 'pending'
  | 'running'
  | 'waiting_approval'
  | 'success'
  | 'failed'
  | 'skipped'

// ─── Approval gate (Epic 8 · 8-4) ──────────────────────────────────────────────

export type ApprovalStatus = 'pending' | 'approved' | 'rejected'

export interface ApprovalRecord {
  stageId: string
  stageName: string
  status: ApprovalStatus
  decidedBy: string
  decidedAt: string
  createdAt: string
}

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
  /**
   * Concrete copy-pasteable fix script / patch snippet for the failing step
   * (AI moat). Empty string when the model gave none. Already masked
   * server-side — no secrets reach the client.
   */
  fixScript: string
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

// ─── Approval gate (Epic 8 · 8-4) ──────────────────────────────────────────────

export function listApprovals(runId: string): Promise<{ items: ApprovalRecord[] }> {
  return http.get<{ items: ApprovalRecord[] }>(`/api/runs/${runId}/approvals`)
}

export function approveStage(runId: string, stageId: string): Promise<{ ok: boolean }> {
  return http.post<{ ok: boolean }>(`/api/runs/${runId}/approve`, { stageId })
}

export function rejectStage(runId: string, stageId: string): Promise<{ ok: boolean }> {
  return http.post<{ ok: boolean }>(`/api/runs/${runId}/reject`, { stageId })
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

// ─── Success/fail diff (Story 7-3 frozen contract — FR-25) ────────────────────
//
// GET /api/runs/{id}/diff → file-level diff between the last successful run
// (baseline) and this run (current). Read-only, authenticated.
//
// available=false (no baseline success run / this run has no commit / clone
// failed / commit unreachable) is a graceful degraded response — NEVER a 500.
// `reason` is human-readable. `status` is the frozen four-word file set. Large
// diffs are truncated (truncated=true; backend caps file count). `summary` is a
// human one-liner that may carry a "most suspect" heuristic hint.

export type RunDiffFileStatus = 'added' | 'modified' | 'deleted' | 'renamed'

export interface RunDiffFile {
  path: string
  status: RunDiffFileStatus
  additions: number
  deletions: number
}

export interface RunDiffDTO {
  available: boolean
  reason: string             // human-readable when available=false
  baselineRunId: string      // empty when no baseline
  baselineCommit: string     // empty when no baseline
  currentCommit: string
  files: RunDiffFile[]       // [] when available=false
  truncated: boolean
  summary: string
}

export function getRunDiff(id: string): Promise<RunDiffDTO> {
  return http.get<RunDiffDTO>(`/api/runs/${id}/diff`)
}

// ─── Diagnosis feedback loop (Story 7-5 frozen contract — FR-26) ──────────────
//
// POST /api/runs/{id}/diagnosis/feedback  body { verdict, correctRootCause? }
// Submit 👍/👎 feedback on a ready diagnosis. `correctRootCause` only meaningful
// for verdict='down' (knowledge-base seed; masked + length-capped server-side).
// Returns { ok: true }. 404 if run not found; 422 no_diagnosis if the run has no
// diagnosis yet; same-run resubmit overwrites (upsert by runId).

export type FeedbackVerdict = 'up' | 'down'

export interface DiagnosisFeedbackInput {
  verdict: FeedbackVerdict
  correctRootCause?: string
}

export function submitDiagnosisFeedback(
  id: string,
  input: DiagnosisFeedbackInput,
): Promise<{ ok: boolean }> {
  return http.post<{ ok: boolean }>(`/api/runs/${id}/diagnosis/feedback`, input)
}

// ─── Manual trigger ───────────────────────────────────────────────────────────
//
// POST /api/projects/{id}/runs  body { branch, commit? }
// Returns the created RunDetail (3-1 frozen DTO shape).
// Caller uses the returned run.id to navigate to /runs/:id.

export interface TriggerManualInput {
  branch?: string
  commit?: string
  /** Parameterized run (Story 8-11): key=value injected as container env (PW_<KEY>). */
  params?: Record<string, string>
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

// ─── Test report + quality gate (Epic 8 · Story 8-6 — FR-8-6) ────────────────
//
// GET /api/runs/{id}/test-report → { reports: TestReportDTO[], gate: GateVerdict }
// Read-only, authenticated. A script step can declare it produces a JUnit report
// (+ optional Cobertura coverage); the platform parses pass/fail/skip counts and
// coverage%, and a quality gate can block downstream deploy when thresholds fail.
// 404 run_not_found if the run does not exist. coverage = -1 means "not provided".

export interface TestReportDTO {
  id: string
  stageId: string
  stageName: string
  format: string // junit
  total: number
  passed: number
  failed: number
  skipped: number
  durationSec: number
  coverage: number // line coverage %, -1 when not provided
  gateEnabled: boolean
  gatePassed: boolean
  gateReason: string // human-readable block reason (no secrets — counts/thresholds only)
  createdAt: string // RFC3339
}

export interface GateVerdict {
  enabled: boolean // any stage declared a gate
  passed: boolean // all gates passed (no gate ⇒ true)
  reasons: string[] // per-stage block reasons (empty when passed)
}

export interface RunTestReportResponse {
  reports: TestReportDTO[]
  gate: GateVerdict
}

export function getRunTestReport(id: string): Promise<RunTestReportResponse> {
  return http.get<RunTestReportResponse>(`/api/runs/${id}/test-report`)
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

// ─── Deploy-time health gate (Story 4-3 frozen sub-contract — FR-12) ──────────
// Optional post-deploy health probe. type='none' / omitted ⇒ skipped (4-2
// backward compatible). For type='http' the backend builds an array-ized
// `curl -fsS --max-time T <url>`; for type='command' it runs the given array
// directly — never shell-concatenated (AC-SEC-02). retries/interval/timeout
// have backend defaults (3 / 3s / 5s) and caps (retries≤20, timeout≤60s).
export type HealthCheckType = 'none' | 'http' | 'command'

export interface HealthCheckInput {
  type: HealthCheckType
  url?: string              // type='http'
  command?: string[]        // type='command' (array, AC-SEC-02)
  retries?: number          // default 3, cap 20
  intervalSeconds?: number  // default 3
  timeoutSeconds?: number   // default 5, cap 60
}

// Deploy strategy (Story 8-8 / FR-8-8). 'rolling' (default) = fan out to all
// targets in parallel, each self-heals. 'canary' = deploy a small batch first,
// gate on its health, then the rest (abort the rest if the canary fails).
// 'blue_green' = stage every target, then cut over all at once; if any cutover
// fails, roll back the whole fleet (release-mode artifacts: dist/jar).
export type DeployStrategy = 'rolling' | 'canary' | 'blue_green'

export interface DeployRunInput {
  artifactId: string
  serverIds: string[]
  deployConfig?: Record<string, string>
  // Optional health gate (Story 4-3). Omit ⇒ identical to 4-2 behavior.
  healthCheck?: HealthCheckInput
  // Optional rollout strategy (Story 8-8). Omit/'rolling' ⇒ identical to prior
  // behavior. Canary batch size flows via deployConfig.canaryCount/canaryPercent.
  strategy?: DeployStrategy
}

export interface DeployRunResponse {
  targets: DeployTarget[]
}

export function deployRun(id: string, input: DeployRunInput): Promise<DeployRunResponse> {
  return http.post<DeployRunResponse>(`/api/runs/${id}/deploy`, input)
}

// ─── Retry only failed targets (Story 4-5 frozen contract — FR-13) ────────────
//
// POST /api/runs/{id}/deploy/retry  body { artifactId, serverIds?, deployConfig?, healthCheck? }
// Re-runs the deploy only on the run's current failed/rolled_back targets (commands
// array-ized server-side; AC-SEC-02). Successful targets are untouched. serverIds
// omitted ⇒ all currently-failed targets; given ⇒ only those among the failed set.
// Reuses the prior deploy's artifact + config (carried by the caller — the frontend
// still holds the last deploy form). Returns the run's FULL latest `targets` array.
//
// 200 with the updated `targets`. Per-target execution failure is NOT an HTTP error —
// that target carries status='failed' + human message (never 500, never secrets).
// 404 run_not_found; 422 when the run is not failed / has no failed targets to retry.

export interface RetryFailedDeployInput {
  artifactId: string
  serverIds?: string[]
  deployConfig?: Record<string, string>
  healthCheck?: HealthCheckInput
}

export function retryFailedDeploy(
  id: string,
  input: RetryFailedDeployInput,
): Promise<DeployRunResponse> {
  return http.post<DeployRunResponse>(`/api/runs/${id}/deploy/retry`, input)
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

import { http, type HttpOptions } from './http'
import type { DeployStrategy, HealthCheckInput } from './runs'

export type VerificationResult =
  | 'passed'
  | 'failed'
  | 'insufficient'
  | 'uncertain'
  | null
export interface GuardImage {
  reference: string
  repository: string
  digest: string
  digestKind: string
  identitySource?: string
}
export interface GuardSummary {
  id: string
  executionStatus: string
  verificationResult: VerificationResult
  createdAt: string
}
export interface DeploymentAttempt {
  id: string
  source: {
    runId: string
    projectId: string
    environment: string
    stageId: string
  }
  createdAt: string
  targets: Array<{
    id: string
    serverId: string
    serverName: string
    containerName: string
    expectedImage: GuardImage
    outcome: string
    receipts: Array<{
      kind: string
      at: string
      actualImage: { containerId: string }
    }>
  }>
  guards: GuardSummary[]
}
export interface GuardPlanTarget {
  id: string
  serverId: string
  serverName: string
  containerName: string
  containerId: string
  expectedImage: GuardImage
  availability: 'available' | 'identity_unavailable' | 'unsupported'
  limitations: string[]
}
export interface GuardPlan {
  id: string
  hash: string
  expiresAt: string
  mode: 'from_now' | 'attached'
  projectId: string
  runId: string
  attemptId: string
  artifactId?: string
  durationSeconds: number
  includeLogs: boolean
  targets: GuardPlanTarget[]
  limitations: string[]
}
export interface GuardReport {
  id: string
  projectId: string
  runId: string
  attemptId: string
  plan: GuardPlan
  executionStatus: string
  verificationResult: VerificationResult
  attributionStatus: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
  reason: string
  identityTrust: string
  targets: Array<{
    id: string
    serverId: string
    serverName: string
    containerId: string
    expectedImage?: GuardImage
    verificationResult: VerificationResult
    coverage: {
      expectedSamples: number
      successfulSamples: number
      maxGapSeconds: number
      complete: boolean
      firstSample?: string
      lastSample?: string
    }
    readyAt?: string
    deadline?: string
    conditions: Array<{
      name: string
      result: string
      reason: string
      evidenceIds: string[]
    }>
    limitations: string[]
  }>
  timeline: Array<{
    id: string
    at: string
    kind: string
    targetId: string
    summary: string
    evidenceIds: string[]
  }>
  investigation: {
    baselineStatus: string
    candidates: Array<{
      id: string
      title: string
      supportingEvidenceIds: string[]
      counterEvidenceIds: string[]
      missingEvidence: string[]
      nextReadOnlyCheck: string
    }>
    limitations: string[]
    baselines?: Array<{
      targetId: string
      status: string
      evidenceId: string
      priorAttemptId: string
      priorRunId: string
      ageSeconds: number
      missingEvidence: string[]
      historicalCandidates?: Array<{
        attemptId: string
        runId: string
        at: string
        ageSeconds: number
        status: string
      }>
    }>
    sourceComparisons?: Array<{
      targetId: string
      workloadKey: string
      available: boolean
      reason: string
      baseSha: string
      currentSha: string
      comparisonKind: 'direct_trees'
      files: Array<{ path: string; status: string }>
      truncated: boolean
    }>
    inputHash?: string
    status?: string
    investigatedAt?: string
  }
  eventsCursor: number
  evidenceExpiresAt: string
  evidenceExpired: boolean
  evidenceSetHash?: string
  parentGuardId?: string
  comparison: { sameConditions: boolean; changes: string[] }
}
export interface GuardEvidence {
  id: string
  sequence: number
  targetId: string
  kind: string
  eventAt: string
  collectedAt: string
  reason: string
  truncated: boolean
  summary: string
  value: Record<string, unknown>
}
export interface GuardEvidencePage {
  items: GuardEvidence[]
  nextCursor: number
  hasMore: boolean
}
export interface GuardAnalysis {
  id: string
  guardId: string
  status: 'preview' | 'sending' | 'completed' | 'failed' | 'unknown' | 'revoked'
  provider: {
    name: string
    model: string
    destination: string
    configHash: string
  }
  input: { includeRuntime: boolean; includeLogs: boolean }
  payload: string
  payloadHash: string
  planHash: string
  evidenceSetHash: string
  evidenceIds: string[]
  createdAt: string
  expiresAt: string
  revoked: boolean
  stale: boolean
  reason: string
  limitations: string[]
  result?: {
    summary: string
    candidates: GuardReport['investigation']['candidates']
  }
}
export type GuardDuration = 300 | 600 | 900
export interface AttachedGuardInput {
  runId: string
  artifactId: string
  serverIds: string[]
  deployConfig: Record<string, string>
  strategy?: DeployStrategy
  healthCheck?: HealthCheckInput
}
export interface ReleaseGuardBinding {
  planId: string
  planHash: string
  idempotencyKey: string
}

const projectPath = (projectId: string) =>
  `/api/projects/${encodeURIComponent(projectId)}`
const reportPath = (projectId: string, guardId: string) =>
  `${projectPath(projectId)}/release-guards/${encodeURIComponent(guardId)}`

export function listDeploymentAttempts(
  runId: string,
  options?: HttpOptions,
): Promise<{ items: DeploymentAttempt[] }> {
  return http.get(
    `/api/runs/${encodeURIComponent(runId)}/deployment-attempts`,
    options,
  )
}
export function previewGuardPlan(
  projectId: string,
  input: (
    | {
        mode: 'from_now'
        attemptId: string
      }
    | ({ mode: 'attached' } & AttachedGuardInput)
  ) & {
    durationSeconds: GuardDuration
    includeLogs: boolean
    parentGuardId?: string
  },
  options?: HttpOptions,
): Promise<GuardPlan> {
  return http.post(
    `${projectPath(projectId)}/release-guard-plans`,
    input,
    options,
  )
}
export function startReleaseGuard(
  projectId: string,
  input: {
    planId: string
    planHash: string
    idempotencyKey: string
  },
  options?: HttpOptions,
): Promise<GuardReport> {
  return http.post(`${projectPath(projectId)}/release-guards`, input, options)
}
export function getReleaseGuard(
  projectId: string,
  guardId: string,
  options?: HttpOptions,
): Promise<GuardReport> {
  return http.get(reportPath(projectId, guardId), options)
}
export function stopReleaseGuard(
  projectId: string,
  guardId: string,
  options?: HttpOptions,
): Promise<GuardReport> {
  return http.post(`${reportPath(projectId, guardId)}/stop`, {}, options)
}
export function investigateReleaseGuard(
  projectId: string,
  guardId: string,
  options?: HttpOptions,
): Promise<GuardReport> {
  return http.post(`${reportPath(projectId, guardId)}/investigate`, {}, options)
}
export function previewGuardAnalysis(
  projectId: string,
  guardId: string,
  input: GuardAnalysis['input'],
  options?: HttpOptions,
): Promise<GuardAnalysis> {
  return http.post(
    `${reportPath(projectId, guardId)}/analysis/preview`,
    input,
    options,
  )
}
export function listGuardAnalyses(
  projectId: string,
  guardId: string,
  options?: HttpOptions,
): Promise<{ items: GuardAnalysis[] }> {
  return http.get(`${reportPath(projectId, guardId)}/analysis`, options)
}
export function sendGuardAnalysis(
  projectId: string,
  guardId: string,
  analysisId: string,
  input: { payloadHash: string; providerConfigHash: string; confirmed: true },
  options?: HttpOptions,
): Promise<GuardAnalysis> {
  return http.post(
    `${reportPath(projectId, guardId)}/analysis/${encodeURIComponent(analysisId)}/send`,
    input,
    options,
  )
}
export function revokeGuardAnalysis(
  projectId: string,
  guardId: string,
  analysisId: string,
  options?: HttpOptions,
): Promise<GuardAnalysis> {
  return http.post(
    `${reportPath(projectId, guardId)}/analysis/${encodeURIComponent(analysisId)}/revoke`,
    {},
    options,
  )
}
export function listGuardEvidence(
  projectId: string,
  guardId: string,
  after = 0,
  options?: HttpOptions,
): Promise<GuardEvidencePage> {
  return http.get(
    `${reportPath(projectId, guardId)}/evidence?after=${encodeURIComponent(after)}&limit=100`,
    options,
  )
}

const activeStatuses = new Set([
  'prepared',
  'queued',
  'binding',
  'observing',
  'stopping',
])
export function guardIsActive(status: string): boolean {
  return activeStatuses.has(status)
}
export function attemptHasIdentity(attempt: DeploymentAttempt): boolean {
  return attempt.targets.some((target) => {
    if (target.expectedImage.digestKind === 'config_id' &&
      (target.expectedImage.identitySource !== 'script_pre_activation' || !/^sha256:[a-f0-9]{64}$/i.test(target.expectedImage.digest))) return false
    return target.receipts.some(
      (receipt) =>
        receipt.kind === 'activated' &&
        /^[a-f0-9]{64}$/i.test(receipt.actualImage?.containerId ?? ''),
    )
  })
}
export function guardReportLocation(projectId: string, guardId: string) {
  return { name: 'release-guard-report', params: { projectId, guardId } }
}

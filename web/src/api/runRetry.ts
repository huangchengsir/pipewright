import { http, type HttpOptions } from './http'
import { t } from '../i18n'

export interface RetryReason { code: string; nodeKey?: string; related?: string[] | null }
export interface RetryNode {
  key: string; stageId: string; jobId?: string; controlKey?: string
  axes?: Record<string, string> | null; executionStageId: string; executionJobId?: string
  name: string; type: string; allowFailure?: boolean
}
export interface RetryDecision {
  action: string; status?: string; sourceAttemptId?: string; sourceNodeKey?: string; reasons?: RetryReason[]
}
export interface RetryPlannedNode {
  node: RetryNode; decision: RetryDecision; reasonCode?: string; executionStarted: boolean
  attemptStepId?: string; ordinal: number; sourceStepId?: string; sourceOrdinal: number
}
export interface RetryContext {
  logicalRunId: string; attemptRunId: string; headAttemptId: string; revision: number
  manifestFingerprint: string; commitSha?: string; outcomeUnknown: boolean
  graph: { version: number; stages?: { id: string; name: string; matrixParentId?: string }[]; nodes: RetryNode[]; edges: { from: string; to: string; kind: string; order?: number }[] }
  nodes: RetryPlannedNode[]
}
export interface RetryPlan {
  schemaVersion: number; logicalRunId: string; baseAttemptId: string; baseRevision: number
  manifestFingerprint: string; planFingerprint: string; targetNodeIds: string[]; canSubmit: boolean
  nodes: RetryPlannedNode[]; blockingReasons: RetryReason[]; warnings: RetryReason[]
  rebuildNodeIds?: string[] | null
  rebuildGroups?: { nodeIds: string[]; consumerIds: string[]; reasonCode: string }[]
}
export interface RetrySubmitInput {
  requestId: string; baseAttemptId: string; baseRevision: number; planFingerprint: string
  targetNodeIds: string[]; rebuildNodeIds?: string[]
}
export interface RetryRequest {
  requestId: string; logicalRunId: string; sourceAttemptId: string; attemptRunId: string
  state: string; reasonCode?: string; initiator?: string; createdAt: string
}
export interface RetryAttempt {
  runId: string; parentAttemptId?: string; revision: number; status: string; executionKind: string
  outcomeUnknown: boolean; initiator?: string; createdAt: string
}
export interface RetryAttemptPage { items: RetryAttempt[]; page: number; total: number }
const base = (id: string) => `/api/runs/${encodeURIComponent(id)}`
export function getRetryContext(id: string, options?: HttpOptions) { return http.get<RetryContext>(`${base(id)}/retry-context`, options) }
export function getRetryPlan(id: string, targets: string[], rebuild: string[] = [], options?: HttpOptions) {
  const q = new URLSearchParams()
  targets.forEach(key => q.append('nodeId', key)); rebuild.forEach(key => q.append('rebuildNodeId', key))
  const uri = `${base(id)}/retry-plan?${q}`
  // Same read-only preflight, using a CSRF-protected body before a proxy's
  // request-line limit can truncate a large exact selection.
  return uri.length > 6000
    ? http.post<RetryPlan>(`${base(id)}/retry-plan`, { targetNodeIds: targets, rebuildNodeIds: rebuild }, options)
    : http.get<RetryPlan>(uri, options)
}
export function submitRetry(id: string, input: RetrySubmitInput, options?: HttpOptions) { return http.post<RetryRequest>(`${base(id)}/retry`, input, options) }
export function getRetryRequest(id: string, requestId: string, options?: HttpOptions) { return http.get<RetryRequest>(`${base(id)}/retry-requests/${encodeURIComponent(requestId)}`, options) }
export function getRetryAttempts(id: string, page = 1, options?: HttpOptions) { return http.get<RetryAttemptPage>(`${base(id)}/attempts?page=${page}&pageSize=20`, options) }
export function reconcileRetry(id: string, options?: HttpOptions) { return http.post<{ drained: boolean; checked: number; reasons: RetryReason[] }>(`${base(id)}/retry-reconcile`, {}, options) }

export function retryNodeLabel(n: RetryNode): string {
  const axes = Object.entries(n.axes ?? {}).map(([key, value]) => `${key}=${value}`).join(', ')
  const controlKey = n.controlKey?.startsWith('post:') ? 'post' : n.controlKey === 'jobs_done' ? 'barrier' : n.controlKey
  const control = controlKey && ['init', 'gate', 'services', 'source', 'post', 'cleanup', 'barrier'].includes(controlKey) ? t(`runRetry.controls.${controlKey}`) : ''
  const postIndex = /^post:(\d+)$/.exec(n.controlKey ?? '')
  const postOrdinal = postIndex ? ` #${Number(postIndex[1]) + 1}` : ''
  const name = control ? `${n.name} · ${control}${postOrdinal}` : n.name
  return axes ? `${name} · ${axes}` : name
}
export function sourceLogLocation(n: RetryPlannedNode) {
  if (!n.decision.sourceAttemptId || !n.sourceStepId || n.sourceOrdinal < 0) return null
  return { name: 'run-detail', params: { id: n.decision.sourceAttemptId }, query: { step: String(n.sourceOrdinal), stepId: n.sourceStepId } }
}

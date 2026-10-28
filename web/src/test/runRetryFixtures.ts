import type { RetryContext, RetryPlan, RetryPlannedNode, RetryRequest } from '../api/runRetry'

export function retryNode(key: string, status: string, extras: Partial<RetryPlannedNode> = {}): RetryPlannedNode {
  return { node: { key, stageId: 'build', executionStageId: 'build', jobId: key, executionJobId: key, name: key, type: 'script' }, decision: { action: 'executed', status, sourceAttemptId: 'root', sourceNodeKey: key }, attemptStepId: `step-${key}`, ordinal: key === 'a' ? 0 : 1, sourceOrdinal: -1, executionStarted: true, ...extras }
}
export function retryContext(patch: Partial<RetryContext> = {}): RetryContext {
  const nodes = [retryNode('a', 'success'), retryNode('b', 'failed'), retryNode('c', 'skipped')]
  return { logicalRunId: 'root', attemptRunId: 'root', headAttemptId: 'root', revision: 1, manifestFingerprint: 'm'.repeat(64), commitSha: 'a'.repeat(40), outcomeUnknown: false, graph: { version: 1, stages: [{ id: 'build', name: 'Original build' }], nodes: nodes.map(n => n.node), edges: [{ from: 'a', to: 'b', kind: 'control' }, { from: 'b', to: 'c', kind: 'control' }] }, nodes, ...patch }
}
export function retryPlan(patch: Partial<RetryPlan> = {}): RetryPlan {
  const nodes = retryContext().nodes
  nodes[0].decision.action = 'reuse'; nodes[0].sourceStepId = 'step-a'; nodes[0].sourceOrdinal = 0
  nodes[1].decision.action = 'execute'; nodes[2].decision.action = 'execute'
  return { schemaVersion: 1, logicalRunId: 'root', baseAttemptId: 'root', baseRevision: 1, manifestFingerprint: 'm'.repeat(64), planFingerprint: 'f'.repeat(64), targetNodeIds: ['b'], canSubmit: true, nodes, blockingReasons: [], warnings: [], rebuildGroups: [], rebuildNodeIds: [], ...patch }
}
export function retryRequest(patch: Partial<RetryRequest> = {}): RetryRequest { return { requestId: 'request-one', logicalRunId: 'root', sourceAttemptId: 'root', attemptRunId: 'child', state: 'accepted', initiator: 'operator', createdAt: '2026-10-28T00:00:00Z', ...patch } }

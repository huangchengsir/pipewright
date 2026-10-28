import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getRetryPlan, getRetryRequest, reconcileRetry, retryNodeLabel, sourceLogLocation, submitRetry } from './runRetry'
import { retryNode } from '../test/runRetryFixtures'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./http', () => ({ http: api }))
describe('recovery API identities', () => {
  beforeEach(() => vi.clearAllMocks())
  it('preserves repeated opaque node and explicit rebuild selections', async () => {
    await getRetryPlan('run/one', ['one+cell', 'two&cell'], ['producer'])
    const url = new URL(api.get.mock.calls[0][0], 'http://localhost')
    expect(url.pathname).toBe('/api/runs/run%2Fone/retry-plan')
    expect(url.searchParams.getAll('nodeId')).toEqual(['one+cell', 'two&cell'])
    expect(url.searchParams.getAll('rebuildNodeId')).toEqual(['producer'])
  })
  it('queries the original request and sends no client identity or reconciliation proof', async () => {
    await getRetryRequest('root', 'fixed-one')
    expect(api.get).toHaveBeenCalledWith('/api/runs/root/retry-requests/fixed-one', undefined)
    const input = { requestId: 'fixed-one', baseAttemptId: 'root', baseRevision: 1, planFingerprint: 'f', targetNodeIds: ['node'] }
    await submitRetry('root', input)
    expect(api.post).toHaveBeenCalledWith('/api/runs/root/retry', input, undefined)
    await reconcileRetry('root')
    expect(api.post).toHaveBeenLastCalledWith('/api/runs/root/retry-reconcile', {}, undefined)
  })
  it('uses the same read-only plan body for a large encoded selection without a submission identity', async () => {
    const targets = Array.from({ length: 100 }, (_, i) => String(i).padStart(64, '0'))
    await getRetryPlan('root', targets, ['producer'])
    expect(api.get).not.toHaveBeenCalled(); expect(api.post).toHaveBeenCalledWith('/api/runs/root/retry-plan', { targetNodeIds: targets, rebuildNodeIds: ['producer'] }, undefined)
    expect(api.post.mock.calls[0][1]).not.toHaveProperty('requestId')
  })
  it('uses the actual producer step ID and ordinal for source logs and rejects missing mappings', () => {
    const node = retryNode('b', 'success', { sourceStepId: 'real-source-row', sourceOrdinal: 9, decision: { action: 'reuse', sourceAttemptId: 'producer' } })
    expect(sourceLogLocation(node)).toEqual({ name: 'run-detail', params: { id: 'producer' }, query: { step: '9', stepId: 'real-source-row' } })
    expect(sourceLogLocation({ ...node, sourceOrdinal: -1 })).toBeNull()
  })
  it('distinguishes multiple Post controls in the same stage and matrix cell', () => {
    const base = { key: 'opaque', stageId: 'build', executionStageId: 'build-linux', name: 'Build', type: 'control', axes: { os: 'linux' } }
    const first = retryNodeLabel({ ...base, controlKey: 'post:0' }); const second = retryNodeLabel({ ...base, controlKey: 'post:1' })
    expect(first).toContain('#1'); expect(second).toContain('#2'); expect(first).not.toBe(second)
  })
})

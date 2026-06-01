import { describe, expect, it } from 'vitest'
import { canRollback, previousSuccess, shortCommit, toBadgeStatus } from './environments.helpers'
import type { EnvDeployment, EnvironmentTimeline } from './environments'

function dep(partial: Partial<EnvDeployment>): EnvDeployment {
  return {
    runId: 'r',
    status: 'success',
    commit: '',
    branch: 'main',
    triggeredBy: 'alice',
    deployedAt: '2026-01-01T00:00:00Z',
    active: false,
    targets: [],
    artifacts: [],
    ...partial,
  }
}

function timeline(deployments: EnvDeployment[]): EnvironmentTimeline {
  const active = deployments.find((d) => d.active) ?? null
  return { environment: 'prod', active, deployments }
}

describe('toBadgeStatus', () => {
  it('maps aggregated statuses to the badge vocabulary', () => {
    expect(toBadgeStatus('success')).toBe('success')
    expect(toBadgeStatus('partial_failed')).toBe('partial')
    expect(toBadgeStatus('failed')).toBe('failed')
    expect(toBadgeStatus('anything-else')).toBe('failed')
  })
})

describe('shortCommit', () => {
  it('truncates to 8 chars and falls back to em-dash', () => {
    expect(shortCommit('abcdef1234567')).toBe('abcdef12')
    expect(shortCommit('')).toBe('—')
  })
})

describe('previousSuccess / canRollback', () => {
  it('finds the next all-success after the active deployment', () => {
    const tl = timeline([
      dep({ runId: 'cur', active: true, status: 'success' }),
      dep({ runId: 'bad', status: 'failed' }),
      dep({ runId: 'old', status: 'success' }),
    ])
    expect(previousSuccess(tl)?.runId).toBe('old')
    expect(canRollback(tl)).toBe(true)
  })

  it('returns null when there is only one success (the active one)', () => {
    const tl = timeline([dep({ runId: 'only', active: true, status: 'success' })])
    expect(previousSuccess(tl)).toBeNull()
    expect(canRollback(tl)).toBe(false)
  })

  it('returns null when there is no active version at all', () => {
    const tl = timeline([dep({ runId: 'f1', status: 'failed' }), dep({ runId: 'f2', status: 'failed' })])
    expect(previousSuccess(tl)).toBeNull()
    expect(canRollback(tl)).toBe(false)
  })

  it('skips failed/partial deployments between active and the rollback target', () => {
    const tl = timeline([
      dep({ runId: 'cur', active: true, status: 'success' }),
      dep({ runId: 'p', status: 'partial_failed' }),
      dep({ runId: 'f', status: 'failed' }),
      dep({ runId: 'target', status: 'success' }),
    ])
    expect(previousSuccess(tl)?.runId).toBe('target')
  })
})

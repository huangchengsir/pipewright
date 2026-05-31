import { describe, it, expect } from 'vitest'
import type { RunStep } from '../../api/runs'
import type { PipelineStage } from '../../api/pipeline'
import {
  deriveStageStatus,
  findStageForStep,
  buildRunStages,
  buildDagEdges,
  topoSort,
} from './runDag'

// ─── helpers ─────────────────────────────────────────────────────────────────

function mkStep(
  id: string,
  name: string,
  status: RunStep['status'] = 'pending',
): RunStep {
  return { id, name, status, startedAt: null, finishedAt: null, durationMs: null }
}

function mkStage(
  id: string,
  name: string,
  needs: string[] = [],
): PipelineStage {
  return { id, name, kind: 'custom', needs, jobs: [] }
}

// ─── deriveStageStatus ────────────────────────────────────────────────────────

describe('deriveStageStatus', () => {
  it('returns pending for empty steps', () => {
    expect(deriveStageStatus([])).toBe('pending')
  })

  it('failed overrides everything', () => {
    expect(deriveStageStatus([mkStep('a', 'a', 'success'), mkStep('b', 'b', 'failed')])).toBe('failed')
  })

  it('running takes priority over pending/success', () => {
    expect(deriveStageStatus([mkStep('a', 'a', 'success'), mkStep('b', 'b', 'running')])).toBe('running')
  })

  it('all success → success', () => {
    expect(deriveStageStatus([mkStep('a', 'a', 'success'), mkStep('b', 'b', 'success')])).toBe('success')
  })

  it('all skipped → skipped', () => {
    expect(deriveStageStatus([mkStep('a', 'a', 'skipped')])).toBe('skipped')
  })

  it('pending when mixed pending/success', () => {
    expect(deriveStageStatus([mkStep('a', 'a', 'success'), mkStep('b', 'b', 'pending')])).toBe('pending')
  })
})

// ─── findStageForStep ─────────────────────────────────────────────────────────

describe('findStageForStep', () => {
  const stages = [
    mkStage('s1', 'Build'),
    mkStage('s2', 'Deploy'),
    mkStage('s3', 'Notify'),
  ]

  it('matches exact name (case-insensitive)', () => {
    expect(findStageForStep(mkStep('x', 'build'), stages)).toBe('s1')
    expect(findStageForStep(mkStep('x', 'Deploy'), stages)).toBe('s2')
  })

  it('matches when step name starts with stage name', () => {
    expect(findStageForStep(mkStep('x', 'build image'), stages)).toBe('s1')
  })

  it('matches when stage name is in step name', () => {
    expect(findStageForStep(mkStep('x', 'run deploy step'), stages)).toBe('s2')
  })

  it('returns null for no match', () => {
    expect(findStageForStep(mkStep('x', 'checkout'), stages)).toBeNull()
  })
})

// ─── buildRunStages ───────────────────────────────────────────────────────────

describe('buildRunStages', () => {
  it('falls back to per-step stages when no pipeline spec', () => {
    const steps = [mkStep('s1', 'build'), mkStep('s2', 'deploy')]
    const result = buildRunStages(steps, [])
    expect(result.length).toBe(2)
    expect(result[0].id).toBe('s1')
    expect(result[0].steps).toEqual([steps[0]])
    expect(result[0].needs).toEqual([])
  })

  it('groups steps into pipeline stages by name', () => {
    const steps = [
      mkStep('step-1', 'Build', 'success'),
      mkStep('step-2', 'Deploy', 'running'),
    ]
    const stages = [mkStage('stg-a', 'Build'), mkStage('stg-b', 'Deploy', ['stg-a'])]
    const result = buildRunStages(steps, stages)
    expect(result.length).toBe(2)
    const buildStage = result.find((r) => r.id === 'stg-a')!
    expect(buildStage.steps).toHaveLength(1)
    expect(buildStage.status).toBe('success')
    const deployStage = result.find((r) => r.id === 'stg-b')!
    expect(deployStage.needs).toEqual(['stg-a'])
    expect(deployStage.status).toBe('running')
  })

  it('puts unmatched steps into _unmatched synthetic stage', () => {
    const steps = [mkStep('s1', 'unknown step', 'failed')]
    const stages = [mkStage('stg-a', 'Build')]
    const result = buildRunStages(steps, stages)
    const unmatched = result.find((r) => r.id === '_unmatched')!
    expect(unmatched).toBeTruthy()
    expect(unmatched.steps).toHaveLength(1)
    expect(unmatched.status).toBe('failed')
  })
})

// ─── buildDagEdges ────────────────────────────────────────────────────────────

describe('buildDagEdges', () => {
  it('uses explicit needs when any stage has them', () => {
    const stages = [
      { id: 'a', name: 'A', needs: [], steps: [], status: 'pending' as const, gate: false },
      { id: 'b', name: 'B', needs: ['a'], steps: [], status: 'pending' as const, gate: false },
      { id: 'c', name: 'C', needs: ['a'], steps: [], status: 'pending' as const, gate: false },
    ]
    const edges = buildDagEdges(stages)
    expect(edges).toHaveLength(2)
    expect(edges).toContainEqual({ from: 'a', to: 'b' })
    expect(edges).toContainEqual({ from: 'a', to: 'c' })
  })

  it('falls back to linear chain when no needs declared', () => {
    const stages = [
      { id: 'a', name: 'A', needs: [], steps: [], status: 'pending' as const, gate: false },
      { id: 'b', name: 'B', needs: [], steps: [], status: 'pending' as const, gate: false },
      { id: 'c', name: 'C', needs: [], steps: [], status: 'pending' as const, gate: false },
    ]
    const edges = buildDagEdges(stages)
    expect(edges).toHaveLength(2)
    expect(edges[0]).toEqual({ from: 'a', to: 'b' })
    expect(edges[1]).toEqual({ from: 'b', to: 'c' })
  })

  it('returns empty edges for single stage', () => {
    const stages = [
      { id: 'a', name: 'A', needs: [], steps: [], status: 'pending' as const, gate: false },
    ]
    expect(buildDagEdges(stages)).toHaveLength(0)
  })
})

// ─── topoSort ─────────────────────────────────────────────────────────────────

describe('topoSort', () => {
  it('sorts linear chain correctly', () => {
    const stages = [
      { id: 'c', name: 'C', needs: ['b'], steps: [], status: 'pending' as const, gate: false },
      { id: 'a', name: 'A', needs: [], steps: [], status: 'pending' as const, gate: false },
      { id: 'b', name: 'B', needs: ['a'], steps: [], status: 'pending' as const, gate: false },
    ]
    const sorted = topoSort(stages)
    const ids = sorted.map((s) => s.id)
    expect(ids.indexOf('a')).toBeLessThan(ids.indexOf('b'))
    expect(ids.indexOf('b')).toBeLessThan(ids.indexOf('c'))
  })

  it('handles diamond DAG', () => {
    // a → b, a → c, b → d, c → d
    const stages = [
      { id: 'a', name: 'A', needs: [], steps: [], status: 'pending' as const, gate: false },
      { id: 'b', name: 'B', needs: ['a'], steps: [], status: 'pending' as const, gate: false },
      { id: 'c', name: 'C', needs: ['a'], steps: [], status: 'pending' as const, gate: false },
      { id: 'd', name: 'D', needs: ['b', 'c'], steps: [], status: 'pending' as const, gate: false },
    ]
    const sorted = topoSort(stages)
    const ids = sorted.map((s) => s.id)
    expect(ids.indexOf('a')).toBeLessThan(ids.indexOf('b'))
    expect(ids.indexOf('a')).toBeLessThan(ids.indexOf('c'))
    expect(ids.indexOf('b')).toBeLessThan(ids.indexOf('d'))
    expect(ids.indexOf('c')).toBeLessThan(ids.indexOf('d'))
  })

  it('handles empty stages', () => {
    expect(topoSort([])).toEqual([])
  })
})

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
  jobs: PipelineStage['jobs'] = [{ id: `${id}-j`, name: 'job', type: 'script', summary: '', config: {} }],
): PipelineStage {
  return { id, name, kind: 'custom', needs, jobs }
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

  it('节点级:优先按 step.stage 精确归到同名阶段(节点名与阶段名不同也能归组)', () => {
    const s = { ...mkStep('x', 'A'), stage: 'Build' }
    expect(findStageForStep(s, stages)).toBe('s1')
    // stage 精确优先于模糊名匹配:名为 deploy 但 stage=Build → 归 Build
    const s2 = { ...mkStep('y', 'deploy'), stage: 'Build' }
    expect(findStageForStep(s2, stages)).toBe('s1')
  })
})

describe('buildRunStages 节点级分组', () => {
  it('同阶段多节点按 step.stage 归到该阶段的 steps(供进度图展开 / 详情两级)', () => {
    const stages = [mkStage('b', '构建')]
    const steps: RunStep[] = [
      { ...mkStep('a', 'A', 'success'), stage: '构建' },
      { ...mkStep('bb', 'B', 'success'), stage: '构建' },
      { ...mkStep('c', 'C', 'running'), stage: '构建' },
    ]
    const result = buildRunStages(steps, stages)
    const build = result.find((s) => s.id === 'b')
    expect(build).toBeTruthy()
    expect(build!.steps.map((s) => s.name)).toEqual(['A', 'B', 'C'])
    expect(build!.status).toBe('running') // 任一运行 → 阶段运行
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

  it('drops stages with no jobs (空阶段不画进拓扑) and cleans dangling needs', () => {
    const steps = [mkStep('step-1', 'Build', 'success')]
    const stages = [
      mkStage('stg-a', 'Build'), // 有 job
      mkStage('stg-b', 'Deploy', ['stg-a'], []), // 空阶段 → 应被剔除
      mkStage('stg-c', 'Notify', ['stg-b'], []), // 空阶段 → 应被剔除
    ]
    const result = buildRunStages(steps, stages)
    expect(result.map((r) => r.id)).toEqual(['stg-a'])
    // stg-a 不应残留指向被剔除阶段的 needs(此处本就无)
    expect(result[0].needs).toEqual([])
  })

  it('keeps a configured-but-not-yet-run stage (有 job 未跑 → pending,仍展示)', () => {
    const steps = [mkStep('step-1', 'Build', 'success')]
    const stages = [mkStage('stg-a', 'Build'), mkStage('stg-b', 'Deploy', ['stg-a'])]
    const result = buildRunStages(steps, stages)
    // Deploy 有 job 但还没产生 step → 保留(状态由 deriveStageStatus 推为 pending)
    expect(result.map((r) => r.id)).toEqual(['stg-a', 'stg-b'])
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

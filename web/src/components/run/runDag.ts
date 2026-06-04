/**
 * runDag — pure helpers for mapping run steps onto pipeline stage topology.
 *
 * The run DTO carries a flat `steps[]` array (no stage grouping). The pipeline
 * spec (`GET /api/projects/{id}/pipeline`) carries `stages[]` with `needs` edges.
 * This module derives a DAG of "run stages" by joining the two:
 *
 *   1. Build stage→steps mapping by matching step.name against stage.name
 *      (best-effort; steps that don't match any stage go into a synthetic
 *      "_unmatched" stage so no data is lost).
 *   2. Provide linear-fallback edges when no stage has explicit needs.
 *   3. Derive per-stage aggregate status from contained step statuses.
 */

import type { RunStep, StepStatus } from '../../api/runs'
import type { PipelineStage } from '../../api/pipeline'

// ─── Types ──────────────────────────────────────────────────────────────────

/** A stage rendered in the DAG — one column. */
export interface RunStage {
  id: string
  name: string
  /** Upstream stage IDs (declared needs). Empty array = no explicit deps. */
  needs: string[]
  steps: RunStep[]
  /** Derived from contained step statuses. */
  status: StepStatus
  /** Gate flag from pipeline spec. */
  gate: boolean
}

/** An edge in the DAG (upstream → downstream). */
export interface DagEdge {
  from: string
  to: string
}

// ─── Status derivation ───────────────────────────────────────────────────────

/**
 * Derive an aggregate status for a stage from its steps.
 * Priority: failed > running > skipped > pending > success.
 */
export function deriveStageStatus(steps: RunStep[]): StepStatus {
  if (steps.length === 0) return 'pending'
  if (steps.some((s) => s.status === 'failed')) return 'failed'
  if (steps.some((s) => s.status === 'running')) return 'running'
  if (steps.every((s) => s.status === 'success')) return 'success'
  if (steps.every((s) => s.status === 'skipped')) return 'skipped'
  if (steps.some((s) => s.status === 'skipped')) return 'skipped'
  return 'pending'
}

// ─── Step→stage matching ─────────────────────────────────────────────────────

/**
 * Normalise a string for fuzzy matching (lower, no special chars, collapsed ws).
 * Steps are matched against stage names to group them.
 */
function normalise(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9一-鿿]+/g, ' ').trim()
}

/**
 * Find which pipeline stage a step belongs to, by name matching.
 * Returns the stage ID or null when no match.
 */
export function findStageForStep(
  step: RunStep,
  stages: ReadonlyArray<PipelineStage>,
): string | null {
  // 0. 节点级:step 自带所属阶段名(后端 run_steps.stage)时按阶段名精确归组,
  //    一个阶段聚合其全部节点 step(进度图阶段框可展开看各节点)。最可靠,优先。
  if (step.stage) {
    const normStage = normalise(step.stage)
    for (const s of stages) {
      if (normalise(s.name) === normStage) return s.id
    }
  }
  const normStep = normalise(step.name)
  // 1. Exact name match (case-insensitive)
  for (const s of stages) {
    if (normalise(s.name) === normStep) return s.id
  }
  // 2. Step name starts with stage name
  for (const s of stages) {
    const normStage = normalise(s.name)
    if (normStage && normStep.startsWith(normStage)) return s.id
  }
  // 3. Stage name contained in step name
  for (const s of stages) {
    const normStage = normalise(s.name)
    if (normStage && normStep.includes(normStage)) return s.id
  }
  // 4. Step name contained in stage name
  for (const s of stages) {
    const normStage = normalise(s.name)
    if (normStage && normStage.includes(normStep)) return s.id
  }
  return null
}

// ─── Build DAG stages ────────────────────────────────────────────────────────

/**
 * Build RunStage[] from a flat step list + pipeline spec stages.
 * When no pipeline spec is available (empty array), falls back to one synthetic
 * "stage" per step (linear DAG).
 */
export function buildRunStages(
  steps: ReadonlyArray<RunStep>,
  pipelineStages: ReadonlyArray<PipelineStage>,
): RunStage[] {
  if (pipelineStages.length === 0) {
    // No pipeline spec: treat each step as its own stage (pure linear)
    return steps.map((step) => ({
      id: step.id,
      name: step.name,
      needs: [],
      steps: [step],
      status: step.status,
      gate: false,
    }))
  }

  // Map stageId → steps bucket
  const buckets = new Map<string, RunStep[]>()
  const unmatched: RunStep[] = []
  for (const stage of pipelineStages) {
    buckets.set(stage.id, [])
  }
  for (const step of steps) {
    const stageId = findStageForStep(step, pipelineStages)
    if (stageId && buckets.has(stageId)) {
      buckets.get(stageId)!.push(step)
    } else {
      unmatched.push(step)
    }
  }

  // 只画「真有 job」的阶段:未配置 job 的空阶段(占位)不进拓扑图,避免冒充已执行
  // (动态阶段语义,与后端 dagrun.executableStages 一致;存量 4 阶段 spec 也即时受益)。
  // 被剔除阶段的 id 从 needs 中清理,避免悬挂依赖产生指向不存在节点的边。
  const kept = new Set(pipelineStages.filter((s) => (s.jobs?.length ?? 0) > 0).map((s) => s.id))
  const result: RunStage[] = pipelineStages
    .filter((stage) => kept.has(stage.id))
    .map((stage) => {
      const stageSteps = buckets.get(stage.id) ?? []
      return {
        id: stage.id,
        name: stage.name,
        needs: (stage.needs ?? []).filter((n) => kept.has(n)),
        steps: stageSteps,
        status: deriveStageStatus(stageSteps),
        gate: stage.gate ?? false,
      }
    })

  // Append unmatched steps as a synthetic stage so they're visible
  if (unmatched.length > 0) {
    result.push({
      id: '_unmatched',
      name: '其他步骤',
      needs: [],
      steps: unmatched,
      status: deriveStageStatus(unmatched),
      gate: false,
    })
  }

  return result
}

// ─── Build DAG edges ─────────────────────────────────────────────────────────

/**
 * Build edge list from RunStage[].
 * Falls back to linear chain when no stage has explicit needs.
 */
export function buildDagEdges(stages: ReadonlyArray<RunStage>): DagEdge[] {
  const hasNeeds = stages.some((s) => s.needs.length > 0)
  if (hasNeeds) {
    const edges: DagEdge[] = []
    for (const s of stages) {
      for (const n of s.needs) {
        edges.push({ from: n, to: s.id })
      }
    }
    return edges
  }
  // Linear fallback
  const edges: DagEdge[] = []
  for (let i = 1; i < stages.length; i++) {
    edges.push({ from: stages[i - 1].id, to: stages[i].id })
  }
  return edges
}

// ─── Topological sort ────────────────────────────────────────────────────────

/**
 * Topological sort of stages for rendering order (column assignment).
 * Returns stages ordered so that no stage appears before its needs.
 * Uses Kahn's algorithm (BFS).
 */
export function topoSort(stages: ReadonlyArray<RunStage>): RunStage[] {
  const byId = new Map(stages.map((s) => [s.id, s]))
  // Build in-degree + adjacency for stages we actually have
  const indegree = new Map<string, number>()
  const children = new Map<string, string[]>()
  for (const s of stages) {
    if (!indegree.has(s.id)) indegree.set(s.id, 0)
    if (!children.has(s.id)) children.set(s.id, [])
    for (const n of s.needs) {
      if (byId.has(n)) {
        indegree.set(s.id, (indegree.get(s.id) ?? 0) + 1)
        children.set(n, [...(children.get(n) ?? []), s.id])
      }
    }
  }
  const queue = stages.filter((s) => (indegree.get(s.id) ?? 0) === 0)
  const result: RunStage[] = []
  while (queue.length > 0) {
    const node = queue.shift()!
    result.push(node)
    for (const childId of children.get(node.id) ?? []) {
      const deg = (indegree.get(childId) ?? 1) - 1
      indegree.set(childId, deg)
      if (deg === 0) {
        const child = byId.get(childId)
        if (child) queue.push(child)
      }
    }
  }
  // Append any unreachable (cycles in data) at end
  for (const s of stages) {
    if (!result.find((r) => r.id === s.id)) result.push(s)
  }
  return result
}

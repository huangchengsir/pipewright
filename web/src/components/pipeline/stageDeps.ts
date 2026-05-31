/**
 * stageDeps — pure helpers for editing pipeline stage dependencies (Epic 8 · 8-7).
 *
 * A stage's `needs` lists upstream stage IDs it depends on. The canvas editor uses
 * these helpers to offer only cycle-safe dependency choices; the backend re-validates
 * (dag.New) on save, so this is UX guardrail, not the source of truth.
 *
 * Dependency edge direction: `S needs N` means N must finish before S (N is upstream).
 */

import type { PipelineStage } from '../../api/pipeline'

/** Does `fromId` (transitively) depend on `targetId` via needs edges? */
export function dependsOn(
  stages: ReadonlyArray<PipelineStage>,
  fromId: string,
  targetId: string,
): boolean {
  const byId = new Map(stages.map((s) => [s.id, s]))
  const seen = new Set<string>()
  const stack: string[] = [...(byId.get(fromId)?.needs ?? [])]
  while (stack.length) {
    const id = stack.pop() as string
    if (id === targetId) return true
    if (seen.has(id)) continue
    seen.add(id)
    const node = byId.get(id)
    if (node?.needs) stack.push(...node.needs)
  }
  return false
}

/**
 * Is it safe to add `needId` to `stageId.needs`?
 * Safe iff it's a different stage and `needId` does not already depend on `stageId`
 * (which would close a cycle).
 */
export function canAddNeed(
  stages: ReadonlyArray<PipelineStage>,
  stageId: string,
  needId: string,
): boolean {
  if (stageId === needId) return false
  return !dependsOn(stages, needId, stageId)
}

/**
 * Stages eligible to be an upstream dependency of `stageId`:
 * every other stage that wouldn't create a cycle. Already-selected needs remain eligible
 * (shown checked). Order preserved as given.
 */
export function eligibleNeeds(
  stages: ReadonlyArray<PipelineStage>,
  stageId: string,
): PipelineStage[] {
  return stages.filter((s) => s.id !== stageId && !dependsOn(stages, s.id, stageId))
}

/** Toggle `needId` in a needs list, returning a new array (immutable). */
export function toggleNeed(needs: ReadonlyArray<string>, needId: string): string[] {
  return needs.includes(needId)
    ? needs.filter((n) => n !== needId)
    : [...needs, needId]
}

/** Does the whole stage set declare any explicit needs? (false ⇒ linear fallback) */
export function hasAnyNeeds(stages: ReadonlyArray<PipelineStage>): boolean {
  return stages.some((s) => (s.needs?.length ?? 0) > 0)
}

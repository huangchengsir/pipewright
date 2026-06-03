/**
 * jobDeps — pure helpers for editing *intra-stage* job-level dependencies.
 *
 * Mirrors stageDeps but operates within a single stage's job list. A job's `needs`
 * lists IDs of other jobs *in the same stage* that must finish first. Canvas semantics:
 *   - horizontal link = serial (this job `needs` that job)
 *   - jobs with no path between them sit in parallel lanes (vertical) and run concurrently
 *
 * The backend re-validates the job DAG (dag.New) on save, so these are UX guardrails,
 * not the source of truth. Edge direction: `J needs N` means N is upstream of J.
 */

import type { PipelineJob } from '../../api/pipeline'
import { toggleNeed } from './stageDeps'

export { toggleNeed }

/** Does `fromId` (transitively) depend on `targetId` via job needs edges? */
export function jobDependsOn(
  jobs: ReadonlyArray<PipelineJob>,
  fromId: string,
  targetId: string,
): boolean {
  const byId = new Map(jobs.map((j) => [j.id, j]))
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
 * Is it safe to add `needId` to `jobId.needs`? Safe iff a different job in the same
 * stage and `needId` does not already (transitively) depend on `jobId` (would cycle).
 */
export function canAddJobNeed(
  jobs: ReadonlyArray<PipelineJob>,
  jobId: string,
  needId: string,
): boolean {
  if (jobId === needId) return false
  return !jobDependsOn(jobs, needId, jobId)
}

/** Jobs eligible to be an upstream dependency of `jobId` (every other cycle-safe job). */
export function eligibleJobNeeds(
  jobs: ReadonlyArray<PipelineJob>,
  jobId: string,
): PipelineJob[] {
  return jobs.filter((j) => j.id !== jobId && !jobDependsOn(jobs, j.id, jobId))
}

/** Does this stage declare any intra-stage job dependency? (false ⇒ flat vertical list) */
export function hasAnyJobNeeds(jobs: ReadonlyArray<PipelineJob>): boolean {
  return jobs.some((j) => (j.needs?.length ?? 0) > 0)
}

/** One job's grid position in the intra-stage DAG: rank = serial depth (x), lane = parallel slot (y). */
export interface JobLayout {
  id: string
  rank: number
  lane: number
}

/**
 * Compute a 2-D grid layout for a stage's jobs from their needs DAG:
 *   - rank = longest dependency depth (root jobs with no in-stage needs → 0); maps to X (serial →)
 *   - lane = slot among jobs sharing a rank, in declaration order; maps to Y (parallel ↓)
 *
 * needs referencing jobs outside the list are ignored (defensive). A cycle (should never
 * reach here — validated on save) is broken by treating a re-entered node as rank 0.
 */
export function layoutJobs(jobs: ReadonlyArray<PipelineJob>): {
  positions: Map<string, JobLayout>
  ranks: number
  lanes: number
} {
  const byId = new Map(jobs.map((j) => [j.id, j]))
  const rankCache = new Map<string, number>()
  const computing = new Set<string>()
  const rankOf = (id: string): number => {
    const cached = rankCache.get(id)
    if (cached !== undefined) return cached
    if (computing.has(id)) return 0 // cycle guard (defensive)
    computing.add(id)
    const deps = (byId.get(id)?.needs ?? []).filter((n) => byId.has(n))
    let r = 0
    for (const d of deps) r = Math.max(r, rankOf(d) + 1)
    computing.delete(id)
    rankCache.set(id, r)
    return r
  }

  const positions = new Map<string, JobLayout>()
  const laneNext = new Map<number, number>() // rank → next free lane
  let ranks = 0
  for (const j of jobs) {
    const rank = rankOf(j.id)
    const lane = laneNext.get(rank) ?? 0
    laneNext.set(rank, lane + 1)
    positions.set(j.id, { id: j.id, rank, lane })
    ranks = Math.max(ranks, rank + 1)
  }
  let lanes = 1
  for (const n of laneNext.values()) lanes = Math.max(lanes, n)
  return { positions, ranks, lanes }
}

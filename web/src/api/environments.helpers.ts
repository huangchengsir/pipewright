/**
 * Pure helpers for the Environments UI — tested in environments.helpers.test.ts.
 *
 * Kept framework-free (no Vue) so the rollback-eligibility / previous-success logic
 * is unit-testable in isolation from the view.
 */

import type { BadgeStatus } from '../components/ui/StatusBadge.vue'
import type { EnvDeployment, EnvironmentTimeline } from './environments'

/** Map the aggregated deployment status to the frozen StatusBadge vocabulary. */
export function toBadgeStatus(status: string): BadgeStatus {
  switch (status) {
    case 'success':
      return 'success'
    case 'partial_failed':
      return 'partial'
    default:
      return 'failed'
  }
}

/** Short commit SHA for display (8 chars; em-dash when empty). */
export function shortCommit(commit: string): string {
  return commit ? commit.slice(0, 8) : '—'
}

/** The previous successful deployment (the rollback target): the next all-success after the active one. */
export function previousSuccess(tl: EnvironmentTimeline): EnvDeployment | null {
  const activeIdx = tl.deployments.findIndex((d) => d.active)
  if (activeIdx < 0) return null
  return tl.deployments.slice(activeIdx + 1).find((d) => d.status === 'success') ?? null
}

/** Whether an environment can be rolled back: it has an active version and a prior successful deployment. */
export function canRollback(tl: EnvironmentTimeline): boolean {
  return previousSuccess(tl) !== null
}

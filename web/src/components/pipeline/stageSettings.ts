/**
 * Pure helpers for the stage settings editor (Epic 8 · 8-4 gate / 8-5 when).
 *
 * Kept framework-free and side-effect-free so the parsing / toggling logic is
 * unit-testable in isolation (mirrors stageDeps.ts). The Vue component owns only
 * the markup and event wiring; all branch-glob / event-set math lives here.
 */

import type { StageWhen } from '../../api/pipeline'

/** Trigger event types a stage's `when` can gate on (backend枚举 in stage_when.go). */
export const WHEN_EVENTS = ['manual', 'webhook', 'schedule'] as const
export type WhenEvent = (typeof WHEN_EVENTS)[number]

/** Human label for each trigger event (UI display only). */
export const WHEN_EVENT_LABELS: Record<WhenEvent, string> = {
  manual: '手动',
  webhook: 'Webhook',
  schedule: '定时',
}

/**
 * Parse a free-text branch field into a clean glob list.
 * Splits on commas / whitespace / newlines, trims, and drops empties + dups
 * (order-preserving). Empty input → [] (meaning "any branch").
 */
export function parseBranches(text: string): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of text.split(/[\s,]+/)) {
    const b = raw.trim()
    if (b && !seen.has(b)) {
      seen.add(b)
      out.push(b)
    }
  }
  return out
}

/** Render a branch glob list back to the editable text field (space-joined). */
export function branchesToText(branches: string[] | undefined): string {
  return (branches ?? []).join(' ')
}

/**
 * Toggle a single event in the `when.events` set (order follows WHEN_EVENTS).
 * Returns a new array; toggling the last-on event off yields [] (= "any event").
 */
export function toggleWhenEvent(events: string[] | undefined, ev: WhenEvent): string[] {
  const set = new Set(events ?? [])
  if (set.has(ev)) {
    set.delete(ev)
  } else {
    set.add(ev)
  }
  return WHEN_EVENTS.filter((e) => set.has(e))
}

/**
 * Normalize an edited `when` into the wire shape, or `undefined` when empty
 * (no branch/event constraint = "always run", so we drop the key entirely to
 * keep the saved spec minimal).
 */
export function normalizeWhen(branches: string[], events: string[]): StageWhen | undefined {
  const w: StageWhen = {}
  if (branches.length) w.branches = branches
  if (events.length) w.events = events
  return w.branches || w.events ? w : undefined
}

/** Report whether a `when` rule actually constrains anything (for badges/labels). */
export function hasWhen(when: StageWhen | undefined): boolean {
  return Boolean((when?.branches?.length ?? 0) || (when?.events?.length ?? 0))
}

/** Short human summary of a when rule for chip display (empty = ""). */
export function whenSummary(when: StageWhen | undefined): string {
  if (!hasWhen(when)) return ''
  const parts: string[] = []
  if (when?.branches?.length) parts.push(when.branches.join(', '))
  if (when?.events?.length) {
    parts.push(when.events.map((e) => WHEN_EVENT_LABELS[e as WhenEvent] ?? e).join('/'))
  }
  return parts.join(' · ')
}

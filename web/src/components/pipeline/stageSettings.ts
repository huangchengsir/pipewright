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

// ─── Matrix build axes (P1) ──────────────────────────────────────────────────
//
// UI editor uses one textarea, one axis per line: `axisName: v1, v2, v3`
// (or `axisName = v1, v2`). Empty/blank lines are ignored. Axis names must be
// identifiers (matches backend stage_matrix.go isValidMatrixAxis); values split
// on commas, trimmed, de-duped. Backend re-validates (caps cell count etc.).

/** Cap on cells (cartesian product) — mirrors backend MatrixMaxCells for client-side preview. */
export const MATRIX_MAX_CELLS = 50
/** Cap on axes — mirrors backend MatrixMaxAxes. */
export const MATRIX_MAX_AXES = 8

const AXIS_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/

/** Report whether an axis name is a valid identifier (mirrors backend). */
export function isValidAxisName(name: string): boolean {
  return AXIS_NAME_RE.test(name)
}

/**
 * Parse the matrix textarea into an axes map. One axis per line: `name: a, b, c`.
 * Trims + de-dups values, drops empty values/lines. Returns `undefined` when no
 * usable axes (so the saved spec stays minimal / single-stage behavior).
 */
export function parseMatrix(text: string): Record<string, string[]> | undefined {
  const out: Record<string, string[]> = {}
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    const sep = line.search(/[:=]/)
    if (sep <= 0) continue
    const name = line.slice(0, sep).trim()
    if (!name) continue
    const seen = new Set<string>()
    const vals: string[] = []
    for (const raw of line.slice(sep + 1).split(',')) {
      const v = raw.trim()
      if (v && !seen.has(v)) {
        seen.add(v)
        vals.push(v)
      }
    }
    if (vals.length) out[name] = vals
  }
  return Object.keys(out).length ? out : undefined
}

/** Render an axes map back to the editable textarea (one `name: a, b` per line). */
export function matrixToText(matrix: Record<string, string[]> | undefined): string {
  if (!matrix) return ''
  return Object.keys(matrix)
    .map((name) => `${name}: ${(matrix[name] ?? []).join(', ')}`)
    .join('\n')
}

/** Report whether a stage actually declares a matrix (for badges/labels). */
export function hasMatrix(matrix: Record<string, string[]> | undefined): boolean {
  return Boolean(matrix && Object.keys(matrix).length)
}

/** Cell count = cartesian product of all axis value counts (0 when no axes). */
export function matrixCellCount(matrix: Record<string, string[]> | undefined): number {
  if (!hasMatrix(matrix)) return 0
  let cells = 1
  for (const name of Object.keys(matrix!)) {
    cells *= (matrix![name] ?? []).length
  }
  return cells
}

/**
 * Validate parsed axes client-side (mirrors backend rules for fast feedback):
 * returns an error message, or `null` when valid / empty.
 */
export function matrixError(matrix: Record<string, string[]> | undefined): string | null {
  if (!hasMatrix(matrix)) return null
  const names = Object.keys(matrix!)
  for (const name of names) {
    if (!isValidAxisName(name)) {
      return `轴名「${name}」非法(须为标识符:字母/下划线起)`
    }
    if (!(matrix![name] ?? []).length) {
      return `轴「${name}」至少需要一个值`
    }
  }
  if (names.length > MATRIX_MAX_AXES) {
    return `矩阵维度 ${names.length} 超过上限 ${MATRIX_MAX_AXES}`
  }
  const cells = matrixCellCount(matrix)
  if (cells > MATRIX_MAX_CELLS) {
    return `矩阵展开 ${cells} 个 cell,超过上限 ${MATRIX_MAX_CELLS}`
  }
  return null
}

/** Short chip summary, e.g. `go×2 · os×1 → 2 cell` (empty when no matrix). */
export function matrixSummary(matrix: Record<string, string[]> | undefined): string {
  if (!hasMatrix(matrix)) return ''
  const parts = Object.keys(matrix!).map((n) => `${n}×${(matrix![n] ?? []).length}`)
  return `${parts.join(' · ')} → ${matrixCellCount(matrix)} cell`
}

/**
 * Pure helpers for ConcurrencyPanel UI logic — tested in concurrency.helpers.test.ts.
 */

/** Valid range for maxConcurrent (inclusive). 0 means unlimited. */
export const CONCURRENCY_MIN = 0
export const CONCURRENCY_MAX = 64

/**
 * Validate a raw maxConcurrent value.
 * Returns an error string, or '' if valid.
 */
export function validateConcurrency(value: number): string {
  if (!Number.isInteger(value)) return '并发上限须为整数'
  if (value < CONCURRENCY_MIN) return `并发上限不能小于 ${CONCURRENCY_MIN}`
  if (value > CONCURRENCY_MAX) return `并发上限不能超过 ${CONCURRENCY_MAX}`
  return ''
}

/**
 * Format the concurrency value for display.
 * 0 is shown as "不限", positive integers as their numeric string.
 */
export function formatConcurrency(value: number): string {
  if (value === 0) return '不限'
  return String(value)
}

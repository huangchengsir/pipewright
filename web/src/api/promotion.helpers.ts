/**
 * Pure helpers for promotion UI logic — tested in promotion.helpers.test.ts.
 */

import type { PromotionStatus } from './promotion'
import { t } from '../i18n'

// ─── Status display ───────────────────────────────────────────────────────────

export interface PromotionStatusConfig {
  label: string
  /** CSS var token for the status dot / text color. */
  color: string
  /** CSS var token for the soft background. */
  bg: string
  /** CSS var token for the border line. */
  border: string
}

export function promotionStatusConfig(status: PromotionStatus): PromotionStatusConfig {
  switch (status) {
    case 'promoted':
      return {
        label: t('labels.promotionPromoted'),
        color: 'var(--color-green)',
        bg: 'var(--color-green-soft)',
        border: 'var(--color-green-line)',
      }
    case 'pending':
      return {
        label: t('labels.promotionPending'),
        color: 'var(--color-amber)',
        bg: 'var(--color-amber-soft)',
        border: 'var(--color-amber-line)',
      }
    case 'rejected':
      return {
        label: t('labels.promotionRejected'),
        color: 'var(--color-red)',
        bg: 'var(--color-red-soft)',
        border: 'var(--color-red-line)',
      }
  }
}

// ─── Date formatting ──────────────────────────────────────────────────────────

/**
 * Format an ISO date string to a short locale string.
 * Returns '—' for empty/null/invalid dates.
 */
export function formatPromotionDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// ─── Environment chain helpers ────────────────────────────────────────────────

/**
 * Validate an environment name: non-empty, no leading/trailing whitespace,
 * only alphanumeric, dash, underscore.
 * Returns an error string, or '' if valid.
 */
export function validateEnvName(name: string): string {
  const n = name.trim()
  if (!n) return t('labels.envNameEmpty')
  if (!/^[\w-]+$/.test(n)) return t('labels.envNameInvalid')
  if (n.length > 64) return t('labels.envNameTooLong')
  return ''
}

/**
 * Check an ordered array of environment names for duplicates.
 * Returns the first duplicate name found, or null if no duplicates.
 */
export function findDuplicateEnvName(names: string[]): string | null {
  const seen = new Set<string>()
  for (const n of names) {
    const key = n.trim().toLowerCase()
    if (seen.has(key)) return n.trim()
    seen.add(key)
  }
  return null
}

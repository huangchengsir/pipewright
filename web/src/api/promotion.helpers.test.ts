import { describe, it, expect } from 'vitest'
import {
  promotionStatusConfig,
  formatPromotionDate,
  validateEnvName,
  findDuplicateEnvName,
} from './promotion.helpers'

describe('promotionStatusConfig', () => {
  it('returns green tokens for promoted', () => {
    const cfg = promotionStatusConfig('promoted')
    expect(cfg.label).toBe('已晋级')
    expect(cfg.color).toContain('green')
    expect(cfg.bg).toContain('green')
  })

  it('returns amber tokens for pending', () => {
    const cfg = promotionStatusConfig('pending')
    expect(cfg.label).toBe('待审批')
    expect(cfg.color).toContain('amber')
  })

  it('returns red tokens for rejected', () => {
    const cfg = promotionStatusConfig('rejected')
    expect(cfg.label).toBe('已拒绝')
    expect(cfg.color).toContain('red')
  })
})

describe('formatPromotionDate', () => {
  it('returns em-dash for empty string', () => {
    expect(formatPromotionDate('')).toBe('—')
  })

  it('returns em-dash for null', () => {
    expect(formatPromotionDate(null)).toBe('—')
  })

  it('returns em-dash for undefined', () => {
    expect(formatPromotionDate(undefined)).toBe('—')
  })

  it('returns em-dash for invalid date string', () => {
    expect(formatPromotionDate('not-a-date')).toBe('—')
  })

  it('returns a non-empty formatted string for a valid ISO date', () => {
    const result = formatPromotionDate('2026-05-31T10:30:00Z')
    expect(result).not.toBe('—')
    expect(typeof result).toBe('string')
    expect(result.length).toBeGreaterThan(0)
  })
})

describe('validateEnvName', () => {
  it('returns error for empty name', () => {
    expect(validateEnvName('')).not.toBe('')
    expect(validateEnvName('  ')).not.toBe('')
  })

  it('returns empty string for valid names', () => {
    expect(validateEnvName('dev')).toBe('')
    expect(validateEnvName('staging')).toBe('')
    expect(validateEnvName('prod')).toBe('')
    expect(validateEnvName('prod-01')).toBe('')
    expect(validateEnvName('my_env')).toBe('')
  })

  it('returns error for names with special chars', () => {
    expect(validateEnvName('dev env')).not.toBe('')
    expect(validateEnvName('dev.env')).not.toBe('')
    expect(validateEnvName('dev/env')).not.toBe('')
  })

  it('returns error for names exceeding 64 chars', () => {
    expect(validateEnvName('a'.repeat(65))).not.toBe('')
  })

  it('accepts names up to 64 chars', () => {
    expect(validateEnvName('a'.repeat(64))).toBe('')
  })
})

describe('findDuplicateEnvName', () => {
  it('returns null for empty array', () => {
    expect(findDuplicateEnvName([])).toBeNull()
  })

  it('returns null for unique names', () => {
    expect(findDuplicateEnvName(['dev', 'staging', 'prod'])).toBeNull()
  })

  it('returns the first duplicate name', () => {
    expect(findDuplicateEnvName(['dev', 'staging', 'dev'])).toBe('dev')
  })

  it('is case-insensitive for duplicate detection', () => {
    expect(findDuplicateEnvName(['Dev', 'Staging', 'dev'])).not.toBeNull()
  })

  it('returns null for single-item array', () => {
    expect(findDuplicateEnvName(['prod'])).toBeNull()
  })
})

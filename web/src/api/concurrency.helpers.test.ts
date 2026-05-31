import { describe, it, expect } from 'vitest'
import {
  validateConcurrency,
  formatConcurrency,
  CONCURRENCY_MIN,
  CONCURRENCY_MAX,
} from './concurrency.helpers'

describe('validateConcurrency', () => {
  it('accepts 0 (unlimited)', () => {
    expect(validateConcurrency(0)).toBe('')
  })

  it('accepts 1', () => {
    expect(validateConcurrency(1)).toBe('')
  })

  it('accepts the maximum value', () => {
    expect(validateConcurrency(CONCURRENCY_MAX)).toBe('')
  })

  it('accepts mid-range values', () => {
    expect(validateConcurrency(10)).toBe('')
    expect(validateConcurrency(32)).toBe('')
  })

  it('rejects negative values', () => {
    expect(validateConcurrency(-1)).not.toBe('')
  })

  it('rejects values exceeding the maximum', () => {
    expect(validateConcurrency(CONCURRENCY_MAX + 1)).not.toBe('')
    expect(validateConcurrency(100)).not.toBe('')
  })

  it('rejects non-integer values', () => {
    expect(validateConcurrency(1.5)).not.toBe('')
    expect(validateConcurrency(0.1)).not.toBe('')
  })

  it('CONCURRENCY_MIN is 0', () => {
    expect(CONCURRENCY_MIN).toBe(0)
  })

  it('CONCURRENCY_MAX is 64', () => {
    expect(CONCURRENCY_MAX).toBe(64)
  })
})

describe('formatConcurrency', () => {
  it('formats 0 as "不限"', () => {
    expect(formatConcurrency(0)).toBe('不限')
  })

  it('formats positive numbers as their string', () => {
    expect(formatConcurrency(1)).toBe('1')
    expect(formatConcurrency(10)).toBe('10')
    expect(formatConcurrency(64)).toBe('64')
  })
})

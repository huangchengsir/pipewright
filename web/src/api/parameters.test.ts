import { describe, it, expect } from 'vitest'
import { validateParamValues, type ParamDef } from './parameters'

const defs: ParamDef[] = [
  { key: 'env', label: '环境', type: 'choice', default: 'prod', options: ['prod', 'staging'], required: false },
  { key: 'ver', label: '版本', type: 'string', default: '1.0', required: false },
  { key: 'force', label: '强制', type: 'boolean', default: 'false', required: false },
  { key: 'count', label: '数量', type: 'number', default: '3', required: false },
  { key: 'token', label: '令牌', type: 'string', default: '', required: true },
]

describe('validateParamValues', () => {
  it('passes when required filled + values valid (defaults count)', () => {
    expect(validateParamValues(defs, { token: 'abc' })).toBe('')
  })

  it('flags missing required', () => {
    expect(validateParamValues(defs, {})).toContain('令牌')
  })

  it('flags number type mismatch', () => {
    expect(validateParamValues(defs, { token: 'x', count: 'abc' })).toContain('数量')
  })

  it('flags boolean type mismatch', () => {
    expect(validateParamValues(defs, { token: 'x', force: 'maybe' })).toContain('强制')
  })

  it('flags choice out of range', () => {
    expect(validateParamValues(defs, { token: 'x', env: 'dev' })).toContain('环境')
  })

  it('empty provided falls back to default (valid)', () => {
    expect(validateParamValues(defs, { token: 'x', env: '' })).toBe('')
  })

  it('no defs → always valid', () => {
    expect(validateParamValues([], { anything: 'x' })).toBe('')
  })
})

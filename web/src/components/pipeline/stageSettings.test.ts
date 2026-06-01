import { describe, it, expect } from 'vitest'
import {
  parseBranches,
  branchesToText,
  toggleWhenEvent,
  normalizeWhen,
  hasWhen,
  whenSummary,
  parseMatrix,
  matrixToText,
  hasMatrix,
  matrixCellCount,
  matrixError,
  matrixSummary,
  isValidAxisName,
  MATRIX_MAX_CELLS,
  hasPost,
  postSummary,
  hasServices,
  servicesSummary,
  envToText,
  parseEnvText,
} from './stageSettings'

describe('parseBranches', () => {
  it('splits on commas / whitespace / newlines and trims', () => {
    expect(parseBranches('main, develop\nrelease/*  hotfix')).toEqual([
      'main',
      'develop',
      'release/*',
      'hotfix',
    ])
  })

  it('drops empties and duplicates (order-preserving)', () => {
    expect(parseBranches('main,,main,  , develop ,main')).toEqual(['main', 'develop'])
  })

  it('returns [] for blank input', () => {
    expect(parseBranches('   \n  ')).toEqual([])
  })
})

describe('branchesToText', () => {
  it('joins with spaces and tolerates undefined', () => {
    expect(branchesToText(['main', 'release/*'])).toBe('main release/*')
    expect(branchesToText(undefined)).toBe('')
  })

  it('round-trips with parseBranches', () => {
    const arr = ['main', 'develop', 'release/*']
    expect(parseBranches(branchesToText(arr))).toEqual(arr)
  })
})

describe('toggleWhenEvent', () => {
  it('adds an event in canonical order', () => {
    expect(toggleWhenEvent(['schedule'], 'manual')).toEqual(['manual', 'schedule'])
  })

  it('removes an already-present event', () => {
    expect(toggleWhenEvent(['manual', 'webhook'], 'manual')).toEqual(['webhook'])
  })

  it('toggling the last event off yields []', () => {
    expect(toggleWhenEvent(['webhook'], 'webhook')).toEqual([])
  })

  it('tolerates undefined input', () => {
    expect(toggleWhenEvent(undefined, 'manual')).toEqual(['manual'])
  })
})

describe('normalizeWhen', () => {
  it('drops empty keys and returns undefined when no constraint', () => {
    expect(normalizeWhen([], [])).toBeUndefined()
  })

  it('keeps only the non-empty constraints', () => {
    expect(normalizeWhen(['main'], [])).toEqual({ branches: ['main'] })
    expect(normalizeWhen([], ['manual'])).toEqual({ events: ['manual'] })
    expect(normalizeWhen(['main'], ['webhook'])).toEqual({
      branches: ['main'],
      events: ['webhook'],
    })
  })
})

describe('hasWhen / whenSummary', () => {
  it('hasWhen reflects whether anything is constrained', () => {
    expect(hasWhen(undefined)).toBe(false)
    expect(hasWhen({})).toBe(false)
    expect(hasWhen({ branches: [] })).toBe(false)
    expect(hasWhen({ branches: ['main'] })).toBe(true)
    expect(hasWhen({ events: ['manual'] })).toBe(true)
  })

  it('whenSummary renders branches and localized events', () => {
    expect(whenSummary(undefined)).toBe('')
    expect(whenSummary({ branches: ['main', 'develop'] })).toBe('main, develop')
    expect(whenSummary({ events: ['manual', 'schedule'] })).toBe('手动/定时')
    expect(whenSummary({ branches: ['main'], events: ['webhook'] })).toBe('main · Webhook')
  })
})

describe('parseMatrix / matrixToText', () => {
  it('parses one axis per line (name: a, b)', () => {
    const m = parseMatrix('go: 1.21, 1.22\nos: linux')
    expect(m).toEqual({ go: ['1.21', '1.22'], os: ['linux'] })
  })

  it('accepts = separator and trims + de-dups + drops empties', () => {
    const m = parseMatrix('go = 1.21 , 1.22, 1.21 ,\n\n  \nos: linux')
    expect(m).toEqual({ go: ['1.21', '1.22'], os: ['linux'] })
  })

  it('returns undefined when no usable axes', () => {
    expect(parseMatrix('')).toBeUndefined()
    expect(parseMatrix('   \n  ')).toBeUndefined()
    expect(parseMatrix('justname')).toBeUndefined()
    expect(parseMatrix('go:')).toBeUndefined()
  })

  it('round-trips through matrixToText', () => {
    const m = { go: ['1.21', '1.22'], os: ['linux'] }
    expect(matrixToText(m)).toBe('go: 1.21, 1.22\nos: linux')
    expect(parseMatrix(matrixToText(m))).toEqual(m)
  })

  it('matrixToText of undefined is empty', () => {
    expect(matrixToText(undefined)).toBe('')
  })
})

describe('matrix derived helpers', () => {
  it('hasMatrix reflects presence', () => {
    expect(hasMatrix(undefined)).toBe(false)
    expect(hasMatrix({})).toBe(false)
    expect(hasMatrix({ go: ['1.21'] })).toBe(true)
  })

  it('matrixCellCount is the cartesian product', () => {
    expect(matrixCellCount(undefined)).toBe(0)
    expect(matrixCellCount({ go: ['1.21', '1.22'], os: ['linux'] })).toBe(2)
    expect(matrixCellCount({ a: ['1', '2'], b: ['x', 'y'] })).toBe(4)
  })

  it('matrixSummary renders axis counts and total cells', () => {
    expect(matrixSummary(undefined)).toBe('')
    expect(matrixSummary({ go: ['1.21', '1.22'], os: ['linux'] })).toBe('go×2 · os×1 → 2 cell')
  })

  it('isValidAxisName mirrors backend identifier rule', () => {
    expect(isValidAxisName('go')).toBe(true)
    expect(isValidAxisName('go_1')).toBe(true)
    expect(isValidAxisName('1go')).toBe(false)
    expect(isValidAxisName('go-v')).toBe(false)
    expect(isValidAxisName('go.v')).toBe(false)
  })
})

describe('matrixError', () => {
  it('null for empty / valid matrices', () => {
    expect(matrixError(undefined)).toBeNull()
    expect(matrixError({ go: ['1.21', '1.22'], os: ['linux'] })).toBeNull()
  })

  it('flags invalid axis names', () => {
    expect(matrixError({ '1bad': ['x'] })).toMatch(/非法/)
  })

  it('flags cell explosion over the cap', () => {
    const vals = Array.from({ length: MATRIX_MAX_CELLS + 1 }, (_, i) => `v${i}`)
    expect(matrixError({ v: vals })).toMatch(/超过上限/)
  })

  it('allows exactly the cap', () => {
    const vals = Array.from({ length: MATRIX_MAX_CELLS }, (_, i) => `v${i}`)
    expect(matrixError({ v: vals })).toBeNull()
  })
})

describe('post + services helpers (P1 canvas editors)', () => {
  it('hasPost / postSummary', () => {
    expect(hasPost(undefined)).toBe(false)
    expect(hasPost([])).toBe(false)
    expect(postSummary([{ condition: 'always', image: 'x', commands: ['c'] }])).toBe('post×1')
  })
  it('hasServices / servicesSummary', () => {
    expect(hasServices(undefined)).toBe(false)
    expect(servicesSummary([{ name: 'testdb', image: 'postgres' }, { name: 'redis', image: 'redis' }])).toBe('svc: testdb, redis')
  })
  it('envToText / parseEnvText round-trip + drops invalid', () => {
    expect(envToText(['A=1', 'B=2'])).toBe('A=1, B=2')
    expect(parseEnvText('A=1, B=2 ,  , bad')).toEqual(['A=1', 'B=2'])
  })
})

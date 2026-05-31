import { describe, it, expect } from 'vitest'
import {
  parseBranches,
  branchesToText,
  toggleWhenEvent,
  normalizeWhen,
  hasWhen,
  whenSummary,
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

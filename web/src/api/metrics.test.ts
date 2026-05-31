import { describe, it, expect } from 'vitest'
import { bandLabel, formatDuration, formatFrequency, formatPercent } from './metrics'

describe('bandLabel', () => {
  it('maps each band to Chinese copy', () => {
    expect(bandLabel('elite')).toBe('精英')
    expect(bandLabel('high')).toBe('高效')
    expect(bandLabel('medium')).toBe('中等')
    expect(bandLabel('low')).toBe('待改进')
    expect(bandLabel('none')).toBe('暂无数据')
  })
})

describe('formatDuration', () => {
  it('returns dash for invalid input', () => {
    expect(formatDuration(-1)).toBe('—')
    expect(formatDuration(NaN)).toBe('—')
  })
  it('formats seconds / minutes / hours / days', () => {
    expect(formatDuration(30)).toBe('30 秒')
    expect(formatDuration(90)).toBe('1.5 分钟')
    expect(formatDuration(3600)).toBe('1 小时')
    expect(formatDuration(5400)).toBe('1.5 小时')
    expect(formatDuration(86400)).toBe('1 天')
    expect(formatDuration(129600)).toBe('1.5 天')
  })
})

describe('formatFrequency', () => {
  it('returns dash for non-positive input', () => {
    expect(formatFrequency(0)).toBe('—')
    expect(formatFrequency(-2)).toBe('—')
  })
  it('formats per-day / per-week / per-month cadence', () => {
    expect(formatFrequency(2)).toBe('2 次/天')
    expect(formatFrequency(0.5)).toBe('3.5 次/周') // 0.5/day → 3.5/week
    expect(formatFrequency(1 / 30)).toBe('1 次/月') // 1/30 day → 1/month
  })
})

describe('formatPercent', () => {
  it('returns dash for invalid input', () => {
    expect(formatPercent(-0.1)).toBe('—')
    expect(formatPercent(NaN)).toBe('—')
  })
  it('formats ratio as percentage', () => {
    expect(formatPercent(0)).toBe('0%')
    expect(formatPercent(0.25)).toBe('25%')
    expect(formatPercent(0.333)).toBe('33.3%')
    expect(formatPercent(1)).toBe('100%')
  })
})

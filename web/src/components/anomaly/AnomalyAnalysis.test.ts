import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AnomalyAnalysis from './AnomalyAnalysis.vue'
import type { AnomalyAnalysis as Analysis } from '../../api/anomaly'
import { setLocale, type LocaleCode } from '../../i18n'

let wrapper: VueWrapper | undefined
const snapshot = (status: Analysis['status'] = 'ready'): Analysis => ({
  status, reason: '', hypothesis: 'CPU contention is possible', confidence: 'medium',
  alternateCauses: ['A scheduled task'], fixSuggestions: ['Inspect CPU limits'],
  evidence: [{ line: 2, text: 'worker cpu=95%' }], generatedAt: '2026-10-14T00:00:02Z',
})
beforeEach(() => setLocale('en'))
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  setLocale('zh-CN')
})

describe('read-only anomaly analysis', () => {
  it('renders nothing for an alert without analysis', () => {
    wrapper = mount(AnomalyAnalysis, { props: { analysis: null } })
    expect(wrapper.find('section').exists()).toBe(false)
  })

  it('shows ready hypotheses, confidence, suggestions, evidence and generation time', () => {
    wrapper = mount(AnomalyAnalysis, { props: { analysis: snapshot() } })
    expect(wrapper.get('[role="status"]').text()).toBe('Analysis ready')
    expect(wrapper.text()).toContain('hypotheses, not verified facts')
    expect(wrapper.text()).toContain('CPU contention is possible')
    expect(wrapper.text()).toContain('medium')
    expect(wrapper.text()).toContain('A scheduled task')
    expect(wrapper.text()).toContain('Inspect CPU limits')
    expect(wrapper.text()).toContain('Line 2')
    expect(wrapper.get('code').text()).toBe('worker cpu=95%')
    expect(wrapper.get('time').attributes('datetime')).toBe('2026-10-14T00:00:02Z')
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it.each([
    ['pending', 'Analysis pending', 'true'],
    ['unavailable', 'Analysis unavailable', 'false'],
  ] as const)('distinguishes %s and displays its reason without presenting a hypothesis', (status, label, busy) => {
    wrapper = mount(AnomalyAnalysis, { props: { analysis: { ...snapshot(status), reason: 'Collector data missing', generatedAt: '' } } })
    expect(wrapper.get('[role="status"]').text()).toBe(label)
    expect(wrapper.get('section').attributes('aria-busy')).toBe(busy)
    expect(wrapper.text()).toContain('Collector data missing')
    expect(wrapper.text()).not.toContain('CPU contention is possible')
    expect(wrapper.find('time').exists()).toBe(false)
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it.each(['pending', 'ready', 'unavailable'] as const)('escapes all model content in %s, including reason and invalid time', (status) => {
    const payload = '<img src=x onerror="alert(1)"><script>alert(2)</script>[run](javascript:alert(3))'
    wrapper = mount(AnomalyAnalysis, { props: { analysis: {
      status, reason: payload, hypothesis: payload, confidence: payload,
      alternateCauses: [payload], fixSuggestions: [payload],
      evidence: [{ line: 7, text: payload }], generatedAt: payload,
    } } })
    expect(wrapper.text()).toContain(payload)
    expect(wrapper.html()).toContain('&lt;img')
    expect(wrapper.find('img, script, a, iframe, button').exists()).toBe(false)
    if (status === 'ready') expect(wrapper.get('code').text()).toBe(payload)
  })

  it('omits empty optional sections', () => {
    wrapper = mount(AnomalyAnalysis, { props: { analysis: {
      ...snapshot(), hypothesis: '', confidence: '', alternateCauses: [], fixSuggestions: [], evidence: [], generatedAt: '',
    } } })
    expect(wrapper.find('h4, ul, time').exists()).toBe(false)
    expect(wrapper.text()).toContain('hypotheses, not verified facts')
  })

  it.each<LocaleCode>(['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'es', 'fr', 'de'])('renders localized analysis labels in %s', (locale) => {
    setLocale(locale)
    wrapper = mount(AnomalyAnalysis, { props: { analysis: snapshot() } })
    expect(wrapper.text()).not.toContain('anomaly.')
    expect(wrapper.get('[role="status"]').text()).not.toBe('')
  })
})

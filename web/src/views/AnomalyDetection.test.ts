import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AnomalyDetection from './AnomalyDetection.vue'
import type { AnomalyAlert, AnomalyAnalysis, AnomalyRule } from '../api/anomaly'
import { setLocale } from '../i18n'

const api = vi.hoisted(() => ({
  rules: vi.fn(), create: vi.fn(), update: vi.fn(), delete: vi.fn(),
  check: vi.fn(), alerts: vi.fn(), config: vi.fn(), metrics: vi.fn(), servers: vi.fn(),
}))
vi.mock('../api/anomaly', () => ({
  listAnomalyRules: api.rules, createAnomalyRule: api.create, updateAnomalyRule: api.update,
  deleteAnomalyRule: api.delete, checkAnomaly: api.check, listAnomalyAlerts: api.alerts,
  getAnomalyConfig: api.config, getMetricsHistory: api.metrics,
}))
vi.mock('../api/servers', () => ({ listServers: api.servers }))

let wrapper: VueWrapper | undefined
let visibility: DocumentVisibilityState
const rule = (overrides: Partial<AnomalyRule> = {}): AnomalyRule => ({
  id: 'rule-1', metric: 'cpu', operator: 'gt', threshold: 90, serverId: null,
  enabled: true, aiAnalysisEnabled: false, createdAt: '2026-10-14T00:00:00Z', ...overrides,
})
const analysis = (status: AnomalyAnalysis['status']): AnomalyAnalysis => ({
  status, reason: status === 'unavailable' ? 'Model unavailable' : '',
  hypothesis: 'Possible contention', confidence: 'medium', alternateCauses: [],
  fixSuggestions: ['Review CPU limits'], evidence: [{ line: 1, text: 'cpu=95%' }], generatedAt: '',
})
const alert = (status: AnomalyAnalysis['status'] | null): AnomalyAlert => ({
  id: 'alert-1', serverId: 'server-1', serverName: 'Server', metric: 'cpu', operator: 'gt',
  threshold: 90, value: 95, message: 'Original alert <img src=x onerror=alert(1)>',
  at: '2026-10-14T00:00:00Z', aiAnalysisEnabled: status !== null,
  analysis: status ? analysis(status) : null,
})

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  setLocale('en')
  visibility = 'visible'
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
  api.rules.mockResolvedValue([rule()])
  api.servers.mockResolvedValue([{ id: 'server-1', name: 'Server' }])
  api.alerts.mockResolvedValue([])
  api.config.mockResolvedValue({ intervalSeconds: 0, cooldownSeconds: 300 })
  api.metrics.mockResolvedValue([])
  api.create.mockImplementation(async (input) => rule({ ...input, id: 'created' }))
  api.update.mockImplementation(async (id, input) => rule({ ...input, id }))
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  vi.useRealTimers()
  setLocale('zh-CN')
})
async function setup(): Promise<VueWrapper> {
  wrapper = mount(AnomalyDetection, { global: { stubs: { MetricTrendChart: true } } })
  await flushPromises()
  return wrapper
}
function setVisibility(state: DocumentVisibilityState): void {
  visibility = state
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('anomaly rule AI settings', () => {
  it('defaults AI off and creates a rule with explicit settings', async () => {
    const w = await setup()
    const boxes = w.findAll('.rule-form input[type="checkbox"]')
    expect((boxes[0]!.element as HTMLInputElement).checked).toBe(true)
    expect((boxes[1]!.element as HTMLInputElement).checked).toBe(false)
    expect(w.get(`#${boxes[1]!.attributes('aria-describedby')}`).text()).toContain('Credentials are excluded')
    expect(w.get('.rule-item__ai').text()).toBe('AI analysis off')
    await w.get('.rule-form').trigger('submit')
    await flushPromises()
    expect(api.create).toHaveBeenCalledExactlyOnceWith({
      metric: 'cpu', operator: 'gt', threshold: 90, serverId: null, enabled: true, aiAnalysisEnabled: false,
    })
    expect(api.check).not.toHaveBeenCalled()
  })

  it('allows AI opt-in on a paused new rule, then resets AI off for the next rule', async () => {
    const confirm = vi.spyOn(window, 'confirm')
    const w = await setup()
    const boxes = w.findAll('.rule-form input[type="checkbox"]')
    await boxes[0]!.setValue(false)
    await boxes[1]!.setValue(true)
    await w.get('#rule-server').setValue('server-1')
    await w.get('.rule-form').trigger('submit')
    await flushPromises()
    expect(api.create).toHaveBeenCalledWith(expect.objectContaining({
      enabled: false, aiAnalysisEnabled: true, serverId: 'server-1',
    }))
    expect(w.get('.rule-item__ai').text()).toBe('AI analysis on')
    expect((boxes[1]!.element as HTMLInputElement).checked).toBe(false)
    expect(confirm).not.toHaveBeenCalled()
    expect(api.check).not.toHaveBeenCalled()
  })

  it('preserves both AI opt-in and a disabled rule when editing its threshold', async () => {
    api.rules.mockResolvedValue([rule({ enabled: false, aiAnalysisEnabled: true })])
    const w = await setup()
    await w.get('.rule-item__actions button').trigger('click')
    const boxes = w.findAll('.rule-edit input[type="checkbox"]')
    expect((boxes[0]!.element as HTMLInputElement).checked).toBe(false)
    expect((boxes[1]!.element as HTMLInputElement).checked).toBe(true)
    expect(w.get(`#${boxes[1]!.attributes('aria-describedby')}`).text()).toContain('configured model')
    await w.get('.rule-edit__threshold').setValue(80)
    await w.get('.rule-edit').trigger('submit')
    await flushPromises()
    expect(api.update).toHaveBeenCalledExactlyOnceWith('rule-1', {
      metric: 'cpu', operator: 'gt', threshold: 80, serverId: null, enabled: false, aiAnalysisEnabled: true,
    })
    expect(w.get('.rule-item__disabled').text()).toBe('Disabled')
  })

  it('treats a legacy rule with no AI setting as off during editing', async () => {
    const { aiAnalysisEnabled: _omitted, ...legacy } = rule({ enabled: false })
    api.rules.mockResolvedValue([legacy])
    const w = await setup()
    expect(w.get('.rule-item__ai').text()).toBe('AI analysis off')
    await w.get('.rule-item__actions button').trigger('click')
    await w.get('.rule-edit').trigger('submit')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith('rule-1', expect.objectContaining({ enabled: false, aiAnalysisEnabled: false }))
  })

  it('serializes explicit enable and AI changes in edit mode', async () => {
    api.rules.mockResolvedValue([rule({ enabled: false, aiAnalysisEnabled: true })])
    const w = await setup()
    await w.get('.rule-item__actions button').trigger('click')
    const boxes = w.findAll('.rule-edit input[type="checkbox"]')
    await boxes[0]!.setValue(true)
    await boxes[1]!.setValue(false)
    await w.get('.rule-edit').trigger('submit')
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith('rule-1', expect.objectContaining({ enabled: true, aiAnalysisEnabled: false }))
  })

  it('cancels editing without mutating the saved settings', async () => {
    const saved = rule({ enabled: false, aiAnalysisEnabled: true })
    api.rules.mockResolvedValue([saved])
    const w = await setup()
    await w.get('.rule-item__actions button').trigger('click')
    await w.findAll('.rule-edit input[type="checkbox"]')[1]!.setValue(false)
    await w.get('.rule-edit__actions button[type="button"]').trigger('click')
    expect(saved.aiAnalysisEnabled).toBe(true)
    expect(api.update).not.toHaveBeenCalled()
    expect(w.get('.rule-item__ai').text()).toBe('AI analysis on')
  })

  it.each(['pending', 'ready', 'unavailable'] as const)('keeps the original alert before its %s analysis', async (status) => {
    api.alerts.mockResolvedValue([alert(status)])
    const w = await setup()
    const body = w.get('.alert-item__body')
    expect(body.get('.alert-item__msg').text()).toContain('Original alert <img')
    expect(body.get('.analysis').classes()).toContain(`analysis--${status}`)
    expect(body.element.children[0]!.className).toBe('alert-item__msg')
    expect(body.find('img, script, button').exists()).toBe(false)
  })
})

describe('pending analysis polling', () => {
  it('polls only alerts after 4s, replaces the snapshot and stops after ready', async () => {
    const pending = Object.freeze(alert('pending'))
    api.alerts.mockResolvedValueOnce([pending]).mockResolvedValue([alert('ready')])
    api.config.mockResolvedValue({ intervalSeconds: 60, cooldownSeconds: 300 })
    const w = await setup()
    expect(api.alerts).toHaveBeenCalledTimes(1)
    expect(api.metrics).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(3999)
    expect(api.alerts).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(w.get('.analysis').classes()).toContain('analysis--ready')
    expect(pending.analysis?.status).toBe('pending')
    await vi.advanceTimersByTimeAsync(12000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(api.metrics).toHaveBeenCalledTimes(1)
    expect(api.check).not.toHaveBeenCalled()
    w.unmount()
    wrapper = undefined
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps polling while any alert is pending and stops when all are terminal', async () => {
    const ready = { ...alert('ready'), id: 'ready' }
    api.alerts.mockResolvedValueOnce([ready, alert('pending')]).mockResolvedValue([ready, alert('unavailable')])
    const w = await setup()
    await vi.advanceTimersByTimeAsync(4000)
    expect(w.findAll('.analysis--unavailable')).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not stack requests during a slow poll or overlap cadence refresh', async () => {
    api.alerts.mockResolvedValueOnce([alert('pending')])
    let resolve!: (alerts: AnomalyAlert[]) => void
    api.alerts.mockImplementation(() => new Promise<AnomalyAlert[]>((r) => { resolve = r }))
    api.config.mockResolvedValue({ intervalSeconds: 10, cooldownSeconds: 300 })
    await setup()
    await vi.advanceTimersByTimeAsync(24000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    resolve([alert('unavailable')])
    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
  })

  it('pauses while hidden and resumes only when visible', async () => {
    api.alerts.mockResolvedValue([alert('pending')])
    await setup()
    setVisibility('hidden')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(0)
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    wrapper!.unmount()
    wrapper = undefined
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores an in-flight result after unmount without scheduling more polls', async () => {
    api.alerts.mockResolvedValueOnce([alert('pending')])
    let resolve!: (alerts: AnomalyAlert[]) => void
    api.alerts.mockImplementation(() => new Promise<AnomalyAlert[]>((r) => { resolve = r }))
    await setup()
    await vi.advanceTimersByTimeAsync(4000)
    wrapper!.unmount()
    wrapper = undefined
    resolve([alert('pending')])
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not poll an initially hidden page until it becomes visible', async () => {
    visibility = 'hidden'
    api.alerts.mockResolvedValue([alert('pending')])
    await setup()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
  })

  it('drops a hidden in-flight result and resumes without overlapping requests', async () => {
    api.alerts.mockResolvedValueOnce([alert('pending')])
    let resolve!: (alerts: AnomalyAlert[]) => void
    api.alerts.mockImplementationOnce(() => new Promise<AnomalyAlert[]>((r) => { resolve = r }))
    const w = await setup()
    await vi.advanceTimersByTimeAsync(4000)
    setVisibility('hidden')
    resolve([alert('ready')])
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(2)
    expect(w.get('.analysis').classes()).toContain('analysis--pending')
    api.alerts.mockResolvedValue([alert('ready')])
    setVisibility('visible')
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.alerts).toHaveBeenCalledTimes(3)
    expect(w.get('.analysis').classes()).toContain('analysis--ready')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not start loading or timers after unmount during initial config loading', async () => {
    let resolve!: (config: { intervalSeconds: number; cooldownSeconds: number }) => void
    api.config.mockImplementation(() => new Promise((r) => { resolve = r }))
    const w = await setup()
    w.unmount()
    wrapper = undefined
    resolve({ intervalSeconds: 60, cooldownSeconds: 300 })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).not.toHaveBeenCalled()
    expect(api.metrics).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retains the pending snapshot on GET failure and recovers on the next bounded poll', async () => {
    api.alerts.mockResolvedValueOnce([alert('pending')]).mockRejectedValueOnce(new Error('temporary')).mockResolvedValue([alert('ready')])
    const w = await setup()
    await vi.advanceTimersByTimeAsync(4000)
    expect(w.get('.analysis').classes()).toContain('analysis--pending')
    await vi.advanceTimersByTimeAsync(4000)
    expect(w.get('.analysis').classes()).toContain('analysis--ready')
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['ready', 'unavailable', null] as const)('does not start polling for %s analysis', async (status) => {
    api.alerts.mockResolvedValue([alert(status)])
    await setup()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.alerts).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
    expect(api.check).not.toHaveBeenCalled()
  })
})

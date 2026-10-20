import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  checkAnomaly,
  createAnomalyRule,
  listAnomalyAlerts,
  listAnomalyRules,
  updateAnomalyRule,
  type AnomalyAnalysis,
} from './anomaly'

const fetchMock = vi.fn()
const legacyRule = {
  id: 'rule-1', metric: 'cpu', operator: 'gt', threshold: 90,
  serverId: null, enabled: false, createdAt: '2026-10-14T00:00:00Z',
}
const legacyAlert = {
  id: 'alert-1', serverId: 'server-1', serverName: 'Server', metric: 'cpu',
  operator: 'gt', threshold: 90, value: 95, message: 'Original alert', at: '2026-10-14T00:00:00Z',
}

function respond(body: unknown): void {
  fetchMock.mockResolvedValue(new Response(JSON.stringify(body), {
    status: 200, headers: { 'content-type': 'application/json' },
  }))
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  document.cookie = 'pipewright_csrf=test-csrf; path=/'
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'pipewright_csrf=; max-age=0; path=/'
})

describe('anomaly AI wire contract', () => {
  it('normalizes missing rule settings to off without mutating the response', async () => {
    respond({ items: [legacyRule, { ...legacyRule, id: 'opted-in', aiAnalysisEnabled: true }] })
    expect(await listAnomalyRules()).toEqual([
      { ...legacyRule, aiAnalysisEnabled: false },
      { ...legacyRule, id: 'opted-in', aiAnalysisEnabled: true },
    ])
    expect(legacyRule).not.toHaveProperty('aiAnalysisEnabled')
  })

  it.each([false, true])('serializes create AI=%s and a paused rule with CSRF', async (aiAnalysisEnabled) => {
    respond({ ...legacyRule, aiAnalysisEnabled })
    const input = { metric: 'cpu' as const, operator: 'gt' as const, threshold: 90, enabled: false, aiAnalysisEnabled }
    expect((await createAnomalyRule(input)).aiAnalysisEnabled).toBe(aiAnalysisEnabled)
    const [url, options] = fetchMock.mock.calls[0]!
    expect(url).toBe('/api/anomaly/rules')
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual(input)
    expect(options.credentials).toBe('same-origin')
    expect(options.headers.get('X-CSRF-Token')).toBe('test-csrf')
  })

  it.each([false, true])('serializes edit AI=%s without enabling a paused rule', async (aiAnalysisEnabled) => {
    respond({ ...legacyRule, aiAnalysisEnabled })
    const input = { metric: 'memory' as const, operator: 'lt' as const, threshold: 20, serverId: 'server-1', enabled: false, aiAnalysisEnabled }
    await updateAnomalyRule('rule-1', input)
    const [url, options] = fetchMock.mock.calls[0]!
    expect(url).toBe('/api/anomaly/rules/rule-1')
    expect(options.method).toBe('PATCH')
    expect(JSON.parse(options.body)).toEqual(input)
  })

  it('keeps omitted AI fields omitted on writes for old callers', async () => {
    respond(legacyRule)
    const input = { metric: 'cpu' as const, operator: 'gt' as const, threshold: 80 }
    expect((await createAnomalyRule(input)).aiAnalysisEnabled).toBe(false)
    respond({ ...legacyRule, aiAnalysisEnabled: true })
    expect((await updateAnomalyRule('rule-1', input)).aiAnalysisEnabled).toBe(true)
    for (const [, options] of fetchMock.mock.calls) {
      expect(JSON.parse(options.body)).not.toHaveProperty('aiAnalysisEnabled')
    }
  })

  it('normalizes old alerts without inventing a pending analysis', async () => {
    respond({ alerts: [legacyAlert] })
    expect(await listAnomalyAlerts()).toEqual([{ ...legacyAlert, aiAnalysisEnabled: false, analysis: null }])
    respond({ alerts: [legacyAlert] })
    expect(await checkAnomaly()).toEqual([{ ...legacyAlert, aiAnalysisEnabled: false, analysis: null }])
  })

  it.each(['pending', 'ready', 'unavailable'] as const)('preserves %s analysis snapshots on both alert endpoints', async (status) => {
    const analysis: AnomalyAnalysis = {
      status, reason: 'Known limitation', hypothesis: 'Possible cause', confidence: 'medium',
      alternateCauses: ['Alternative'], fixSuggestions: ['Inspect resource limits'],
      evidence: [{ line: 1, text: '<process>' }], generatedAt: '2026-10-14T00:00:02Z',
    }
    const alert = { ...legacyAlert, aiAnalysisEnabled: true, analysis }
    respond({ alerts: [alert] })
    expect(await listAnomalyAlerts({ serverId: 'server-1', limit: 50 })).toEqual([alert])
    expect(fetchMock.mock.calls[0]![0]).toBe('/api/anomaly/alerts?serverId=server-1&limit=50')
    respond({ alerts: [alert] })
    expect(await checkAnomaly()).toEqual([alert])
  })
})

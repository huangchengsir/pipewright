import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import Dashboard from './Dashboard.vue'
import type { RunListItem } from '../api/runs'
const api = vi.hoisted(() => ({ runs: vi.fn(), projects: vi.fn(), servers: vi.fn(), serverMetrics: vi.fn(), dora: vi.fn(), alerts: vi.fn(), diagnosis: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../api/runs', () => ({ listRuns: api.runs }))
vi.mock('../api/projects', () => ({ listProjects: api.projects }))
vi.mock('../api/servers', () => ({ listServers: api.servers, getAllServerMetrics: api.serverMetrics }))
vi.mock('../api/metrics', async original => ({ ...(await original<typeof import('../api/metrics')>()), getDoraMetrics: api.dora }))
vi.mock('../api/anomaly', () => ({ listAnomalyAlerts: api.alerts }))
vi.mock('../api/settings', () => ({ getDiagnosisStats: api.diagnosis }))
const row = (id: string, status: RunListItem['status'], executionKind?: string): RunListItem => ({ id, projectId: 'p', projectName: 'P', status, trigger: { type: 'manual', branch: 'main', commit: '', actor: 'admin' }, createdAt: '', durationMs: null, executionKind })
const stubs = { DashRecentRuns: true, DashServers: true, DashEnvironments: true, DashAlerts: true, DoraMetricCard: true }
describe('original-run dashboard sample', () => {
  beforeEach(() => { vi.clearAllMocks(); api.projects.mockResolvedValue([]); api.servers.mockResolvedValue([]); api.serverMetrics.mockResolvedValue([]); api.dora.mockRejectedValue(new Error('no metrics')); api.alerts.mockResolvedValue([]); api.diagnosis.mockResolvedValue(null) })
  it('excludes recovery attempts and shows the actual included count, never a claimed fixed sample', async () => {
    api.runs.mockResolvedValue({ page: 1, total: 100, items: [row('original-ok', 'success'), row('original-bad', 'failed', 'original'), row('recovery-ok', 'success', 'recovery'), row('pending', 'queued')] })
    const wrapper = mount(Dashboard, { global: { stubs } }); await flushPromises()
    expect(wrapper.text()).toContain('50%'); expect(wrapper.text()).toContain('2 个原始运行样本'); expect(wrapper.text()).not.toContain('67%'); expect(wrapper.text()).not.toContain('12 个原始运行样本'); wrapper.unmount()
  })
  it('shows no fabricated original success rate when the recent list contains only recoveries', async () => {
    api.runs.mockResolvedValue({ page: 1, total: 2, items: [row('one', 'success', 'recovery'), row('two', 'success', 'recovery')] })
    const wrapper = mount(Dashboard, { global: { stubs } }); await flushPromises(); expect(wrapper.text()).not.toContain('100%'); expect(wrapper.find('.kpi-suffix').exists()).toBe(false); wrapper.unmount()
  })
})

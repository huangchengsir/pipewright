import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DoraDashboard from './DoraDashboard.vue'
const api = vi.hoisted(() => ({ dora: vi.fn(), projects: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('../api/projects', () => ({ listProjects: api.projects }))
vi.mock('../api/metrics', async original => ({ ...(await original<typeof import('../api/metrics')>()), getDoraMetrics: api.dora }))
describe('DORA recovery accounting', () => {
  it('displays original deployment totals and separate recovery counts with an explicit policy', async () => {
    const metric = { value: 0, band: 'none', sampleCount: 0 }
    api.projects.mockResolvedValue([]); api.dora.mockResolvedValue({ window: '30d', windowDays: 30, projectId: '', totalDeployments: 4, successfulDeployments: 3, failedDeployments: 1, recoveryAttempts: 2, successfulRecoveries: 1, metrics: { deploymentFrequency: metric, leadTime: metric, changeFailureRate: metric, mttr: metric }, trend: [] })
    const wrapper = mount(DoraDashboard, { global: { stubs: { DoraMetricCard: true, AppSelect: true } } }); await flushPromises()
    expect(wrapper.text()).toContain('恢复尝试 2 次，成功 1 次'); expect(wrapper.get('.dora-note').text()).toContain('统计原始运行'); expect(wrapper.get('.dora-note').text()).toContain('复用结果不算新的部署'); wrapper.unmount()
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import MCPConfigMaintenance from './MCPConfigMaintenance.vue'
import type { MCPConfigActivity } from '../api/mcpMaintenance'

const mocks = vi.hoisted(() => ({ get: vi.fn(), recover: vi.fn() }))
vi.mock('../api/mcpMaintenance', () => ({ getMCPConfigMaintenance: mocks.get, recoverMCPConfigActivity: mocks.recover }))
const wrappers: VueWrapper[] = []
function item(overrides: Partial<MCPConfigActivity> = {}): MCPConfigActivity { return { activityId: 'activity-1', projectId: 'project-1', runId: 'run-1', ownerEpoch: 'owner-1', phase: 'cleanup_unknown', projectRevision: 'cfg-7', digest: 'bound-digest', evidenceId: '', recoverable: false, reason: 'evidence_unavailable', ...overrides } }
function render() { const wrapper = mount(MCPConfigMaintenance); wrappers.push(wrapper); return wrapper }
describe('MCP configuration maintenance', () => {
  beforeEach(() => { vi.resetAllMocks(); mocks.get.mockResolvedValue({ executorEpoch: 2, items: [item()], truncated: false }); mocks.recover.mockResolvedValue({}) })
  afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })
  it('keeps unsupported legacy or external activity read-only with clear maintenance instructions', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('保持停写')
    expect(wrapper.text()).toContain('旧控制进程已退出')
    expect(wrapper.text()).toContain('其他实例或外部交接状态仍不可验证')
    expect(wrapper.find('[data-activity-id] button').exists()).toBe(false)
    expect(mocks.recover).not.toHaveBeenCalled()
  })
  it.each([{ recoverable: true }, { recoverable: false, evidenceId: 'evidence-1' }, { recoverable: true, evidenceId: 'evidence-1', digest: '' }])('requires all server proof fields before exposing recovery: %j', async (proof) => {
    mocks.get.mockResolvedValue({ executorEpoch: 2, items: [item(proof)], truncated: false })
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('[data-activity-id] button').exists()).toBe(false)
  })
  it('submits exactly the shown proof and reloads authoritative state after success', async () => {
    mocks.get.mockResolvedValueOnce({ executorEpoch: 2, items: [item({ recoverable: true, evidenceId: 'evidence-1', reason: '' })], truncated: false }).mockResolvedValueOnce({ executorEpoch: 2, items: [], truncated: false })
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('引用计数已终止')
    await wrapper.get('[data-activity-id] button').trigger('click'); await flushPromises()
    expect(mocks.recover).toHaveBeenCalledExactlyOnceWith({ activityId: 'activity-1', expectedDigest: 'bound-digest', evidenceId: 'evidence-1' }, { signal: expect.any(AbortSignal) })
    expect(mocks.get).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-activity-id]').exists()).toBe(false)
  })
  it('does not retry an uncertain recovery and hides actions until refreshed', async () => {
    mocks.get.mockResolvedValue({ executorEpoch: 2, items: [item({ recoverable: true, evidenceId: 'evidence-1' })], truncated: false })
    mocks.recover.mockRejectedValue(new Error('private backend diagnostics'))
    const wrapper = render(); await flushPromises()
    await wrapper.get('[data-activity-id] button').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('结果不确定')
    expect(wrapper.text()).not.toContain('private backend diagnostics')
    expect(wrapper.find('[data-activity-id] button').exists()).toBe(false)
    expect(mocks.recover).toHaveBeenCalledTimes(1)
    expect(mocks.get).toHaveBeenCalledTimes(1)
  })
})

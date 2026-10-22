import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import MCPConfigOperationPanel from './MCPConfigOperationPanel.vue'
import type { MCPConfigOperationResult } from '../../api/mcpMaintenance'

const mocks = vi.hoisted(() => ({ get: vi.fn(), fence: vi.fn() }))
vi.mock('../../api/mcpMaintenance', () => ({ getMCPConfigOperation: mocks.get, fenceMCPConfigOperation: mocks.fence }))
let wrapper: VueWrapper
const result = (state = 'pending'): MCPConfigOperationResult => ({ operationId: 'operation-1', projectId: 'project-1', state, baseRevision: 'cfg-1', changedOperations: [] })
async function lookup() { await wrapper.get('input').setValue('operation-1'); await wrapper.get('form').trigger('submit'); await flushPromises() }
const fenceButton = () => wrapper.find('.operation-panel > div button')
describe('MCP operation reconciliation', () => {
  beforeEach(() => { vi.resetAllMocks(); mocks.get.mockResolvedValue(result()); mocks.fence.mockResolvedValue({ ...result('failed'), reason: 'interrupted_not_applied' }); wrapper = mount(MCPConfigOperationPanel) })
  afterEach(() => wrapper.unmount())
  it('requires a manual query before exposing an explicit fencing action', async () => {
    expect(mocks.get).not.toHaveBeenCalled(); expect(fenceButton().exists()).toBe(false)
    await lookup()
    expect(mocks.get).toHaveBeenCalledExactlyOnceWith('operation-1', { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('不会重试、新建操作或自动应用')
    expect(fenceButton().exists()).toBe(true); expect(mocks.fence).not.toHaveBeenCalled()
    await fenceButton().trigger('click'); await flushPromises()
    expect(mocks.fence).toHaveBeenCalledExactlyOnceWith('operation-1', { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('确认未应用')
    expect(fenceButton().exists()).toBe(false)
    expect(mocks.get).toHaveBeenCalledTimes(1)
  })
  it('shows an applied result returned by fencing without claiming it was stopped', async () => {
    mocks.get.mockResolvedValue(result('unknown'))
    mocks.fence.mockResolvedValue({ ...result('applied'), newRevision: 'cfg-2' })
    await lookup(); await fenceButton().trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('原操作已应用'); expect(wrapper.text()).toContain('cfg-2')
    expect(wrapper.text()).not.toContain('确认未应用')
    expect(fenceButton().exists()).toBe(false)
  })
  it.each(['applied', 'failed', 'rejected', 'unrecognized'])('does not fence a recorded %s operation', async (state) => {
    mocks.get.mockResolvedValue(result(state)); await lookup()
    expect(fenceButton().exists()).toBe(false); expect(mocks.fence).not.toHaveBeenCalled()
  })
  it('locks an uncertain fencing action until a fresh manual query', async () => {
    mocks.fence.mockRejectedValue(new Error('private diagnostic'))
    await lookup(); await fenceButton().trigger('click'); await flushPromises()
    expect(fenceButton().exists()).toBe(false); expect(wrapper.text()).toContain('先重新查询原操作')
    expect(wrapper.text()).not.toContain('private diagnostic')
    expect(mocks.fence).toHaveBeenCalledTimes(1); expect(mocks.get).toHaveBeenCalledTimes(1)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(fenceButton().exists()).toBe(true)
    expect(mocks.fence).toHaveBeenCalledTimes(1)
  })
  it('rejects a result for a different operation and ignores a late query after input changes', async () => {
    mocks.get.mockResolvedValueOnce({ ...result(), operationId: 'another-operation' })
    await lookup(); expect(fenceButton().exists()).toBe(false)
    let finish!: (value: MCPConfigOperationResult) => void
    mocks.get.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await wrapper.get('form').trigger('submit')
    await wrapper.get('input').setValue('new-operation')
    finish(result()); await flushPromises()
    expect(fenceButton().exists()).toBe(false); expect(wrapper.find('dl').exists()).toBe(false)
  })
})

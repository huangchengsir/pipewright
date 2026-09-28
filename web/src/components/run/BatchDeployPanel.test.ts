import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BatchDeployPanel from './BatchDeployPanel.vue'

const api = vi.hoisted(() => ({
  listServers: vi.fn(),
  listDeployBatches: vi.fn(),
  deployBatch: vi.fn(),
  retryDeployBatch: vi.fn(),
  continueDeployBatch: vi.fn(),
}))

vi.mock('../../api/servers', () => ({ listServers: api.listServers }))
vi.mock('../../api/runs', () => ({
  listDeployBatches: api.listDeployBatches,
  deployBatch: api.deployBatch,
  retryDeployBatch: api.retryDeployBatch,
  continueDeployBatch: api.continueDeployBatch,
}))

const artifact = {
  id: 'art-1', type: 'jar' as const, name: 'Backend API', reference: '0123456789abcdef',
  sizeBytes: 3, metadata: { workspacePath: 'target/app.jar' }, createdAt: '2026-01-01T00:00:00Z',
}

function panel() {
  return mount(BatchDeployPanel, {
    props: { runId: 'run-1', artifacts: [artifact], canCreate: true },
  })
}

describe('BatchDeployPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.listServers.mockResolvedValue([{ id: 'server-1', name: 'Web 1' }])
    api.listDeployBatches.mockResolvedValue({ batches: [] })
    api.deployBatch.mockResolvedValue({ id: 'batch-1', items: [], status: 'success' })
  })

  it('requires an absolute path and selected server before calling the API', async () => {
    const wrapper = panel()
    await flushPromises()
    await wrapper.get('.batch-button').trigger('click')
    await wrapper.get('.submit-button').trigger('click')
    expect(api.deployBatch).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('绝对部署路径')
    wrapper.unmount()
  })

  it('submits a distinct artifact/server/path row and resets the form on success', async () => {
    const wrapper = panel()
    await flushPromises()
    await wrapper.get('.batch-button').trigger('click')
    await wrapper.get('.server-list input').setValue(true)
    await wrapper.get('.row-fields input').setValue('/srv/backend')
    await wrapper.get('.submit-button').trigger('click')
    await flushPromises()
    expect(api.deployBatch).toHaveBeenCalledWith('run-1', {
      idempotencyKey: expect.any(String),
      items: [{ artifactId: 'art-1', serverIds: ['server-1'], deployConfig: { path: '/srv/backend' } }],
    })
    expect(wrapper.findAll('.batch-row')).toHaveLength(0)
    expect(wrapper.emitted('completed')).toHaveLength(1)
    wrapper.unmount()
  })
})

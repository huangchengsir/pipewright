import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReleaseGuardInvestigation from './ReleaseGuardInvestigation.vue'
import { guardReport } from '../../test/releaseGuardFixtures'
import { HttpError } from '../../api/http'

const api = vi.hoisted(() => ({ investigate: vi.fn() }))
vi.mock('../../api/releaseGuard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/releaseGuard')>()),
  investigateReleaseGuard: api.investigate,
}))
describe('explicit local change investigation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.investigate.mockResolvedValue(guardReport())
  })
  it('does not investigate on mounting or report refresh and only updates on explicit action', async () => {
    const wrapper = mount(ReleaseGuardInvestigation, {
      props: { report: guardReport() },
      global: { stubs: { RouterLink: true } },
    })
    await flushPromises()
    await wrapper.setProps({ report: guardReport({ eventsCursor: 2 }) })
    expect(api.investigate).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(api.investigate).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })
  it('discloses an evidence conflict without automatically retrying', async () => {
    api.investigate.mockRejectedValue(new HttpError(409, null, 'changed'))
    const wrapper = mount(ReleaseGuardInvestigation, {
      props: { report: guardReport() },
      global: { stubs: { RouterLink: true } },
    })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('请先刷新报告')
    expect(api.investigate).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

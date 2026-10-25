import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReleaseGuardInvestigation from './ReleaseGuardInvestigation.vue'
import { guardReport } from '../../test/releaseGuardFixtures'
import type { GuardReport } from '../../api/releaseGuard'
import { HttpError } from '../../api/http'
import { i18n, setLocale } from '../../i18n'

const api = vi.hoisted(() => ({ investigate: vi.fn() }))
vi.mock('../../api/releaseGuard', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/releaseGuard')>()),
  investigateReleaseGuard: api.investigate,
}))
describe('explicit local change investigation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setLocale('zh-CN')
    api.investigate.mockResolvedValue(guardReport())
  })
  afterEach(() => setLocale('zh-CN'))
  it('retranslates an existing conflict without rerunning the investigation', async () => {
    api.investigate.mockRejectedValueOnce(new HttpError(409, null, 'changed'))
    const wrapper = mount(ReleaseGuardInvestigation, {
      props: { report: guardReport() },
      global: { stubs: { RouterLink: true } },
    })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    const chinese = wrapper.get('.guard-investigation-error').text()
    setLocale('de')
    await flushPromises()
    expect(wrapper.get('.guard-investigation-error').text()).toBe(
      i18n.global.t('releaseGuardInvestigation.changed'),
    )
    expect(wrapper.get('.guard-investigation-error').text()).not.toBe(chinese)
    expect(api.investigate).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })
  it('does not investigate on mounting or report refresh and only updates on explicit action', async () => {
    const wrapper = mount(ReleaseGuardInvestigation, {
      props: { report: guardReport() },
      global: { stubs: { RouterLink: true } },
    })
    await flushPromises()
    await wrapper.setProps({ report: guardReport({ eventsCursor: 2 }) })
    expect(api.investigate).not.toHaveBeenCalled()
    api.investigate.mockResolvedValue(guardReport({ eventsCursor: 2 }))
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
  it('rejects a late same-report investigation result after a newer poll cursor', async () => {
    let finish!: (report: GuardReport) => void
    api.investigate.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    const wrapper = mount(ReleaseGuardInvestigation, {
      props: {
        report: guardReport({ eventsCursor: 1, verificationResult: 'passed' }),
      },
      global: { stubs: { RouterLink: true } },
    })
    await wrapper.get('button').trigger('click')
    const signal = api.investigate.mock.calls[0][2].signal as AbortSignal
    await wrapper.setProps({
      report: guardReport({ eventsCursor: 2, verificationResult: 'failed' }),
    })
    expect(signal.aborted).toBe(false)
    finish(guardReport({ eventsCursor: 1, verificationResult: 'passed' }))
    await flushPromises()
    expect(wrapper.emitted('updated')).toBeUndefined()
    expect(wrapper.get('.guard-investigation-error').text()).toContain(
      '请先刷新报告',
    )
    expect(api.investigate).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

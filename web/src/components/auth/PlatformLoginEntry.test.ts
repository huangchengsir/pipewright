import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import PlatformLoginEntry from './PlatformLoginEntry.vue'
import { PLATFORM_PROVIDERS, type PublicPlatform } from '../../api/platformLogin'

const mocks = vi.hoisted(() => ({
  get: vi.fn(), start: vi.fn(), verify: vi.fn(), replace: vi.fn(),
  target: vi.fn(async (value: unknown) => value ?? '/onboarding'),
  route: { query: {} as Record<string, string> },
}))
vi.mock('../../api/platformLogin', async () => ({
  ...await vi.importActual<typeof import('../../api/platformLogin')>('../../api/platformLogin'),
  getPublicPlatforms: (...args: unknown[]) => mocks.get(...args),
  startPlatformLogin: (...args: unknown[]) => mocks.start(...args),
}))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: mocks.replace }) }))
vi.mock('../../stores/session', () => ({ useSessionStore: () => ({ ensureSession: mocks.verify }) }))
vi.mock('../../composables/useOnboarding', () => ({ onboardingLoginTarget: (value: unknown) => mocks.target(value) }))
let wrapper: VueWrapper | undefined
function policy(visible = true): PublicPlatform[] {
  return PLATFORM_PROVIDERS.map(provider => ({ provider, visible, available: false, reason: 'not_configured' }))
}
beforeEach(() => {
  for (const fn of [mocks.get, mocks.start, mocks.verify, mocks.replace, mocks.target]) fn.mockClear()
  mocks.route.query = {}
  mocks.get.mockReset().mockResolvedValue(policy())
  mocks.start.mockReset()
  mocks.replace.mockResolvedValue(undefined)
  mocks.verify.mockResolvedValue({ kind: 'unauthenticated' })
  sessionStorage.clear()
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.unstubAllGlobals() })
describe('platform login entry', () => {
  it('does not flash five defaults before instance visibility is known', async () => {
    let resolve: ((p: PublicPlatform[]) => void) | undefined
    mocks.get.mockReturnValueOnce(new Promise<PublicPlatform[]>(r => { resolve = r }))
    wrapper = mount(PlatformLoginEntry)
    expect(wrapper.findAll('[data-provider]')).toHaveLength(0)
    resolve!(policy(false)); await flushPromises()
    expect(wrapper.find('.platform-divider').exists()).toBe(false)
    expect(wrapper.find('.platform-entry').exists()).toBe(false)
  })
  it('keeps configured state separate from visibility and reports unconfigured locally', async () => {
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    expect(wrapper.findAll('[data-provider]')).toHaveLength(5)
    await wrapper.get('[data-provider="github"]').trigger('click')
    expect(mocks.start).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('管理员尚未配置')
  })
  it('packs visible providers in fixed order and refreshes after focus without defaults', async () => {
    const first = policy().map(p => ({ ...p, visible: p.provider === 'gitee' || p.provider === 'microsoft' }))
    mocks.get.mockResolvedValueOnce(first).mockResolvedValueOnce(policy(false))
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    expect(wrapper.findAll('[data-provider]').map(x => x.attributes('data-provider'))).toEqual(['gitee', 'microsoft'])
    expect(wrapper.get('.platform-options').attributes('style')).toContain('repeat(2')
    window.dispatchEvent(new Event('focus')); await flushPromises()
    expect(wrapper.findAll('[data-provider]')).toHaveLength(0)
  })
  it('rejects late read results after a newer focus refresh', async () => {
    let resolve: ((p: PublicPlatform[]) => void) | undefined
    mocks.get.mockReturnValueOnce(new Promise<PublicPlatform[]>(r => { resolve = r })).mockResolvedValueOnce(policy(false))
    wrapper = mount(PlatformLoginEntry)
    window.dispatchEvent(new Event('focus')); await flushPromises()
    resolve!(policy(true)); await flushPromises()
    expect(wrapper.find('.platform-options').exists()).toBe(false)
  })
  it('keeps read failure visible and retries only on an explicit click', async () => {
    mocks.get.mockRejectedValueOnce(new Error('do-not-show-provider-details')).mockResolvedValueOnce(policy())
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    expect(wrapper.findAll('[data-provider]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('do-not-show-provider-details')
    await wrapper.get('.platform-link').trigger('click'); await flushPromises()
    expect(wrapper.findAll('[data-provider]')).toHaveLength(5)
  })
  it('allows only one in-flight explicit start', async () => {
    mocks.get.mockResolvedValue(policy().map(p => ({ ...p, available: true, reason: '' })))
    mocks.start.mockReturnValue(new Promise(() => {}))
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    await wrapper.get('[data-provider="github"]').trigger('click')
    await wrapper.get('[data-provider="gitlab"]').trigger('click')
    expect(mocks.start).toHaveBeenCalledTimes(1)
  })
  it('does not trust a forged successful callback without a fresh server session', async () => {
    mocks.route.query = { platform_result: 'success', returnTo: '/runs' }
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    expect(mocks.verify).toHaveBeenCalledWith(true)
    expect(mocks.target).not.toHaveBeenCalled()
    expect(mocks.replace).toHaveBeenCalledTimes(1)
    expect(mocks.replace).toHaveBeenCalledWith({ name: 'login', query: {} })
    expect(wrapper.text()).toContain('暂时无法完成授权')
  })
  it('confirms a real session before returning to a safe original target', async () => {
    mocks.route.query = { platform_result: 'success', returnTo: '/runs?page=3' }
    mocks.verify.mockResolvedValue({ kind: 'ok', user: { username: 'fixture' } })
    wrapper = mount(PlatformLoginEntry); await flushPromises()
    expect(mocks.target).toHaveBeenCalledWith('/runs?page=3')
    expect(mocks.replace).toHaveBeenLastCalledWith('/runs?page=3')
  })
})

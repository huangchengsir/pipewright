import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import InstantSubdomain from './InstantSubdomain.vue'
import { access } from '../../api/access'
import { listUsableDnsProviders } from '../../api/dnsProviders'
import { allocateSubdomain, type ProxyRoute } from '../../api/reverseProxy'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/dnsProviders', () => ({ listUsableDnsProviders: vi.fn() }))
vi.mock('../../api/reverseProxy', () => ({ allocateSubdomain: vi.fn() }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success: vi.fn(), error: vi.fn() }) }))
const provider = { id: 'p', name: 'Selected', baseDomain: 'selected.invalid', type: 'cloudflare' as const }
const other = { id: 'q', name: 'Other', baseDomain: 'other.invalid', type: 'cloudflare' as const }
const route = { id: 'r', domain: 'app.selected.invalid', upstreamContainer: 'app', upstreamPort: 8080 } as ProxyRoute
const container = { id: 'c', names: 'app', image: 'fixture', state: 'running' as const, status: '', ports: '8080/tcp', createdAt: '' }
let wrapper: VueWrapper | undefined
let granted: Set<string>
async function open(instanceAdmin = false) {
  useSessionStore().setUser({ username: 'member', userId: 'u', multiTenant: true, instanceAdmin })
  wrapper = mount(InstantSubdomain, {
    attachTo: document.body,
    props: { serverId: 'a', containers: [container] },
    global: {
      plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: {} }, missingWarn: false, fallbackWarn: false })],
      stubs: { teleport: true, RouterLink: true, NIcon: true },
    },
  })
  await flushPromises()
  return wrapper
}
async function draft(w: VueWrapper) {
  await w.get('.isub__cta').trigger('click')
  await flushPromises()
  await w.get('select').setValue('p')
  await w.findAll('select')[1]!.setValue('app')
  await flushPromises()
}
beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  granted = new Set(['proxy.manage/a', 'server.use/a', 'dns.use/p'])
  usePermissionStore().setTeam('team-a')
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    teamId: team, userId: 'u', catalogVersion: 1, modeEpoch: 1, revision: 1,
    allowed: queries.map(q => team === 'team-a' && granted.has(q.action + '/' + q.resourceId)),
  }))
  vi.mocked(listUsableDnsProviders).mockResolvedValue([provider, other])
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined })

describe('instant subdomain action and data permissions', () => {
  it('requires both selected host actions before opening or loading providers', async () => {
    granted.delete('server.use/a')
    const w = await open()
    expect(w.find('.isub__cta').exists()).toBe(false)
    expect(listUsableDnsProviders).not.toHaveBeenCalled()
    granted.add('server.use/a')
    usePermissionStore().invalidate()
    await flushPromises()
    await w.get('.isub__cta').trigger('click')
    expect(w.find('[role="dialog"]').exists()).toBe(true)
    expect(listUsableDnsProviders).toHaveBeenCalledTimes(1)
  })

  it('filters submit by selected provider and never writes without the exact decision', async () => {
    const w = await open()
    await draft(w)
    expect(w.get('input[inputmode="numeric"]').element).toHaveProperty('value', '8080')
    expect(w.find('button[type="submit"]').exists()).toBe(true)
    await w.get('select').setValue('q')
    await flushPromises()
    expect(w.find('button[type="submit"]').exists()).toBe(false)
    await w.get('form').trigger('submit')
    expect(allocateSubdomain).not.toHaveBeenCalled()
    await w.get('select').setValue('p')
    await flushPromises()
    vi.mocked(allocateSubdomain).mockResolvedValue(route)
    await w.get('button[type="submit"]').trigger('click')
    await flushPromises()
    expect(allocateSubdomain).toHaveBeenCalledTimes(1)
    expect(allocateSubdomain).toHaveBeenCalledWith(
      { providerId: 'p', serverId: 'a', upstreamContainer: 'app', upstreamPort: 8080 },
      expect.objectContaining({ headers: { 'X-Pipewright-Team': 'team-a' }, signal: expect.any(AbortSignal) }),
    )
    expect(w.emitted('allocated')).toEqual([[route]])
    await w.get('.celebrate__actions button').trigger('click')
    expect(w.find('[role="dialog"]').exists()).toBe(false)
  })

  it('drops a completed response when the provider decision changes in flight', async () => {
    const w = await open()
    await draft(w)
    let finish!: (route: ProxyRoute) => void
    vi.mocked(allocateSubdomain).mockReturnValue(new Promise(resolve => { finish = resolve }))
    await w.get('form').trigger('submit')
    const options = vi.mocked(allocateSubdomain).mock.calls[0]![1]!
    usePermissionStore().reject('team-a', { action: 'dns.use', resourceId: 'p' })
    expect(options.signal?.aborted).toBe(true)
    finish(route)
    await flushPromises()
    expect(w.find('.celebrate').exists()).toBe(false)
    expect(w.emitted('allocated')).toBeUndefined()
  })

  it('closes through the header, footer, Escape and backdrop without an API write', async () => {
    const w = await open()
    for (const dismiss of [
      () => w.get('.modal-close').trigger('click'),
      () => w.get('.modal-footer button[type="button"]').trigger('click'),
      () => w.get('.modal-scrim').trigger('keydown', { key: 'Escape' }),
      () => w.get('.modal-scrim').trigger('click'),
    ]) {
      await draft(w)
      await dismiss()
      expect(w.find('.modal-scrim').exists()).toBe(false)
    }
    expect(allocateSubdomain).not.toHaveBeenCalled()
  })

  it('hides global DNS settings from employees without providers', async () => {
    vi.mocked(listUsableDnsProviders).mockResolvedValue([])
    const w = await open()
    await w.get('.isub__cta').trigger('click')
    expect(w.find('.noprov').exists()).toBe(true)
    expect(w.find('.noprov__link').exists()).toBe(false)
    w.unmount()
    const admin = await open(true)
    await admin.get('.isub__cta').trigger('click')
    expect(admin.find('.noprov__link').exists()).toBe(true)
  })

  it('clears visible provider data when host permissions are withdrawn', async () => {
    const w = await open()
    await draft(w)
    expect(w.text()).toContain('selected.invalid')
    usePermissionStore().reject('team-a', { action: 'server.use', resourceId: 'a' })
    await flushPromises()
    expect(w.find('[role="dialog"]').exists()).toBe(false)
    expect(w.find('.isub__cta').exists()).toBe(false)
    expect(w.text()).not.toContain('selected.invalid')
    expect(allocateSubdomain).not.toHaveBeenCalled()
  })
})

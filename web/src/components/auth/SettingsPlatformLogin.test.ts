import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SettingsPlatformLogin from './SettingsPlatformLogin.vue'
import { PLATFORM_PROVIDERS, type PlatformSnapshot, type PlatformSettings } from '../../api/platformLogin'
import { HttpError } from '../../api/http'

const m = vi.hoisted(() => ({
  get: vi.fn(), visibility: vi.fn(), app: vi.fn(), reauth: vi.fn(), bind: vi.fn(), pending: vi.fn(), confirmBinding: vi.fn(),
  enabled: vi.fn(), unbind: vi.fn(), confirm: vi.fn(), success: vi.fn(), clearSession: vi.fn(), replace: vi.fn(),
  route: { path: '/settings/account', query: {} as Record<string, string> },
}))
vi.mock('../../api/platformLogin', async () => ({
  ...await vi.importActual<typeof import('../../api/platformLogin')>('../../api/platformLogin'),
  getPlatformSettings: (...args: unknown[]) => m.get(...args),
  savePlatformVisibility: (...args: unknown[]) => m.visibility(...args),
  savePlatformApp: (...args: unknown[]) => m.app(...args),
  reauthenticatePlatform: (...args: unknown[]) => m.reauth(...args),
  startPlatformBinding: (...args: unknown[]) => m.bind(...args),
  getPlatformCandidate: (...args: unknown[]) => m.pending(...args),
  confirmPlatformBinding: (...args: unknown[]) => m.confirmBinding(...args),
  setPlatformEnabled: (...args: unknown[]) => m.enabled(...args),
  unbindPlatform: (...args: unknown[]) => m.unbind(...args),
}))
vi.mock('../../composables/useConfirm', () => ({ useConfirm: () => ({ open: m.confirm }) }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success: m.success }) }))
vi.mock('../../stores/session', () => ({ useSessionStore: () => ({ clearSession: m.clearSession }) }))
vi.mock('vue-router', () => ({ useRoute: () => m.route, useRouter: () => ({ replace: m.replace }) }))

function snapshot(recent = true): PlatformSnapshot {
  return {
    visibilityVersion: 4, reauthenticatedUntil: recent ? new Date(Date.now() + 300000).toISOString() : '',
    providers: PLATFORM_PROVIDERS.map(provider => ({
      provider, visible: true, enabled: false, configured: provider === 'github', hasSecret: provider === 'github',
      clientId: provider === 'github' ? 'saved-app' : '', microsoftTenant: 'common', giteeMode: '',
      callbackURL: 'https://pipewright.example/api/auth/platform/' + provider + '/callback', configVersion: 3,
    })),
  }
}
function row(extra: Partial<PlatformSettings> = {}): PlatformSettings { return { ...snapshot().providers[0], ...extra } }
let wrapper: VueWrapper | undefined
function button(label: string) {
  const found = wrapper!.findAll('button').find(b => b.text() === label)
  if (!found) throw new Error('Button not found: ' + label)
  return found
}
async function mountSettings() {
  wrapper = mount(SettingsPlatformLogin, { attachTo: document.body, global: { stubs: { teleport: true } } })
  await flushPromises()
}
beforeEach(() => {
  for (const value of Object.values(m)) if (typeof value === 'function' && 'mockReset' in value) value.mockReset()
  m.route.query = {}
  m.get.mockImplementation(async () => snapshot())
  m.confirm.mockResolvedValue(true)
  m.replace.mockResolvedValue(undefined)
  m.reauth.mockResolvedValue({ reauthenticatedUntil: new Date(Date.now() + 300000).toISOString() })
  m.app.mockResolvedValue({ provider: row({ configVersion: 4 }), sessionRevoked: false })
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value() { this.setAttribute('open', '') } })
  Object.defineProperty(HTMLDialogElement.prototype, 'close', { configurable: true, value() { this.removeAttribute('open') } })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; document.body.innerHTML = '' })

describe('account platform sign-in settings', () => {
  it('does not invent default instance settings after a failed read', async () => {
    m.get.mockRejectedValue(new Error('DO_NOT_RENDER_RAW_FAILURE'))
    await mountSettings()
    expect(wrapper!.findAll('[role="switch"]')).toHaveLength(0)
    expect(wrapper!.text()).toContain('无法加载登录设置')
    expect(wrapper!.text()).not.toContain('DO_NOT_RENDER_RAW_FAILURE')
  })
  it('saves a complete visibility policy independently from enabled and configured', async () => {
    const saved = snapshot(); saved.visibilityVersion = 5; saved.providers[0].visible = false
    m.visibility.mockResolvedValue(saved)
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenCalledWith(4, { github: false, gitlab: true, gitee: true, google: true, microsoft: true })
    expect(m.app).not.toHaveBeenCalled()
    expect(m.enabled).not.toHaveBeenCalled()
    expect(m.success).toHaveBeenCalledWith('显示设置已保存')
  })
  it('preserves visibility drafts on a conflict and reloads a new expected version', async () => {
    m.visibility.mockRejectedValue(new HttpError(409, { code: 'platform_config_conflict', message: 'raw' }, 'raw'))
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
    const current = snapshot(); current.visibilityVersion = 8
    m.get.mockResolvedValue(current)
    await button('刷新核对').trigger('click'); await flushPromises()
    expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenLastCalledWith(8, expect.objectContaining({ github: false }))
  })
  it('blocks another ambiguous save until explicit read-back, without automatic retries', async () => {
    m.visibility.mockRejectedValue(new HttpError(0, null, 'response lost'))
    await mountSettings()
    await wrapper!.get('#platform-visible-github').trigger('click')
    await button('保存显示设置').trigger('click'); await flushPromises()
    expect(button('保存显示设置').attributes('disabled')).toBeDefined()
    expect(m.visibility).toHaveBeenCalledTimes(1)
    const actual = snapshot(); actual.visibilityVersion = 5; actual.providers[0].visible = false
    m.get.mockResolvedValue(actual)
    await button('刷新核对').trigger('click'); await flushPromises()
    expect(m.visibility).toHaveBeenCalledTimes(1)
    expect(button('保存显示设置').attributes('disabled')).toBeDefined()
    expect(m.success).not.toHaveBeenCalled()
  })
  it('never prefills or echoes a stored secret and prevents discarding an unsaved app in a bind action', async () => {
    const actual = snapshot(); Object.assign(actual.providers[0], { clientSecret: 'SERVER_SECRET_CANARY' })
    m.get.mockResolvedValue(actual)
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    expect((wrapper!.get('#platform-client-secret').element as HTMLInputElement).value).toBe('')
    expect(wrapper!.text()).not.toContain('SERVER_SECRET_CANARY')
    await wrapper!.get('#platform-client-id').setValue('new-app')
    expect(button('绑定账号').attributes('disabled')).toBeDefined()
    expect(wrapper!.text()).toContain('先保存或取消应用更改')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(m.app).not.toHaveBeenCalled()
  })
  it('clears passwords and secrets, handles a wrong password locally, then performs one deliberate save', async () => {
    m.get.mockResolvedValue(snapshot(false))
    m.reauth.mockRejectedValueOnce(new HttpError(422, { code: 'invalid_current_password', message: 'raw' }, 'raw'))
      .mockResolvedValueOnce({ reauthenticatedUntil: new Date(Date.now() + 300000).toISOString() })
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await wrapper!.get('#platform-client-secret').setValue('NEW_SECRET_CANARY')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(wrapper!.find('#platform-client-secret').exists()).toBe(false)
    await wrapper!.get('#platform-current-password').setValue('wrong-fixture-value')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(wrapper!.text()).toContain('当前密码不正确')
    expect((wrapper!.get('#platform-current-password').element as HTMLInputElement).value).toBe('')
    expect(m.app).not.toHaveBeenCalled()
    await wrapper!.get('#platform-current-password').setValue('correct-fixture-value')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    expect(m.app).toHaveBeenCalledTimes(1)
    expect(m.app).toHaveBeenCalledWith('github', { expectedVersion: 3, clientId: 'saved-app', clientSecret: 'NEW_SECRET_CANARY' })
    expect(wrapper!.text()).not.toContain('NEW_SECRET_CANARY')
    expect(wrapper!.find('#platform-current-password').exists()).toBe(false)
  })
  it('drops a pending sensitive action when password confirmation is canceled', async () => {
    m.get.mockResolvedValue(snapshot(false))
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await wrapper!.get('#platform-client-secret').setValue('DISCARDED_SECRET_CANARY')
    await wrapper!.get('.platform-app-form').trigger('submit'); await flushPromises()
    await button('取消').trigger('click'); await flushPromises()
    expect(m.app).not.toHaveBeenCalled()
    expect(wrapper!.find('#platform-current-password').exists()).toBe(false)
  })
  it('confirms only the server candidate flow and version, never a client supplied subject', async () => {
    m.route.query = { platform_binding: 'server-flow', subject: 'forged-client-subject' }
    m.pending.mockResolvedValue({ flowId: 'server-flow', provider: 'github', displayLabel: 'Server verified account', expiresAt: new Date(Date.now() + 300000).toISOString(), configVersion: 3 })
    m.confirmBinding.mockResolvedValue({ provider: row({ enabled: true, binding: { displayLabel: 'Server verified account', issuer: 'https://github.com', subject: '9007199254740993', boundAt: new Date().toISOString() } }), sessionRevoked: false })
    await mountSettings()
    expect(m.replace).toHaveBeenCalledWith({ path: '/settings/account', query: { subject: 'forged-client-subject' } })
    expect(wrapper!.text()).toContain('Server verified account')
    expect(wrapper!.text()).not.toContain('forged-client-subject')
    await button('确认绑定并启用').trigger('click'); await flushPromises()
    expect(m.confirmBinding).toHaveBeenCalledWith('github', 3, 'server-flow')
  })
  it('returns to password login when the server says the current source session was revoked', async () => {
    const actual = snapshot(); actual.providers[0] = row({ enabled: true, binding: { displayLabel: 'linked', issuer: 'https://github.com', subject: '42', boundAt: '2026-11-02T00:00:00Z' } })
    m.get.mockResolvedValue(actual)
    m.enabled.mockResolvedValue({ provider: row({ enabled: false }), sessionRevoked: true })
    await mountSettings()
    await wrapper!.get('[data-provider="github"] .platform-configure').trigger('click'); await flushPromises()
    await button('停用').trigger('click'); await flushPromises()
    expect(m.confirm).toHaveBeenCalledTimes(1)
    expect(m.enabled).toHaveBeenCalledWith('github', 3, false)
    expect(m.clearSession).toHaveBeenCalledTimes(1)
    expect(m.replace).toHaveBeenLastCalledWith({ name: 'login', query: { redirect: '/settings/account' } })
  })
})

it('review regression: keeps a new visibility draft made during an in-flight refresh', async () => {
 await mountSettings()
 let finish!: (v: PlatformSnapshot) => void
 m.get.mockReturnValueOnce(new Promise<PlatformSnapshot>(resolve => { finish = resolve }))
 await button('刷新核对').trigger('click')
 await wrapper!.get('#platform-visible-github').trigger('click')
 expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
 finish(snapshot()); await flushPromises()
 expect(wrapper!.get('#platform-visible-github').attributes('aria-checked')).toBe('false')
})
it('review regression: a transient candidate-read failure remains retryable on explicit refresh', async () => {
 m.route.query = { platform_binding: 'unexpired-server-flow' }
 m.replace.mockImplementation(async value => { m.route.query = value.query })
 m.pending.mockRejectedValueOnce(new HttpError(0, null, 'transient network error')).mockResolvedValue({ flowId: 'unexpired-server-flow', provider: 'github', displayLabel: 'Verified candidate', expiresAt: new Date(Date.now() + 300000).toISOString(), configVersion: 3 })
 await mountSettings()
 expect(m.pending).toHaveBeenCalledTimes(1)
 expect(wrapper!.text()).toContain('暂时无法读取待绑定账号')
 expect(wrapper!.text()).not.toContain('授权已过期')
 expect(m.route.query.platform_binding).toBe('unexpired-server-flow')
 await button('刷新核对').trigger('click'); await flushPromises()
 expect(m.pending).toHaveBeenCalledTimes(2)
 expect(m.route.query.platform_binding).toBeUndefined()
 expect(wrapper!.text()).toContain('Verified candidate')
 expect(wrapper!.text()).not.toContain('暂时无法读取待绑定账号')
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { i18n, setLocale, SUPPORTED_LOCALES } from '../../i18n'
import SettingsVault from './SettingsVault.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), reconnect: vi.fn(), update: vi.fn() }))
vi.mock('../../api/credentials', () => ({ listCredentials: api.list, reconnectOAuthCredential: api.reconnect, updateCredential: api.update, createCredential: vi.fn(), deleteCredential: vi.fn(), revealCredential: vi.fn() }))
vi.mock('../../api/oauth', () => ({ getOAuthApps: vi.fn().mockResolvedValue([{ provider: 'gitea', enabled: true, configured: true }]), authorizeUrl: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('../../components/AuditTimeline.vue', () => ({ default: { render: () => null } }))
vi.mock('../../composables/useToast', () => ({ useToast: () => ({ success: vi.fn(), error: vi.fn() }) }))

const credential = {
  id: 'credential-1', name: 'Synthetic repository', type: 'git_token', scope: 'project', username: 'alice', maskedValue: '••••1234', createdAt: '2026-10-27T01:00:00Z', lastUsedAt: null,
  oauth: { managed: true, provider: 'gitea', account: 'alice', status: 'refresh_uncertain', expiresAt: null, refreshable: true, revision: 4 },
}
let wrapper: VueWrapper | undefined
beforeEach(() => { vi.clearAllMocks(); setLocale('zh-CN'); api.list.mockResolvedValue([credential]); api.reconnect.mockRejectedValue(new Error('unavailable')) })
afterEach(() => { wrapper?.unmount(); setLocale('zh-CN') })
function create() { wrapper = mount(SettingsVault, { global: { stubs: { Teleport: true, 'router-link': true } } }); return wrapper }

describe('OAuth credential lifecycle', () => {
  it('shows declared state and unknown expiry without refreshing credentials', async () => {
    const w = create(); await flushPromises()
    expect(w.get('[data-testid="oauth-lifecycle"]').text()).toContain('兑换结果不确定')
    expect(w.text()).toContain('未声明到期时间')
    expect(api.list).toHaveBeenCalledTimes(1)
    expect(api.reconnect).not.toHaveBeenCalled()
  })

  it('reconnects the same ID and revision only after explicit confirmation', async () => {
    const w = create(); await flushPromises(); await w.get('.oauth-reconnect').trigger('click')
    expect(api.reconnect).not.toHaveBeenCalled()
    await w.get('[data-testid="oauth-confirm-reconnect"]').trigger('click'); await flushPromises()
    expect(api.reconnect).toHaveBeenCalledWith('credential-1', { provider: 'gitea', revision: 4, adoptExisting: false })
    expect(w.get('[role="alert"]').text()).toContain('无法发起重新授权')
  })

  it('requires a provider and explicit adoption of an unknown older identity', async () => {
    api.list.mockResolvedValue([{ ...credential, oauth: undefined }])
    const w = create(); await flushPromises(); await w.get('.oauth-reconnect').trigger('click')
    expect(w.get('[data-testid="oauth-confirm-reconnect"]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid="oauth-provider"]').setValue('gitea')
    expect(w.get('[data-testid="oauth-confirm-reconnect"]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid="oauth-adopt"]').setValue(true)
    await w.get('[data-testid="oauth-confirm-reconnect"]').trigger('click'); await flushPromises()
    expect(api.reconnect).toHaveBeenCalledWith('credential-1', { provider: 'gitea', revision: 0, adoptExisting: true })
  })

  it('translates state, expiry, and a retained failure in all eight locales without resending', async () => {
    const w = create(); await flushPromises(); await w.get('.oauth-reconnect').trigger('click')
    await w.get('[data-testid="oauth-confirm-reconnect"]').trigger('click'); await flushPromises()
    for (const { code } of SUPPORTED_LOCALES) {
      setLocale(code); await flushPromises()
      expect(w.text()).not.toContain('settingsVault.oauth')
      expect(w.get('[role="alert"]').text()).toBe(i18n.global.t('settingsVault.oauthReconnectError'))
      expect(w.text()).toContain(i18n.global.t('settingsVault.oauthUnknownExpiry'))
    }
    expect(api.reconnect).toHaveBeenCalledTimes(1)
    expect(api.list).toHaveBeenCalledTimes(1)
  })

  it('requires explicit OAuth detachment when a manual secret replaces the managed value', async () => {
    const w = create(); await flushPromises()
    await w.get('button[aria-label="编辑凭据 Synthetic repository"]').trigger('click')
    await w.get('#cred-secret').setValue('synthetic-user-secret')
    await w.get('.modal-form').trigger('submit'); await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    await w.get('.oauth-detach input').setValue(true)
    api.update.mockResolvedValue({ ...credential, oauth: undefined })
    await w.get('.modal-form').trigger('submit'); await flushPromises()
    expect(api.update).toHaveBeenCalledWith('credential-1', expect.objectContaining({ detachOAuth: true, secret: 'synthetic-user-secret' }))
  })
})

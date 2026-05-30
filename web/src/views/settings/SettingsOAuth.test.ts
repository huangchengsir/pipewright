import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SettingsOAuth from './SettingsOAuth.vue'

// ─── module mocks ──────────────────────────────────────────────────────────

const getOAuthApps = vi.fn()
const saveOAuthApp = vi.fn()
vi.mock('../../api/oauth', async () => {
  const actual = await vi.importActual<typeof import('../../api/oauth')>('../../api/oauth')
  return {
    ...actual,
    getOAuthApps: (...args: unknown[]) => getOAuthApps(...args),
    saveOAuthApp: (...args: unknown[]) => saveOAuthApp(...args),
  }
})

const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock('../../composables/useToast', () => ({
  useToast: () => ({ success: toastSuccess, error: toastError }),
}))

function makeApp(over: Partial<import('../../api/oauth').OAuthApp> = {}) {
  return {
    provider: 'github' as const,
    clientId: 'existing-id',
    baseUrl: '',
    enabled: true,
    maskedSecret: '••••a91f',
    configured: true,
    updatedAt: '2026-05-30T00:00:00Z',
    ...over,
  }
}

describe('SettingsOAuth', () => {
  beforeEach(() => {
    getOAuthApps.mockReset()
    saveOAuthApp.mockReset()
    toastSuccess.mockReset()
    toastError.mockReset()
  })

  it('renders a card per provider and hydrates saved values', async () => {
    getOAuthApps.mockResolvedValue([makeApp()])
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    // four provider cards (gitee/github/gitlab/custom)
    expect(wrapper.findAll('[data-provider]')).toHaveLength(4)

    // github clientId hydrated from server
    const githubInput = wrapper.get('#oauth-clientid-github')
      .element as HTMLInputElement
    expect(githubInput.value).toBe('existing-id')
  })

  it('never echoes the plaintext secret; shows masked hint + keep-existing placeholder', async () => {
    getOAuthApps.mockResolvedValue([makeApp()])
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    const secret = wrapper.get('#oauth-secret-github').element as HTMLInputElement
    expect(secret.type).toBe('password')
    expect(secret.value).toBe('') // never pre-filled
    expect(secret.placeholder).toContain('留空保留已存')
    // masked echo is shown, plaintext is not
    expect(wrapper.text()).toContain('••••a91f')
  })

  it('only the self-hosted (custom) card exposes a Base URL field', async () => {
    getOAuthApps.mockResolvedValue([])
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    expect(wrapper.find('#oauth-baseurl-custom').exists()).toBe(true)
    expect(wrapper.find('#oauth-baseurl-github').exists()).toBe(false)
  })

  it('blocks save when enabling a provider with no Client ID', async () => {
    getOAuthApps.mockResolvedValue([
      makeApp({ provider: 'gitee', clientId: '', enabled: false, configured: false, maskedSecret: '' }),
    ])
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    const card = wrapper.get('[data-provider="gitee"]')
    // turn the enable toggle on
    await card.get('[role="switch"]').trigger('click')
    await card.get('form').trigger('submit')
    await flushPromises()

    expect(saveOAuthApp).not.toHaveBeenCalled()
    expect(card.text()).toContain('启用时 Client ID 必填')
  })

  it('omits clientSecret on save when left blank (keep existing) and toasts success', async () => {
    getOAuthApps.mockResolvedValue([makeApp()])
    saveOAuthApp.mockResolvedValue(makeApp())
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    await wrapper.get('[data-provider="github"] form').trigger('submit')
    await flushPromises()

    expect(saveOAuthApp).toHaveBeenCalledTimes(1)
    const [provider, payload] = saveOAuthApp.mock.calls[0]
    expect(provider).toBe('github')
    expect(payload).not.toHaveProperty('clientSecret')
    expect(payload).toMatchObject({ clientId: 'existing-id', enabled: true })
    expect(toastSuccess).toHaveBeenCalledWith('OAuth 应用已保存', { detail: 'GitHub' })
  })

  it('sends clientSecret when the user enters a new one (rotation)', async () => {
    getOAuthApps.mockResolvedValue([makeApp()])
    saveOAuthApp.mockResolvedValue(makeApp())
    const wrapper = mount(SettingsOAuth)
    await flushPromises()

    await wrapper.get('#oauth-secret-github').setValue('new-secret')
    await wrapper.get('[data-provider="github"] form').trigger('submit')
    await flushPromises()

    const payload = saveOAuthApp.mock.calls[0][1]
    expect(payload.clientSecret).toBe('new-secret')
  })
})

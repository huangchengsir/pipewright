import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SettingsVault from './SettingsVault.vue'

// ─── stub the OAuth-callback collaborators ───────────────────────────────────

const listCredentials = vi.fn()
vi.mock('../../api/credentials', () => ({
  listCredentials: (...a: unknown[]) => listCredentials(...a),
  createCredential: vi.fn(),
  updateCredential: vi.fn(),
  deleteCredential: vi.fn(),
}))

const getOAuthApps = vi.fn()
vi.mock('../../api/oauth', async () => {
  const actual = await vi.importActual<typeof import('../../api/oauth')>('../../api/oauth')
  return { ...actual, getOAuthApps: (...a: unknown[]) => getOAuthApps(...a) }
})

const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock('../../composables/useToast', () => ({
  useToast: () => ({ success: toastSuccess, error: toastError }),
}))

// AuditTimeline pulls its own data — stub it out of this unit.
vi.mock('../../components/AuditTimeline.vue', () => ({
  default: { name: 'AuditTimeline', render: () => null },
}))

// vue-router: drive route.query and capture replace() calls.
const routeQuery: { value: Record<string, string> } = { value: {} }
const replace = vi.fn()
vi.mock('vue-router', () => ({
  useRoute: () => ({ get query() { return routeQuery.value } }),
  useRouter: () => ({ replace }),
}))

function mountVault() {
  return mount(SettingsVault, {
    global: { stubs: { Teleport: true, 'router-link': true } },
  })
}

describe('SettingsVault — OAuth callback query handling', () => {
  beforeEach(() => {
    listCredentials.mockReset().mockResolvedValue([])
    getOAuthApps.mockReset().mockResolvedValue([])
    toastSuccess.mockReset()
    toastError.mockReset()
    replace.mockReset()
    routeQuery.value = {}
  })

  it('shows a success toast and refreshes credentials on ?connected=&account=', async () => {
    routeQuery.value = { connected: 'github', account: 'octocat' }
    mountVault()
    await flushPromises()

    expect(toastSuccess).toHaveBeenCalledWith(
      '已连接 GitHub(octocat)',
      expect.objectContaining({ detail: expect.any(String) }),
    )
    // credentials loaded once on mount + once on callback refresh
    expect(listCredentials).toHaveBeenCalledTimes(2)
    // query is stripped so a refresh doesn't re-fire
    expect(replace).toHaveBeenCalledWith({ query: {} })
  })

  it('falls back to provider-only message when account is absent', async () => {
    routeQuery.value = { connected: 'gitee' }
    mountVault()
    await flushPromises()

    expect(toastSuccess).toHaveBeenCalledWith(
      '已连接 Gitee',
      expect.any(Object),
    )
  })

  it('shows an error toast on ?oauth_error= and clears the query', async () => {
    routeQuery.value = { oauth_error: 'access_denied' }
    mountVault()
    await flushPromises()

    expect(toastError).toHaveBeenCalledWith('连接失败', { detail: 'access_denied' })
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(replace).toHaveBeenCalledWith({ query: {} })
  })

  it('does nothing (no toast) on a clean URL', async () => {
    mountVault()
    await flushPromises()

    expect(toastSuccess).not.toHaveBeenCalled()
    expect(toastError).not.toHaveBeenCalled()
    expect(replace).not.toHaveBeenCalled()
  })

  it('preserves unrelated query params when clearing OAuth keys', async () => {
    routeQuery.value = { connected: 'gitlab', account: 'me', tab: 'x' }
    mountVault()
    await flushPromises()

    expect(replace).toHaveBeenCalledWith({ query: { tab: 'x' } })
  })

  it('renders connect buttons only for enabled & configured providers', async () => {
    getOAuthApps.mockResolvedValue([
      { provider: 'github', clientId: 'a', baseUrl: '', enabled: true, maskedSecret: 'm', configured: true, updatedAt: null },
      { provider: 'gitee', clientId: 'b', baseUrl: '', enabled: false, maskedSecret: 'm', configured: true, updatedAt: null },
      { provider: 'gitlab', clientId: '', baseUrl: '', enabled: true, maskedSecret: '', configured: false, updatedAt: null },
    ])
    const wrapper = mountVault()
    await flushPromises()

    const buttons = wrapper.findAll('.connect-btn')
    expect(buttons).toHaveLength(1)
    expect(buttons[0].text()).toContain('连接 GitHub')
  })
})

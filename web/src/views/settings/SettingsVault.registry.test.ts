import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SettingsVault from './SettingsVault.vue'

const api = vi.hoisted(() => ({
  list: vi.fn().mockResolvedValue([]),
  create: vi.fn(),
  update: vi.fn(),
}))
vi.mock('../../api/credentials', () => ({
  listCredentials: api.list,
  createCredential: api.create,
  updateCredential: api.update,
  deleteCredential: vi.fn(),
}))
vi.mock('../../api/oauth', () => ({
  getOAuthApps: vi.fn().mockResolvedValue([]),
}))
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}))
vi.mock('../../components/AuditTimeline.vue', () => ({
  default: { render: () => null },
}))
vi.mock('../../composables/useToast', () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}))
let w: VueWrapper | undefined
afterEach(() => {
  w?.unmount()
  vi.clearAllMocks()
})
describe('registry independent fields', () => {
  it('sends the exact password independently of the username and clears it on close', async () => {
    api.create.mockResolvedValue({
      id: 'registry',
      name: 'Nexus',
      type: 'registry',
      username: 'alice',
      maskedValue: 'alice ••••',
      scope: '',
      lastUsedAt: null,
      createdAt: new Date().toISOString(),
    })
    w = mount(SettingsVault, {
      global: { stubs: { Teleport: true, 'router-link': true } },
    })
    await flushPromises()
    const add = w.findAll('button').find((b) => b.text() === '添加凭据')!
    await add.trigger('click')
    const registry = w
      .findAll('.seg-item')
      .find((b) => b.text().includes('镜像'))!
    await registry.trigger('click')
    await w.get('#cred-name').setValue('Nexus')
    await w.get('#cred-username').setValue('alice')
    await w.get('#cred-secret').setValue(' p:a:ss ')
    expect(w.get('#cred-secret').attributes('type')).toBe('password')
    await w.get('.modal-form').trigger('submit')
    await flushPromises()
    expect(api.create).toHaveBeenCalledWith({
      name: 'Nexus',
      type: 'registry',
      scope: '',
      username: 'alice',
      secret: ' p:a:ss ',
    })
    expect(w.find('#cred-secret').exists()).toBe(false)
  })
  it('rejects colon usernames before calling the API', async () => {
    w = mount(SettingsVault, {
      global: { stubs: { Teleport: true, 'router-link': true } },
    })
    await flushPromises()
    await w
      .findAll('button')
      .find((b) => b.text() === '添加凭据')!
      .trigger('click')
    await w
      .findAll('.seg-item')
      .find((b) => b.text().includes('镜像'))!
      .trigger('click')
    await w.get('#cred-name').setValue('Nexus')
    await w.get('#cred-username').setValue('bad:user')
    await w.get('#cred-secret').setValue('password')
    await w.get('.modal-form').trigger('submit')
    await flushPromises()
    expect(api.create).not.toHaveBeenCalled()
    expect(w.text()).toContain('用户名不能包含冒号')
  })
})

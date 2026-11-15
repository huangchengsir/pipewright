import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import Login from './Login.vue'
import { login } from '../api/auth'
import { useSessionStore } from '../stores/session'

const router = vi.hoisted(() => ({ replace: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => router, useRoute: () => ({ query: {} }) }))
vi.mock('../api/auth', () => ({ login: vi.fn() }))
vi.mock('../composables/useLoginViewport', async () => {
  const { ref } = await import('vue')
  return { useLoginViewport: () => ({ overflow: ref(false), minHeight: ref(0), cardTop: ref(0) }) }
})
vi.mock('../composables/useOnboarding', () => ({ onboardingLoginTarget: vi.fn(async () => '/dashboard') }))

it('keeps password reveal local and submits through the native login button with valid inputs', async () => {
  const pinia = createPinia()
  setActivePinia(pinia)
  vi.mocked(login).mockResolvedValue({ username: 'fixture-user', multiTenant: true })
  vi.stubGlobal('matchMedia', () => ({ matches: true }))
  const wrapper = mount(Login, { attachTo: document.body, global: { plugins: [pinia], stubs: { PlatformLoginEntry: true } } })
  try {
    const button = wrapper.get<HTMLButtonElement>('form button[type="submit"]')
    button.element.click()
    await flushPromises()
    expect(login).not.toHaveBeenCalled()
    const password = wrapper.get<HTMLInputElement>('#password')
    await wrapper.get('#username').setValue(' fixture-user ')
    await password.setValue('synthetic-acceptance-phrase')
    await wrapper.get('.reveal').trigger('click')
    expect(password.element.type).toBe('text')
    await wrapper.get('.reveal').trigger('click')
    expect(password.element.type).toBe('password')
    expect(login).not.toHaveBeenCalled()
    button.element.click()
    await flushPromises()
    expect(login).toHaveBeenCalledExactlyOnceWith('fixture-user', 'synthetic-acceptance-phrase')
    expect(useSessionStore().user?.username).toBe('fixture-user')
    expect(router.replace).toHaveBeenCalledExactlyOnceWith('/dashboard')
  } finally {
    wrapper.unmount()
    vi.unstubAllGlobals()
  }
})

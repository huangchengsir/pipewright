import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AppShell from './AppShell.vue'
import { i18n, setLocale } from '../i18n'

const preferenceKey = 'pipewright_sidebar_expanded'
const compactQuery = '(max-width: 767px)'
type MediaListener = (event: MediaQueryListEvent) => void
let wrappers: VueWrapper[] = []

function viewport(initial: boolean) {
  let matches = initial
  const listeners = new Set<MediaListener>()
  const add = vi.fn((_type: string, listener: MediaListener) => listeners.add(listener))
  const remove = vi.fn((_type: string, listener: MediaListener) => listeners.delete(listener))
  const media = { get matches() { return matches }, media: compactQuery, onchange: null, addEventListener: add, removeEventListener: remove, addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: () => true } as unknown as MediaQueryList
  const original = window.matchMedia.bind(window)
  vi.spyOn(window, 'matchMedia').mockImplementation(query => query === compactQuery ? media : original(query))
  return {
    listeners, add, remove,
    change(value: boolean, notify = true) {
      matches = value
      if (notify) for (const listener of listeners) listener({ matches: value, media: compactQuery } as MediaQueryListEvent)
    },
  }
}
async function render(compact: boolean, preference?: '0' | '1') {
  if (preference !== undefined) localStorage.setItem(preferenceKey, preference)
  const media = viewport(compact)
  const storage = vi.spyOn(Storage.prototype, 'setItem')
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div>Content</div>' } }] })
  await router.push('/runs/root'); await router.isReady()
  const wrapper = mount(AppShell, { attachTo: document.body, global: { plugins: [router, createPinia()], stubs: { ThemeToggle: true, ContinueOnboarding: true } } })
  wrappers.push(wrapper); await flushPromises()
  return { wrapper, media, writes: () => storage.mock.calls.filter(([key]) => key === preferenceKey) }
}
beforeEach(() => { localStorage.clear(); setLocale('zh-CN') })
afterEach(() => { wrappers.forEach(wrapper => wrapper.unmount()); wrappers = []; document.body.innerHTML = ''; vi.restoreAllMocks(); setLocale('zh-CN') })

describe('viewport-scoped sidebar state', () => {
  it('starts compact at375px even when the desktop preference is expanded, without overwriting it', async () => {
    const { wrapper, writes } = await render(true, '1')
    expect(wrapper.classes()).not.toContain('is-expanded')
    expect(wrapper.get('.rail-edge-toggle').attributes('aria-pressed')).toBe('false')
    expect(localStorage.getItem(preferenceKey)).toBe('1'); expect(writes()).toEqual([])
  })
  it.each(['0', '1'] as const)('restores desktop preference %s across compact viewport changes', async preference => {
    const { wrapper, media, writes } = await render(false, preference)
    expect(wrapper.classes().includes('is-expanded')).toBe(preference === '1')
    media.change(true); await flushPromises(); expect(wrapper.classes()).not.toContain('is-expanded')
    await wrapper.get('.rail-edge-toggle').trigger('click'); expect(wrapper.classes()).toContain('is-expanded')
    expect(localStorage.getItem(preferenceKey)).toBe(preference); expect(writes()).toEqual([])
    media.change(false); await flushPromises(); expect(wrapper.classes().includes('is-expanded')).toBe(preference === '1')
    media.change(true); await flushPromises(); expect(wrapper.classes()).not.toContain('is-expanded')
    expect(writes()).toEqual([])
  })
  it('keeps the existing desktop toggle persistence and native button focus', async () => {
    const { wrapper, writes } = await render(false, '0')
    const button = wrapper.get('.rail-edge-toggle'); (button.element as HTMLButtonElement).focus()
    expect(button.attributes('type')).toBe('button')
    await button.trigger('click'); expect(localStorage.getItem(preferenceKey)).toBe('1'); expect(document.activeElement).toBe(button.element)
    await button.trigger('click'); expect(localStorage.getItem(preferenceKey)).toBe('0')
    expect(writes()).toEqual([[preferenceKey, '1'], [preferenceKey, '0']])
  })
  it('uses the live compact viewport when a click arrives before its change event', async () => {
    const { wrapper, media, writes } = await render(false, '0')
    media.change(true, false); await wrapper.get('.rail-edge-toggle').trigger('click')
    expect(wrapper.classes()).toContain('is-expanded'); expect(localStorage.getItem(preferenceKey)).toBe('0'); expect(writes()).toEqual([])
    media.change(false); await flushPromises(); expect(wrapper.classes()).not.toContain('is-expanded')
  })
  it('keeps compact-toggle labels live in all8 locales without new stored preferences', async () => {
    const { wrapper, writes } = await render(true, '0'); const button = wrapper.get('.rail-edge-toggle')
    for (const locale of ['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'es', 'fr', 'de'] as const) {
      setLocale(locale); await flushPromises(); expect(button.attributes('aria-label')).toBe(i18n.global.t('nav.expand'))
      ;(button.element as HTMLButtonElement).focus(); await button.trigger('click')
      expect(button.attributes('aria-pressed')).toBe('true'); expect(button.attributes('aria-label')).toBe(i18n.global.t('nav.collapse')); expect(document.activeElement).toBe(button.element)
      await button.trigger('click')
    }
    expect(writes()).toEqual([])
  })
  it('removes its viewport listener on unmount', async () => {
    const { wrapper, media, writes } = await render(true, '1')
    expect(media.listeners.size).toBe(1)
    wrapper.unmount(); wrappers = wrappers.filter(item => item !== wrapper)
    expect(media.listeners.size).toBe(0); expect(media.remove).toHaveBeenCalledWith('change', media.add.mock.calls[0][1])
    media.change(false); expect(writes()).toEqual([])
  })
})

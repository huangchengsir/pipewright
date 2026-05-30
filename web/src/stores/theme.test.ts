import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { nextTick } from 'vue'
import { useThemeStore } from './theme'

const KEY = 'pipewright-theme'

describe('theme store', () => {
  beforeEach(() => {
    localStorage.clear()
    delete document.documentElement.dataset.theme
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('defaults to light when nothing is stored', () => {
    const store = useThemeStore()
    expect(store.current).toBe('light')
  })

  it('applies the theme to <html data-theme> on init', () => {
    useThemeStore()
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('reads a previously stored light theme', () => {
    localStorage.setItem(KEY, 'light')
    const store = useThemeStore()
    expect(store.current).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('ignores a corrupt stored value and falls back to default', () => {
    localStorage.setItem(KEY, 'rainbow')
    const store = useThemeStore()
    expect(store.current).toBe('light')
  })

  it('toggle flips light <-> dark and persists + applies to DOM', async () => {
    const store = useThemeStore()
    store.toggle()
    expect(store.current).toBe('dark')
    await nextTick()
    expect(localStorage.getItem(KEY)).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')

    store.toggle()
    expect(store.current).toBe('light')
    await nextTick()
    expect(localStorage.getItem(KEY)).toBe('light')
  })

  it('degrades gracefully when localStorage.getItem throws (private mode)', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    const store = useThemeStore()
    expect(store.current).toBe('light') // fell back, did not crash
  })
})

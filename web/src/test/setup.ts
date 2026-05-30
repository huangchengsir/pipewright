/**
 * Vitest global setup (jsdom).
 *
 * jsdom 不实现 matchMedia / ResizeObserver,部分组件(主题、动效降级)
 * 间接触达它们 → 这里提供最小 stub,避免无关报错污染单测信号。
 * 真实断言仍由各 spec 自行 mock 所需 API(如 fetch / localStorage)。
 */
import { vi } from 'vitest'

// Node 25 ships a native global `localStorage` stub (the `--localstorage-file`
// experiment) that shadows jsdom's implementation but lacks getItem/clear/etc.
// Install a real in-memory Web Storage so theme/onboarding code under test works.
class MemoryStorage implements Storage {
  private store = new Map<string, string>()
  get length(): number {
    return this.store.size
  }
  clear(): void {
    this.store.clear()
  }
  getItem(key: string): string | null {
    return this.store.has(key) ? this.store.get(key)! : null
  }
  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null
  }
  removeItem(key: string): void {
    this.store.delete(key)
  }
  setItem(key: string, value: string): void {
    this.store.set(key, String(value))
  }
}

if (typeof localStorage === 'undefined' || typeof localStorage.clear !== 'function') {
  const ls = new MemoryStorage()
  const ss = new MemoryStorage()
  Object.defineProperty(globalThis, 'localStorage', { value: ls, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: ss, configurable: true })
  Object.defineProperty(window, 'localStorage', { value: ls, configurable: true })
  Object.defineProperty(window, 'sessionStorage', { value: ss, configurable: true })
}

// matchMedia stub — prefers-reduced-motion / 主题查询会用到
if (!window.matchMedia) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }))
}

// ResizeObserver stub — naive-ui 等组件挂载时可能引用
if (!('ResizeObserver' in globalThis)) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

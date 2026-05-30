import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type Theme = 'dark' | 'light'

const STORAGE_KEY = 'pipewright-theme'
const DEFAULT_THEME: Theme = 'light'

function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme
}

/**
 * Read a stored theme value.
 *
 * Falls back to DEFAULT_THEME when:
 *   - localStorage is unavailable (private mode / quota / security policy)
 *   - stored value is not a recognised Theme literal
 */
function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // Silently ignore — private mode, QuotaExceededError, SecurityError, etc.
  }
  return DEFAULT_THEME
}

function writeStoredTheme(theme: Theme): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // Silently ignore — degrade to in-memory state only
  }
}

export const useThemeStore = defineStore('theme', () => {
  const current = ref<Theme>(readStoredTheme())

  // Apply immediately on init
  applyTheme(current.value)

  function toggle(): void {
    current.value = current.value === 'dark' ? 'light' : 'dark'
  }

  // Sync to DOM + localStorage on every change
  watch(current, (theme) => {
    applyTheme(theme)
    writeStoredTheme(theme)
  })

  return { current, toggle }
})

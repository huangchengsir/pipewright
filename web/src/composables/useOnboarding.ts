/**
 * useOnboarding — derives first-run onboarding state on the frontend.
 *
 * Per the frozen 1.7 contract there is NO onboarding backend endpoint:
 *   - hasProject  → derived from GET /api/projects (list length > 0)
 *   - hasAI       → always false this story (7-1 not built — UI shows "即将可用")
 *   - hasServer   → always false this story (4-1 not built — UI shows "即将可用")
 *
 * "Skip / dismissed" is persisted in localStorage under `onboarding_dismissed`.
 * The account settings "重新引导" button clears that flag.
 */

import { ref } from 'vue'
import { listProjects } from '../api/projects'

const DISMISS_KEY = 'onboarding_dismissed'

export function isOnboardingDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISS_KEY) === '1'
  } catch {
    return false
  }
}

export function dismissOnboarding(): void {
  try {
    localStorage.setItem(DISMISS_KEY, '1')
  } catch {
    // localStorage unavailable (private mode) — non-fatal; user simply re-sees onboarding.
  }
}

/** Clear the dismissed flag so onboarding shows again ("重新引导"). */
export function resetOnboarding(): void {
  try {
    localStorage.removeItem(DISMISS_KEY)
  } catch {
    // non-fatal
  }
}

export interface OnboardingStatus {
  hasAI: boolean
  hasServer: boolean
  hasProject: boolean
}

export function useOnboardingStatus() {
  const status = ref<OnboardingStatus>({ hasAI: false, hasServer: false, hasProject: false })
  const loading = ref(true)
  const error = ref<string | null>(null)

  async function refresh(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const projects = await listProjects()
      // hasAI / hasServer are always false this story (7-1 / 4-1 not built).
      status.value = { hasAI: false, hasServer: false, hasProject: projects.length > 0 }
    } catch (err) {
      error.value = err instanceof Error ? err.message : '无法加载引导状态'
    } finally {
      loading.value = false
    }
  }

  return { status, loading, error, refresh }
}

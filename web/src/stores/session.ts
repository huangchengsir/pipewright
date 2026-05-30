/**
 * Session store — caches the auth session so route guards don't
 * hit /api/auth/session on every navigation.
 *
 * Error semantics:
 *   - 401  → user is not logged in  (session = null, isNetworkError = false)
 *   - 5xx / network → backend unreachable (session stays as-is if already
 *     loaded; isNetworkError = true so callers can show a fault state
 *     instead of kicking a logged-in user to /login)
 */

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { http, HttpError } from '../api/http'

export interface SessionUser {
  username: string
}

/** Discriminated result returned by fetchSession */
export type SessionResult =
  | { kind: 'ok'; user: SessionUser }
  | { kind: 'unauthenticated' }
  | { kind: 'error'; status: number; message: string }

export const useSessionStore = defineStore('session', () => {
  /** null = not logged in (confirmed 401); undefined = not yet fetched */
  const user = ref<SessionUser | null | undefined>(undefined)
  const isNetworkError = ref(false)

  /** Whether we have already fetched once (cache primed). */
  let fetched = false

  /**
   * Fetch (or return cached) session.
   *
   * - Returns { kind:'ok' }            — valid session
   * - Returns { kind:'unauthenticated' } — confirmed 401
   * - Returns { kind:'error' }           — 5xx / network; does NOT clear
   *   an existing cached session so a logged-in user is not evicted.
   *
   * Pass force=true to bypass cache (e.g. after explicit logout).
   */
  async function ensureSession(force = false): Promise<SessionResult> {
    if (fetched && !force) {
      if (user.value === null) return { kind: 'unauthenticated' }
      if (user.value !== undefined) return { kind: 'ok', user: user.value }
    }

    try {
      const data = await http.get<SessionUser>('/api/auth/session')
      user.value = data
      isNetworkError.value = false
      fetched = true
      return { kind: 'ok', user: data }
    } catch (err) {
      if (err instanceof HttpError && err.status === 401) {
        // Confirmed not logged in
        user.value = null
        isNetworkError.value = false
        fetched = true
        return { kind: 'unauthenticated' }
      }

      // 5xx or network error — do NOT overwrite a valid cached session
      isNetworkError.value = true
      fetched = true // mark fetched so we don't hammer on every nav
      const status = err instanceof HttpError ? err.status : 0
      const message =
        err instanceof HttpError
          ? (err.apiError?.message ?? err.message)
          : err instanceof Error
            ? err.message
            : 'Network error'
      return { kind: 'error', status, message }
    }
  }

  /** Prime the cache with a known user (call after a successful login). */
  function setUser(u: SessionUser): void {
    user.value = u
    isNetworkError.value = false
    fetched = true
  }

  /** Clear the cached session (call after logout). */
  function clearSession(): void {
    user.value = null
    isNetworkError.value = false
    fetched = true
  }

  return { user, isNetworkError, ensureSession, setUser, clearSession }
})

/**
 * Auth API — aligns to frozen 1.2 contract.
 *
 * GET  /api/auth/session → { username } | 401
 * POST /api/auth/login   → { username } | 401 | 429
 * POST /api/auth/logout  → 204
 */

import { http, HttpError } from './http'

export interface SessionUser {
  username: string
}

/**
 * Fetch the current session from the server.
 *
 * Returns:
 *   - SessionUser   — authenticated
 *   - null          — confirmed 401 (not logged in)
 *
 * Throws HttpError (status ≠ 401) or network Error for 5xx / network faults,
 * so callers can distinguish "not logged in" from "backend down".
 */
export async function fetchSession(): Promise<SessionUser | null> {
  try {
    return await http.get<SessionUser>('/api/auth/session')
  } catch (err) {
    if (err instanceof HttpError && err.status === 401) {
      return null
    }
    // Re-throw so route guards / stores can handle 5xx / network errors
    // without incorrectly treating an authenticated user as unauthenticated.
    throw err
  }
}

export async function login(username: string, password: string): Promise<SessionUser> {
  return http.post<SessionUser>('/api/auth/login', { username, password })
}

export async function logout(): Promise<void> {
  await http.post<void>('/api/auth/logout')
}

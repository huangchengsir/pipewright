/**
 * Account API — aligns to frozen 1.7 contract.
 *
 * POST   /api/account/password        → 204  (needs CSRF)
 *   { currentPassword, newPassword }
 *   401 invalid_current_password · 422 weak_password
 *   On success the server revokes every OTHER session (current token kept).
 * GET    /api/account/sessions        → { sessions: Session[] }
 *   id = sha256 prefix of the token — the raw token is NEVER returned.
 * DELETE /api/account/sessions/:id     → 204  (needs CSRF)
 *   404 session_not_found
 */

import { http } from './http'

export interface Session {
  /** sha256 hex prefix of the session token — never the raw token. */
  id: string
  createdAt: string
  lastSeenAt: string
  expiresAt: string
  /** True for the session that issued this request. */
  current: boolean
}

export interface ChangePasswordInput {
  currentPassword: string
  newPassword: string
}

export async function changePassword(input: ChangePasswordInput): Promise<void> {
  return http.post<void>('/api/account/password', input)
}

export async function listSessions(): Promise<Session[]> {
  const res = await http.get<{ sessions: Session[] }>('/api/account/sessions')
  return res.sessions
}

export async function revokeSession(id: string): Promise<void> {
  return http.delete<void>(`/api/account/sessions/${id}`)
}

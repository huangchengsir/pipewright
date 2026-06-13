/**
 * Credentials API — aligns to frozen 1.3 contract.
 *
 * GET    /api/credentials            → Credential[]
 * POST   /api/credentials            → Credential  (needs CSRF)
 * PATCH  /api/credentials/:id        → Credential  (needs CSRF)
 * DELETE /api/credentials/:id        → 204          (needs CSRF)
 * POST   /api/credentials/:id/reveal → { secret }   (needs CSRF; audited)
 *
 * List/get never return plaintext — only maskedValue is exposed. Plaintext is
 * returned solely by the explicit, audited reveal endpoint.
 */

import { http } from './http'

export type CredentialType = 'git_token' | 'ssh_key' | 'ssh_password' | 'registry'

export interface Credential {
  id: string
  name: string
  type: CredentialType
  scope: string
  /** Server-computed mask, e.g. "ghp_••••a91f" — never plaintext. */
  maskedValue: string
  lastUsedAt: string | null
  createdAt: string
}

export interface CreateCredentialInput {
  name: string
  type: CredentialType
  scope: string
  /** Plaintext secret — sent once on creation, never returned by the server. */
  secret: string
}

export interface UpdateCredentialInput {
  name?: string
  scope?: string
  /** Providing secret rotates the key. */
  secret?: string
}

export async function listCredentials(): Promise<Credential[]> {
  return http.get<Credential[]>('/api/credentials')
}

export async function createCredential(input: CreateCredentialInput): Promise<Credential> {
  return http.post<Credential>('/api/credentials', input)
}

export async function updateCredential(
  id: string,
  input: UpdateCredentialInput,
): Promise<Credential> {
  return http.patch<Credential>(`/api/credentials/${id}`, input)
}

export async function deleteCredential(id: string): Promise<void> {
  return http.delete<void>(`/api/credentials/${id}`)
}

/**
 * Reveal the plaintext secret on explicit demand (POST + CSRF; audited server-side
 * as `credential_reveal`). The only endpoint that returns plaintext — use sparingly.
 */
export async function revealCredential(id: string): Promise<string> {
  const res = await http.post<{ secret: string }>(`/api/credentials/${id}/reveal`, {})
  return res.secret
}

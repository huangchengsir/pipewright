/**
 * OAuth Apps API — aligns to the frozen OAuth contract.
 *
 * GET /api/oauth/apps               → OAuthApp[]
 * PUT /api/oauth/apps/{provider}    → OAuthApp  (needs CSRF)
 * GET /api/oauth/{provider}/authorize → backend 302 to the git platform.
 *      This is NOT fetched — the browser must do a full-page navigation so
 *      the 302 redirect chain runs. Use `authorizeUrl(provider)` with
 *      `window.location.href = …`, never `http.get`.
 *
 * clientSecret is WRITE-ONLY: the server never returns plaintext.
 * GET/PUT responses only include maskedSecret (e.g. "••••a91f").
 * Leaving clientSecret empty on PUT keeps the existing secret unchanged.
 */

import { http } from './http'

export type OAuthProvider = 'gitee' | 'github' | 'gitlab' | 'custom'

/** GET /api/oauth/apps element — never contains plaintext clientSecret. */
export interface OAuthApp {
  provider: OAuthProvider
  clientId: string
  baseUrl: string
  enabled: boolean
  /** Server-computed mask, e.g. "••••a91f" — never plaintext. */
  maskedSecret: string
  configured: boolean
  updatedAt: string | null
}

/** PUT /api/oauth/apps/{provider} request body. */
export interface SaveOAuthAppInput {
  clientId: string
  /**
   * Write-only: omit or leave empty to keep the existing secret unchanged.
   * Non-empty rotates to the new secret.
   */
  clientSecret?: string
  /** Only meaningful for the self-hosted (`custom`) provider. */
  baseUrl?: string
  enabled: boolean
}

export async function getOAuthApps(): Promise<OAuthApp[]> {
  return http.get<OAuthApp[]>('/api/oauth/apps')
}

export async function saveOAuthApp(
  provider: OAuthProvider,
  input: SaveOAuthAppInput,
): Promise<OAuthApp> {
  return http.put<OAuthApp>(`/api/oauth/apps/${provider}`, input)
}

/**
 * Build the authorize entrypoint URL for a provider.
 *
 * The caller must perform a full-page navigation:
 *   window.location.href = authorizeUrl(provider)
 * The backend responds with a 302 to the git platform's consent screen.
 * Do NOT fetch this — fetch would follow the redirect without leaving the SPA.
 */
export function authorizeUrl(provider: OAuthProvider): string {
  return `/api/oauth/${provider}/authorize`
}

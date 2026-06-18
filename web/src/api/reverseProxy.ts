/**
 * Reverse-proxy / auto-HTTPS API client (R1 / FR-1..FR-5).
 *
 * A `ProxyRoute` binds one domain on a target host to an upstream container:port.
 * Caddy (managed per-host) issues & renews a Let's Encrypt certificate over
 * HTTP-01 automatically — the user only points an A record at the host. The cert
 * lifecycle is reflected by `certStatus` (pending / issued / failed) and the
 * human-readable `certDetail`.
 *
 * GET    /api/proxy/routes?serverId=<id>        → { items: ProxyRoute[] }
 * POST   /api/proxy/routes                      → ProxyRoute   (needs CSRF)
 * POST   /api/proxy/routes/{id}/enabled         → ProxyRoute   (needs CSRF)
 * POST   /api/proxy/routes/{id}/refresh         → ProxyRoute   (needs CSRF)
 * DELETE /api/proxy/routes/{id}                 → { ok: true } (needs CSRF)
 *
 * Writes go through the shared `http` wrapper, which auto-attaches the CSRF token
 * (cookie `pipewright_csrf` → `X-CSRF-Token`) and the active UI locale. Never
 * returns secrets.
 */

import { http } from './http'

/** Certificate lifecycle for a route, as reported by the backend / Caddy. */
export type CertStatus = 'pending' | 'issued' | 'failed'

/** TLS mode — only Let's Encrypt auto-issuance in this MVP. */
export type TlsMode = 'auto'

/** One domain → upstream container:port reverse-proxy route on a host. */
export interface ProxyRoute {
  id: string
  serverId: string
  /** Bound FQDN, e.g. `app.mydomain.com`. */
  domain: string
  /** Upstream container name Caddy reverse-proxies to (resolved on the shared network). */
  upstreamContainer: string
  /** Upstream port inside the container, e.g. `8080`. */
  upstreamPort: number
  tlsMode: TlsMode
  /** Whether the route is active (rendered into Caddy and serving). */
  enabled: boolean
  certStatus: CertStatus
  /** Human-readable cert detail: issuer/expiry when issued, the failure reason when failed. Never a secret. */
  certDetail: string
  createdAt: string
  updatedAt: string
}

/** Body for creating a route. Upstream port defaults to the container's exposed port when known. */
export interface CreateProxyRouteInput {
  serverId: string
  domain: string
  upstreamContainer: string
  upstreamPort: number
}

/** List all reverse-proxy routes bound on a single server. */
export async function listProxyRoutes(serverId: string): Promise<ProxyRoute[]> {
  const res = await http.get<{ items: ProxyRoute[] }>(
    `/api/proxy/routes?serverId=${encodeURIComponent(serverId)}`,
  )
  return res.items ?? []
}

/** Bind a new domain → container:port route (FR-1). Returns the created route. */
export async function createProxyRoute(input: CreateProxyRouteInput): Promise<ProxyRoute> {
  return http.post<ProxyRoute>('/api/proxy/routes', input)
}

/** Enable / disable a route (FR-5). Re-renders Caddy and reloads. Returns the updated route. */
export async function setProxyRouteEnabled(id: string, enabled: boolean): Promise<ProxyRoute> {
  return http.post<ProxyRoute>(`/api/proxy/routes/${encodeURIComponent(id)}/enabled`, { enabled })
}

/**
 * Re-check / retry a route's certificate (FR-4): re-runs the ACME order so a
 * fixed DNS / port can be picked up. Returns the route with refreshed certStatus.
 */
export async function refreshProxyRoute(id: string): Promise<ProxyRoute> {
  return http.post<ProxyRoute>(`/api/proxy/routes/${encodeURIComponent(id)}/refresh`, {})
}

/** Delete a route (FR-5). Removes it from Caddy and reloads; the cert volume is kept. */
export async function deleteProxyRoute(id: string): Promise<{ ok: boolean }> {
  return http.delete<{ ok: boolean }>(`/api/proxy/routes/${encodeURIComponent(id)}`)
}

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
 * PUT    /api/proxy/routes/{id}                 → ProxyRoute   (needs CSRF)  (R2 / FR-6..FR-9)
 * POST   /api/proxy/routes/{id}/enabled         → ProxyRoute   (needs CSRF)
 * POST   /api/proxy/routes/{id}/refresh         → ProxyRoute   (needs CSRF)
 * DELETE /api/proxy/routes/{id}                 → { ok: true } (needs CSRF)
 * GET    /api/proxy/overview                    → { items: ProxyRouteOverview[] }  (R2 / FR-10)
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

/** Redirect HTTP statuses we support (R2 / FR-9). */
export type RedirectStatus = 301 | 302 | 307 | 308

/** One HTTP redirect rule on a route: `from` path/host → `to` URL with a status. */
export interface Redirect {
  from: string
  to: string
  status: RedirectStatus
}

/**
 * Per-route advanced config (R2 / FR-6..FR-9). Returned on every route; mutated
 * via `updateProxyRoute`. The Basic-Auth password is **write-only** — it is never
 * returned (only `basicAuthEnabled` reflects whether one is set).
 */
export interface ProxyRouteConfig {
  /** Extra FQDNs served by the same route (besides `domain`). */
  aliases: string[]
  /** Redirect plain HTTP → HTTPS (default on for `auto` TLS). */
  forceHttps: boolean
  /** Emit `Strict-Transport-Security`. */
  hsts: boolean
  /** Emit hardening headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy…). */
  securityHeaders: boolean
  /** gzip/zstd response compression. */
  compression: boolean
  /** Basic-Auth username (`''` if auth disabled). */
  basicAuthUser: string
  /** Server-derived: whether a Basic-Auth password hash is stored. Never sent on write. */
  basicAuthEnabled: boolean
  /** IP allow list (CIDRs). Non-empty → only these may reach the upstream. */
  ipAllow: string[]
  /** IP deny list (CIDRs). Matching clients are rejected. */
  ipDeny: string[]
  /** HTTP redirect rules. */
  redirects: Redirect[]
}

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
  /** R2 advanced config (multi-domain, access control, hardening, redirects). */
  config: ProxyRouteConfig
  createdAt: string
  updatedAt: string
}

/** A route enriched with its host's display name — for the cross-host overview dashboard. */
export interface ProxyRouteOverview extends ProxyRoute {
  /** Human-readable server name (from the server registry). */
  serverName: string
}

/**
 * Body for `updateProxyRoute`. Send `config` minus the server-derived
 * `basicAuthEnabled`; send `basicAuthPassword` **only** when the user typed a new
 * one (omit/empty keeps the existing password). Upstream fields are optional —
 * omit to leave the binding unchanged.
 */
export interface UpdateProxyRouteInput {
  upstreamContainer?: string
  upstreamPort?: number
  config: Omit<ProxyRouteConfig, 'basicAuthEnabled'>
  /** New Basic-Auth password (write-only). Omit / `''` to keep the existing one. */
  basicAuthPassword?: string
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

/**
 * Update a route's upstream binding and/or advanced config (R2 / FR-6..FR-9).
 * Re-renders Caddy and reloads. Returns the updated route. The Basic-Auth
 * password is write-only — only pass `basicAuthPassword` when the user typed a
 * new one (an empty / omitted value keeps the existing password).
 */
export async function updateProxyRoute(id: string, body: UpdateProxyRouteInput): Promise<ProxyRoute> {
  return http.put<ProxyRoute>(`/api/proxy/routes/${encodeURIComponent(id)}`, body)
}

/** Delete a route (FR-5). Removes it from Caddy and reloads; the cert volume is kept. */
export async function deleteProxyRoute(id: string): Promise<{ ok: boolean }> {
  return http.delete<{ ok: boolean }>(`/api/proxy/routes/${encodeURIComponent(id)}`)
}

/**
 * Cross-host / cross-domain certificate overview (R2 / FR-10): every route on
 * every reachable host, each enriched with its server's display name. Powers the
 * cert dashboard's table + summary cards. Read-only aggregate.
 */
export async function getProxyOverview(): Promise<ProxyRouteOverview[]> {
  const res = await http.get<{ items: ProxyRouteOverview[] }>('/api/proxy/overview')
  return res.items ?? []
}

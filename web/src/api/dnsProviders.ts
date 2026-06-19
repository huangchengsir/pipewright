/**
 * DNS providers API client (R3 / E3.1 — 零 DNS 体验).
 *
 * A `DnsProvider` ties a managed DNS zone (a base domain) to a provider account
 * (Cloudflare / DNSPod / 阿里云 DNS). Its API token is **write-only** — supplied
 * once on creation, stored via the backend vault, and **never** returned. The DTO
 * only reflects whether a credential is configured (`credentialConfigured`).
 *
 * Providers unlock two zero-DNS capabilities:
 *  - DNS-01 ACME on a route (wildcard certs `*.example.com`), and
 *  - instant subdomain allocation (`app-xxxx.basedomain` + auto A record + route).
 *
 * GET    /api/dns/providers              → { items: DnsProvider[] }
 * POST   /api/dns/providers              → DnsProvider           (needs CSRF)
 * DELETE /api/dns/providers/{id}         → { ok: true }          (needs CSRF)
 * POST   /api/dns/providers/{id}/verify  → { ok, message? }      (needs CSRF)
 *
 * Writes go through the shared `http` wrapper (auto CSRF + locale). Never returns
 * a token.
 */

import { http } from './http'

/** Supported managed-DNS backends for DNS-01 + instant subdomains. */
export type DnsProviderType = 'cloudflare' | 'dnspod' | 'alidns'

/** One managed DNS zone + provider account. The API token is never returned. */
export interface DnsProvider {
  id: string
  type: DnsProviderType
  /** Human-readable label, e.g. "生产区 Cloudflare". */
  name: string
  /** The managed zone / apex domain, e.g. `example.com`. Subdomains live under it. */
  baseDomain: string
  /** Server-derived: whether an API token is stored in the vault. Never the token itself. */
  credentialConfigured: boolean
  createdAt: string
}

/** Body for creating a provider. `token` is write-only — sent once, never echoed. */
export interface CreateDnsProviderInput {
  type: DnsProviderType
  name: string
  baseDomain: string
  /** Provider API token (write-only). Stored via the vault; never returned. */
  token: string
}

/** Result of a verify call: whether the token can reach the zone, plus a reason. */
export interface VerifyDnsProviderResult {
  ok: boolean
  /** Human-readable detail (zone name on success, failure reason otherwise). */
  message?: string
}

/** List all configured DNS providers. Tokens are never included. */
export async function listDnsProviders(): Promise<DnsProvider[]> {
  const res = await http.get<{ items: DnsProvider[] }>('/api/dns/providers')
  return res.items ?? []
}

/** Add a DNS provider (token stored write-only via the vault). Returns the created DTO. */
export async function createDnsProvider(input: CreateDnsProviderInput): Promise<DnsProvider> {
  return http.post<DnsProvider>('/api/dns/providers', input)
}

/** Delete a DNS provider. Routes pinned to it lose DNS-01 until re-attached. */
export async function deleteDnsProvider(id: string): Promise<{ ok: boolean }> {
  return http.delete<{ ok: boolean }>(`/api/dns/providers/${encodeURIComponent(id)}`)
}

/** Verify a provider's token reaches its zone (read-only probe). Returns ok + message. */
export async function verifyDnsProvider(id: string): Promise<VerifyDnsProviderResult> {
  return http.post<VerifyDnsProviderResult>(`/api/dns/providers/${encodeURIComponent(id)}/verify`, {})
}

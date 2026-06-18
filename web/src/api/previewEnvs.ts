/**
 * Per-PR preview environments API client (R4 / E4.1 — the headline).
 *
 * When PR previews are enabled for a project, each pull-request run spins up an
 * ephemeral environment reachable at `pr-<N>-<proj>.<baseDomain>`: a fresh
 * subdomain (DNS record + cert) bound to the run's deployed container. When the
 * PR closes (or on manual reclaim) the environment is torn down — the subdomain,
 * route, and DNS record are released. Think Vercel/Netlify preview deployments,
 * self-hosted.
 *
 * Two concerns:
 *  - `PreviewConfig` (per project): the on/off switch + which DNS provider zone
 *    previews mint subdomains under, and the base domain.
 *  - `PreviewEnv` (per PR): one live (or reclaimed) ephemeral environment.
 *
 * GET    /api/preview-envs?projectId=<id>     → { items: PreviewEnv[] }
 * POST   /api/preview-envs/{id}/reclaim       → { ok: true }            (needs CSRF)
 * GET    /api/projects/{id}/preview-config     → PreviewConfig
 * PUT    /api/projects/{id}/preview-config     → PreviewConfig          (needs CSRF)
 *
 * Writes go through the shared `http` wrapper (auto CSRF + locale).
 */

import { http } from './http'

/** Lifecycle of an ephemeral preview environment. */
export type PreviewEnvStatus = 'active' | 'reclaimed'

/** One per-PR ephemeral preview environment. */
export interface PreviewEnv {
  id: string
  /** Project this preview belongs to. */
  projectId: string
  /** The pipeline whose run produced/owns this preview. */
  pipelineId: string
  /** Pull-request number that triggered the preview. */
  prNumber: number
  /** Source branch of the PR. */
  branch: string
  /** Target host the preview's route/container live on. */
  serverId: string
  /** The minted subdomain, e.g. `pr-42-shop.preview.example.com`. */
  subdomain: string
  /** The reverse-proxy route bound for this preview. */
  routeId: string
  /** Live while `active`; torn down once `reclaimed`. */
  status: PreviewEnvStatus
  createdAt: string
  /** When the environment was reclaimed (empty while active). */
  reclaimedAt: string
}

/** Per-project preview-environment configuration. */
export interface PreviewConfig {
  projectId: string
  /** Master switch: mint a preview env for each PR run. */
  enabled: boolean
  /** DNS provider whose zone preview subdomains are minted under (references a `DnsProvider`). */
  dnsProviderId: string
  /** Base domain previews live under, e.g. `preview.example.com`. */
  baseDomain: string
}

/** List a project's preview environments (active + recently reclaimed). */
export async function listPreviewEnvs(projectId: string): Promise<PreviewEnv[]> {
  const res = await http.get<{ items: PreviewEnv[] }>(
    `/api/preview-envs?projectId=${encodeURIComponent(projectId)}`,
  )
  return res.items ?? []
}

/**
 * Manually reclaim (tear down) a preview environment: releases its subdomain,
 * route, and DNS record. Idempotent — reclaiming an already-reclaimed env is a
 * no-op. Returns `{ ok: true }`.
 */
export async function reclaimPreviewEnv(id: string): Promise<{ ok: boolean }> {
  return http.post<{ ok: boolean }>(`/api/preview-envs/${encodeURIComponent(id)}/reclaim`, {})
}

/** Read a project's preview-environment config (provider + base domain + on/off). */
export async function getPreviewConfig(projectId: string): Promise<PreviewConfig> {
  return http.get<PreviewConfig>(`/api/projects/${encodeURIComponent(projectId)}/preview-config`)
}

/** Update a project's preview-environment config. Returns the persisted config. */
export async function setPreviewConfig(projectId: string, body: PreviewConfig): Promise<PreviewConfig> {
  return http.put<PreviewConfig>(`/api/projects/${encodeURIComponent(projectId)}/preview-config`, body)
}

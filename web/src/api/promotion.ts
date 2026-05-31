/**
 * Promotion API — Epic 8 · Story 8-7 (FR-8-7).
 *
 * GET  /api/projects/{id}/environments       → EnvironmentsResponse
 * PUT  /api/projects/{id}/environments       → { environments: EnvStage[] }  (CSRF)
 * POST /api/runs/{id}/promote                → PromotionDTO                  (CSRF)
 * GET  /api/runs/{id}/promotions             → { items: PromotionDTO[] }
 * GET  /api/projects/{id}/promotions         → { items: PromotionDTO[] }
 *
 * Secret variables are never returned in plaintext — the backend only exposes
 * a masked value / credentialId reference. This client mirrors that guarantee
 * by typing `value` as a plain string (empty for secrets) and exposing
 * `credentialId` separately.
 *
 * Gated environments enter a waiting-approval state on promote; the caller
 * reuses the existing /runs/{id}/approve|reject endpoints (see api/runs.ts).
 * A 409 is returned for already-promoted / gated-rejected promotions — the
 * response body is still a PromotionDTO (not an error envelope) so callers
 * should read the record's `status` field.
 */

import { http } from './http'

// ─── Domain types ─────────────────────────────────────────────────────────────

/** One stage in the ordered environment chain. */
export interface EnvStage {
  name: string
  /** Whether this stage requires manual approval before promotion proceeds. */
  gated: boolean
}

/**
 * A per-environment variable.
 * Secret variables expose only `credentialId` (never plaintext); `value` is
 * empty for secrets.
 */
export interface EnvVariable {
  key: string
  /** Plaintext value for non-secret variables; empty string for secrets. */
  value: string
  secret: boolean
  /** Credential ID reference (only set when secret === true). */
  credentialId: string
}

/** Full response from GET /api/projects/{id}/environments. */
export interface EnvironmentsResponse {
  environments: EnvStage[]
  /** Map from environment name → list of variables (masked for secrets). */
  variables: Record<string, EnvVariable[]>
}

/** Promotion record DTO (matches Go's promotionDTO camelCase json tags). */
export interface PromotionDTO {
  id: string
  projectId: string
  sourceRunId: string
  fromEnvironment: string
  targetEnvironment: string
  /** "pending" | "promoted" | "rejected" */
  status: PromotionStatus
  promotedBy: string
  createdAt: string
  decidedAt: string
}

export type PromotionStatus = 'pending' | 'promoted' | 'rejected'

// ─── Input types ──────────────────────────────────────────────────────────────

export interface SaveEnvironmentsInput {
  environments: EnvStage[]
  variables?: Record<string, EnvVariableInput[]>
}

export interface EnvVariableInput {
  key: string
  /** Plaintext for non-secret; empty for secret (backend uses credentialId). */
  value: string
  secret: boolean
  /** Must be set when secret === true. */
  credentialId?: string
}

export interface PromoteRunInput {
  /** Leave empty to auto-advance to the next environment in the chain. */
  targetEnvironment?: string
}

// ─── API functions ────────────────────────────────────────────────────────────

export async function getEnvironments(projectId: string): Promise<EnvironmentsResponse> {
  return http.get<EnvironmentsResponse>(`/api/projects/${projectId}/environments`)
}

export async function saveEnvironments(
  projectId: string,
  input: SaveEnvironmentsInput,
): Promise<{ environments: EnvStage[] }> {
  return http.put<{ environments: EnvStage[] }>(
    `/api/projects/${projectId}/environments`,
    input,
  )
}

export async function promoteRun(
  runId: string,
  input: PromoteRunInput = {},
): Promise<PromotionDTO> {
  return http.post<PromotionDTO>(`/api/runs/${runId}/promote`, input)
}

export async function listRunPromotions(runId: string): Promise<{ items: PromotionDTO[] }> {
  return http.get<{ items: PromotionDTO[] }>(`/api/runs/${runId}/promotions`)
}

export async function listProjectPromotions(
  projectId: string,
): Promise<{ items: PromotionDTO[] }> {
  return http.get<{ items: PromotionDTO[] }>(`/api/projects/${projectId}/promotions`)
}

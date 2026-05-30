/**
 * Audit API — aligns to frozen Story 1.4 contract.
 *
 * GET /api/audit?limit=&before=&action=&targetType=
 *   → { entries: AuditEntry[], nextBefore: string | null }
 *
 * Append-only and read-only: there is intentionally no create / update / delete.
 * `detail` is server-side scrubbed before persistence — it never contains
 * plaintext secrets, ciphertext, or the master key.
 */

import { http } from './http'

/** Action enum (snake_case) — must mirror the backend audit.Action constants. */
export type AuditAction =
  | 'credential_create'
  | 'credential_update'
  | 'credential_delete'
  | 'trigger_secret_reset'
  | 'project_create'
  | 'project_update'
  | 'project_delete'
  | 'run_trigger_manual'

export interface AuditEntry {
  id: string
  /** RFC3339 timestamp. */
  timestamp: string
  actor: string
  action: AuditAction | string
  targetType: string
  targetId: string
  /** Already-masked structured detail; never plaintext secrets. */
  detail: Record<string, unknown>
  ip: string
}

export interface ListAuditParams {
  /** Page size (server default 50, max 200). */
  limit?: number
  /** Cursor: the id of the last entry on the previous page. */
  before?: string
  /** Filter by action. */
  action?: AuditAction | string
  /** Filter by target type. */
  targetType?: string
}

export interface AuditListResponse {
  entries: AuditEntry[]
  /** Cursor for the next page; null when the end is reached. */
  nextBefore: string | null
}

export async function listAudit(params: ListAuditParams = {}): Promise<AuditListResponse> {
  const q = new URLSearchParams()
  if (params.limit != null) q.set('limit', String(params.limit))
  if (params.before) q.set('before', params.before)
  if (params.action) q.set('action', params.action)
  if (params.targetType) q.set('targetType', params.targetType)
  const qs = q.toString()
  return http.get<AuditListResponse>(`/api/audit${qs ? `?${qs}` : ''}`)
}

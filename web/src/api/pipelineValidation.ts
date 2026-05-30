/**
 * Pipeline Validation API — aligns to frozen 2.6 contract.
 *
 * GET /api/projects/{id}/pipeline/validation → ValidationDTO
 *
 * Frozen DTO shape (冻结):
 *   { ready: boolean, issues: Array<{ severity, code, scope, field, message }> }
 *
 * ready = true iff zero error-severity issues.
 * severity ∈ 'error' | 'warning' | 'info'
 * scope    ∈ 'canvas' | 'vars' | 'triggers' | 'envs'  (maps to pipeline tabs)
 * code     is a stable enum usable for localisation / routing (see story 2-6)
 * field    is a dot/bracket path into the config; may be empty string
 * message  is human-readable Chinese text from the server
 */

import { http } from './http'

// ─── Frozen DTO types (do NOT modify shape — only new code values may be added) ─

export type IssueSeverity = 'error' | 'warning' | 'info'

/** Scope maps 1-to-1 with the four pipeline tab keys. */
export type IssueScope = 'canvas' | 'vars' | 'triggers' | 'envs'

export interface ValidationIssue {
  severity: IssueSeverity
  /** Stable code enum, e.g. "environment_undefined", "toolchain_incomplete" */
  code: string
  /** Which tab this issue belongs to. Used by the locate event. */
  scope: IssueScope
  /** Dot/bracket path to the offending field; may be empty string. */
  field: string
  /** Human-readable Chinese message from server. */
  message: string
}

export interface ValidationDTO {
  /** true = no error-severity issues present; warnings/info do not affect ready. */
  ready: boolean
  issues: ValidationIssue[]
}

// ─── API ────────────────────────────────────────────────────────────────────────

/**
 * Fetch the aggregated pipeline validation result for a project.
 * Read-only GET; does not modify any server state.
 */
export async function getValidation(projectId: string): Promise<ValidationDTO> {
  return http.get<ValidationDTO>(`/api/projects/${projectId}/pipeline/validation`)
}

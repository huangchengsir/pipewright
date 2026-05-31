/**
 * Cron (scheduled trigger) API — Epic 8 · Story 8-6.
 *
 * GET  /api/projects/{id}/cron  → CronConfig
 * PUT  /api/projects/{id}/cron  → CronConfig  (needs CSRF)
 *
 * `expression` is a 5-field Vixie cron string (分 时 日 月 周). `nextRun` is
 * server-computed (RFC3339) from the current expression when enabled; empty when
 * disabled / invalid / no upcoming fire. Validation lives in the backend
 * cron.Service — a bad expression returns 422 invalid_cron.
 */

import { http } from './http'

export interface CronConfig {
  /** 5-field cron expression (分 时 日 月 周); empty = unset. */
  expression: string
  /** Branch to build on each fire; empty = project default branch. */
  branch: string
  /** Whether the schedule is active. */
  enabled: boolean
  /** Next fire time (RFC3339); empty when disabled / invalid / none. Read-only. */
  nextRun: string
}

export interface SaveCronInput {
  expression: string
  branch: string
  enabled: boolean
}

export async function getCron(projectId: string): Promise<CronConfig> {
  return http.get<CronConfig>(`/api/projects/${projectId}/cron`)
}

export async function saveCron(projectId: string, input: SaveCronInput): Promise<CronConfig> {
  return http.put<CronConfig>(`/api/projects/${projectId}/cron`, input)
}

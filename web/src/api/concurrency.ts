/**
 * Concurrency API — Epic 8 · Story 8-10 (FR-8-10 并发/队列控制).
 *
 * GET  /api/projects/{id}/concurrency  → ConcurrencyConfig
 * PUT  /api/projects/{id}/concurrency  → ConcurrencyConfig  (needs CSRF)
 *
 * `maxConcurrent` is the per-project limit on simultaneously running pipeline
 * runs. 0 means no project-level limit (still bounded by the global
 * PIPEWRIGHT_MAX_CONCURRENT env var). Valid range: 0..64. 422 on bad value.
 */

import { http } from './http'

export interface ConcurrencyConfig {
  /** Simultaneous run limit; 0 = no project-level limit. */
  maxConcurrent: number
}

export interface SaveConcurrencyInput {
  maxConcurrent: number
}

export async function getConcurrency(projectId: string): Promise<ConcurrencyConfig> {
  return http.get<ConcurrencyConfig>(`/api/projects/${projectId}/concurrency`)
}

export async function saveConcurrency(
  projectId: string,
  input: SaveConcurrencyInput,
): Promise<ConcurrencyConfig> {
  return http.put<ConcurrencyConfig>(`/api/projects/${projectId}/concurrency`, input)
}

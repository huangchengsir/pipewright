/**
 * Chain API — Epic 8 · Story 8-11 (FR-8-11 流水线串联).
 *
 * GET  /api/projects/{id}/chain  → ChainConfig
 * PUT  /api/projects/{id}/chain  → ChainConfig  (needs CSRF)
 *
 * Each entry in `downstream` represents a downstream project that is triggered
 * automatically when this project's pipeline succeeds. `branch` is optional
 * (empty = downstream project default). `enabled` toggles the link without
 * deleting the row.
 */

import { http } from './http'
import { configWriteOptions } from './configRevision'

export interface ChainTarget {
  /** The downstream project's ID. */
  downstreamProjectId: string
  /** Branch to trigger in the downstream project; empty = default branch. */
  branch: string
  /** Whether this downstream trigger is active. */
  enabled: boolean
}

export interface ChainConfig {
  downstream: ChainTarget[]
}

export interface SaveChainInput {
  downstream: ChainTarget[]
}

export async function getChain(projectId: string): Promise<ChainConfig> {
  return http.get<ChainConfig>(`/api/projects/${projectId}/chain`)
}

export async function saveChain(
  projectId: string,
  input: SaveChainInput,
  expectedRevision?: string,
): Promise<ChainConfig> {
  return expectedRevision ? http.put<ChainConfig>(`/api/projects/${projectId}/chain`, input, configWriteOptions(expectedRevision)) : http.put<ChainConfig>(`/api/projects/${projectId}/chain`, input)
}

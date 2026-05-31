/**
 * Variable groups API — FR-8-13 (reuse base · à la 云效变量组 / GitLab CI variable groups).
 *
 * Named, reusable sets of variables (key=value with vault secret refs), shared across
 * pipelines. Mirrors the per-pipeline variable model (BuildVar).
 *
 *   GET    /api/variable-groups       → { variableGroups: VariableGroup[] }
 *   POST   /api/variable-groups       → VariableGroup   (needs CSRF)
 *   GET    /api/variable-groups/{id}  → VariableGroup
 *   PUT    /api/variable-groups/{id}  → VariableGroup   (needs CSRF)
 *   DELETE /api/variable-groups/{id}  → 204             (needs CSRF)
 *
 * Secret variables are stored as credentialId references only — the server never returns
 * plaintext, only a server-computed maskedValue. On save, secret items send
 * {key, secret:true, credentialId}; the plaintext value is dropped server-side.
 */

import { http } from './http'
import type { BuildVar } from './pipelineSettings'

export interface VariableGroup {
  id: string
  name: string
  description: string
  vars: BuildVar[]
  createdAt: string
  updatedAt: string
}

/** Save payload var (secret items omit plaintext value; send credentialId instead). */
export interface VariableGroupVarInput {
  id?: string
  key: string
  secret: boolean
  value?: string
  credentialId?: string
}

export interface SaveVariableGroupInput {
  name: string
  description?: string
  vars: VariableGroupVarInput[]
}

interface ListVariableGroupsResponse {
  variableGroups: VariableGroup[]
}

export async function listVariableGroups(): Promise<VariableGroup[]> {
  const res = await http.get<ListVariableGroupsResponse>('/api/variable-groups')
  return res.variableGroups ?? []
}

export async function getVariableGroup(id: string): Promise<VariableGroup> {
  return http.get<VariableGroup>(`/api/variable-groups/${id}`)
}

export async function createVariableGroup(input: SaveVariableGroupInput): Promise<VariableGroup> {
  return http.post<VariableGroup>('/api/variable-groups', input)
}

export async function updateVariableGroup(
  id: string,
  input: SaveVariableGroupInput,
): Promise<VariableGroup> {
  return http.put<VariableGroup>(`/api/variable-groups/${id}`, input)
}

export async function deleteVariableGroup(id: string): Promise<void> {
  await http.delete<void>(`/api/variable-groups/${id}`)
}

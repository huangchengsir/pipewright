import { http } from './http'

export interface MCPConfigActivity {
  activityId: string
  projectId: string
  runId: string
  ownerEpoch: string
  phase: string
  projectRevision: string
  digest: string
  evidenceId: string
  recoverable: boolean
  reason: string
}
export interface MCPConfigMaintenance {
  executorEpoch: number
  items: MCPConfigActivity[]
  truncated: boolean
}
export function getMCPConfigMaintenance(options?: RequestInit): Promise<MCPConfigMaintenance> {
  return http.get('/api/mcp/config-maintenance', options)
}
export function recoverMCPConfigActivity(input: { activityId: string; expectedDigest: string; evidenceId: string }, options?: RequestInit): Promise<unknown> {
  return http.post('/api/mcp/config-maintenance/recover', input, options)
}

export interface MCPConfigOperationResult {
  operationId: string
  projectId: string
  state: string
  reason?: string
  baseRevision?: string
  newRevision?: string
  changedOperations: string[]
}
export function getMCPConfigOperation(id: string, options?: RequestInit): Promise<MCPConfigOperationResult> {
  return http.get(`/api/mcp/config-operations/${encodeURIComponent(id)}`, options)
}
export function fenceMCPConfigOperation(id: string, options?: RequestInit): Promise<MCPConfigOperationResult> {
  return http.post(`/api/mcp/config-operations/${encodeURIComponent(id)}/fence`, {}, options)
}

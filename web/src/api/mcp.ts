import { http } from './http'

export const MCP_CONFIG_SECTIONS = ['project', 'pipeline', 'build', 'step_limits', 'source_and_impact'] as const
export const MCP_CONFIG_OPERATIONS = ['project.set_name', 'project.set_default_branch', 'stage.rename', 'job.rename', 'step.set_limits', 'build.set'] as const
export type MCPConfigSection = typeof MCP_CONFIG_SECTIONS[number]
export type MCPConfigOperation = typeof MCP_CONFIG_OPERATIONS[number]

export interface MCPConfigGrant {
  projectId: string
  read: boolean
  propose: boolean
  apply: boolean
  sections: MCPConfigSection[]
  operations: MCPConfigOperation[]
}

export interface MCPToken {
  id: string
  name: string
  projectIds: string[]
  canTrigger: boolean
  createdAt: string
  expiresAt: string
  revokedAt: string | null
  configGrants?: MCPConfigGrant[]
  authzRevision?: number
}

export interface CreateMCPToken {
  name: string
  projectIds: string[]
  canTrigger: boolean
  expiresInDays: number
  configGrants?: MCPConfigGrant[]
}

export async function listMCPTokens(): Promise<MCPToken[]> {
  return (await http.get<{ tokens: MCPToken[] }>('/api/settings/mcp/tokens')).tokens
}

export function createMCPToken(input: CreateMCPToken): Promise<{ token: string; item: MCPToken }> {
  return http.post('/api/settings/mcp/tokens', input)
}

export function revokeMCPToken(id: string): Promise<void> {
  return http.delete(`/api/settings/mcp/tokens/${encodeURIComponent(id)}`)
}

export function mcpTokenState(token: MCPToken, now: number): 'active' | 'expired' | 'revoked' {
  if (token.revokedAt) return 'revoked'
  const expires = Date.parse(token.expiresAt)
  return Number.isFinite(expires) && expires > now ? 'active' : 'expired'
}

import { http } from './http'

export interface MCPToken {
  id: string
  name: string
  projectIds: string[]
  canTrigger: boolean
  createdAt: string
  expiresAt: string
  revokedAt: string | null
}

export interface CreateMCPToken {
  name: string
  projectIds: string[]
  canTrigger: boolean
  expiresInDays: number
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

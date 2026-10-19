import { beforeEach, describe, expect, it, vi } from 'vitest'
import { http } from './http'
import { createMCPToken, listMCPTokens, mcpTokenState, revokeMCPToken, type MCPToken } from './mcp'

vi.mock('./http', () => ({ http: { get: vi.fn(), post: vi.fn(), delete: vi.fn() } }))
const token: MCPToken = { id: 'id', name: 'test', projectIds: ['p'], canTrigger: false, createdAt: '2026-01-01T00:00:00Z', expiresAt: '2026-01-02T00:00:00Z', revokedAt: null }
describe('MCP token API', () => {
  beforeEach(() => vi.clearAllMocks())
  it('lists metadata and issues a scoped token', async () => {
    vi.mocked(http.get).mockResolvedValue({ tokens: [token] })
    expect(await listMCPTokens()).toEqual([token])
    const input = { name: 'test', projectIds: ['p'], canTrigger: false, expiresInDays: 7 }
    vi.mocked(http.post).mockResolvedValue({ token: 'one-time', item: token })
    expect(await createMCPToken(input)).toEqual({ token: 'one-time', item: token })
    expect(http.post).toHaveBeenCalledWith('/api/settings/mcp/tokens', input)
  })
  it('encodes revoke identifiers', async () => {
    await revokeMCPToken('a/b')
    expect(http.delete).toHaveBeenCalledWith('/api/settings/mcp/tokens/a%2Fb')
  })
  it('uses expiry and revocation, rejecting malformed dates', () => {
    expect(mcpTokenState(token, Date.parse('2026-01-01T12:00:00Z'))).toBe('active')
    expect(mcpTokenState(token, Date.parse(token.expiresAt))).toBe('expired')
    expect(mcpTokenState({ ...token, expiresAt: 'bad' }, 0)).toBe('expired')
    expect(mcpTokenState({ ...token, revokedAt: token.createdAt }, 0)).toBe('revoked')
  })
})

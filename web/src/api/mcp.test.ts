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
    expect(http.post).toHaveBeenCalledWith('/api/settings/mcp/tokens', input, {throwOnUnauthorized:true})
  })
  it('encodes revoke identifiers', async () => {
    await revokeMCPToken('a/b')
    expect(http.delete).toHaveBeenCalledWith('/api/settings/mcp/tokens/a%2Fb', {throwOnUnauthorized:true})
  })
  it('preserves captured identity headers and cancellation for every endpoint', async () => {
    const options={headers:{'X-Pipewright-Team':'fixed-team'},signal:new AbortController().signal}
    vi.mocked(http.get).mockResolvedValue({tokens:[]})
    const input={name:'scope',projectIds:['p'],canTrigger:false,expiresInDays:7}
    await listMCPTokens(options); await createMCPToken(input,options);await revokeMCPToken('id',options)
    const expected={...options,throwOnUnauthorized:true}
    expect(http.get).toHaveBeenCalledWith('/api/settings/mcp/tokens',expected)
    expect(http.post).toHaveBeenCalledWith('/api/settings/mcp/tokens',input,expected)
    expect(http.delete).toHaveBeenCalledWith('/api/settings/mcp/tokens/id',expected)
  })
  it('uses expiry and revocation, rejecting malformed dates', () => {
    expect(mcpTokenState(token, Date.parse('2026-01-01T12:00:00Z'))).toBe('active')
    expect(mcpTokenState(token, Date.parse(token.expiresAt))).toBe('expired')
    expect(mcpTokenState({ ...token, expiresAt: 'bad' }, 0)).toBe('expired')
    expect(mcpTokenState({ ...token, revokedAt: token.createdAt }, 0)).toBe('revoked')
  })
})

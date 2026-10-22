import { beforeEach, describe, expect, it, vi } from 'vitest'
import { http } from './http'
import { fenceMCPConfigOperation, getMCPConfigOperation } from './mcpMaintenance'
vi.mock('./http', () => ({ http: { get: vi.fn(), post: vi.fn() } }))
describe('MCP operation recovery API', () => {
  beforeEach(() => vi.clearAllMocks())
  it('encodes the original operation and forwards cancellation', async () => {
    const options = { signal: new AbortController().signal }
    await getMCPConfigOperation('a/b', options)
    expect(http.get).toHaveBeenCalledExactlyOnceWith('/api/mcp/config-operations/a%2Fb', options)
  })
  it('fences explicitly through the CSRF client without applying or retrying', async () => {
    await fenceMCPConfigOperation('a/b')
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/mcp/config-operations/a%2Fb/fence', {}, undefined)
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { http } from './http'
import { approveMCPConfigDraft, getMCPConfigReview } from './mcpConfig'

vi.mock('./http', () => ({ http: { get: vi.fn(), post: vi.fn() } }))

describe('MCP configuration review API', () => {
  beforeEach(() => vi.clearAllMocks())
  it('encodes the draft ID and propagates read cancellation', async () => {
    const signal = new AbortController().signal
    await getMCPConfigReview('a/b', { signal })
    expect(http.get).toHaveBeenCalledExactlyOnceWith('/api/mcp/config-drafts/a%2Fb/review', { signal })
  })
  it('approves the exact reviewed binding through the existing CSRF client without applying', async () => {
    const binding = { reviewDigest: 'digest', baseRevision: 'r1', policyVersion: 'p1' }
    await approveMCPConfigDraft('a/b', binding)
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/mcp/config-drafts/a%2Fb/approve', binding, undefined)
  })
})

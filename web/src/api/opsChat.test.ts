import { describe, expect, it, vi } from 'vitest'
import { requestId, sseParser, utf8Bytes, opsApi } from './opsChat'
import { http } from './http'
describe('ops wire helpers', () => {
  it('creates a cryptographic UUID on insecure HTTP without randomUUID', () => {
    const spy = vi
      .spyOn(crypto, 'randomUUID')
      .mockImplementation(undefined as unknown as typeof crypto.randomUUID)
    const ids = Array.from({ length: 100 }, requestId)
    expect(new Set(ids).size).toBe(100)
    for (const id of ids)
      expect(id).toMatch(
        /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
      )
    spy.mockRestore()
  })
  it('counts UTF-8, not characters', () => {
    expect(utf8Bytes('中文')).toBe(6)
  })
  it('parses split CRLF frames, multiline JSON and ignores comments', () => {
    const out = vi.fn(),
      parse = sseParser(out)
    parse(': keepalive\r\nevent: ready\r\ndata: {"watermark":9}\r\n')
    expect(out).not.toHaveBeenCalled()
    parse('\r\nid: s:2\nevent: entry\ndata: {\ndata: "seq":2}\n\n')
    expect(out.mock.calls.map((c) => c[0])).toEqual([
      { type: 'ready', id: '', data: { watermark: 9 } },
      { type: 'entry', id: 's:2', data: { seq: 2 } },
    ])
  })
  it('bounds incomplete and complete frames', () => {
    expect(() => sseParser(vi.fn())('x'.repeat(256 * 1024 + 1))).toThrow()
    expect(() =>
      sseParser(vi.fn())('data: "' + 'x'.repeat(256 * 1024 + 1) + '"\n\n'),
    ).toThrow()
  })
  it('sends the full exact confirmation without view-only properties', async () => {
    const post = vi.spyOn(http, 'post').mockResolvedValue({})
    const calls = [
      {
        callId: 'c',
        serverId: 's',
        toolId: 'docker_action',
        object: 'a'.repeat(64),
        argsHash: 'a',
        targetHash: 'b',
      },
    ]
    await opsApi.confirm('chat', {
      runId: 'r',
      nonce: 'n',
      expiresAt: 'time',
      valid: true,
      calls,
    })
    expect(post).toHaveBeenCalledWith(
      '/api/ai/ops/sessions/chat/calls/confirm',
      { runId: 'r', nonce: 'n', calls },
    )
    post.mockRestore()
  })
  it('accepts legal escaped analysis frames and continues to the next event', () => {
    const deliver = vi.fn(),
      parse = sseParser(deliver)
    const text = '<'.repeat(16 * 1024)
    const body = JSON.stringify({ text }).replace(/</g, '\\u003c')
    const frame = `event: entry\ndata: ${body}\n\n`
    parse(frame.slice(0, 70000))
    parse(frame.slice(70000) + 'event: heartbeat\ndata: {"watermark":2}\n\n')
    expect(deliver.mock.calls[0]?.[0].data.text).toBe(text)
    expect(deliver.mock.calls[1]?.[0].type).toBe('heartbeat')
  })
})

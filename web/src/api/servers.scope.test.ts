import { afterEach, describe, expect, it, vi } from 'vitest'
import { createServer, deleteServer, listServers, testServer, updateServer } from './servers'

const server = { name: 'Synthetic host', host: '127.0.0.1', port: 22, user: 'user', credentialId: 'fixture-credential' }

afterEach(() => vi.unstubAllGlobals())

describe('server API request scope', () => {
  it.each([
    ['GET', '/api/servers', (options: RequestInit) => listServers(options)],
    ['POST', '/api/servers', (options: RequestInit) => createServer(server, options)],
    ['PUT', '/api/servers/test-id', (options: RequestInit) => updateServer('test-id', server, options)],
    ['DELETE', '/api/servers/test-id', (options: RequestInit) => deleteServer('test-id', options)],
    ['POST', '/api/servers/test-id/test', (options: RequestInit) => testServer('test-id', options)],
  ] as const)('passes the exact team and cancellation into %s %s', async (method, url, request) => {
    const controller = new AbortController()
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [] }), {
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetch)
    await request({ headers: { 'X-Pipewright-Team': 'selected-team' }, signal: controller.signal })
    expect(fetch).toHaveBeenCalledExactlyOnceWith(url, expect.objectContaining({
      method,
      signal: controller.signal,
      credentials: 'same-origin',
    }))
    expect((fetch.mock.calls[0]![1] as RequestInit).headers).toBeInstanceOf(Headers)
    expect(new Headers((fetch.mock.calls[0]![1] as RequestInit).headers).get('X-Pipewright-Team')).toBe('selected-team')
  })
})
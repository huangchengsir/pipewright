import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getOAuthApps, saveOAuthApp, authorizeUrl } from './oauth'

function jsonResponse(status: number, body: unknown, ok = status < 400): Response {
  return {
    status,
    ok,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

describe('oauth api client', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    document.cookie.split(';').forEach((c) => {
      const name = c.split('=')[0].trim()
      if (name) document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT`
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('GET /api/oauth/apps returns the app list', async () => {
    const apps = [
      {
        provider: 'github',
        clientId: 'abc',
        baseUrl: '',
        enabled: true,
        maskedSecret: '••••a91f',
        configured: true,
        updatedAt: '2026-05-30T00:00:00Z',
      },
    ]
    fetchMock.mockResolvedValue(jsonResponse(200, apps))
    const result = await getOAuthApps()
    expect(result).toEqual(apps)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/oauth/apps')
    expect(init.method).toBe('GET')
  })

  it('PUT /api/oauth/apps/{provider} hits the per-provider URL with the body', async () => {
    document.cookie = 'pipewright_csrf=tok123'
    fetchMock.mockResolvedValue(jsonResponse(200, { provider: 'gitee' }))
    await saveOAuthApp('gitee', {
      clientId: 'cid',
      clientSecret: 'sec',
      enabled: true,
    })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/oauth/apps/gitee')
    expect(init.method).toBe('PUT')
    expect(init.body).toBe(JSON.stringify({ clientId: 'cid', clientSecret: 'sec', enabled: true }))
    // write methods carry the CSRF token
    expect((init.headers as Headers).get('X-CSRF-Token')).toBe('tok123')
  })

  it('saveOAuthApp omits clientSecret when not provided (keep-existing semantics)', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await saveOAuthApp('github', { clientId: 'cid', enabled: false })
    const init = fetchMock.mock.calls[0][1]
    const body = JSON.parse(init.body)
    expect(body).not.toHaveProperty('clientSecret')
    expect(body).toEqual({ clientId: 'cid', enabled: false })
  })

  it('authorizeUrl builds the per-provider full-page authorize entrypoint', () => {
    expect(authorizeUrl('gitee')).toBe('/api/oauth/gitee/authorize')
    expect(authorizeUrl('github')).toBe('/api/oauth/github/authorize')
    expect(authorizeUrl('gitlab')).toBe('/api/oauth/gitlab/authorize')
    expect(authorizeUrl('custom')).toBe('/api/oauth/custom/authorize')
  })
})

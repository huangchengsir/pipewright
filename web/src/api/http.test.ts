import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { http, HttpError } from './http'

/** Build a minimal Response-like object the wrapper understands. */
function jsonResponse(status: number, body: unknown, ok = status < 400): Response {
  return {
    status,
    ok,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

function textResponse(status: number, text: string): Response {
  return {
    status,
    ok: status < 400,
    headers: new Headers({ 'content-type': 'text/plain' }),
    json: async () => {
      throw new Error('not json')
    },
    text: async () => text,
  } as unknown as Response
}

describe('http client', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    // Clear cookies
    document.cookie.split(';').forEach((c) => {
      const name = c.split('=')[0].trim()
      if (name) document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT`
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sends GET without a body and parses JSON', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { username: 'admin' }))
    const data = await http.get<{ username: string }>('/api/auth/session')
    expect(data).toEqual({ username: 'admin' })

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/auth/session')
    expect(init.method).toBe('GET')
    expect(init.credentials).toBe('same-origin')
  })

  it('sets Content-Type application/json for POST with a body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { ok: true }))
    await http.post('/api/projects', { name: 'x' })
    const init = fetchMock.mock.calls[0][1]
    expect((init.headers as Headers).get('Content-Type')).toBe('application/json')
    expect(init.body).toBe(JSON.stringify({ name: 'x' }))
  })

  it('attaches X-CSRF-Token from the pipewright_csrf cookie on write methods', async () => {
    document.cookie = 'pipewright_csrf=tok123'
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await http.post('/api/x', { a: 1 })
    const headers = fetchMock.mock.calls[0][1].headers as Headers
    expect(headers.get('X-CSRF-Token')).toBe('tok123')
  })

  it('does NOT attach CSRF header on GET (read method)', async () => {
    document.cookie = 'pipewright_csrf=tok123'
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await http.get('/api/x')
    const headers = fetchMock.mock.calls[0][1].headers as Headers
    expect(headers.get('X-CSRF-Token')).toBeNull()
  })

  it('returns undefined for 204 No Content', async () => {
    fetchMock.mockResolvedValue({
      status: 204,
      ok: true,
      headers: new Headers(),
    } as unknown as Response)
    const res = await http.delete('/api/x')
    expect(res).toBeUndefined()
  })

  it('parses the canonical { error: { code, message } } envelope on non-2xx', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { error: { code: 'invalid_input', message: '字段缺失' } }, false),
    )
    await expect(http.post('/api/x', {})).rejects.toMatchObject({
      status: 400,
      apiError: { code: 'invalid_input', message: '字段缺失' },
      message: '字段缺失',
    })
  })

  it('falls back to HTTP <status> message when no error envelope is present', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { something: 'else' }, false))
    try {
      await http.get('/api/x')
      throw new Error('should have thrown')
    } catch (err) {
      expect(err).toBeInstanceOf(HttpError)
      expect((err as HttpError).status).toBe(500)
      expect((err as HttpError).apiError).toBeNull()
      expect((err as HttpError).message).toBe('HTTP 500')
    }
  })

  it('wraps network failures as HttpError status 0', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    try {
      await http.get('/api/x')
      throw new Error('should have thrown')
    } catch (err) {
      expect(err).toBeInstanceOf(HttpError)
      expect((err as HttpError).status).toBe(0)
      expect((err as HttpError).message).toBe('Failed to fetch')
    }
  })

  it('reads a plain-text body when content-type is not JSON', async () => {
    fetchMock.mockResolvedValue(textResponse(200, 'pong'))
    const res = await http.get<string>('/healthz')
    expect(res).toBe('pong')
  })

  it('does NOT redirect on 401 from an /api/auth/ endpoint (auth owns its 401)', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'unauthorized', message: '未登录' } }, false))
    await expect(http.post('/api/auth/login', {})).rejects.toBeInstanceOf(HttpError)
  })
})

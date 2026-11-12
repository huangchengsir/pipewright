import { afterEach, expect, it, vi } from 'vitest'
import { databaseTestConnection } from './databaseMigration'

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'pipewright_csrf=; Max-Age=0; path=/'
})
it('sends a standalone connection-only request with CSRF and cancellation', async () => {
  document.cookie = 'pipewright_csrf=probe-csrf; path=/'
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(JSON.stringify({ kind: 'connection' }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  const controller = new AbortController()
  const result = await databaseTestConnection(
    {
      driver: 'postgres',
      host: 'db.local',
      username: 'operator',
      password: 'private',
    },
    controller.signal,
  )
  expect(result).toEqual({ kind: 'connection' })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const [url, request] = fetchMock.mock.calls[0]!
  expect(url).toBe('/api/settings/database/test-connection')
  expect(request.method).toBe('POST')
  expect(request.signal).toBe(controller.signal)
  expect(request.credentials).toBe('same-origin')
  expect(request.headers.get('X-CSRF-Token')).toBe('probe-csrf')
  expect(JSON.parse(request.body)).toEqual({
    driver: 'postgres',
    host: 'db.local',
    username: 'operator',
    password: 'private',
  })
  expect(JSON.parse(request.body)).not.toHaveProperty('managed')
})

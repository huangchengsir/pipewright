import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSessionStore } from './session'
import { http, HttpError } from '../api/http'

describe('session store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('returns { kind: ok } and caches the user on a successful session fetch', async () => {
    const getSpy = vi.spyOn(http, 'get').mockResolvedValue({ username: 'admin' })
    const store = useSessionStore()

    const r1 = await store.ensureSession()
    expect(r1).toEqual({ kind: 'ok', user: { username: 'admin' } })
    expect(store.user).toEqual({ username: 'admin' })

    // Cached: second call must NOT hit the network again.
    const r2 = await store.ensureSession()
    expect(r2).toEqual({ kind: 'ok', user: { username: 'admin' } })
    expect(getSpy).toHaveBeenCalledTimes(1)
  })

  it('returns { kind: unauthenticated } and nulls user on 401', async () => {
    vi.spyOn(http, 'get').mockRejectedValue(new HttpError(401, null, 'unauthorized'))
    const store = useSessionStore()

    const r = await store.ensureSession()
    expect(r).toEqual({ kind: 'unauthenticated' })
    expect(store.user).toBeNull()
    expect(store.isNetworkError).toBe(false)
  })

  it('returns { kind: error } on 5xx and sets isNetworkError without evicting a cached user', async () => {
    const store = useSessionStore()
    // Prime with a known user first.
    store.setUser({ username: 'admin' })

    vi.spyOn(http, 'get').mockRejectedValue(new HttpError(503, null, 'unavailable'))
    const r = await store.ensureSession(true) // force bypass cache

    expect(r.kind).toBe('error')
    if (r.kind === 'error') expect(r.status).toBe(503)
    expect(store.isNetworkError).toBe(true)
    // Cached user must survive a backend fault.
    expect(store.user).toEqual({ username: 'admin' })
  })

  it('treats raw network errors (non-HttpError) as { kind: error, status: 0 }', async () => {
    vi.spyOn(http, 'get').mockRejectedValue(new Error('boom'))
    const store = useSessionStore()
    const r = await store.ensureSession()
    expect(r.kind).toBe('error')
    if (r.kind === 'error') {
      expect(r.status).toBe(0)
      expect(r.message).toBe('boom')
    }
  })

  it('setUser primes the cache so a later ensureSession does not fetch', async () => {
    const getSpy = vi.spyOn(http, 'get')
    const store = useSessionStore()
    store.setUser({ username: 'root' })

    const r = await store.ensureSession()
    expect(r).toEqual({ kind: 'ok', user: { username: 'root' } })
    expect(getSpy).not.toHaveBeenCalled()
  })

  it('clearSession marks the user as confirmed-logged-out', async () => {
    const getSpy = vi.spyOn(http, 'get')
    const store = useSessionStore()
    store.setUser({ username: 'admin' })
    store.clearSession()

    const r = await store.ensureSession()
    expect(r).toEqual({ kind: 'unauthenticated' })
    expect(getSpy).not.toHaveBeenCalled()
  })
})

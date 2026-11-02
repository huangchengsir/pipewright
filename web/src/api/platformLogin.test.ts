import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { HttpError } from './http'
import {
  PLATFORM_PROVIDERS, getPublicPlatforms, startPlatformLogin, unbindPlatform, confirmPlatformBinding,
  safePlatformReturn, safePlatformAuthorize, platformErrorKey, rememberPlatformReturn, consumePlatformReturn,
} from './platformLogin'

const calls = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('./http', async () => ({ ...await vi.importActual<typeof import('./http')>('./http'), http: calls }))
beforeEach(() => { for (const fn of Object.values(calls)) fn.mockReset(); sessionStorage.clear() })
afterEach(() => vi.useRealTimers())

describe('platform sign-in API boundaries', () => {
  it('rejects ambiguous and encoded external return targets while preserving ordinary internal context', () => {
    for (const raw of ['https://evil.example/', '//evil.example', '/\\evil.example', '/%2f%2fevil.example', '/%252f%252fevil.example', '/x%0d%0a', '/bad path', '/', null]) {
      if (raw === '/') expect(safePlatformReturn(raw)).toBe('/')
      else expect(safePlatformReturn(raw)).toBeNull()
    }
    expect(safePlatformReturn('/runs?q=release%2Fmain&page=2#step')).toBe('/runs?q=release%2Fmain&page=2#step')
    for (const target of ['/runs?q=100%25', '/runs?q=feature%20branch', '/runs/detail?returnTo=%2Fruns%3Fq%3D100%2525']) expect(safePlatformReturn(target)).toBe(target)
  })
  it('accepts only a fixed platform authorization path on its own HTTPS host', () => {
    expect(safePlatformAuthorize('github', 'https://github.com/login/oauth/authorize?state=fixture')).toContain('github.com')
    for (const raw of ['https://github.com.evil.example/login/oauth/authorize', 'http://github.com/login/oauth/authorize', 'https://github.com/other', 'https://user@github.com/login/oauth/authorize', 'https://github.com/login/oauth/authorize#token']) expect(safePlatformAuthorize('github', raw)).toBeNull()
    expect(safePlatformAuthorize('gitee', 'https://gitlab.com/oauth/authorize')).toBeNull()
  })
  it('does not supply display defaults when reading instance policy fails or is incomplete', async () => {
    calls.get.mockResolvedValueOnce({ providers: [] })
    await expect(getPublicPlatforms()).rejects.toThrow()
    calls.get.mockResolvedValueOnce({ providers: PLATFORM_PROVIDERS.map(provider => ({ provider, visible: false, available: false, reason: 'not_configured' })) })
    expect((await getPublicPlatforms()).every(p => !p.visible)).toBe(true)
  })
  it('sends only a flow and version for confirmation and includes DELETE JSON version', async () => {
    calls.post.mockResolvedValue({})
    calls.delete.mockResolvedValue({})
    await confirmPlatformBinding('github', 7, 'server-candidate')
    await unbindPlatform('github', 8)
    expect(calls.post).toHaveBeenCalledWith('/api/account/platform-login/github/bind/confirm', { expectedVersion: 7, flowId: 'server-candidate' }, expect.any(Object))
    expect(calls.delete).toHaveBeenCalledWith('/api/account/platform-login/github/binding', expect.objectContaining({ body: '{"expectedVersion":8}', signal: expect.any(AbortSignal) }))
  })
  it('maps local safe enums and treats an ambiguous write as requiring read-back', () => {
    const secret = 'UPSTREAM_SECRET_CANARY'
    expect(platformErrorKey(new HttpError(403, { code: 'platform_identity_denied', message: secret }, secret))).toBe('notAuthorized')
    expect(platformErrorKey(new HttpError(500, { code: 'internal', message: secret }, secret), true)).toBe('unknownResult')
    expect(platformErrorKey(new HttpError(422, { code: 'invalid_current_password', message: secret }, secret))).toBe('invalidPassword')
    expect(platformErrorKey(new Error(secret), true)).toBe('unknownResult')
  })
  it('stores only an internal UX target and consumes it once, never an auth assertion', () => {
    rememberPlatformReturn('/runs?page=3')
    expect(JSON.parse(sessionStorage.getItem('pipewright_platform_return')!)).toMatchObject({ path: '/runs?page=3' })
    expect(consumePlatformReturn()).toBe('/runs?page=3')
    expect(consumePlatformReturn()).toBeNull()
    sessionStorage.setItem('pipewright_platform_return', JSON.stringify({ path: '//evil.example', expires: Date.now() + 5000 }))
    expect(consumePlatformReturn()).toBeNull()
  })
  it('bounds a stalled start request and never automatically submits it again', async () => {
    vi.useFakeTimers()
    calls.post.mockImplementation((_url, _data, opts) => new Promise((_resolve, reject) => {
      opts.signal.addEventListener('abort', () => reject(new HttpError(0, null, 'aborted')))
    }))
    const rejected = expect(startPlatformLogin('github', '/runs')).rejects.toBeInstanceOf(HttpError)
    await vi.advanceTimersByTimeAsync(20001)
    await rejected
    expect(calls.post).toHaveBeenCalledTimes(1)
  })
})

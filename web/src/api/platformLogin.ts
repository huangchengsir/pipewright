import { http, HttpError } from './http'

export const PLATFORM_PROVIDERS = ['github', 'gitlab', 'gitee', 'google', 'microsoft'] as const
export type PlatformProvider = (typeof PLATFORM_PROVIDERS)[number]
export const PLATFORM_NAMES: Record<PlatformProvider, string> = {
  github: 'GitHub', gitlab: 'GitLab', gitee: 'Gitee', google: 'Google', microsoft: 'Microsoft',
}
export function isPlatformProvider(value: unknown): value is PlatformProvider {
  return typeof value === 'string' && PLATFORM_PROVIDERS.some(p => p === value)
}
export interface PublicPlatform {
  provider: PlatformProvider
  visible: boolean
  available: boolean
  reason: '' | 'not_configured' | 'temporarily_unavailable'
}
export interface PlatformBinding { displayLabel: string; issuer: string; subject: string; boundAt: string }
export interface PlatformSettings {
  provider: PlatformProvider
  visible: boolean
  enabled: boolean
  configured: boolean
  hasSecret: boolean
  clientId: string
  microsoftTenant: string
  giteeMode: '' | 'confidential_code' | 's256'
  callbackURL: string
  configVersion: number
  binding?: PlatformBinding
}
export interface PlatformSnapshot {
  visibilityVersion: number
  reauthenticatedUntil: string
  providers: PlatformSettings[]
}
export interface PlatformMutation { provider: PlatformSettings; sessionRevoked: boolean }
export interface PlatformStart { flowId: string; authorizeURL: string; expiresAt: string }
export interface PlatformCandidate {
  flowId: string; provider: PlatformProvider; displayLabel: string; expiresAt: string; configVersion: number
}
export interface PlatformAppInput {
  expectedVersion: number; clientId: string; clientSecret?: string; microsoftTenant?: string; giteeMode?: string
}
const accountPath = '/api/account/platform-login'
async function limited<T>(request: (signal: AbortSignal) => Promise<T>, outer?: AbortSignal): Promise<T> {
  const controller = new AbortController()
  const abort = () => controller.abort()
  const timer = setTimeout(abort, 20000)
  outer?.addEventListener('abort', abort, { once: true })
  if (outer?.aborted) controller.abort()
  try { return await request(controller.signal) }
  finally { clearTimeout(timer); outer?.removeEventListener('abort', abort) }
}
export async function getPublicPlatforms(signal?: AbortSignal): Promise<PublicPlatform[]> {
  const data = await limited(active => http.get<{ providers: PublicPlatform[] }>('/api/auth/platform/providers', { signal: active }), signal)
  if (!Array.isArray(data.providers)) throw new Error('Invalid platform response')
  const byProvider = new Map(data.providers.map(p => [p.provider, p]))
  if (!PLATFORM_PROVIDERS.every(p => {
    const entry = byProvider.get(p)
    return entry && typeof entry.visible === 'boolean' && typeof entry.available === 'boolean'
  })) throw new Error('Invalid platform response')
  return PLATFORM_PROVIDERS.map(p => byProvider.get(p)!)
}
export function startPlatformLogin(provider: PlatformProvider, returnTo: string): Promise<PlatformStart> {
  return limited(signal => http.post<PlatformStart>('/api/auth/platform/' + provider + '/start', { returnTo }, { signal }))
}
export function getPlatformSettings(signal?: AbortSignal): Promise<PlatformSnapshot> {
  return limited(active => http.get<PlatformSnapshot>(accountPath, { signal: active, throwOnUnauthorized: true }), signal)
}
export function savePlatformVisibility(expectedVersion: number, visibility: Record<PlatformProvider, boolean>): Promise<PlatformSnapshot> {
  return limited(signal => http.put<PlatformSnapshot>(accountPath + '/visibility', { expectedVersion, visibility }, { signal }))
}
export function savePlatformApp(provider: PlatformProvider, input: PlatformAppInput): Promise<PlatformMutation> {
  return limited(signal => http.put<PlatformMutation>(accountPath + '/' + provider + '/app', input, { signal }))
}
export function reauthenticatePlatform(currentPassword: string): Promise<{ reauthenticatedUntil: string }> {
  return limited(signal => http.post<{ reauthenticatedUntil: string }>('/api/account/reauth', { currentPassword }, { signal, throwOnUnauthorized: true }))
}
export function startPlatformBinding(provider: PlatformProvider, expectedVersion: number): Promise<PlatformStart> {
  return limited(signal => http.post<PlatformStart>(accountPath + '/' + provider + '/bind/start', { expectedVersion }, { signal }))
}
export function getPlatformCandidate(flowId: string): Promise<PlatformCandidate> {
  return limited(signal => http.get<PlatformCandidate>(accountPath + '/bind/pending/' + encodeURIComponent(flowId), { signal, throwOnUnauthorized: true }))
}
export function confirmPlatformBinding(provider: PlatformProvider, expectedVersion: number, flowId: string): Promise<PlatformMutation> {
  return limited(signal => http.post<PlatformMutation>(accountPath + '/' + provider + '/bind/confirm', { expectedVersion, flowId }, { signal }))
}
export function setPlatformEnabled(provider: PlatformProvider, expectedVersion: number, enabled: boolean): Promise<PlatformMutation> {
  return limited(signal => http.post<PlatformMutation>(accountPath + '/' + provider + '/enabled', { expectedVersion, enabled }, { signal }))
}
export function unbindPlatform(provider: PlatformProvider, expectedVersion: number): Promise<PlatformMutation> {
  return limited(signal => http.delete<PlatformMutation>(accountPath + '/' + provider + '/binding', { signal, body: JSON.stringify({ expectedVersion }) }))
}
/** Only local enum keys are shown, never upstream bodies or raw error strings. */
export function platformErrorKey(error: unknown, mutation = false): string {
  if (!(error instanceof HttpError)) return mutation ? 'unknownResult' : 'authFailed'
  const code = error.apiError?.code
  if (error.status === 0 || (mutation && error.status >= 500) || code === 'platform_result_uncertain') return mutation ? 'unknownResult' : 'expired'
  const keys: Record<string, string> = {
    platform_not_configured: 'notConfigured', platform_disabled: 'unavailable',
    platform_config_conflict: 'conflict', platform_reauth_required: 'reauthRequired',
    invalid_current_password: 'invalidPassword', locked_out: 'rateLimited',
    platform_rate_limited: 'rateLimited', platform_flow_expired: 'expired',
    platform_flow_consumed: 'expired', platform_flow_invalid: 'expired',
    platform_candidate_invalid: 'bindingExpired', platform_identity_denied: 'notAuthorized',
    platform_provider_rejected: 'authFailed', platform_provider_unavailable: 'unavailable',
    platform_unavailable: 'unavailable', bad_request: 'invalidSettings',
  }
  return (code && keys[code]) || 'authFailed'
}
function unsafeReturnCharacters(value: string): boolean {
  return value.includes('\\') || [...value].some(c => c.charCodeAt(0) <= 0x20 || c.charCodeAt(0) === 0x7f)
}
export function safePlatformReturn(raw: unknown): string | null {
  if (typeof raw !== 'string' || raw.length > 2048 || unsafeReturnCharacters(raw)) return null
  // Only the path can change redirect authority; encoded query/fragment data stays intact.
  let value = raw.split(/[?#]/, 1)[0] ?? ''
  for (let i = 0; i < 6; i++) {
    if (!value.startsWith('/') || value.startsWith('//') || unsafeReturnCharacters(value)) return null
    let decoded: string
    try { decoded = decodeURIComponent(value) } catch { return null }
    if (decoded === value) return raw
    value = decoded
  }
  return null
}
export function safePlatformAuthorize(provider: PlatformProvider, raw: string): string | null {
  try {
    const u = new URL(raw)
    const host: Record<PlatformProvider, string> = {
      github: 'github.com', gitlab: 'gitlab.com', gitee: 'gitee.com',
      google: 'accounts.google.com', microsoft: 'login.microsoftonline.com',
    }
    if (u.protocol !== 'https:' || u.host !== host[provider] || u.username || u.password || u.hash) return null
    if (provider === 'github' && u.pathname !== '/login/oauth/authorize') return null
    if ((provider === 'gitlab' || provider === 'gitee') && u.pathname !== '/oauth/authorize') return null
    if (provider === 'google' && u.pathname !== '/o/oauth2/v2/auth') return null
    if (provider === 'microsoft' && !/^\/(?:common|organizations|consumers|[0-9a-f-]{36})\/oauth2\/v2\.0\/authorize$/.test(u.pathname)) return null
    return u.href
  } catch { return null }
}
const returnKey = 'pipewright_platform_return'
export function rememberPlatformReturn(path: string | null): void {
  try { sessionStorage.setItem(returnKey, JSON.stringify({ path, expires: Date.now() + 600000 })) } catch { /* UX only. */ }
}
export function consumePlatformReturn(): string | null {
  try {
    const raw = sessionStorage.getItem(returnKey)
    sessionStorage.removeItem(returnKey)
    if (!raw) return null
    const item = JSON.parse(raw)
    return typeof item.expires === 'number' && item.expires >= Date.now() ? safePlatformReturn(item.path) : null
  } catch { return null }
}

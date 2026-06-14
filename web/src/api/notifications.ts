/**
 * Notification Channels API — aligns to frozen 5.1 contract (FR-19).
 *
 * GET    /api/notifications/channels        → { items: NotificationChannel[] }
 * POST   /api/notifications/channels        → NotificationChannel       (needs CSRF)
 * GET    /api/notifications/channels/{id}   → NotificationChannel
 * PUT    /api/notifications/channels/{id}   → NotificationChannel       (needs CSRF)
 * DELETE /api/notifications/channels/{id}   → 204                       (needs CSRF)
 * POST   /api/notifications/channels/{id}/test → ChannelTestResult      (needs CSRF)
 *
 * Sensitive fields (SMTP password) are WRITE-ONLY: the server never returns the
 * plaintext. Responses only carry `config.hasPassword: boolean`.
 *
 * This release implements webhook + email. wecom / dingtalk / feishu are accepted
 * by the API (saved) but their test/send returns a human-readable not_implemented.
 */

import { http } from './http'

export type ChannelType = 'webhook' | 'email' | 'wecom' | 'dingtalk' | 'feishu'

/** Non-sensitive per-type config (union). Password is never returned — only hasPassword. */
export interface ChannelConfig {
  // webhook
  url?: string
  // email
  smtpHost?: string
  smtpPort?: number
  from?: string
  to?: string
  username?: string
  /** True when an SMTP password is stored (write-only; plaintext never returned). */
  hasPassword?: boolean
}

/** GET response item — never contains a plaintext secret. */
export interface NotificationChannel {
  id: string
  name: string
  type: ChannelType
  enabled: boolean
  config: ChannelConfig
  createdAt: string
  updatedAt: string
}

/** Per-type config sent on create/update. `password` is write-only. */
export interface ChannelConfigInput {
  url?: string
  smtpHost?: string
  smtpPort?: number
  from?: string
  to?: string
  username?: string
  /**
   * Write-only. On update: omit/undefined keeps existing, empty string clears,
   * non-empty rotates. Never echoed back by the server.
   */
  password?: string
}

export interface CreateChannelInput {
  name: string
  type: ChannelType
  enabled: boolean
  config: ChannelConfigInput
}

/** Update body — all fields optional (omit = keep existing). */
export interface UpdateChannelInput {
  name?: string
  enabled?: boolean
  config?: ChannelConfigInput
}

/** POST .../{id}/test response */
export interface ChannelTestResult {
  ok: boolean
  latencyMs: number
  detail: string
  error: string | null
}

export async function listChannels(): Promise<NotificationChannel[]> {
  const res = await http.get<{ items: NotificationChannel[] }>('/api/notifications/channels')
  return res.items ?? []
}

export async function createChannel(input: CreateChannelInput): Promise<NotificationChannel> {
  return http.post<NotificationChannel>('/api/notifications/channels', input)
}

export async function updateChannel(
  id: string,
  input: UpdateChannelInput,
): Promise<NotificationChannel> {
  return http.put<NotificationChannel>(`/api/notifications/channels/${id}`, input)
}

export async function deleteChannel(id: string): Promise<void> {
  await http.delete<void>(`/api/notifications/channels/${id}`)
}

export async function testChannel(id: string): Promise<ChannelTestResult> {
  return http.post<ChannelTestResult>(`/api/notifications/channels/${id}/test`, {})
}

/**
 * Notification Event Routes API — Story 5.2 (FR-20).
 *
 * GET    /api/notifications/routes        → { items: NotificationRoute[] }
 * POST   /api/notifications/routes        → NotificationRoute   (needs CSRF)
 * DELETE /api/notifications/routes/{id}   → 204                 (needs CSRF)
 *
 * Maps events → channels. An event with no enabled route is NOT delivered (FR-20).
 * An event may map to multiple channels (multiple routes).
 *
 * Story 5.4 (per-pipeline override): routes carry an optional projectId.
 *   - listRoutes(projectId) — projectId set → that project's override routes;
 *     omitted/empty → global default routes (projectId IS NULL).
 *   - createRoute({ projectId, ... }) — projectId set = project-level override,
 *     omitted/empty = global default.
 * Resolution at send time is WHOLE-SCOPE override: if a project has any enabled
 * route for an event it uses ONLY its own routes (not merged with global);
 * otherwise it inherits the global routes.
 */

/** Frozen event enum (run terminal status → event mapping lives server-side). */
export type NotificationEvent =
  | 'build_succeeded'
  | 'build_failed'
  | 'deploy_succeeded'
  | 'deploy_failed'
  | 'rollback'
  | 'health_check_failed'
  | 'approval_required'
  | 'anomaly_detected'

/** GET response item — an event→channel mapping. */
export interface NotificationRoute {
  id: string
  /** Per-pipeline override scope (Story 5.4); empty = global default. */
  projectId?: string
  event: NotificationEvent
  channelId: string
  enabled: boolean
  createdAt: string
}

/** Create body. enabled defaults to true server-side when omitted. */
export interface CreateRouteInput {
  /** Set = project-level override (Story 5.4); omit/empty = global default. */
  projectId?: string
  event: NotificationEvent
  channelId: string
  enabled?: boolean
}

/**
 * List routes for a scope. projectId set → that project's override routes;
 * omitted/empty → global default routes.
 */
export async function listRoutes(projectId?: string): Promise<NotificationRoute[]> {
  const query = projectId ? `?projectId=${encodeURIComponent(projectId)}` : ''
  const res = await http.get<{ items: NotificationRoute[] }>(`/api/notifications/routes${query}`)
  return res.items ?? []
}

export async function createRoute(input: CreateRouteInput): Promise<NotificationRoute> {
  return http.post<NotificationRoute>('/api/notifications/routes', input)
}

export async function deleteRoute(id: string): Promise<void> {
  await http.delete<void>(`/api/notifications/routes/${id}`)
}

/**
 * Notification Templates API — Story 5.3 (FR-21).
 *
 * GET    /api/notifications/templates        → { items: NotificationTemplate[] }
 * POST   /api/notifications/templates        → NotificationTemplate   (needs CSRF)
 * PUT    /api/notifications/templates/{id}   → NotificationTemplate   (needs CSRF)
 * DELETE /api/notifications/templates/{id}   → 204                    (needs CSRF)
 *
 * Customizes notification title/body per event (optionally per channel). Placeholders
 * use {{name}} plain-text substitution (no RCE). Variable set is frozen:
 * project / branch / commit / status / event / durationMs / runId / errorSummary.
 * Unknown placeholders render to empty string. An event with no template falls back to
 * the platform default copy (5-2 behaviour unchanged). Match priority: exact channelId >
 * generic (empty channelId) > platform default. projectId is reserved for 5-4.
 */

/** Frozen template variable placeholders (for the "available variables" hint). */
export const TEMPLATE_VARIABLES = [
  'project',
  'branch',
  'commit',
  'status',
  'event',
  'durationMs',
  'runId',
  'errorSummary',
] as const

/** GET response item — an event(+optional channel) → title/body template mapping. */
export interface NotificationTemplate {
  id: string
  /** Reserved for 5-4 per-pipeline override; empty = global default this release. */
  projectId?: string
  event: NotificationEvent
  /** Empty = applies to all channels for this event; non-empty = only that channel. */
  channelId?: string
  titleTemplate: string
  bodyTemplate: string
  createdAt: string
}

/** Create body. channelId empty/omitted = generic (all channels for the event). */
export interface CreateTemplateInput {
  event: NotificationEvent
  channelId?: string
  titleTemplate: string
  bodyTemplate: string
}

/** Update body — all fields optional (omit = keep existing). */
export interface UpdateTemplateInput {
  event?: NotificationEvent
  channelId?: string
  titleTemplate?: string
  bodyTemplate?: string
}

export async function listTemplates(): Promise<NotificationTemplate[]> {
  const res = await http.get<{ items: NotificationTemplate[] }>('/api/notifications/templates')
  return res.items ?? []
}

export async function createTemplate(input: CreateTemplateInput): Promise<NotificationTemplate> {
  return http.post<NotificationTemplate>('/api/notifications/templates', input)
}

export async function updateTemplate(
  id: string,
  input: UpdateTemplateInput,
): Promise<NotificationTemplate> {
  return http.put<NotificationTemplate>(`/api/notifications/templates/${id}`, input)
}

export async function deleteTemplate(id: string): Promise<void> {
  await http.delete<void>(`/api/notifications/templates/${id}`)
}

/** Global notification config — currently just the outbound-content language. */
export interface NotifyConfig {
  language: string
}

export async function getNotifyConfig(): Promise<NotifyConfig> {
  return http.get<NotifyConfig>('/api/notifications/config')
}

export async function setNotifyConfig(language: string): Promise<NotifyConfig> {
  return http.put<NotifyConfig>('/api/notifications/config', { language })
}

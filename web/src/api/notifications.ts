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
 * An event may map to multiple channels (multiple routes). projectId is reserved
 * for 5-4 (per-pipeline override) and is always empty this release (global default).
 */

/** Frozen event enum (run terminal status → event mapping lives server-side). */
export type NotificationEvent =
  | 'build_succeeded'
  | 'build_failed'
  | 'deploy_succeeded'
  | 'deploy_failed'
  | 'rollback'
  | 'health_check_failed'

/** GET response item — an event→channel mapping. */
export interface NotificationRoute {
  id: string
  /** Reserved for 5-4 per-pipeline override; empty = global default this release. */
  projectId?: string
  event: NotificationEvent
  channelId: string
  enabled: boolean
  createdAt: string
}

/** Create body. enabled defaults to true server-side when omitted. */
export interface CreateRouteInput {
  event: NotificationEvent
  channelId: string
  enabled?: boolean
}

export async function listRoutes(): Promise<NotificationRoute[]> {
  const res = await http.get<{ items: NotificationRoute[] }>('/api/notifications/routes')
  return res.items ?? []
}

export async function createRoute(input: CreateRouteInput): Promise<NotificationRoute> {
  return http.post<NotificationRoute>('/api/notifications/routes', input)
}

export async function deleteRoute(id: string): Promise<void> {
  await http.delete<void>(`/api/notifications/routes/${id}`)
}

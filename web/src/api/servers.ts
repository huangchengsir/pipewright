/**
 * Servers API — aligns to frozen 4.1 contract (FR-14).
 *
 * GET    /api/servers            → { items: Server[] }
 * POST   /api/servers            → Server          (needs CSRF)
 * GET    /api/servers/:id        → Server
 * PUT    /api/servers/:id        → Server          (needs CSRF)
 * DELETE /api/servers/:id        → 204             (needs CSRF)
 * POST   /api/servers/:id/test   → ServerTestResult (needs CSRF)
 *
 * A server binds a SSH credential by reference only (credentialId); the API
 * never returns the private key or password. Connectivity is proven by the
 * test endpoint, which runs a read-only `uname -a` over SSH and reports
 * latency / output, or a human-readable error — never the secret.
 */

import { http } from './http'

export interface Server {
  id: string
  name: string
  host: string
  port: number
  user: string
  /** Reference to a ssh_key credential — never the key material itself. */
  credentialId: string
  /** Redundant display name joined from credentials, for the list UI. */
  credentialName: string
  createdAt: string
  updatedAt: string
}

export interface CreateServerInput {
  name: string
  host: string
  port: number
  user: string
  credentialId: string
}

export interface UpdateServerInput {
  name?: string
  host?: string
  port?: number
  user?: string
  credentialId?: string
}

export interface ServerTestResult {
  ok: boolean
  latencyMs: number
  /** Truncated `uname -a` output on success; empty on failure. */
  output: string
  /** Human-readable error on failure; null on success. Never contains secrets. */
  error: string | null
}

export async function listServers(): Promise<Server[]> {
  const res = await http.get<{ items: Server[] }>('/api/servers')
  return res.items
}

export async function createServer(input: CreateServerInput): Promise<Server> {
  return http.post<Server>('/api/servers', input)
}

export async function updateServer(id: string, input: UpdateServerInput): Promise<Server> {
  return http.put<Server>(`/api/servers/${id}`, input)
}

export async function deleteServer(id: string): Promise<void> {
  return http.delete<void>(`/api/servers/${id}`)
}

export async function testServer(id: string): Promise<ServerTestResult> {
  return http.post<ServerTestResult>(`/api/servers/${id}/test`)
}

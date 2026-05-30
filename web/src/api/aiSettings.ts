/**
 * AI Settings API — aligns to frozen 7.1 contract.
 *
 * GET  /api/settings/ai          → AISettings
 * PUT  /api/settings/ai          → AISettings  (needs CSRF)
 * POST /api/settings/ai/test     → AITestResult (needs CSRF)
 *
 * apiKey is WRITE-ONLY: the server never returns plaintext.
 * GET/PUT responses only include apiKeyMasked (e.g. "sk-ant-••••a91f").
 * Ollama does not require an apiKey.
 */

import { http } from './http'

export type AIProvider = 'claude' | 'openai' | 'ollama' | ''

export interface AIBudget {
  monthlyTokenLimit: number | null
}

/** GET /api/settings/ai response — never contains plaintext apiKey */
export interface AISettings {
  configured: boolean
  enabled: boolean
  provider: AIProvider
  baseUrl: string
  model: string
  /** Server-computed mask, e.g. "sk-ant-••••a91f" — never plaintext. */
  apiKeyMasked: string
  budget: AIBudget
  updatedAt: string | null
}

/** PUT /api/settings/ai request body */
export interface SaveAISettingsInput {
  provider: AIProvider
  baseUrl: string
  model: string
  /**
   * Write-only: omit or leave empty to keep existing key unchanged.
   * Non-empty rotates to the new key.
   */
  apiKey?: string
  budget: AIBudget
  enabled: boolean
}

/** POST /api/settings/ai/test response */
export interface AITestResult {
  ok: boolean
  latencyMs: number
  detail: string
  error: string | null
}

/** POST /api/settings/ai/test request body (all optional — falls back to saved config) */
export interface TestAIConnectionInput {
  provider?: AIProvider
  baseUrl?: string
  model?: string
  /** Write-only draft key for testing before saving. */
  apiKey?: string
}

export async function getAISettings(): Promise<AISettings> {
  return http.get<AISettings>('/api/settings/ai')
}

export async function saveAISettings(input: SaveAISettingsInput): Promise<AISettings> {
  return http.put<AISettings>('/api/settings/ai', input)
}

export async function testAIConnection(draft?: TestAIConnectionInput): Promise<AITestResult> {
  return http.post<AITestResult>('/api/settings/ai/test', draft ?? {})
}

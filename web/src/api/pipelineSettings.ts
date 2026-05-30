/**
 * Pipeline build/deploy settings API — aligns to frozen 2.4 contract.
 *
 * GET /api/projects/{id}/pipeline/settings  → SettingsDTO  (lazily creates default on first access)
 * PUT /api/projects/{id}/pipeline/settings  → SettingsDTO  (needs CSRF)
 *
 * Independent of the 2.2 pipeline spec. Secret variables / registry credentials are
 * stored as a credentialId reference only — the server never returns plaintext, only
 * a server-computed maskedValue. On save, secret items send {key,secret:true,credentialId}
 * (no plaintext / mask).
 */

import { http } from './http'

// ─── Enums (frozen) ──────────────────────────────────────────────────────────

export type BuildModel = 'dockerfile' | 'toolchain'
export type ArtifactType = 'image' | 'jar' | 'dist'
export type RegistryType = 'harbor' | 'acr' | 'dockerhub' | 'custom'

// ─── Domain types (frozen DTO shape) ─────────────────────────────────────────

export interface BuildVar {
  id: string
  key: string
  secret: boolean
  /** Present only for non-secret vars. */
  value?: string
  /** Present only for secret vars — vault reference. */
  credentialId?: string
  /** Server-computed mask for secret vars, e.g. "••••_zzz" — never plaintext. */
  maskedValue?: string
}

export interface Toolchain {
  language: string
  version: string
}

export interface Cache {
  enabled: boolean
  paths: string[]
}

export interface BuildConfig {
  model: BuildModel
  dockerfilePath: string
  toolchain: Toolchain
  artifactType: ArtifactType
  vars: BuildVar[]
  cache: Cache
}

export interface ImageRegistry {
  /** Empty string means "not bound yet". */
  type: RegistryType | ''
  url: string
  credentialId?: string
  maskedCredential?: string
}

export interface Environment {
  id: string
  name: string
  targetServerIds: string[]
  envVars: BuildVar[]
  imageRegistry: ImageRegistry
}

export interface SettingsDTO {
  build: BuildConfig
  environments: Environment[]
  updatedAt: string
}

// ─── Save request types (secret items send only credentialId; no plaintext/mask) ──

export interface SaveBuildVar {
  id?: string
  key: string
  secret: boolean
  value?: string
  credentialId?: string
}

export interface SaveImageRegistry {
  type: RegistryType | ''
  url: string
  credentialId?: string
}

export interface SaveEnvironment {
  id?: string
  name: string
  targetServerIds: string[]
  envVars: SaveBuildVar[]
  imageRegistry: SaveImageRegistry
}

export interface SaveSettingsInput {
  build: {
    model: BuildModel
    dockerfilePath?: string
    toolchain?: Toolchain
    artifactType: ArtifactType
    vars: SaveBuildVar[]
    cache: Cache
  }
  environments: SaveEnvironment[]
}

// ─── API functions ────────────────────────────────────────────────────────────

export async function getSettings(projectId: string): Promise<SettingsDTO> {
  return http.get<SettingsDTO>(`/api/projects/${projectId}/pipeline/settings`)
}

export async function saveSettings(
  projectId: string,
  input: SaveSettingsInput,
): Promise<SettingsDTO> {
  return http.put<SettingsDTO>(`/api/projects/${projectId}/pipeline/settings`, input)
}

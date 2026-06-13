/**
 * Projects API — aligns to frozen 2.1 contract.
 *
 * GET    /api/projects                  → Project[]
 * POST   /api/projects                  → Project   (needs CSRF)
 * POST   /api/projects/test-clone       → TestCloneResult (needs CSRF)
 * PATCH  /api/projects/{id}             → Project   (needs CSRF)
 * DELETE /api/projects/{id}             → 204       (needs CSRF)
 *
 * credentialName is read-only display; credentialId is the reference.
 * lastRunStatus / targetServers may be null/[] until future stories fill them.
 * The API never returns credential plaintext — only credentialName (masked display).
 */

import { http } from './http'

export type RunStatus =
  | '成功'
  | '失败'
  | '进行中'
  | '部分失败'
  | '已回滚'
  | '排队中'

export interface Project {
  id: string
  name: string
  repoUrl: string
  defaultBranch: string
  credentialId: string
  /** Masked display name from the vault — never plaintext. */
  credentialName: string
  /**
   * Pipeline-as-code (GitOps) toggle. When true, each run reads `.pipewright.yml`
   * from the run's branch in the repo and uses it to drive the run (falling back
   * to the stored UI pipeline if the file is missing or invalid). FR-8-12.
   */
  pacEnabled: boolean
  /**
   * PR status checks toggle (Story 8-9 / FR-8-9). When true, on a run reaching a
   * terminal status Pipewright detects the repo platform (GitHub/Gitee) and writes
   * back the commit status (PR check) using the project credential. Best-effort.
   */
  prStatusEnabled: boolean
  /** null until pipeline runs exist (Story 2.x). */
  lastRunStatus: RunStatus | null
  /** Empty until servers are bound (Story 2.x). */
  targetServers: string[]
  createdAt: string
  updatedAt: string
}

export interface CreateProjectInput {
  name: string
  repoUrl: string
  credentialId: string
  /** Optional — server auto-detects from ls-remote HEAD if omitted. */
  defaultBranch?: string
}

export interface UpdateProjectInput {
  name?: string
  defaultBranch?: string
  credentialId?: string
  /** Toggle pipeline-as-code (GitOps) for this project. */
  pacEnabled?: boolean
  /** Toggle PR status checks (commit status writeback) for this project. */
  prStatusEnabled?: boolean
}

export interface TestCloneResult {
  ok: true
  defaultBranch: string
}

export interface TestCloneInput {
  repoUrl: string
  credentialId: string
}

export async function listProjects(): Promise<Project[]> {
  return http.get<Project[]>('/api/projects')
}

export async function createProject(input: CreateProjectInput): Promise<Project> {
  return http.post<Project>('/api/projects', input)
}

export async function testClone(input: TestCloneInput): Promise<TestCloneResult> {
  return http.post<TestCloneResult>('/api/projects/test-clone', input)
}

export async function updateProject(id: string, input: UpdateProjectInput): Promise<Project> {
  return http.patch<Project>(`/api/projects/${id}`, input)
}

export async function deleteProject(id: string): Promise<void> {
  return http.delete<void>(`/api/projects/${id}`)
}

/** One stage's summary from a previewed `.pipewright.yml` (no secrets). */
export interface PacStageSummary {
  name: string
  kind: string
  jobCount: number
}

/**
 * Result of previewing/validating the repo's `.pipewright.yml` at a chosen ref.
 * - found=false: the file does not exist at that ref (or repo unreadable) — runs fall back to the UI pipeline.
 * - found=true, valid=false: the file exists but failed to parse/validate; `error` carries the
 *   server's human-readable, secret-free message; `stages` is empty.
 * - found=true, valid=true: `stages`/`stageCount` summarize what the runtime would use.
 */
export interface PacPreviewResult {
  found: boolean
  valid: boolean
  ref: string
  file: string
  error: string
  stageCount: number
  stages: PacStageSummary[]
}

/**
 * Fetch & validate the repo's `.pipewright.yml` at `ref` (defaults to the project's default
 * branch when omitted). Read-only; never returns secrets or the repo URL credentials.
 */
export async function previewPacConfig(id: string, ref?: string): Promise<PacPreviewResult> {
  const qs = ref && ref.trim() ? `?ref=${encodeURIComponent(ref.trim())}` : ''
  return http.get<PacPreviewResult>(`/api/projects/${id}/pac/preview${qs}`)
}

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

/**
 * Pipeline API — aligns to frozen 2.2 contract.
 *
 * GET  /api/projects/{id}/pipeline  → PipelineDTO  (lazily creates default on first access)
 * PUT  /api/projects/{id}/pipeline  → PipelineDTO  (needs CSRF)
 *
 * Frozen DTO shape: stages[].{id,name,kind,jobs[].{id,name,type,summary,config}}
 * + yaml + status + updatedAt
 * Post-2.2 stories only fill job.config, never change the outer shape.
 */

import { http } from './http'

// ─── Domain types (frozen DTO shape) ─────────────────────────────────────────

export type StageKind = 'source' | 'build' | 'deploy' | 'notify' | 'custom'

export interface PipelineJob {
  id: string
  name: string
  /** Free-form token; e.g. git_source | build_image | push_image | deploy_ssh | health_check | notify | custom */
  type: string
  /** Card subtitle — may be empty */
  summary: string
  /** Arbitrary KV object; free-form in this story, tightened in 2-4/2-6 */
  config: Record<string, string>
}

export interface PipelineStage {
  id: string
  name: string
  kind: StageKind
  jobs: PipelineJob[]
}

export interface PipelineDTO {
  stages: PipelineStage[]
  /** Server-rendered read-only YAML representation */
  yaml: string
  /** Always "draft" in this story */
  status: 'draft' | string
  updatedAt: string
}

// ─── Save request type (only stages; server derives yaml/status) ──────────────

export interface SavePipelineInput {
  stages: Array<{
    id?: string
    name: string
    kind: StageKind
    jobs: Array<{
      id?: string
      name: string
      type: string
      summary?: string
      config?: Record<string, string>
    }>
  }>
}

// ─── API functions ────────────────────────────────────────────────────────────

export async function getPipeline(projectId: string): Promise<PipelineDTO> {
  return http.get<PipelineDTO>(`/api/projects/${projectId}/pipeline`)
}

export async function savePipeline(
  projectId: string,
  input: SavePipelineInput,
): Promise<PipelineDTO> {
  return http.put<PipelineDTO>(`/api/projects/${projectId}/pipeline`, input)
}

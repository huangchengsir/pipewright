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

/** Conditional execution rule (Epic 8 · 8-5). Empty = always run. */
export interface StageWhen {
  /** Run only if the trigger branch matches one of these globs (empty = any). */
  branches?: string[]
  /** Run only on these trigger types: manual | webhook | schedule (empty = any). */
  events?: string[]
}

export interface PipelineStage {
  id: string
  name: string
  kind: StageKind
  /** Upstream stage IDs this stage depends on (Epic 8 DAG). Empty = no explicit deps. */
  needs?: string[]
  /** When true, this stage's failure does not block downstream stages. */
  allowFailure?: boolean
  /** Conditional execution; unmet → stage (and downstream) skipped. */
  when?: StageWhen
  /** Require manual approval before entering this stage (Epic 8 · 8-4). */
  gate?: boolean
  /**
   * Matrix build axes (P1): axisName → value list. Scheduler expands to the
   * cartesian product of parallel cells, each running this stage's jobs with
   * `MATRIX_<AXIS>` injected as container env. Empty = no expansion (single stage).
   */
  matrix?: Record<string, string[]>
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
    needs?: string[]
    allowFailure?: boolean
    when?: StageWhen
    gate?: boolean
    matrix?: Record<string, string[]>
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

// ─── Pipeline-as-code import / preview (FR-8-12) ──────────────────────────────

/**
 * Parse + validate a `.pipewright.yml` document into the pipeline model.
 *
 * POST /api/projects/{id}/pipeline/import  (needs CSRF)
 * Body: { yaml, save }
 *   - save=false (default): returns the parsed PipelineDTO as a preview, does NOT persist.
 *   - save=true: persists the parsed spec via the same Save path as the canvas PUT.
 * Validation failures surface as HttpError 422 with codes invalid_yaml/invalid_stage/...
 */
export async function importPipeline(
  projectId: string,
  yaml: string,
  save = false,
): Promise<PipelineDTO> {
  return http.post<PipelineDTO>(`/api/projects/${projectId}/pipeline/import`, { yaml, save })
}

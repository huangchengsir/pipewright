/**
 * Environments API — first-class environments (GitLab-environments parity).
 *
 *   GET  /api/projects/{id}/environments/deployments      → per-environment deployment timelines + active version
 *   GET  /api/projects/{id}/environments/{env}/history     → single environment timeline
 *   POST /api/projects/{id}/environments/{env}/rollback    → one-click rollback to the previous successful deployment
 *
 * Read-only aggregation over existing pipeline-run data (pipeline_runs + deploy_targets +
 * run_artifacts); zero migration. Rollback re-runs the previous successful deployment's
 * artifact onto the same set of target servers via the existing deploy pipeline.
 *
 * Errors follow the canonical envelope; callers inspect `err.apiError?.code` on HttpError.
 */

import { http } from './http'

/** Aggregated deployment status across all target servers of one deployment. */
export type EnvDeployStatus = 'success' | 'partial_failed' | 'failed'

/** One target server's result within a deployment. */
export interface EnvTarget {
  serverId: string
  serverName: string
  /** Per-target status: pending|deploying|success|failed|rolled_back. */
  status: string
}

/** Artifact published by a deployment. */
export interface EnvArtifact {
  id: string
  /** image|jar|dist|archive. */
  type: string
  name: string
  reference: string
}

/** A single deployment event on an environment's timeline. */
export interface EnvDeployment {
  runId: string
  status: EnvDeployStatus
  commit: string
  branch: string
  triggeredBy: string
  /** RFC3339; the latest target-server finish time of this deployment. */
  deployedAt: string
  /** Whether this is the environment's current active version (most recent all-success). */
  active: boolean
  targets: EnvTarget[]
  artifacts: EnvArtifact[]
}

/** One environment's deployment timeline (most recent first). */
export interface EnvironmentTimeline {
  environment: string
  /** Current active version (most recent all-success); null when never fully succeeded. */
  active: EnvDeployment | null
  deployments: EnvDeployment[]
}

interface EnvironmentsResponse {
  environments: EnvironmentTimeline[]
}

/** Rollback result: deploy targets shape + provenance (from which run to which run). */
export interface RollbackResult {
  environment: string
  /** Run that was active before rollback. */
  fromRunId: string
  /** Rollback target (previous successful deployment) run. */
  toRunId: string
  artifactId: string
  targets: EnvTarget[]
}

/** List per-environment deployment timelines for a project (most-recently-deployed env first). */
export async function listEnvironmentDeployments(projectId: string): Promise<EnvironmentTimeline[]> {
  const res = await http.get<EnvironmentsResponse>(
    `/api/projects/${encodeURIComponent(projectId)}/environments/deployments`,
  )
  return res.environments ?? []
}

/** Fetch a single environment's deployment timeline. */
export function getEnvironmentHistory(projectId: string, env: string): Promise<EnvironmentTimeline> {
  return http.get<EnvironmentTimeline>(
    `/api/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(env)}/history`,
  )
}

/** One-click rollback an environment to its previous successful deployment. */
export function rollbackEnvironment(projectId: string, env: string): Promise<RollbackResult> {
  return http.post<RollbackResult>(
    `/api/projects/${encodeURIComponent(projectId)}/environments/${encodeURIComponent(env)}/rollback`,
  )
}

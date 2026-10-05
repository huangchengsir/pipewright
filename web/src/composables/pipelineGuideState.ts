import type { PipelineStage } from '../api/pipeline'
import type { Snapshot } from '../api/onboarding'

export type GuidePhase = 'loading' | 'unknown' | 'repository' | 'stage' | 'task' | 'choose' | 'configure' | 'save' | 'issues' | 'runtime' | 'ready'
export interface GuideContext { selectedJobId: string | null; pickerOpen: boolean }
export interface GuideInput {
  stages: PipelineStage[]; snapshot: Snapshot | null; projectId: string
  loading: boolean; error: boolean; dirty: boolean; context: GuideContext; reviewedJobId: string | null
}
// Inspector suggestions only: source and delegated push markers are not task setup targets.
// Applicability and readiness remain authoritative on the server.
export function guideJobs(stages: PipelineStage[]) {
  return stages.filter(s => s.kind !== 'source').flatMap(s => s.jobs).filter(j => !['git_source', 'push_image'].includes(j.type.trim()))
}
export function guidePhase(input: GuideInput): GuidePhase {
  const { snapshot, loading, error, dirty, stages, context, reviewedJobId } = input
  if (loading) return 'loading'
  if (error || !snapshot || snapshot.selectedProject?.id !== input.projectId || snapshot.pipeline.state === 'unknown') return 'unknown'
  if (snapshot.selectedProject.pacEnabled || snapshot.pipeline.state === 'repository') return 'repository'
  if (!dirty && snapshot.pipeline.state === 'ready' && snapshot.pipeline.savedAt && snapshot.runtime === 'available') return 'ready'
  if (context.pickerOpen) return 'choose'
  const taskStages = stages.filter(s => s.kind !== 'source')
  const jobs = guideJobs(stages)
  if (!taskStages.length) return 'stage'
  if (!jobs.length) return 'task'
  if (!reviewedJobId || (context.selectedJobId && reviewedJobId !== context.selectedJobId)) return 'configure'
  if (dirty || !snapshot.pipeline.savedAt) return 'save'
  if (snapshot.pipeline.issues.some(i => i.code !== 'runtime_stub')) return 'issues'
  if (snapshot.runtime !== 'available' || snapshot.pipeline.issues.some(i => i.code === 'runtime_stub')) return 'runtime'
  return 'unknown'
}

// Issue codes contain no task IDs. Locate the applicable tab, never guess which task is invalid.
export function guideIssueTarget(code: string, scope: string, projectId: string): string {
  if (code === 'project_credential_missing') return `/projects?onboardingEdit=${encodeURIComponent(projectId)}`
  if (code === 'vault_unconfigured' || code === 'credential_missing') return '/settings/vault'
  if (code === 'server_missing') return '/settings/servers'
  return `/projects/${encodeURIComponent(projectId)}/pipeline?tab=${['canvas', 'vars', 'envs', 'triggers'].includes(scope) ? scope : 'canvas'}&onboardingGuide=1`
}

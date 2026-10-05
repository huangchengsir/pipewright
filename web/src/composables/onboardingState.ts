import type { Snapshot, RunSummary } from '../api/onboarding'

export type StepState = 'done' | 'saved' | 'current' | 'pending' | 'unknown'
export type FlowKind = 'loading' | 'unknown' | 'create' | 'queued' | 'running' | 'waiting_approval'
  | 'failed' | 'configure' | 'unconfirmed' | 'repository' | 'ready' | 'success' | 'legacy' | 'stub' | 'mixed' | 'pending'
export interface FlowAction { label: string; to?: string; kind?: 'retry' | 'help' }
export interface FlowState { kind: FlowKind; steps: [StepState, StepState, StepState]; primary: FlowAction | null; secondary: FlowAction[] }
export const ACTIVE_RUN_STATUSES = ['queued', 'running', 'waiting_approval'] as const
export const ISSUE_KEYS: Record<string, string> = {
  build_invalid: 'build', toolchain_incomplete: 'build', script_incomplete: 'tasks',
  credential_missing: 'credentials', project_credential_missing: 'projectCredential', vault_unconfigured: 'vault',
  environment_undefined: 'environment', no_tasks: 'noTasks', notification_channel_missing: 'notification',
  pipeline_invalid: 'pipeline', source_stage_missing: 'pipeline', task_execution_unavailable: 'tasks',
  runtime_stub: 'runtime', server_missing: 'server', credentials_unavailable: 'storage',
  notifications_unavailable: 'storage', runner_unavailable: 'storage', servers_unavailable: 'storage',
  settings_unavailable: 'storage', triggers_unavailable: 'storage',
}
export function qualifyingSuccess(run: RunSummary | null): boolean {
  return run?.status === 'success' && (run.executionMode === 'real' || run.executionMode === 'legacy_unknown')
}
const link = (label: string, to: string): FlowAction => ({ label, to })
const help: FlowAction = { label: 'runtimeHelp', kind: 'help' }

export function deriveOnboarding(snapshot: Snapshot | null, loading = false, failed = false): FlowState {
  const result = (kind: FlowKind, steps: FlowState['steps'], primary: FlowAction | null, secondary: FlowAction[] = []): FlowState => ({ kind, steps, primary, secondary })
  if (!snapshot) return result(loading && !failed ? 'loading' : 'unknown', ['unknown', 'unknown', 'unknown'], loading && !failed ? null : { label: 'retry', kind: 'retry' })
  if (snapshot.successState === 'known' && qualifyingSuccess(snapshot.success)) {
    const success = snapshot.success!
    return result(success.executionMode === 'legacy_unknown' ? 'legacy' : 'success', ['done', 'done', 'done'],
      link('viewResult', `/runs/${encodeURIComponent(success.id)}`), success.projectExists ? [link('autoTrigger', `/projects/${encodeURIComponent(success.projectId)}/pipeline?tab=triggers`)] : [])
  }
  if (snapshot.projectsState === 'unknown' || snapshot.runsState === 'unknown' || snapshot.successState === 'unknown') {
    return result('unknown', [snapshot.projectsState === 'known' && snapshot.selectedProject ? 'done' : 'unknown', 'unknown', 'unknown'], { label: 'retry', kind: 'retry' })
  }
  if (snapshot.projectCount === 0 && snapshot.projects.length === 0) return result('create', ['current', 'pending', 'pending'], link('create', '/projects?onboardingCreate=1'))
  const project = snapshot.selectedProject
  if (!project || !snapshot.projects.some(p => p.id === project.id)) return result('unknown', ['unknown', 'unknown', 'unknown'], { label: 'retry', kind: 'retry' })
  const pipelineURL = `/projects/${encodeURIComponent(project.id)}/pipeline`
  const edit = link('edit', pipelineURL)
  const manual = link('goRun', `/projects?onboardingRun=${encodeURIComponent(project.id)}`)
  const pipelineDone = snapshot.pipeline.state === 'ready' && !!snapshot.pipeline.savedAt
  const preparation: StepState = pipelineDone ? 'done' : snapshot.pipeline.savedAt ? 'saved'
    : ['unknown', 'unconfirmed', 'repository'].includes(snapshot.pipeline.state) ? 'unknown' : 'current'
  const steps: FlowState['steps'] = ['done', preparation, pipelineDone ? 'current' : 'pending']
  const latest = snapshot.latestRun?.projectId === project.id ? snapshot.latestRun : null
  if (latest && ACTIVE_RUN_STATUSES.some(s => s === latest.status)) {
    steps[2] = 'current'
    return result(latest.status as FlowKind, steps, link('viewRun', `/runs/${encodeURIComponent(latest.id)}`))
  }
  if (latest && ['failed', 'partial_failed', 'rolled_back'].includes(latest.status)) {
    steps[2] = 'current'
    return result('failed', steps, link('viewFailed', `/runs/${encodeURIComponent(latest.id)}`), [edit])
  }
  if (snapshot.pipeline.state === 'unknown') return result('unknown', steps, { label: 'retry', kind: 'retry' })
  if (latest?.status === 'success' && !qualifyingSuccess(latest)) {
    const mode = latest.executionMode === 'stub' || latest.executionMode === 'mixed' ? latest.executionMode : 'pending'
    return result(mode, steps, link('viewRun', `/runs/${encodeURIComponent(latest.id)}`), [help, edit])
  }
  const issues = snapshot.pipeline.issues
  if (snapshot.pipeline.state === 'absent' || snapshot.pipeline.state === 'needs_configuration') {
    const projectCredential = issues.some(i => i.code === 'project_credential_missing')
    const tab = issues.find(i => i.code !== 'runtime_stub')?.scope ?? 'canvas'
    const target = projectCredential ? `/projects?onboardingEdit=${encodeURIComponent(project.id)}` : `${pipelineURL}?tab=${tab}`
    const onlyRuntime = issues.length > 0 && issues.every(i => i.code === 'runtime_stub')
    return result('configure', steps, onlyRuntime ? help : link('configure', target), snapshot.runtime !== 'available' && !onlyRuntime ? [help] : [])
  }
  if (snapshot.pipeline.state === 'unconfirmed' || snapshot.pipeline.state === 'repository') return result(snapshot.pipeline.state, steps, link('checkPipeline', pipelineURL), [manual])
  if (!pipelineDone || snapshot.runtime !== 'available') return result('unconfirmed', steps, link('checkPipeline', pipelineURL), [manual, help])
  return result('ready', steps, manual)
}

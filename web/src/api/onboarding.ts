import { http } from './http'

export type EvidenceState = 'known' | 'unknown'
export type Runtime = 'stub' | 'available' | 'unknown'
export type ExecutionMode = 'real' | 'legacy_unknown' | 'stub' | 'mixed' | 'pending'
export type IssueScope = 'canvas' | 'vars' | 'envs' | 'triggers'
export interface RunSummary {
  id: string; projectId: string; projectName: string; status: string
  executionMode: ExecutionMode | ''; createdAt: string; projectExists: boolean
}
export interface Snapshot {
  projectsState: EvidenceState; projectCount: number | null
  projects: { id: string; name: string }[]
  runsState: EvidenceState; runCount: number | null
  selectedProject: { id: string; name: string; pacEnabled: boolean } | null
  successState: EvidenceState; success: RunSummary | null; latestRun: RunSummary | null
  pipeline: {
    state: 'absent' | 'unconfirmed' | 'repository' | 'needs_configuration' | 'ready' | 'unknown'
    savedAt: string | null; issues: { code: string; scope: IssueScope }[]
  }
  runtime: Runtime
}

function object(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value) }
function run(value: unknown): boolean {
  return value === null || (object(value) && ['id', 'projectId', 'projectName', 'status', 'executionMode', 'createdAt'].every(k => typeof value[k] === 'string') && typeof value.projectExists === 'boolean')
}
export function isSnapshot(value: unknown): value is Snapshot {
  if (!object(value) || !object(value.pipeline)) return false
  const known = (v: unknown) => v === 'known' || v === 'unknown'
  const count = (v: unknown) => v === null || (typeof v === 'number' && Number.isInteger(v) && v >= 0)
  const project = (v: unknown) => object(v) && typeof v.id === 'string' && typeof v.name === 'string'
  return known(value.projectsState) && known(value.runsState) && known(value.successState)
    && count(value.projectCount) && count(value.runCount) && Array.isArray(value.projects) && value.projects.every(project)
    && (value.selectedProject === null || (project(value.selectedProject) && object(value.selectedProject) && typeof value.selectedProject.pacEnabled === 'boolean'))
    && run(value.success) && run(value.latestRun)
    && ['absent', 'unconfirmed', 'repository', 'needs_configuration', 'ready', 'unknown'].includes(String(value.pipeline.state))
    && (value.pipeline.savedAt === null || typeof value.pipeline.savedAt === 'string') && Array.isArray(value.pipeline.issues)
    && value.pipeline.issues.every(i => object(i) && typeof i.code === 'string' && ['canvas', 'vars', 'envs', 'triggers'].includes(String(i.scope)))
    && ['stub', 'available', 'unknown'].includes(String(value.runtime))
}
export async function getOnboardingStatus(projectId = '', signal?: AbortSignal): Promise<Snapshot> {
  const query = projectId ? `?projectId=${encodeURIComponent(projectId)}` : ''
  const data = await http.get<unknown>(`/api/onboarding/status${query}`, { signal })
  if (!isSnapshot(data)) throw new Error('Invalid onboarding snapshot')
  return data
}

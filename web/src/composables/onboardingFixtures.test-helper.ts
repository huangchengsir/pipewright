import type { Snapshot, RunSummary } from '../api/onboarding'
export function snapshot(overrides: Partial<Snapshot> = {}): Snapshot {
  return { projectsState: 'known', projectCount: 1, projects: [{ id: 'a', name: 'Project A' }, { id: 'b', name: 'Project B' }],
    runsState: 'known', runCount: 0, selectedProject: { id: 'a', name: 'Project A', pacEnabled: false },
    successState: 'known', success: null, latestRun: null,
    pipeline: { state: 'ready', savedAt: '2026-10-05T00:00:00Z', issues: [] }, runtime: 'available', ...overrides }
}
export function run(status = 'success', executionMode: RunSummary['executionMode'] = 'real', projectId = 'a'): RunSummary {
  return { id: `run-${projectId}`, projectId, projectName: `Project ${projectId.toUpperCase()}`, status, executionMode,
    createdAt: '2026-10-05T00:00:00Z', projectExists: true }
}

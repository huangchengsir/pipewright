import { describe, expect, it } from 'vitest'
import { guidePhase, guideIssueTarget, type GuideInput } from './pipelineGuideState'
import { snapshot } from './onboardingFixtures.test-helper'

const source = { id: 'src', name: 'Source', kind: 'source' as const, jobs: [{ id: 'src-job', type: 'git_source', name: 'Source', summary: '', config: {} }] }
const task = { id: 'task', name: 'Script', type: 'script', summary: '', config: { image: 'alpine:3', commands: 'echo test' } }
function input(overrides: Partial<GuideInput> = {}): GuideInput {
  return { projectId: 'a', snapshot: snapshot({ pipeline: { state: 'needs_configuration', savedAt: null, issues: [] } }),
    stages: [source], context: { selectedJobId: null, pickerOpen: false }, reviewedJobId: null,
    loading: false, error: false, dirty: false, ...overrides }
}
const stages = [source, { id: 'build', name: 'Build', kind: 'build' as const, jobs: [task] }]
describe('pipeline guide suggests controls, never decides readiness from clicks', () => {
  it('starts at add stage, then add task, picker and configuration', () => {
    expect(guidePhase(input())).toBe('stage')
    expect(guidePhase(input({ stages: [...stages.slice(0, 1), { ...stages[1]!, jobs: [] }] }))).toBe('task')
    expect(guidePhase(input({ context: { selectedJobId: null, pickerOpen: true } }))).toBe('choose')
    expect(guidePhase(input({ stages }))).toBe('configure')
    expect(guidePhase(input({ stages, reviewedJobId: 'task' }))).toBe('save')
  })
  it('dirty draft cannot inherit ready status from the persisted pipeline', () => {
    expect(guidePhase(input({ stages, snapshot: snapshot(), dirty: true, reviewedJobId: 'task' }))).toBe('save')
    expect(guidePhase(input({ snapshot: snapshot() }))).toBe('ready')
  })
  it('does not guide source and delegated push markers as actual task configuration', () => {
    expect(guidePhase(input({ stages: [source, { ...stages[1]!, jobs: [{ ...task, type: 'git_source' }, { ...task, id: 'push', type: 'push_image' }] }] }))).toBe('task')
  })
  it('saved evidence cannot bypass actual issues or demo runtime', () => {
    const saved = snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'saved', issues: [{ code: 'no_tasks', scope: 'canvas' }] } })
    expect(guidePhase(input({ stages, snapshot: saved, reviewedJobId: 'task' }))).toBe('issues')
    expect(guidePhase(input({ stages, snapshot: { ...saved, runtime: 'stub', pipeline: { ...saved.pipeline, issues: [{ code: 'runtime_stub', scope: 'vars' }] } }, reviewedJobId: 'task' }))).toBe('runtime')
  })
  it.each([{ error: true }, { snapshot: null }, { snapshot: snapshot({ selectedProject: { id: 'b', name: 'B', pacEnabled: false } }) }, { snapshot: snapshot({ pipeline: { state: 'unknown', savedAt: 'saved', issues: [] } }) }])('does not guess on missing or stale evidence: %j', override => {
    expect(guidePhase(input(override))).toBe('unknown')
  })
  it('PAC remains repository-driven even with a UI draft', () => {
    expect(guidePhase(input({ stages, dirty: true, snapshot: snapshot({ selectedProject: { id: 'a', name: 'A', pacEnabled: true } }) }))).toBe('repository')
  })
  it('newly selected task needs review and loading hides previous actions', () => {
    expect(guidePhase(input({ stages, reviewedJobId: 'task', context: { selectedJobId: 'other', pickerOpen: false } }))).toBe('configure')
    expect(guidePhase(input({ snapshot: snapshot(), loading: true }))).toBe('loading')
  })
  it('maps only local, safe issue routes', () => {
    expect(guideIssueTarget('project_credential_missing', 'canvas', 'a/b')).toBe('/projects?onboardingEdit=a%2Fb')
    expect(guideIssueTarget('server_missing', 'envs', 'a')).toBe('/settings/servers')
    expect(guideIssueTarget('credential_missing', 'envs', 'a')).toBe('/settings/vault')
    expect(guideIssueTarget('script_incomplete', 'canvas', 'a')).toBe('/projects/a/pipeline?tab=canvas&onboardingGuide=1')
    expect(guideIssueTarget('unknown', 'https://external', 'a')).toContain('?tab=canvas&')
  })
})

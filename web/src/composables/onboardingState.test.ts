import { describe, it, expect } from 'vitest'
import { deriveOnboarding, ISSUE_KEYS } from './onboardingState'
import { snapshot, run } from './onboardingFixtures.test-helper'

describe('onboarding authoritative state priority', () => {
  it.each(['real', 'legacy_unknown'] as const)('global %s success wins unrelated faults and selected project', mode => {
    const flow = deriveOnboarding(snapshot({ success: run('success', mode, 'b'), projectsState: 'unknown', runsState: 'unknown', pipeline: { state: 'unknown', savedAt: null, issues: [] } }))
    expect(flow.kind).toBe(mode === 'real' ? 'success' : 'legacy')
    expect(flow.steps).toEqual(['done', 'done', 'done'])
    expect(flow.primary?.to).toBe('/runs/run-b')
    expect(flow.secondary[0]?.to).toBe('/projects/b/pipeline?tab=triggers')
  })
  it('does not qualify a record with unknown success authority', () => {
    expect(deriveOnboarding(snapshot({ successState: 'unknown', success: run() })).kind).toBe('unknown')
  })
  it('does not link a missing success project', () => {
    expect(deriveOnboarding(snapshot({ success: { ...run(), projectExists: false } })).secondary).toEqual([])
  })
  it.each(['projectsState', 'runsState', 'successState'] as const)('critical %s failure wins latest running', key => {
    expect(deriveOnboarding(snapshot({ [key]: 'unknown', latestRun: run('running', 'pending') })).primary?.kind).toBe('retry')
  })
  it('only confirmed no-project state creates', () => {
    expect(deriveOnboarding(snapshot({ projectCount: 0, projects: [], selectedProject: null })).kind).toBe('create')
    expect(deriveOnboarding(snapshot({ projectCount: null, projects: [], selectedProject: null })).kind).toBe('unknown')
  })
  it.each(['queued', 'running', 'waiting_approval'])('%s wins configuration unknown without resave', status => {
    const flow = deriveOnboarding(snapshot({ latestRun: run(status, 'pending'), pipeline: { state: 'unknown', savedAt: null, issues: [] } }))
    expect(flow.kind).toBe(status)
    expect(flow.primary?.to).toBe('/runs/run-a')
    expect(flow.steps[1]).toBe('unknown')
  })
  it.each(['failed', 'partial_failed', 'rolled_back'])('%s binds both actions to the same project', status => {
    const flow = deriveOnboarding(snapshot({ latestRun: run(status) }))
    expect(flow.primary?.to).toBe('/runs/run-a')
    expect(flow.secondary[0]?.to).toBe('/projects/a/pipeline')
  })
  it.each(['stub', 'mixed', 'pending'] as const)('%s success never completes', mode => {
    const flow = deriveOnboarding(snapshot({ latestRun: run('success', mode) }))
    expect(flow.kind).toBe(mode)
    expect(flow.steps[2]).not.toBe('done')
    expect(flow.secondary.some(a => a.kind === 'help')).toBe(true)
  })
  it.each(['unconfirmed', 'repository'] as const)('%s retains manual secondary and unknown preparation', state => {
    const flow = deriveOnboarding(snapshot({ pipeline: { state, savedAt: null, issues: [] } }))
    expect(flow.steps[1]).toBe('unknown')
    expect(flow.primary?.to).toBe('/projects/a/pipeline')
    expect(flow.secondary[0]?.to).toBe('/projects?onboardingRun=a')
  })
  it('relevant scopes and Git credential repair select the correct route', () => {
    for (const scope of ['canvas', 'vars', 'envs', 'triggers'] as const) {
      expect(deriveOnboarding(snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'server_missing', scope }] } })).primary?.to).toBe(`/projects/a/pipeline?tab=${scope}`)
    }
    expect(deriveOnboarding(snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'project_credential_missing', scope: 'envs' }] } })).primary?.to).toBe('/projects?onboardingEdit=a')
  })
  it('known stub explains runtime and cannot masquerade as ready', () => {
    expect(deriveOnboarding(snapshot({ runtime: 'stub', pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'runtime_stub', scope: 'vars' }] } })).primary?.kind).toBe('help')
    expect(deriveOnboarding(snapshot({ runtime: 'unknown' })).kind).toBe('unconfirmed')
  })
  it.each(['no_tasks', 'server_missing', 'runtime_stub'])('acknowledges saved configuration despite %s without claiming readiness', code => {
    const flow = deriveOnboarding(snapshot({ runtime: code === 'runtime_stub' ? 'stub' : 'available', pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code, scope: 'canvas' }] } }))
    expect(flow.steps).toEqual(['done', 'saved', 'pending'])
    expect(flow.kind).toBe('configure')
    expect(flow.primary?.label).not.toBe('goRun')
  })
  it.each(['unconfirmed', 'unknown'] as const)('keeps saved evidence when readiness is %s', state => {
    expect(deriveOnboarding(snapshot({ pipeline: { state, savedAt: 'now', issues: [] } })).steps[1]).toBe('saved')
  })
  it('requires server evidence for saved and readiness for done', () => {
    expect(deriveOnboarding(snapshot({ pipeline: { state: 'needs_configuration', savedAt: null, issues: [] } })).steps[1]).toBe('current')
    expect(deriveOnboarding(snapshot({ pipeline: { state: 'ready', savedAt: null, issues: [] } })).steps[1]).not.toBe('done')
    expect(deriveOnboarding(snapshot()).steps).toEqual(['done', 'done', 'current'])
  })
  it('ignores a latest run belonging to another project', () => {
    const flow = deriveOnboarding(snapshot({ latestRun: run('failed', 'real', 'b') }))
    expect(flow.kind).toBe('ready')
    expect(flow.primary?.to).toBe('/projects?onboardingRun=a')
  })
  it('loading/error remove stale action links', () => {
    expect(deriveOnboarding(null, true).primary).toBeNull()
    expect(deriveOnboarding(null, false, true).primary).toEqual({ label: 'retry', kind: 'retry' })
  })
})

const modules = import.meta.glob<{ default: Record<string, any> }>('../i18n/locales/*/onboardingFlow.ts', { eager: true })
describe('all dynamic onboarding locale branches', () => {
  it('covers every safe backend issue and every dynamic state/action in all eight locales', () => {
    expect(Object.keys(ISSUE_KEYS)).toHaveLength(20)
    expect(Object.keys(modules)).toHaveLength(8)
    for (const { default: messages } of Object.values(modules)) {
      for (const key of [...Object.values(ISSUE_KEYS), 'pipeline']) expect(messages.issues[key]).toEqual(expect.any(String))
      for (const key of ['loading', 'unknown', 'create', 'queued', 'running', 'waiting_approval', 'failed', 'configure', 'unconfirmed', 'repository', 'ready', 'success', 'legacy', 'stub', 'mixed', 'pending']) {
        expect(messages.states[key]).toEqual(expect.any(String))
        expect(messages.descriptions[key]).toEqual(expect.any(String))
      }
      for (const key of ['create', 'configure', 'checkPipeline', 'goRun', 'viewRun', 'viewFailed', 'edit', 'viewResult', 'autoTrigger', 'runtimeHelp', 'retry', 'open', 'servers', 'repairCredential', 'credentials']) expect(messages.actions[key]).toEqual(expect.any(String))
      for (const key of ['done', 'saved', 'current', 'pending', 'unknown']) expect(messages.stepStates[key]).toEqual(expect.any(String))
      for (const key of ['project', 'pipeline', 'run']) {
        expect(messages.steps[key]).toEqual(expect.any(String))
        expect(messages.stepDescriptions[key]).toEqual(expect.any(String))
      }
      for (const key of ['stub', 'available', 'unknown', 'instructions']) expect(messages.runtime[key]).toEqual(expect.any(String))
    }
  })
})

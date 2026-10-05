import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { getOnboardingStatus } from '../api/onboarding'
import type { Snapshot } from '../api/onboarding'
import { activateOnboarding, dismissOnboarding, resetOnboarding, rereadOnboardingPreferences, useOnboardingPreferences,
  useOnboardingStatus, shouldAutoOnboard, onboardingLoginTarget, selectCreatedOnboardingProject } from './useOnboarding'
import { snapshot, run } from './onboardingFixtures.test-helper'
vi.mock('../api/onboarding', () => ({ getOnboardingStatus: vi.fn() }))
const get = vi.mocked(getOnboardingStatus)
let wrapper: VueWrapper | undefined
let state: ReturnType<typeof useOnboardingStatus>
function start() {
  wrapper = mount(defineComponent({ setup() { state = useOnboardingStatus(); return () => null } }))
}
function deferred() {
  let resolve!: (value: Snapshot) => void
  const promise = new Promise<Snapshot>(r => { resolve = r })
  return { promise, resolve }
}
beforeEach(() => {
  localStorage.clear()
  resetOnboarding()
  get.mockReset()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
})
afterEach(() => {
  wrapper?.unmount(); wrapper = undefined
  vi.useRealTimers(); vi.restoreAllMocks()
  resetOnboarding()
})
describe('browser preferences are not domain evidence', () => {
  it('preserves legacy dismissal and only explicitly reset reactivates', () => {
    localStorage.setItem('onboarding_dismissed', '1')
    activateOnboarding()
    expect(useOnboardingPreferences().continuing.value).toBe(false)
    resetOnboarding()
    expect(localStorage.getItem('onboarding_dismissed')).toBeNull()
    expect(useOnboardingPreferences().continuing.value).toBe(true)
  })
  it('selects a created project only while active', () => {
    dismissOnboarding(); selectCreatedOnboardingProject('a')
    expect(localStorage.getItem('onboarding_project_id')).toBeNull()
    resetOnboarding(); selectCreatedOnboardingProject('b')
    expect(localStorage.getItem('onboarding_project_id')).toBe('b')
  })
  it('survives getter access with failing persistence', () => {
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('quota') })
    dismissOnboarding(); rereadOnboardingPreferences(); activateOnboarding()
    expect(useOnboardingPreferences().preferences.dismissed).toBe(true)
    expect(shouldAutoOnboard(snapshot({ projectCount: 0, runCount: 0 }))).toBe(false)
  })
  it('survives denied storage access', () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => { throw new Error('denied') })
    expect(() => { resetOnboarding(); dismissOnboarding(); activateOnboarding() }).not.toThrow()
    expect(useOnboardingPreferences().continuing.value).toBe(false)
  })
  it.each(['real', 'legacy_unknown'] as const)('known %s completion survives historical purge', async mode => {
    get.mockResolvedValue(snapshot({ success: run('success', mode) }))
    start(); await flushPromises()
    expect(localStorage.getItem('onboarding_completed')).toBe('1')
    expect(useOnboardingPreferences().continuing.value).toBe(false)
    wrapper!.unmount(); wrapper = undefined
    get.mockResolvedValue(snapshot({ projectCount: 0, runCount: 0, projects: [], selectedProject: null }))
    expect(await onboardingLoginTarget(undefined)).toBe('/')
    expect(get).toHaveBeenCalledTimes(1)
    resetOnboarding()
    expect(localStorage.getItem('onboarding_completed')).toBeNull()
  })
  it('unknown success authority never writes completion', async () => {
    get.mockResolvedValue(snapshot({ successState: 'unknown', success: run() }))
    start(); await flushPromises()
    expect(localStorage.getItem('onboarding_completed')).toBeNull()
    expect(state.flow.value.kind).toBe('unknown')
  })
  it.each(['stub', 'mixed', 'pending'] as const)('%s evidence does not write completion', async mode => {
    get.mockResolvedValue(snapshot({ latestRun: run('success', mode) }))
    start(); await flushPromises()
    expect(localStorage.getItem('onboarding_completed')).toBeNull()
    expect(useOnboardingPreferences().continuing.value).toBe(true)
  })
})
describe('login entry', () => {
  it('explicit same-origin deep link wins without querying', async () => {
    expect(await onboardingLoginTarget('/runs/r?tab=logs')).toBe('/runs/r?tab=logs')
    expect(get).not.toHaveBeenCalled()
  })
  it.each(['https://external.example', '//external.example', '/\\external.example', ['/', '/runs']])('rejects unsafe redirect %s', async raw => {
    get.mockRejectedValue(new Error('unreachable'))
    expect(await onboardingLoginTarget(raw)).toBe('/')
  })
  it('only known zero projects and runs trigger automatic onboarding', async () => {
    const empty = snapshot({ projectCount: 0, runCount: 0, projects: [], selectedProject: null })
    get.mockResolvedValue(empty)
    expect(await onboardingLoginTarget(undefined)).toBe('/onboarding')
    for (const overrides of [{ runCount: 1 }, { projectCount: 1 }, { projectsState: 'unknown' as const }, { runsState: 'unknown' as const }, { successState: 'unknown' as const }, { runCount: null },
      { projects: [{ id: 'new', name: 'Concurrent creation' }] }, { selectedProject: { id: 'new', name: 'Concurrent creation', pacEnabled: false } }, { success: run() }]) {
      expect(shouldAutoOnboard(snapshot({ ...empty, ...overrides }))).toBe(false)
    }
    expect(shouldAutoOnboard(snapshot({ ...empty, pipeline: { state: 'unknown', savedAt: null, issues: [] }, runtime: 'unknown' }))).toBe(true)
    get.mockRejectedValue(new Error('SECRET_FAILURE'))
    expect(await onboardingLoginTarget(undefined)).toBe('/')
  })
})
describe('request and visibility lifecycle', () => {
  it('aborts old refresh and ignores its late response', async () => {
    const old = deferred(), fresh = deferred()
    get.mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise)
    start()
    const signal = get.mock.calls[0]![1]!
    const refresh = state.refresh()
    expect(signal.aborted).toBe(true)
    fresh.resolve(snapshot({ selectedProject: { id: 'b', name: 'Project B', pacEnabled: false } }))
    await refresh
    old.resolve(snapshot({ success: run() })); await flushPromises()
    expect(state.snapshot.value?.selectedProject?.id).toBe('b')
    expect(localStorage.getItem('onboarding_completed')).toBeNull()
  })
  it('persists only a server-returned valid selection and requests the new choice', async () => {
    get.mockResolvedValueOnce(snapshot())
    start(); await flushPromises()
    const pending = deferred()
    get.mockReturnValueOnce(pending.promise)
    state.select('b')
    expect(state.snapshot.value).toBeNull()
    expect(get.mock.calls.at(-1)?.[0]).toBe('b')
    pending.resolve(snapshot({ selectedProject: { id: 'b', name: 'Project B', pacEnabled: false }, latestRun: run('failed', 'real', 'b') }))
    await flushPromises()
    expect(localStorage.getItem('onboarding_project_id')).toBe('b')
    expect(state.flow.value.primary?.to).toBe('/runs/run-b')
  })
  it('late request after skip cannot restore preferences or links', async () => {
    const pending = deferred(); get.mockReturnValue(pending.promise)
    start(); dismissOnboarding()
    expect(get.mock.calls[0]![1]!.aborted).toBe(true)
    pending.resolve(snapshot({ success: run() })); await flushPromises()
    expect(state.snapshot.value).toBeNull()
    expect(localStorage.getItem('onboarding_completed')).toBeNull()
    expect(useOnboardingPreferences().continuing.value).toBe(false)
  })
  it('failure clears stale actions and exposes no raw error', async () => {
    get.mockResolvedValueOnce(snapshot())
    start(); await flushPromises()
    get.mockRejectedValueOnce(new Error('SECRET_LOG'))
    await state.refresh()
    expect(state.snapshot.value).toBeNull()
    expect(state.error.value).toBe(true)
    expect(state.flow.value.primary?.to).toBeUndefined()
  })
  it.each(['queued', 'running', 'waiting_approval'])('polls visible %s only, pauses hidden and cleans up on unmount', async status => {
    vi.useFakeTimers()
    get.mockResolvedValue(snapshot({ latestRun: run(status, 'pending') }))
    start(); await flushPromises()
    expect(get).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(5000)
    expect(get).toHaveBeenCalledTimes(2)
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(15000)
    expect(get).toHaveBeenCalledTimes(2)
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange')); await flushPromises()
    expect(get).toHaveBeenCalledTimes(3)
    window.dispatchEvent(new Event('focus')); await flushPromises()
    expect(get).toHaveBeenCalledTimes(4)
    wrapper!.unmount(); wrapper = undefined
    const signal = get.mock.calls.at(-1)![1]!
    expect(signal.aborted).toBe(true)
    window.dispatchEvent(new Event('focus')); document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(10000)
    expect(get).toHaveBeenCalledTimes(4)
  })
  it('does not poll terminal snapshots or revive completion when storage writes fail', async () => {
    vi.useFakeTimers()
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('quota') })
    get.mockResolvedValue(snapshot({ success: run() }))
    start(); await flushPromises(); rereadOnboardingPreferences()
    expect(useOnboardingPreferences().preferences.completed).toBe(true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(get).toHaveBeenCalledTimes(1)
  })
})

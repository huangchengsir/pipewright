import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, shallowRef } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { getOnboardingStatus, type Snapshot } from '../api/onboarding'
import { usePipelineGuide } from './usePipelineGuide'
import { snapshot } from './onboardingFixtures.test-helper'
import type { PipelineStage } from '../api/pipeline'
vi.mock('../api/onboarding', () => ({ getOnboardingStatus: vi.fn() }))
const get = vi.mocked(getOnboardingStatus)
let wrapper: VueWrapper | undefined
function start() {
  const projectId = shallowRef('a'), stages = shallowRef<PipelineStage[]>([]), dirty = shallowRef(false)
  const context = shallowRef({ selectedJobId: null as string | null, pickerOpen: false })
  let guide!: ReturnType<typeof usePipelineGuide>
  wrapper = mount(defineComponent({ setup() { guide = usePipelineGuide({ projectId, stages, dirty, context }); return () => null } }))
  return { guide, projectId, dirty }
}
beforeEach(() => get.mockReset())
afterEach(() => { wrapper?.unmount(); wrapper = undefined })
function deferred() { let resolve!: (v: Snapshot) => void; const promise = new Promise<Snapshot>(r => { resolve = r }); return { promise, resolve } }
describe('guide reads only after opt-in mounting and explicit refresh', () => {
  it('refreshes persisted readiness, not draft clicks', async () => {
    get.mockResolvedValue(snapshot())
    const { guide, dirty } = start(); await flushPromises()
    expect(guide.phase.value).toBe('ready')
    dirty.value = true; await flushPromises()
    expect(guide.phase.value).not.toBe('ready')
    expect(get).toHaveBeenCalledTimes(1)
    await guide.refresh()
    expect(get).toHaveBeenCalledTimes(2)
  })
  it('aborts a stale project request and rejects late evidence', async () => {
    const old = deferred(), fresh = deferred()
    get.mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise)
    const { guide, projectId } = start()
    const signal = get.mock.calls[0]![1]!
    projectId.value = 'b'; await flushPromises()
    expect(signal.aborted).toBe(true)
    fresh.resolve(snapshot({ selectedProject: { id: 'b', name: 'B', pacEnabled: false } })); await flushPromises()
    old.resolve(snapshot()); await flushPromises()
    expect(guide.snapshot.value?.selectedProject?.id).toBe('b')
  })
  it('failed refresh clears old ready status and unmount aborts', async () => {
    get.mockResolvedValueOnce(snapshot())
    const { guide } = start(); await flushPromises()
    get.mockRejectedValueOnce(new Error('SECRET'))
    await guide.refresh()
    expect(guide.phase.value).toBe('unknown')
    expect(guide.snapshot.value).toBeNull()
    const pending = deferred(); get.mockReturnValueOnce(pending.promise)
    const refresh = guide.refresh(), signal = get.mock.calls.at(-1)![1]!
    wrapper!.unmount(); wrapper = undefined
    expect(signal.aborted).toBe(true)
    pending.resolve(snapshot()); await refresh
    expect(guide.snapshot.value).toBeNull()
  })
})

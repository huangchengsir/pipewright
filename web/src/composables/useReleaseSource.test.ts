import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { HttpError } from '../api/http'
import { useReleaseSource } from './useReleaseSource'
const api = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }))
vi.mock('../api/version', () => ({ getReleaseSourceSettings: api.get, setReleaseSourceSettings: api.set }))
const config = (source: 'github' | 'gitee' = 'github', revision = 0, available = true) => ({ source, revision, sources: [
  { id: 'github', name: 'GitHub', repository: 'owner/product', available: true },
  { id: 'gitee', name: 'Gitee', repository: available ? 'owner/product' : '', available },
] })
const wrappers: VueWrapper[] = []
let state: ReturnType<typeof useReleaseSource>
function render() { const w = mount(defineComponent({ setup() { state = useReleaseSource(); return () => null } })); wrappers.push(w); return w }
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
beforeEach(() => { vi.resetAllMocks(); api.get.mockResolvedValue(config()) })
afterEach(() => wrappers.splice(0).forEach(w => w.unmount()))
describe('release source settings', () => {
  it('loads persisted settings without a write or an external release check', async () => {
    render(); await flushPromises(); expect(state.settings.value?.source).toBe('github'); expect(state.canCheck.value).toBe(true); expect(api.set).not.toHaveBeenCalled()
  })
  it('requires saving an available new source before checking', async () => {
    render(); await flushPromises(); state.draft.value = 'gitee'; expect(state.canCheck.value).toBe(false); expect(state.canSave.value).toBe(true)
    api.set.mockResolvedValue(config('gitee', 1)); expect(await state.save()).toBe(true)
    expect(api.set).toHaveBeenCalledWith({ source: 'gitee', expectedRevision: 0 }); expect(state.canCheck.value).toBe(true); expect(state.canSave.value).toBe(false)
  })
  it('refuses an unconfigured Gitee source without dispatching a write', async () => {
    api.get.mockResolvedValue(config('github', 0, false)); render(); await flushPromises(); state.draft.value = 'gitee'
    expect(state.canSave.value).toBe(false); expect(await state.save()).toBe(false); expect(api.set).not.toHaveBeenCalled()
  })
  it('requires readback on revision conflict and preserves the edited choice', async () => {
    render(); await flushPromises(); state.draft.value = 'gitee'; api.set.mockRejectedValue(new HttpError(409, null, 'conflict')); await state.save()
    expect(state.error.value).toBe('conflict'); expect(state.draft.value).toBe('gitee'); expect(state.settings.value?.source).toBe('github'); expect(state.canCheck.value).toBe(false)
    await state.save(); expect(api.set).toHaveBeenCalledTimes(1)
    api.get.mockResolvedValue(config('gitee', 2)); await state.load(); expect(state.canCheck.value).toBe(true); expect(state.settings.value?.revision).toBe(2)
  })
  it('does not repeat an uncertain save or allow stale checking', async () => {
    render(); await flushPromises(); state.draft.value = 'gitee'; api.set.mockRejectedValue(new Error('lost response')); await state.save(); await state.save()
    expect(state.error.value).toBe('save'); expect(state.needsReload.value).toBe(true); expect(state.canCheck.value).toBe(false); expect(api.set).toHaveBeenCalledTimes(1)
  })
  it('blocks checks if the initial settings read fails', async () => {
    api.get.mockRejectedValue(new Error('offline')); render(); await flushPromises(); expect(state.error.value).toBe('load'); expect(state.canCheck.value).toBe(false); expect(api.set).not.toHaveBeenCalled()
  })
  it('rejects malformed server settings instead of guessing a default', async () => {
    api.get.mockResolvedValue({ ...config(), source: 'custom' }); render(); await flushPromises(); expect(state.settings.value).toBeNull(); expect(state.error.value).toBe('load')
  })
  it('ignores a stale settings response and aborts its request', async () => {
    const old = deferred<ReturnType<typeof config>>(); api.get.mockReturnValueOnce(old.promise).mockResolvedValueOnce(config('gitee', 2)); render()
    const signal = api.get.mock.calls[0][0].signal; await state.load(); old.resolve(config('github', 1)); await flushPromises()
    expect(signal.aborted).toBe(true); expect(state.settings.value?.source).toBe('gitee')
  })
  it('ignores completion after unmount', async () => {
    const result = deferred<ReturnType<typeof config>>(); api.get.mockReturnValueOnce(result.promise); const w = render(); w.unmount(); result.resolve(config()); await flushPromises(); expect(state.settings.value).toBeNull()
  })
  it('never dispatches overlapping saves', async () => {
    render(); await flushPromises(); state.draft.value = 'gitee'; const result = deferred<ReturnType<typeof config>>(); api.set.mockReturnValueOnce(result.promise)
    const first = state.save(); expect(await state.save()).toBe(false); result.resolve(config('gitee', 1)); await first; expect(api.set).toHaveBeenCalledTimes(1)
  })
})

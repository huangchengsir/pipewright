import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import RunTerminal from './RunTerminal.vue'
const api = vi.hoisted(() => ({ logs: vi.fn(), subscribe: vi.fn() }))
vi.mock('../../api/runs', () => ({ getRunLogs: api.logs, subscribeRunEvents: api.subscribe }))
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
const page = (text: string) => ({ lines: [{ seq: 1, ts: '2026-10-28T00:00:00Z', stream: 'stdout', stepOrdinal: 0, text }], nextSeq: 1, complete: true })
describe('run log identity lifecycle', () => {
  beforeEach(() => { vi.clearAllMocks(); api.logs.mockResolvedValue(page('current log')); api.subscribe.mockReturnValue(vi.fn()) })
  it('ignores a previous run’s late historical page', async () => {
    const old = deferred<ReturnType<typeof page>>(); api.logs.mockReturnValueOnce(old.promise)
    const wrapper = mount(RunTerminal, { props: { runId: 'old', live: false } }); await wrapper.setProps({ runId: 'new' }); await flushPromises()
    old.resolve(page('wrong old evidence')); await flushPromises()
    expect(wrapper.text()).toContain('current log'); expect(wrapper.text()).not.toContain('wrong old evidence'); wrapper.unmount()
  })
  it('disposes the old subscription and ignores late callback delivery', async () => {
    const stop = vi.fn(); api.subscribe.mockReturnValue(stop)
    const wrapper = mount(RunTerminal, { props: { runId: 'old', live: true } }); await flushPromises()
    const oldHandlers = api.subscribe.mock.calls[0][1]
    await wrapper.setProps({ runId: 'new' }); await flushPromises(); expect(stop).toHaveBeenCalled()
    oldHandlers.onLog({ seq: 99, ts: '', stream: 'stdout', stepOrdinal: 0, text: 'late old SSE' }); await flushPromises()
    expect(wrapper.text()).not.toContain('late old SSE'); wrapper.unmount(); expect(stop).toHaveBeenCalledTimes(2)
  })
})

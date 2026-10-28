import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { useRunRetry } from './useRunRetry'
import { HttpError } from '../api/http'
import { retryContext, retryPlan, retryRequest } from '../test/runRetryFixtures'
const api = vi.hoisted(() => ({ context: vi.fn(), attempts: vi.fn(), plan: vi.fn(), request: vi.fn(), submit: vi.fn(), reconcile: vi.fn() }))
vi.mock('../api/runRetry', async original => ({ ...(await original<typeof import('../api/runRetry')>()), getRetryContext: api.context, getRetryAttempts: api.attempts, getRetryPlan: api.plan, getRetryRequest: api.request, submitRetry: api.submit, reconcileRetry: api.reconcile }))
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { resolve, promise } }
let state: ReturnType<typeof useRunRetry>
let wrappers: VueWrapper[] = []
function render(id = ref('root')) { const wrapper = mount(defineComponent({ setup() { state = useRunRetry(id); return () => h('div') } })); wrappers.push(wrapper); return { wrapper, state, id } }
describe('durable explicit node retry', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); localStorage.clear(); sessionStorage.clear(); api.context.mockResolvedValue(retryContext()); api.attempts.mockResolvedValue({ page: 1, total: 0, items: [] }); api.plan.mockResolvedValue(retryPlan()); api.request.mockRejectedValue(new HttpError(404, { code: 'retry_not_found', message: 'missing' }, 'missing')); api.submit.mockResolvedValue(retryRequest()); api.reconcile.mockResolvedValue({ drained: true, checked: 1, reasons: [] }) })
  afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.useRealTimers() })
  it('previews without submitting and only sends explicitly selected prerequisite rebuilds', async () => {
    render(); await flushPromises(); await state.openPlan(['b'])
    expect(api.plan).toHaveBeenLastCalledWith('root', ['b'], [], expect.any(Object)); expect(api.submit).not.toHaveBeenCalled()
    await state.preview(undefined, ['a'])
    expect(api.plan).toHaveBeenLastCalledWith('root', ['b'], ['a'], expect.any(Object))
  })
  it('persists a fixed ID before dispatch and blocks a concurrent second submission', async () => {
    const response = deferred<ReturnType<typeof retryRequest>>(); api.submit.mockImplementationOnce(() => { expect(JSON.parse(sessionStorage.getItem('pipewright.retry.pending:root')!).requestId).toMatch(/^[a-f0-9]{32}$/); return response.promise })
    render(); await flushPromises(); await state.openPlan(['b'])
    const first = state.submit(); await state.submit(); expect(api.submit).toHaveBeenCalledTimes(1)
    response.resolve(retryRequest()); await first
    expect(state.request.value?.attemptRunId).toBe('child')
  })
  it.each([new HttpError(0, null, 'lost'), new HttpError(503, { code: 'retry_unavailable', message: 'unknown' }, 'unknown')])('queries a lost outcome without replaying POST: %s', async error => {
    api.submit.mockRejectedValueOnce(error); render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    const original = state.pending.value?.requestId
    expect(original).toBeTruthy(); expect(state.error.value).toBe('retry_request_unconfirmed')
    await vi.advanceTimersByTimeAsync(9000); await state.submit()
    expect(api.submit).toHaveBeenCalledTimes(1); expect(state.pending.value?.requestId).toBe(original)
    expect(api.request.mock.calls.every(call => call[1] === original)).toBe(true)
  })
  it('restores an in-flight ID after reload and ignores the old response', async () => {
    const response = deferred<ReturnType<typeof retryRequest>>(); api.submit.mockReturnValueOnce(response.promise)
    const first = render(); await flushPromises(); await state.openPlan(['b']); const inFlight = state.submit()
    const saved = JSON.parse(sessionStorage.getItem('pipewright.retry.pending:root')!); first.wrapper.unmount()
    api.request.mockResolvedValue(retryRequest({ requestId: saved.requestId }))
    render(); await flushPromises(); response.resolve(retryRequest({ attemptRunId: 'late-wrong-child' })); await inFlight
    expect(state.request.value?.attemptRunId).toBe('child'); expect(api.submit).toHaveBeenCalledTimes(1)
    expect(api.request).toHaveBeenCalledWith('root', saved.requestId, expect.any(Object))
  })
  it('retires a definite conflict, refreshes head and never silently resubmits', async () => {
    api.submit.mockRejectedValueOnce(new HttpError(409, { code: 'retry_conflict', message: 'changed' }, 'changed'))
    render(); await flushPromises(); await state.openPlan(['b']); api.context.mockResolvedValue(retryContext({ headAttemptId: 'other-tab' })); await state.submit(); await flushPromises()
    expect(state.pending.value).toBeNull(); expect(state.plan.value).toBeNull(); expect(state.isHead.value).toBe(false)
    expect(state.error.value).toBe('retry_conflict'); expect(api.submit).toHaveBeenCalledTimes(1)
  })
  it('queries the original ledger before retiring a canonical refusal, and follows a record if it exists', async () => {
    api.submit.mockRejectedValueOnce(new HttpError(409, { code: 'retry_conflict', message: 'changed' }, 'changed')); api.request.mockResolvedValue(retryRequest())
    render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    expect(api.request).toHaveBeenCalledTimes(1); expect(state.pending.value).not.toBeNull(); expect(state.request.value?.attemptRunId).toBe('child')
    await state.submit(); expect(api.submit).toHaveBeenCalledTimes(1)
  })
  it('allows a new explicit plan on the latest attempt after a rejected stale request has no ledger', async () => {
    api.submit.mockRejectedValueOnce(new HttpError(409, { code: 'retry_conflict', message: 'stale' }, 'stale'))
    const mounted = render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    const rejected = api.submit.mock.calls[0][1].requestId
    expect(state.pending.value).toBeNull(); expect(api.submit).toHaveBeenCalledTimes(1)
    api.context.mockResolvedValue(retryContext({ attemptRunId: 'latest', headAttemptId: 'latest', revision: 2 })); mounted.id.value = 'latest'; await flushPromises()
    api.plan.mockResolvedValue(retryPlan({ baseAttemptId: 'latest', baseRevision: 2 })); await state.openPlan(['b'])
    expect(api.submit).toHaveBeenCalledTimes(1)
    await state.submit(); expect(api.submit).toHaveBeenCalledTimes(2); expect(api.submit.mock.calls[1][1].requestId).not.toBe(rejected)
    expect(api.submit.mock.calls[1][0]).toBe('latest')
  })
  it.each([new HttpError(403, null, 'proxy HTML'), new HttpError(404, null, 'proxy HTML'), new HttpError(408, { code: 'proxy_timeout', message: 'proxy' }, 'proxy')])('does not treat a noncanonical proxy refusal as proof: %s', async error => {
    api.submit.mockRejectedValueOnce(error); render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    expect(state.pending.value).not.toBeNull(); expect(state.pending.value?.rejectionCode).toBeUndefined(); expect(api.submit).toHaveBeenCalledTimes(1)
  })
  it('requires a canonical original-request not_found even after a canonical refusal', async () => {
    api.submit.mockRejectedValueOnce(new HttpError(403, { code: 'csrf_invalid', message: 'refused' }, 'refused')); api.request.mockRejectedValue(new HttpError(404, null, 'HTML missing'))
    render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    expect(state.pending.value?.rejectionCode).toBe('csrf_invalid')
    api.request.mockRejectedValue(new HttpError(404, { code: 'retry_not_found', message: 'missing' }, 'missing')); await state.queryOriginal()
    expect(state.pending.value).toBeNull(); expect(api.submit).toHaveBeenCalledTimes(1)
  })
  it('records a late canonical refusal for its own request without mutating the new route', async () => {
    let reject!: (e: unknown) => void
    api.submit.mockReturnValueOnce(new Promise<ReturnType<typeof retryRequest>>((_, r) => { reject = r }))
    const mounted = render(); await flushPromises(); await state.openPlan(['b']); const inFlight = state.submit()
    mounted.id.value = 'child'; await flushPromises(); reject(new HttpError(409, { code: 'retry_conflict', message: 'refused' }, 'refused')); await inFlight
    expect(state.error.value).toBe(''); expect(JSON.parse(sessionStorage.getItem('pipewright.retry.pending:root')!).rejectionCode).toBe('retry_conflict')
    mounted.id.value = 'root'; await flushPromises(); expect(state.pending.value).toBeNull(); expect(api.submit).toHaveBeenCalledTimes(1)
  })
  it('does not dispatch when durable browser storage fails', async () => {
    const storage = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota') })
    render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    expect(api.submit).not.toHaveBeenCalled(); expect(state.error.value).toBe('retry_storage_unavailable'); storage.mockRestore()
  })
  it('does not replace an unresolved request when another tab changes browser storage', async () => {
    api.submit.mockRejectedValue(new HttpError(0, null, 'lost')); const first = render(); await flushPromises(); await state.openPlan(['b']); await state.submit()
    const original = state.pending.value?.requestId
    localStorage.setItem('pipewright.retry.pending:root', JSON.stringify({ runId: 'root', requestId: 'other-tab' })); window.dispatchEvent(new StorageEvent('storage', { key: 'pipewright.retry.pending:root', newValue: null })); await flushPromises()
    expect(state.pending.value?.requestId).toBe(original); await state.submit(); expect(api.submit).toHaveBeenCalledTimes(1)
    first.wrapper.unmount(); render(); await flushPromises(); expect(state.pending.value?.requestId).toBe(original)
  })
  it('discards late context and plan responses after a route change and clears polling on unmount', async () => {
    const oldContext = deferred<ReturnType<typeof retryContext>>(); api.context.mockReturnValueOnce(oldContext.promise)
    const oldPlan = deferred<ReturnType<typeof retryPlan>>(); api.plan.mockReturnValueOnce(oldPlan.promise)
    const mounted = render(); const planRead = state.openPlan(['b'])
    api.context.mockResolvedValue(retryContext({ attemptRunId: 'child', headAttemptId: 'child', revision: 2 })); mounted.id.value = 'child'; await flushPromises()
    oldContext.resolve(retryContext()); oldPlan.resolve(retryPlan()); await planRead; await flushPromises()
    expect(state.context.value?.attemptRunId).toBe('child'); expect(state.plan.value).toBeNull(); expect(state.selected.value).toEqual([])
    mounted.wrapper.unmount(); const calls = api.context.mock.calls.length; await vi.advanceTimersByTimeAsync(12000); expect(api.context).toHaveBeenCalledTimes(calls)
  })
})

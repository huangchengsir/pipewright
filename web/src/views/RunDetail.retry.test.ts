import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import RunDetail from './RunDetail.vue'
import type { RunDetail as RunDTO, RunStatus } from '../api/runs'
import { retryContext, retryPlan } from '../test/runRetryFixtures'
import { HttpError } from '../api/http'
const api = vi.hoisted(() => ({ get: vi.fn(), subscribe: vi.fn(), approvals: vi.fn(), context: vi.fn(), attempts: vi.fn(), plan: vi.fn(), request: vi.fn() }))
vi.mock('../api/runs', async original => ({ ...(await original<typeof import('../api/runs')>()), getRun: api.get, subscribeRunEvents: api.subscribe, listApprovals: api.approvals }))
vi.mock('../api/runRetry', async original => ({ ...(await original<typeof import('../api/runRetry')>()), getRetryContext: api.context, getRetryAttempts: api.attempts, getRetryPlan: api.plan, getRetryRequest: api.request }))
const Dag = defineComponent({ props: ['projectId', 'steps', 'runStatus', 'recovery'], emits: ['retry', 'logs'], template: '<div><button class="dag-retry" @click="$emit(\'retry\', [\'b\'])">Retry {{runStatus}}</button><button class="dag-logs" @click="$emit(\'logs\', [\'a\', \'b\'], \'Original build\')">Logs</button></div>' })
const Terminal = defineComponent({ props: ['runId', 'live', 'filterOrdinal'], template: '<div class="test-terminal">{{runId}}/{{filterOrdinal}}</div>' })
const stubs = { Teleport: true, RunDagView: Dag, RunTerminal: Terminal, RunStepList: true, DiagnosisPanel: true, SuccessFailDiff: true, ArtifactList: true, TestReportPanel: true, DeployTargets: true, PromotionPanel: true, BatchDeployPanel: true, ReleaseGuardPanel: true, ReleaseGuardAttach: true, LocalDockerDeploymentFields: true, AppSelect: true }
const stops: ReturnType<typeof vi.fn>[] = []
let wrappers: VueWrapper[] = []
function run(id: string, status: RunStatus = 'running'): RunDTO {
  return { id, projectId: 'project', projectName: `${id} project`, status, trigger: { type: 'manual', branch: 'main', commit: 'a'.repeat(40), actor: 'operator' }, steps: [{ id: `step-${id}`, name: `${id} task`, status: 'running', startedAt: null, finishedAt: null, durationMs: null }], createdAt: '2026-10-28T00:00:00Z', startedAt: null, finishedAt: null, durationMs: null, specSource: 'stored', specSourceRef: '', specSourceFile: '', specSourceFallback: '', artifacts: [], targets: null, diagnosis: null }
}
async function render(path = '/runs/root') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/runs/:id', name: 'run-detail', component: RunDetail }, { path: '/runs', name: 'runs', component: { template: '<div>Runs</div>' } }] })
  await router.push(path); await router.isReady()
  const wrapper = mount(RouterView, { global: { plugins: [router], stubs } }); wrappers.push(wrapper); await flushPromises(); return { wrapper, router }
}
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { resolve, promise } }
describe('existing RunDetail recovery integration', () => {
  it('opens node logs in the existing panel without requesting a retry plan', async () => {
    const original = run('root', 'failed'); original.steps = ['a', 'b'].map(id => ({ id: `step-${id}`, name: id, status: 'failed', startedAt: null, finishedAt: null, durationMs: null }))
    api.get.mockResolvedValue(original); const { wrapper, router } = await render()
    await wrapper.findComponent(Dag).get('.dag-logs').trigger('click'); await flushPromises()
    expect(wrapper.get('[role=dialog] h2').text()).toBe('Original build'); expect(wrapper.findAll('[role=tab]')).toHaveLength(2)
    expect(wrapper.findAllComponents(Terminal).some(t => t.props('runId') === 'root' && t.props('filterOrdinal') === 1)).toBe(true)
    expect(api.plan).not.toHaveBeenCalled()
    await router.push('/runs/child'); await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
  })

  beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); localStorage.clear(); sessionStorage.clear(); stops.length = 0; api.get.mockImplementation(async (id: string) => run(id)); api.subscribe.mockImplementation(() => { const stop = vi.fn(); stops.push(stop); return stop }); api.approvals.mockResolvedValue({ items: [] }); api.context.mockImplementation(async (id: string) => retryContext({ attemptRunId: id, headAttemptId: id })); api.attempts.mockResolvedValue({ items: [], total: 0, page: 1 }); api.plan.mockResolvedValue(retryPlan()) })
  afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.useRealTimers() })
  it.each<RunStatus>(['running', 'queued', 'success', 'failed', 'partial_failed', 'rolled_back', 'waiting_approval'])('exposes a frozen node plan in the %s branch', async status => {
    api.get.mockResolvedValue(run('root', status)); const { wrapper } = await render()
    const dag = wrapper.findComponent(Dag); expect(dag.exists()).toBe(true); expect(dag.props('recovery').attemptRunId).toBe('root')
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
    await dag.get('.dag-retry').trigger('click'); await flushPromises(); expect(api.plan).toHaveBeenCalledWith('root', ['b'], [], expect.any(Object))
    expect(wrapper.findAll('.retry-panel')).toHaveLength(1)
  })
  it('closes node details on a reused route and keeps history behind the metadata entry', async () => {
    const { wrapper, router } = await render()
    expect(wrapper.find('.retry-panel').exists()).toBe(false)
    await wrapper.get('.retry-history-entry').trigger('click'); await flushPromises()
    expect(wrapper.get('[role=dialog] h2').text()).toBe('运行尝试历史'); expect(api.plan).not.toHaveBeenCalled()
    await router.push('/runs/child'); await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
    await wrapper.findComponent(Dag).get('.dag-retry').trigger('click'); await flushPromises()
    expect(wrapper.get('[role=dialog] h2').text()).toBe('b')
  })
  it('reuses the route component while rejecting an old detail response', async () => {
    const old = deferred<RunDTO>(); api.get.mockImplementation((id: string) => id === 'root' ? old.promise : Promise.resolve(run(id)))
    const { wrapper, router } = await render(); const originalComponent = wrapper.findComponent(RunDetail).vm.$.uid
    await router.push('/runs/child'); await flushPromises(); expect(wrapper.findComponent(RunDetail).vm.$.uid).toBe(originalComponent)
    old.resolve(run('root', 'failed')); await flushPromises()
    expect(wrapper.findComponent(Dag).props('steps')[0].name).toBe('child task')
    expect(wrapper.findComponent(Dag).props('recovery').attemptRunId).toBe('child')
  })
  it('stops old SSE and polling, ignores late old callbacks and releases the new subscription', async () => {
    const { wrapper, router } = await render(); const oldHandlers = api.subscribe.mock.calls[0][1]
    const lateRefresh = deferred<RunDTO>(); api.get.mockImplementation((id: string) => id === 'root' ? lateRefresh.promise : Promise.resolve(run(id)))
    await vi.advanceTimersByTimeAsync(4100); await router.push('/runs/child'); await flushPromises()
    expect(stops[0]).toHaveBeenCalledTimes(1)
    oldHandlers.onStatus({ runId: 'root', status: 'failed' }); oldHandlers.onStep({ runId: 'root', step: { id: 'step-child', name: 'wrong old SSE' } })
    lateRefresh.resolve(run('root', 'failed')); await flushPromises()
    expect(wrapper.findComponent(Dag).props('runStatus')).toBe('running'); expect(wrapper.findComponent(Dag).props('steps')[0].name).toBe('child task')
    wrapper.unmount(); expect(stops[1]).toHaveBeenCalledTimes(1)
    const count = api.get.mock.calls.length; await vi.advanceTimersByTimeAsync(9000); expect(api.get).toHaveBeenCalledTimes(count)
  })
  it('only selects source logs when the actual producer ordinal and row ID both match', async () => {
    const original = run('root', 'failed'); original.steps = Array.from({ length: 10 }, (_, i) => ({ id: `source-${i}`, name: `Source ${i}`, status: 'failed', startedAt: null, finishedAt: null, durationMs: null }))
    api.get.mockResolvedValue(original); const { wrapper, router } = await render('/runs/root?step=7&stepId=source-7')
    expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBe(7)
    await router.push('/runs/root?step=9&stepId=source-9'); await flushPromises(); expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBe(9)
    await router.push('/runs/root?step=7&stepId=wrong-row'); await flushPromises(); expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBeNull()
  })
  it('clears a locally selected step when opening complete logs for the same run', async () => {
    const original = run('root', 'success'); original.steps = ['a', 'b'].map(id => ({ id: `step-${id}`, name: id, status: 'success', startedAt: null, finishedAt: null, durationMs: null }))
    api.get.mockResolvedValue(original); const { wrapper, router } = await render()
    wrapper.findComponent({ name: 'RunStepList' }).vm.$emit('select', 0); await flushPromises()
    expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBe(0); expect(router.currentRoute.value.query).toEqual({})
    await wrapper.findComponent(Dag).get('.dag-logs').trigger('click'); await flushPromises()
    await wrapper.get('.retry-log-step > a').trigger('click'); await flushPromises()
    expect(wrapper.find('[role=dialog]').exists()).toBe(false)
    expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBeNull(); expect(api.plan).not.toHaveBeenCalled()
  })
  it('keeps the normal run readable when recovery metadata is unavailable', async () => {
    api.context.mockRejectedValue(new Error('unavailable private evidence')); api.attempts.mockRejectedValue(new Error('unavailable'))
    const { wrapper } = await render(); expect(wrapper.findComponent(Dag).props('steps')[0].name).toBe('root task'); expect(wrapper.text()).not.toContain('unavailable private evidence')
  })
  it('keeps original-request lookup and the actual child link visible when the source detail is retired', async () => {
    sessionStorage.setItem('pipewright.retry.pending:root', JSON.stringify({ runId: 'root', requestId: 'retained-request' }))
    api.get.mockRejectedValue(new HttpError(404, { code: 'run_not_found', message: 'retired' }, 'retired'))
    api.context.mockRejectedValue(new HttpError(404, { code: 'run_not_found', message: 'retired' }, 'retired'))
    api.request.mockResolvedValue({ requestId: 'retained-request', logicalRunId: 'root', sourceAttemptId: 'root', attemptRunId: 'retained-child', state: 'accepted', createdAt: '' })
    const { wrapper } = await render()
    expect(wrapper.find('.error-state').exists()).toBe(true); expect(wrapper.find('.retry-request').exists()).toBe(true)
    expect(wrapper.get('.retry-request a').attributes('href')).toBe('/runs/retained-child')
    expect(api.request).toHaveBeenCalledWith('root', 'retained-request', expect.any(Object))
  })
})

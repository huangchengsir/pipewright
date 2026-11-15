import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
beforeEach(()=>{setActivePinia(createPinia());useSessionStore().user={username:'admin',multiTenant:false}})
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import RunDetail from './RunDetail.vue'
import type { RunDetail as RunDTO, RunStatus } from '../api/runs'
import { retryContext, retryPlan } from '../test/runRetryFixtures'
import { http } from '../api/http'
import { listServers } from '../api/servers'
const api = vi.hoisted(() => ({ get: vi.fn(), subscribe: vi.fn(), approvals: vi.fn(), context: vi.fn(), attempts: vi.fn(), plan: vi.fn(), request: vi.fn(), approve:vi.fn(),reject:vi.fn() }))
vi.mock('../api/runs', async original => ({ ...(await original<typeof import('../api/runs')>()), getRun: api.get, subscribeRunEvents: api.subscribe, listApprovals: api.approvals,approveStage:api.approve,rejectStage:api.reject }))
vi.mock('../api/runRetry', async original => ({ ...(await original<typeof import('../api/runRetry')>()), getRetryContext: api.context, getRetryAttempts: api.attempts, getRetryPlan: api.plan, getRetryRequest: api.request }))
const Dag = defineComponent({ props: ['projectId', 'steps', 'runStatus', 'recovery'], emits: ['retry', 'logs'], template: '<div><button class="dag-retry" @click="$emit(\'retry\', [\'b\'])">Retry {{runStatus}}</button><button class="dag-logs" @click="$emit(\'logs\', [\'a\', \'b\'], \'Original build\')">Logs</button></div>' })
const Terminal = defineComponent({ props: ['runId', 'live', 'filterOrdinal'], template: '<div class="test-terminal">{{runId}}/{{filterOrdinal}}</div>' })
const stubs = { Teleport: true, RunDagView: Dag, RunTerminal: Terminal, RunStepList: true, DiagnosisPanel: true, SuccessFailDiff: true, ArtifactList: true, TestReportPanel: true, DeployTargets: true, PromotionPanel: true, BatchDeployPanel: true, ReleaseGuardPanel: true, ReleaseGuardAttach: true, LocalDockerDeploymentFields: true, AppSelect: true }
let wrappers: VueWrapper[] = []
function run(id: string, status: RunStatus = 'running'): RunDTO {
  return { id, projectId: 'project', projectName: `${id} project`, status, trigger: { type: 'manual', branch: 'main', commit: 'a'.repeat(40), actor: 'operator' }, steps: [{ id: `step-${id}`, name: `${id} task`, status: 'running', startedAt: null, finishedAt: null, durationMs: null }], createdAt: '2026-10-28T00:00:00Z', startedAt: null, finishedAt: null, durationMs: null, specSource: 'stored', specSourceRef: '', specSourceFile: '', specSourceFallback: '', artifacts: [], targets: null, diagnosis: null }
}
async function render(path = '/runs/root') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/runs/:id', name: 'run-detail', component: RunDetail }, { path: '/runs', name: 'runs', component: { template: '<div>Runs</div>' } }] })
  await router.push(path); await router.isReady()
  const wrapper = mount(RouterView, { global: { plugins: [router], stubs } }); wrappers.push(wrapper); await flushPromises(); return { wrapper, router }
}

vi.mock('../api/servers',()=>({listServers:vi.fn(async()=>[])}))

let granted:Set<string>
const actions=[{selector:'.btn-danger-ghost',action:'run.cancel',endpoint:'/cancel',status:'running'},{selector:'.batch-btn--go',action:'deployment.execute',endpoint:'/deploy/continue',status:'success'},{selector:'.batch-btn--abort',action:'deployment.execute',endpoint:'/deploy/abort',status:'success'}] as const
async function settle(){await vi.advanceTimersByTimeAsync(50);await flushPromises()}
function fixture(status:RunStatus):RunDTO{return {...run('root',status),accessEnvironmentId:'env-a',artifacts:[{id:'artifact-a',type:'archive',name:'a',reference:'a',sizeBytes:1,metadata:{stored:true},createdAt:'2026-01-01T00:00:00Z'}],targets:[{serverId:'server-a',serverName:'A',status:'pending',message:'',startedAt:'2026-01-01T00:00:00Z',finishedAt:null}]} }
beforeEach(()=>{
 vi.useFakeTimers();vi.clearAllMocks();localStorage.clear();sessionStorage.clear()
 useSessionStore().user={username:'employee',userId:'u',multiTenant:true};usePermissionStore().activeTeam='team-a'
 granted=new Set(['run.read','run.cancel','deployment.execute'])
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>granted.has(q.action))}))
 api.subscribe.mockReturnValue(vi.fn());api.approvals.mockResolvedValue({items:[]});api.context.mockImplementation(async(id:string)=>retryContext({attemptRunId:id,headAttemptId:id}));api.attempts.mockResolvedValue({items:[],total:0,page:1});api.plan.mockResolvedValue(retryPlan())
 vi.spyOn(http,'post').mockImplementation(async()=>({...fixture('failed'),targets:[]}))
})
afterEach(()=>{wrappers.forEach(w=>w.unmount());wrappers=[];vi.restoreAllMocks();vi.useRealTimers()})
it.each(actions)('click $endpoint keeps exact run and team context',async c=>{
 api.get.mockResolvedValue(fixture(c.status));const {wrapper}=await render();await settle()
 await wrapper.get(c.selector).trigger('click');await flushPromises()
 expect(http.post).toHaveBeenCalledOnce();const [url,body,options]=vi.mocked(http.post).mock.calls[0]!
 expect(url).toBe('/api/runs/root'+c.endpoint);expect(new Headers(options?.headers).get('X-Pipewright-Team')).toBe('team-a');expect(options?.signal).toBeInstanceOf(AbortSignal)
 if(c.endpoint==='/deploy/continue')expect(body).toMatchObject({artifactId:'artifact-a'})
 const queries=vi.mocked(access.decisions).mock.calls.flatMap(call=>call[1]);expect(queries).toContainEqual(expect.objectContaining({action:c.action,resourceId:c.action==='run.cancel'?'root':'env-a'}))
})
it.each(actions)('denies $endpoint and removes a previously rendered action after revocation',async c=>{
 api.get.mockResolvedValue(fixture(c.status));const {wrapper}=await render();await settle();const old=wrapper.get(c.selector)
 granted.delete(c.action);usePermissionStore().invalidate();await settle();expect(wrapper.find(c.selector).exists()).toBe(false)
 await old.trigger('click');await flushPromises();expect(http.post).not.toHaveBeenCalled()
})
it.each(actions)('old $endpoint button cannot write after team switch',async c=>{
 api.get.mockResolvedValue(fixture(c.status));const {wrapper}=await render();await settle();const old=wrapper.get(c.selector)
 usePermissionStore().setTeam('team-b');await flushPromises();await old.trigger('click');await flushPromises();expect(http.post).not.toHaveBeenCalled()
})

const approvalActions = [
 { selector: '.approval-btn--approve', method: 'approve' },
 { selector: '.approval-btn--reject', method: 'reject' },
] as const
async function approvalFixture() {
 granted.add('release.approve')
 api.get.mockResolvedValue(fixture('waiting_approval'))
 api.approvals.mockResolvedValue({items:[{stageId:'release-a',stageName:'Release A',status:'pending',accessAction:'release.approve',accessEnvironmentIds:['env-a']}]})
 api.approve.mockResolvedValue(undefined); api.reject.mockResolvedValue(undefined)
 const result=await render(); await settle(); return result
}
it.each(approvalActions)('clicks $method approval with the captured run and team',async ({selector,method})=>{
 const {wrapper}=await approvalFixture()
 await wrapper.get(selector).trigger('click'); await flushPromises()
 expect(api[method]).toHaveBeenCalledOnce()
 const [id,stage,opts]=api[method].mock.calls[0]!
 expect(id).toBe('root'); expect(stage).toBe('release-a')
 expect(new Headers(opts.headers).get('X-Pipewright-Team')).toBe('team-a')
 expect(opts.signal).toBeInstanceOf(AbortSignal)
 expect(api[method==='approve'?'reject':'approve']).not.toHaveBeenCalled()
})
it.each(approvalActions)('blocks retained $method approval after revocation',async ({selector})=>{
 const {wrapper}=await approvalFixture(); const old=wrapper.get(selector)
 granted.delete('release.approve'); usePermissionStore().invalidate(); await settle()
 expect(wrapper.find(selector).exists()).toBe(false)
 await old.trigger('click'); await flushPromises()
 expect(api.approve).not.toHaveBeenCalled(); expect(api.reject).not.toHaveBeenCalled()
})
it.each(approvalActions)('blocks retained $method approval after team switch',async ({selector})=>{
 const {wrapper}=await approvalFixture(); const old=wrapper.get(selector)
 usePermissionStore().setTeam('team-b'); await flushPromises()
 await old.trigger('click'); await flushPromises()
 expect(api.approve).not.toHaveBeenCalled(); expect(api.reject).not.toHaveBeenCalled()
})

function failedRuns() {
  api.get.mockImplementation(async (id: string) => ({ ...fixture('failed'), id }))
  api.context.mockImplementation(async (id: string) => {
    const context = retryContext({ attemptRunId: id, headAttemptId: id })
    context.nodes[2]!.decision.status = 'failed'
    return context
  })
}

it('has no bulk retry or retry plan request for a readable run without run.retry', async () => {
  failedRuns()
  const { wrapper } = await render()
  await settle()
  expect(wrapper.find('.retry-history-entry').exists()).toBe(true)
  const retry = wrapper.find('.retry-all-entry')
  if (retry.exists()) await retry.trigger('click')
  await settle()
  expect(api.plan).not.toHaveBeenCalled()
  expect(retry.exists()).toBe(false)
})

it('opens bulk retry for the exact run and removes old controls when its permission is revoked', async () => {
  failedRuns()
  granted.add('run.retry')
  const { wrapper } = await render()
  await settle()
  const retry = wrapper.get('.retry-all-entry')
  await retry.trigger('click')
  await settle()
  expect(api.plan).toHaveBeenCalledExactlyOnceWith('root', ['b', 'c'], [], expect.objectContaining({
    headers: expect.objectContaining({ 'X-Pipewright-Team': 'team-a' }),
  }))
  granted.delete('run.retry')
  usePermissionStore().invalidate()
  await settle()
  await retry.trigger('click')
  await settle()
  expect(wrapper.find('.retry-all-entry').exists()).toBe(false)
  expect(api.plan).toHaveBeenCalledTimes(1)
  expect(wrapper.find('.retry-history-entry').exists()).toBe(true)
})

it('keeps the same-environment sibling run readable while its cancel action is denied', async () => {
  api.get.mockImplementation(async (id: string) => ({ ...fixture('running'), id, projectName: id + ' project' }))
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    userId: 'u', teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(query => granted.has(query.action) &&
      !(query.action === 'run.cancel' && query.resourceId === 'sibling')),
  }))
  const { wrapper, router } = await render()
  await settle()
  const oldCancel = wrapper.get('.btn-danger-ghost')
  await router.push('/runs/sibling')
  await settle()
  expect(wrapper.text()).toContain('sibling project')
  expect(wrapper.find('.btn-danger-ghost').exists()).toBe(false)
  await oldCancel.trigger('click')
  await settle()
  expect(http.post).not.toHaveBeenCalled()
})

it('keeps read-only source logs, clears only source-step filters with the actual button, and blocks other run reads', async () => {
  api.get.mockImplementation(async (id: string) => ({ ...fixture('failed'), id }))
  vi.mocked(access.decisions).mockImplementation(async (team, queries) => ({
    userId: 'u', teamId: team, modeEpoch: 1, revision: 1, catalogVersion: 1,
    allowed: queries.map(query => query.action === 'run.read' && query.resourceId === 'root'),
  }))
  const { wrapper, router } = await render('/runs/root?step=0&stepId=step-root&returnTo=%2Fruns%3Fstatus%3Dfailed')
  await settle()
  expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBe(0)
  await wrapper.get('.source-log-selection button').trigger('click')
  await settle()
  expect(router.currentRoute.value.query).toEqual({ returnTo: '/runs?status=failed' })
  expect(wrapper.findComponent(Terminal).props('filterOrdinal')).toBeNull()
  expect(api.plan).not.toHaveBeenCalled()

  await router.push('/runs/private')
  await settle()
  expect(api.get.mock.calls.every(([id]) => id === 'root')).toBe(true)
  expect(wrapper.findComponent(Terminal).exists()).toBe(false)
})

it('uses the load error retry button only for the current readable run', async () => {
  api.get.mockRejectedValueOnce(new Error('temporary load')).mockResolvedValue(fixture('failed'))
  const { wrapper } = await render()
  await settle()
  await wrapper.get('.error-state button').trigger('click')
  await settle()
  expect(api.get).toHaveBeenCalledTimes(2)
  expect(api.get).toHaveBeenLastCalledWith('root', expect.objectContaining({
    headers: expect.objectContaining({ 'X-Pipewright-Team': 'team-a' }),
  }))
  expect(wrapper.find('.error-state').exists()).toBe(false)
})

const deployServer = {
  id: 'server-selected', name: 'Selected test server', host: 'test.example', port: 22,
  user: 'deploy', credentialId: 'fixture-key', credentialName: 'Fixture key',
  createdAt: '2026-01-01', updatedAt: '2026-01-01',
}

it('opens, configures, closes and submits only the exact authorized deployment', async () => {
  api.get.mockResolvedValue(fixture('success'))
  vi.mocked(listServers).mockResolvedValueOnce([deployServer])
  const { wrapper } = await render()
  await settle()
  await wrapper.get('.deploy-entry > button').trigger('click')
  await settle()
  expect(listServers).toHaveBeenCalledWith(expect.objectContaining({
    headers: expect.objectContaining({ 'X-Pipewright-Team': 'team-a' }),
  }))
  expect(wrapper.get('.deploy-panel').text()).toContain('Selected test server')
  expect(wrapper.get('.deploy-actions button').attributes('disabled')).toBeDefined()
  await wrapper.get('.deploy-close').trigger('click')
  await settle()
  expect(wrapper.find('.deploy-panel').exists()).toBe(false)
  expect(http.post).not.toHaveBeenCalled()

  await wrapper.get('.deploy-entry > button').trigger('click')
  const strategies = wrapper.findAll('[role=radio]')
  expect(strategies).toHaveLength(4)
  for (const strategy of [...strategies].reverse()) {
    await strategy.trigger('click')
    expect(strategy.attributes('aria-checked')).toBe('true')
  }
  await strategies[1]!.trigger('click')
  await wrapper.get('.strat-canary input').setValue('2')
  const advanced = wrapper.get('.adv-toggle')
  await advanced.trigger('click')
  expect(advanced.attributes('aria-expanded')).toBe('true')
  await advanced.trigger('click')
  expect(advanced.attributes('aria-expanded')).toBe('false')
  await wrapper.get('.deploy-server-opt input').setValue(true)
  await wrapper.get('.deploy-actions button').trigger('click')
  await settle()
  expect(http.post).toHaveBeenCalledExactlyOnceWith(
    '/api/runs/root/deploy',
    expect.objectContaining({
      artifactId: 'artifact-a', serverIds: ['server-selected'], strategy: 'canary',
      deployConfig: expect.objectContaining({ canaryCount: '2' }),
    }),
    expect.objectContaining({ headers: expect.objectContaining({ 'X-Pipewright-Team': 'team-a' }) }),
  )
})

it('cannot deploy with retained controls if deployment permission is removed', async () => {
  api.get.mockResolvedValue(fixture('success'))
  vi.mocked(listServers).mockResolvedValueOnce([deployServer])
  const { wrapper } = await render()
  await settle()
  const entry = wrapper.get('.deploy-entry > button')
  await entry.trigger('click')
  await settle()
  await wrapper.get('.deploy-server-opt input').setValue(true)
  const submit = wrapper.get('.deploy-actions button')
  granted.delete('deployment.execute')
  usePermissionStore().invalidate()
  await settle()
  expect(wrapper.find('.deploy-panel').exists()).toBe(false)
  const count = vi.mocked(listServers).mock.calls.length
  await entry.trigger('click')
  await submit.trigger('click')
  await settle()
  expect(listServers).toHaveBeenCalledTimes(count)
  expect(http.post).not.toHaveBeenCalled()
})

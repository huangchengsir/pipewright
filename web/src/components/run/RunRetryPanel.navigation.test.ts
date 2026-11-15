import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, ref } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { access } from '../../api/access'
import type { RetryContext, RetryPlan, RetryRequest, RetryAttemptPage } from '../../api/runRetry'
import type { useRunRetry } from '../../composables/useRunRetry'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import { retryContext, retryPlan, retryRequest } from '../../test/runRetryFixtures'
import PermissionGate from '../PermissionGate.vue'
import RunRetryPanel from './RunRetryPanel.vue'

vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
let wrapper:VueWrapper|undefined
const returnTo='/runs?status=failed'
const steps=['step-a','step-b','step-c'].map(id=>({id,name:id,status:'failed' as const,startedAt:null,finishedAt:null,durationMs:null}))
function fixture(){
  const context=retryContext()
  context.nodes[0]={...retryPlan().nodes[0],decision:{...retryPlan().nodes[0].decision,sourceAttemptId:'producer'}}
  return {
    context:ref<RetryContext|null>(context),
    plan:ref<RetryPlan|null>(retryPlan()), contextError:ref(''),error:ref(''),
    planLoading:ref(false),submitting:ref(false),querying:ref(false),reconciling:ref(false),
    pending:ref<{runId:string;requestId:string}|null>(null),request:ref<RetryRequest|null>(null),
    attempts:ref<RetryAttemptPage|null>({page:1,total:1,items:[{
      runId:'history-target',revision:2,status:'failed',executionKind:'retry',outcomeUnknown:false,createdAt:'',
    }]}),
    failedNodes:ref(context.nodes.filter(n=>n.decision.status==='failed')),
    isHead:ref(true),selected:ref<string[]>([]),rebuild:ref<string[]>([]),reconcileReasons:ref([]),
    preview:vi.fn(),openPlan:vi.fn(),queryOriginal:vi.fn(),reconcile:vi.fn(),submit:vi.fn(),setHistoryPage:vi.fn(),
  }
}
async function render(state=fixture()){
  const router=createRouter({history:createMemoryHistory(),routes:[
    {path:'/runs/:id',name:'run-detail',component:{template:'<div />'}},
    {path:'/runs',name:'runs',component:{template:'<div />'}},
  ]})
  await router.push('/runs/root')
  const page=defineComponent({setup:()=>()=>h(PermissionGate,
    {query:{action:'run.read',resourceId:'root'}},{
    default:()=>h(RunRetryPanel,{state:state as unknown as ReturnType<typeof useRunRetry>,runId:'root',steps,
      sourceRunId:'parent-attempt',returnTo,runStatus:'failed'}),
  })})
  const view=mount(page,{attachTo:document.body,global:{plugins:[router],stubs:{Teleport:true,RunTerminal:true}}})
  wrapper=view
  await flushPromises()
  const panel=view.getComponent(RunRetryPanel).vm as unknown as {
    show:(key?:string,mode?:'plan'|'details')=>Promise<void>;showLogs:(keys:string[])=>Promise<void>;showHistory:()=>void
  }
  return {view,router,panel,state}
}
function assertReadOnly(state:ReturnType<typeof fixture>) {
  for(const fn of [state.preview,state.openPlan,state.queryOriginal,state.reconcile,state.submit,state.setHistoryPage])expect(fn).not.toHaveBeenCalled()
}
beforeEach(()=>{
  vi.resetAllMocks();setActivePinia(createPinia())
  useSessionStore().setUser({username:'reader',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-r')
  vi.mocked(access.decisions).mockImplementation(async (team,queries)=>({
    teamId:team,userId:'u',revision:1,modeEpoch:1,catalogVersion:1,
    allowed:queries.map(q=>team==='team-r'&&q.action==='run.read'&&q.resourceId==='root'),
  }))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

describe('exact read-only recovery navigation controls',()=>{
  it.each(['request summary','closed error'] as const)('opens recovery from the actual %s button without any retry action',async entry=>{
    const state=fixture()
    if(entry==='request summary') {
      state.pending.value={runId:'root',requestId:'fixed'}
      state.request.value=retryRequest()
    } else state.error.value='retry_conflict'
    const {view}=await render(state)
    expect(view.find('[role=dialog]').exists()).toBe(false)
    await view.get(entry==='request summary'?'.retry-request-summary button':'.retry-closed-error button').trigger('click')
    await flushPromises()
    expect(view.find('.retry-panel--details').exists()).toBe(true)
    expect(view.find('.retry-all').exists()).toBe(false)
    assertReadOnly(state)
  })

  it('only closes on actual recovery backdrop or Escape and restores the invoking focus',async()=>{
    const state=fixture()
    state.error.value='retry_conflict'
    const {view}=await render(state)
    const open=view.get<HTMLButtonElement>('.retry-closed-error button')
    open.element.focus()
    await open.trigger('click')
    await view.get('.retry-panel').trigger('click')
    expect(view.find('.retry-panel').exists()).toBe(true)
    await view.get('.retry-overlay').trigger('click')
    expect(view.find('.retry-panel').exists()).toBe(false)
    expect(document.activeElement).toBe(view.get('.retry-closed-error button').element)
    await view.get('.retry-closed-error button').trigger('click')
    await view.get('.retry-overlay').trigger('keydown',{key:'Escape'})
    expect(view.find('.retry-panel').exists()).toBe(false)
    assertReadOnly(state)
  })

  it('does not restore recovery focus into a changed team after an actual backdrop click',async()=>{
    const state=fixture()
    state.error.value='retry_conflict'
    const {view}=await render(state)
    await view.get('.retry-closed-error button').trigger('click')
    const focusOutside=document.createElement('button')
    document.body.append(focusOutside)
    try {
      focusOutside.focus()
      view.get<HTMLElement>('.retry-overlay').element.click()
      usePermissionStore().setTeam('other')
      await flushPromises()
      expect(view.find('.retry-panel').exists()).toBe(false)
      expect(view.find('.retry-closed-error button').exists()).toBe(false)
      expect(document.activeElement).toBe(focusOutside)
      assertReadOnly(state)
    } finally { focusOutside.remove() }
  })

  it.each([
    ['accepted summary','.retry-request-summary a','child',false],
    ['original header','.retry-header a','parent-attempt',false],
    ['latest superseding attempt','.retry-panel > .retry-warning a','head-target',false],
    ['accepted modal request','.retry-panel .retry-request a','child',false],
    ['log evidence source run','.retry-node-evidence p > a','producer',false],
    ['log evidence source step','.retry-node-evidence > a','producer',true],
    ['details source run','.retry-node-detail > a:first-of-type','producer',false],
    ['details source step','.retry-node-detail > a:last-of-type','producer',true],
    ['plan reuse source step','[data-retry-group=reuse] a','root',true],
    ['history attempt','.retry-history a','history-target',false],
  ] as const)('clicks the exact %s link and preserves the scoped return route without submitting',async (entry,selector,target,sourceStep)=>{
    const state=fixture()
    if(entry.startsWith('accepted')){
      state.pending.value={runId:'root',requestId:'fixed'}
      state.request.value=retryRequest()
    }
    if(entry==='latest superseding attempt'){
      state.isHead.value=false
      state.context.value!.headAttemptId='head-target'
    }
    const {view,router,panel}=await render(state)
    if(entry.startsWith('log evidence')) await panel.showLogs(['a'])
    else if(entry.startsWith('details'))await panel.show('a')
    else if(entry==='history attempt')panel.showHistory()
    else await panel.show(undefined,'plan')
    await flushPromises()
    if(entry.startsWith('log evidence'))await view.get('.retry-node-evidence summary').trigger('click')
    expect(view.find('.retry-panel').exists()).toBe(true)
    await view.get(selector).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.params.id).toBe(target)
    expect(router.currentRoute.value.query).toEqual(sourceStep?{step:'0',stepId:'step-a',returnTo}:{returnTo})
    expect(view.find('.retry-panel').exists()).toBe(false)
    assertReadOnly(state)
  })
})

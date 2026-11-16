import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h, ref } from 'vue'
import { mount, flushPromises, RouterLinkStub, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { access } from '../../api/access'
import { HttpError } from '../../api/http'
import type { RetryRequest } from '../../api/runRetry'
import { useRunRetry } from '../../composables/useRunRetry'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import { retryContext, retryPlan, retryRequest } from '../../test/runRetryFixtures'
import PermissionGate from '../PermissionGate.vue'
import IdentityPage from '../IdentityPage.vue'
import RunRetryPanel from './RunRetryPanel.vue'

const api = vi.hoisted(() => ({ context:vi.fn(), attempts:vi.fn(), plan:vi.fn(), submit:vi.fn(), query:vi.fn() }))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/runRetry',async original=>({
  ...await original<typeof import('../../api/runRetry')>(),
  getRetryContext:api.context, getRetryAttempts:api.attempts, getRetryPlan:api.plan,
  submitRetry:api.submit, getRetryRequest:api.query,
}))
let wrapper:VueWrapper|undefined
const runId=ref('root')
const accepted=vi.fn()
let grants:Set<string>
const employee={username:'member',userId:'u',multiTenant:true}
const rootRead='team-a:run.read:root'
const rootRetry='team-a:run.retry:root'
const options=(team='team-a')=>expect.objectContaining({headers:{'X-Pipewright-Team':team},signal:expect.any(AbortSignal)})
const step=(id:string)=>({id,name:id,status:'failed' as const,startedAt:null,finishedAt:null,durationMs:null})
const steps=['step-a','step-b','step-c'].map(step)
const rebuildGroups=[{nodeIds:['a'],consumerIds:['b'],reasonCode:'workspace_rebuild_required'}]
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function render(){
  const ScopedPanel=defineComponent({
    setup(){
      const state=useRunRetry(runId)
      return ()=>h(RunRetryPanel,{state,runId:runId.value,steps,onAccepted:accepted})
    },
  })
  const Page=defineComponent({
    setup(){return ()=>h(PermissionGate,{query:{action:'run.read',resourceId:runId.value}},{
      default:()=>h(IdentityPage,{component:ScopedPanel,scopeKey:runId.value}),
    })},
  })
  const view=mount(Page,{attachTo:document.body,global:{stubs:{Teleport:true,RouterLink:RouterLinkStub,RunTerminal:true}}})
  wrapper=view
  await settle()
  return view
}
function panel(){
  return wrapper!.getComponent(RunRetryPanel).vm as unknown as {
    show:(key?:string,mode?:'plan'|'details')=>Promise<void>
    showLogs:(keys:string[],name?:string)=>Promise<void>
    showHistory:()=>void
  }
}
async function openNodePlan(){
  await panel().show('b')
  await settle()
  await wrapper!.get('.retry-node-detail .retry-focused').trigger('click')
  await settle()
}
beforeEach(()=>{
  vi.useFakeTimers()
  vi.resetAllMocks()
  sessionStorage.clear()
  runId.value='root'
  setActivePinia(createPinia())
  useSessionStore().setUser(employee)
  usePermissionStore().setTeam('team-a')
  grants=new Set([rootRead,rootRetry])
  vi.mocked(access.decisions).mockImplementation(async (team,queries)=>({
    userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>grants.has(team+':'+q.action+':'+q.resourceId)),
  }))
  api.context.mockImplementation(async (id:string)=>retryContext({attemptRunId:id,headAttemptId:id}))
  api.attempts.mockImplementation(async (id:string,page:number)=>({
    page,total:39,items:[{runId:id+'-page-'+page,revision:page,status:'failed',executionKind:'original',outcomeUnknown:false,createdAt:''}],
  }))
  api.plan.mockImplementation(async (id:string,targets:string[],rebuild:string[])=>retryPlan({
    baseAttemptId:id,targetNodeIds:targets,rebuildNodeIds:rebuild,rebuildGroups,
  }))
  api.submit.mockResolvedValue(retryRequest())
  api.query.mockRejectedValue(new HttpError(404,{code:'retry_not_found',message:'absent'},'absent'))
})
afterEach(()=>{
  wrapper?.unmount()
  wrapper=undefined
  vi.useRealTimers()
})

describe('recovery plan actual scoped controls',()=>{
  it.each(['details','logs'] as const)('chooses the exact failed node from its actual %s control and rejects retained actions after revocation',async source=>{
    const view=await render()
    if(source==='details') await panel().show('b')
    else await panel().showLogs(['a','b'],'fixture stage')
    await settle()
    const button=view.get(source==='details'?'.retry-node-detail .retry-focused':'.retry-log-view .retry-focused')
    await button.trigger('click')
    await settle()
    expect(view.find('.retry-panel--plan').exists()).toBe(true)
    expect(api.plan).toHaveBeenCalledExactlyOnceWith('root',['b'],[],options())
    grants.delete(rootRetry)
    usePermissionStore().invalidate()
    await settle()
    await button.trigger('click')
    await settle()
    expect(api.plan).toHaveBeenCalledOnce()
    expect(api.submit).not.toHaveBeenCalled()
  })

  it('reloads a failed plan through the actual fallback button with its same target and action scope',async()=>{
    const view=await render()
    api.plan.mockRejectedValueOnce(new HttpError(503,null,'temporary'))
    await openNodePlan()
    expect(view.find('.retry-groups').exists()).toBe(false)
    const retry=view.get('.retry-content > button')
    await retry.trigger('click')
    await settle()
    expect(api.plan).toHaveBeenNthCalledWith(1,'root',['b'],[],options())
    expect(api.plan).toHaveBeenNthCalledWith(2,'root',['b'],[],options())
    expect(view.find('.retry-groups').exists()).toBe(true)
    await view.get('.retry-actions button:not(.app-btn--primary)').trigger('click')
    await settle()
    expect(api.plan).toHaveBeenNthCalledWith(3,'root',['b'],[],options())
    expect(api.submit).not.toHaveBeenCalled()
  })

  it('checks and clears the exact rebuild group through the real input and leaves commit to confirmation',async()=>{
    const view=await render()
    await openNodePlan()
    const check=view.get<HTMLInputElement>('.retry-rebuild input')
    await check.setValue(true)
    await settle()
    expect(api.plan).toHaveBeenNthCalledWith(2,'root',['b'],['a'],options())
    await check.setValue(false)
    await settle()
    expect(api.plan).toHaveBeenNthCalledWith(3,'root',['b'],[],options())
    expect(api.submit).not.toHaveBeenCalled()
    grants.delete(rootRetry)
    usePermissionStore().invalidate()
    await settle()
    await check.setValue(true)
    await settle()
    expect(api.plan).toHaveBeenCalledTimes(3)
  })

  it('closes the recovery dialog and cannot refresh the plan with the old rebuild checkbox',async()=>{
    const view=await render()
    const previousFocus=document.createElement('button')
    document.body.append(previousFocus)
    try {
      previousFocus.focus()
      await openNodePlan()
      const oldCheckbox=view.get('.retry-rebuild input')
      await view.get('.retry-close').trigger('click')
      await settle()
      expect(view.find('[role="dialog"]').exists()).toBe(false)
      expect(document.activeElement).toBe(previousFocus)
      await oldCheckbox.setValue(true)
      await settle()
      expect(api.plan).toHaveBeenCalledOnce()
      expect(api.submit).not.toHaveBeenCalled()
    } finally { previousFocus.remove() }
  })

  it('submits the confirmed target and rebuild snapshot with the selected run and team',async()=>{
    const view=await render()
    await openNodePlan()
    await view.get('.retry-rebuild input').setValue(true)
    await settle()
    await view.get('.retry-actions .app-btn--primary').trigger('click')
    await settle()
    expect(api.submit).toHaveBeenCalledExactlyOnceWith('root',expect.objectContaining({
      requestId:expect.stringMatching(/^[a-f0-9]{32}$/),baseAttemptId:'root',baseRevision:1,
      targetNodeIds:['b'],rebuildNodeIds:['a'],planFingerprint:'f'.repeat(64),
    }),options())
    expect(accepted).toHaveBeenCalledExactlyOnceWith('child')
  })

  it('preserves the recorded attempt but suppresses late navigation after retry permission is withdrawn',async()=>{
    const view=await render()
    let done!: (result:RetryRequest)=>void
    api.submit.mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
    await openNodePlan()
    await view.get('.retry-actions .app-btn--primary').trigger('click')
    await settle()
    const requestId=api.submit.mock.calls[0]![1].requestId
    usePermissionStore().reject('team-a',{action:'run.retry',resourceId:'root'})

    await settle()
    done(retryRequest({requestId}))
    await settle()
    expect(api.submit).toHaveBeenCalledOnce()
    expect(view.find('.retry-request-summary').exists()).toBe(true)
    expect(accepted).not.toHaveBeenCalled()
  })

  it('pages forward and backward on the readable run without a retry grant and blocks the prior team button',async()=>{
    grants.delete(rootRetry)
    const view=await render()
    panel().showHistory()
    await settle()
    const pages=()=>view.findAll('.retry-history .retry-actions button')
    expect(pages()[0]!.attributes('disabled')).toBeDefined()
    const oldNext=pages()[1]!
    await oldNext.trigger('click')
    await settle()
    expect(api.attempts).toHaveBeenLastCalledWith('root',2,options())
    expect(pages()[1]!.attributes('disabled')).toBeDefined()
    await pages()[0]!.trigger('click')
    await settle()
    expect(api.attempts).toHaveBeenLastCalledWith('root',1,options())
    expect(api.plan).not.toHaveBeenCalled()
    const firstSignal=api.attempts.mock.calls[0]![2].signal
    grants.add('team-b:run.read:root')
    usePermissionStore().setTeam('team-b')
    await settle()
    expect(firstSignal.aborted).toBe(true)
    expect(api.attempts).toHaveBeenLastCalledWith('root',1,options('team-b'))
    const before=api.attempts.mock.calls.length
    await oldNext.trigger('click')
    await settle()
    expect(api.attempts).toHaveBeenCalledTimes(before)
  })

  it('queries only the original unresolved ID through both actual read-only request controls',async()=>{
    grants.delete(rootRetry)
    sessionStorage.setItem('pipewright.retry.pending:["u","team-a"]:root',JSON.stringify({runId:'root',requestId:'fixed-id'}))
    const view=await render()
    const queryLabel=(part:string)=>view.findAll(part+' button').find(item=>item.text()==='核对原操作')!
    await queryLabel('.retry-request-summary').trigger('click')
    await settle()
    panel().showHistory()
    await settle()
    await queryLabel('.retry-panel .retry-request').trigger('click')
    await settle()
    expect(api.query).toHaveBeenCalledTimes(3)
    for(const i of [1,2,3])expect(api.query).toHaveBeenNthCalledWith(i,'root','fixed-id',options())
    expect(api.plan).not.toHaveBeenCalled()
    expect(api.submit).not.toHaveBeenCalled()
  })
})

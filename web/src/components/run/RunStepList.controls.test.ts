import { createPinia,setActivePinia } from 'pinia'
import { flushPromises,mount,type VueWrapper } from '@vue/test-utils'
import { afterEach,beforeEach,describe,expect,it,vi } from 'vitest'
import { access } from '../../api/access'
import type { RunStep } from '../../api/runs'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import { retryContext } from '../../test/runRetryFixtures'
import RunStepList from './RunStepList.vue'

vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
const steps:RunStep[]=[
  {id:'step-a',name:'Success fixture',status:'success',startedAt:null,finishedAt:null,durationMs:1000,stage:'Build'},
  {id:'step-b',name:'Failure fixture',status:'failed',startedAt:null,finishedAt:null,durationMs:null,stage:'Verify'},
]
let wrapper:VueWrapper|undefined
let mayRetry:boolean
async function render(recovery=retryContext() as ReturnType<typeof retryContext>|null){
  const view=mount(RunStepList,{props:{runId:'root',steps,selected:null,recovery}})
  wrapper=view
  await flushPromises()
  return view
}
beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'run reader',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  mayRetry=true
  vi.mocked(access.decisions).mockImplementation(async (team,queries)=>({
    teamId:team,userId:'u',revision:1,modeEpoch:1,catalogVersion:1,
    allowed:queries.map(q=>mayRetry&&team==='team-a'&&q.action==='run.retry'&&q.resourceId==='root'),
  }))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

describe('actual step list read and retry controls',()=>{
  it('uses the actual step and all-log buttons only for displayed ordinals and never retries for the reader',async()=>{
    mayRetry=false
    const w=await render()
    await w.get('button.step-row--failed').trigger('click')
    expect(w.emitted('select')).toEqual([[1]])
    await w.setProps({selected:1})
    expect(w.get('button.step-row--failed').classes()).toContain('step-row--selected')
    await w.get('button.step-row--all').trigger('click')
    expect(w.emitted('select')).toEqual([[1],[null]])
    expect(w.find('.step-retry').exists()).toBe(false)
    expect(w.emitted('retry')).toBeUndefined()
  })

  it('retries only the exact failed node from the current allowed run and rejects the retained button after revocation',async()=>{
    const w=await render()
    const b=w.get('.step-retry')
    await b.trigger('click')
    expect(w.emitted('retry')).toEqual([[['b']]])
    usePermissionStore().reject('team-a',{action:'run.retry',resourceId:'root'})
    await flushPromises()
    expect(w.find('.step-retry').exists()).toBe(false)
    await b.trigger('click')
    expect(w.emitted('retry')).toEqual([[['b']]])
    await w.get('button.step-row--all').trigger('click')
    expect(w.emitted('select')).toEqual([[null]])
  })

  it('only opens read-only legacy recovery on the actual legacy step control and rejects it on a different run',async()=>{
    const w=await render(null)
    const b=w.get('.step-retry')
    await b.trigger('click')
    expect(w.emitted('retry')).toEqual([[[]]])
    await w.setProps({runId:'other'})
    await flushPromises()
    expect(w.find('.step-retry').exists()).toBe(false)
    await b.trigger('click')
    expect(w.emitted('retry')).toEqual([[[]]])
  })
})

import { createPinia,setActivePinia } from 'pinia'
import { flushPromises,mount,type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory,createRouter,RouterView } from 'vue-router'
import { afterEach,beforeEach,describe,expect,it,vi } from 'vitest'
import { access } from '../api/access'
import { usePermissionStore } from '../stores/permissions'
import { useSessionStore } from '../stores/session'
import Runs from './Runs.vue'

const api=vi.hoisted(()=>({list:vi.fn(),projects:vi.fn(),trigger:vi.fn()}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/runs',()=>({listRuns:api.list,triggerManual:api.trigger}))
vi.mock('../api/projects',()=>({listProjects:api.projects}))
vi.mock('../api/parameters',async original=>({...await original<typeof import('../api/parameters')>(),getParameters:vi.fn().mockResolvedValue([])}))
const row={id:'r1',projectId:'p1',projectName:'Project A',status:'failed',trigger:{type:'manual',branch:'trunk',commit:'abc',actor:'fixture'},createdAt:'2026-10-30T00:00:00Z',durationMs:1000}
const team=(teamId='team-a')=>expect.objectContaining({headers:{'X-Pipewright-Team':teamId},signal:expect.any(AbortSignal)})
let wrapper:VueWrapper|undefined
let canStart:boolean
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function render(path='/runs'){
  const router=createRouter({history:createMemoryHistory(),routes:[
    {path:'/runs',name:'runs',component:Runs},
    {path:'/runs/:id',name:'run-detail',component:{template:'<div>Run</div>'}},
  ]})
  await router.push(path)
  const view=mount(RouterView,{attachTo:document.body,global:{plugins:[router],stubs:{Teleport:true}}})
  wrapper=view
  await settle()
  return {view,router}
}
beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  vi.spyOn(window,'scrollTo').mockImplementation(()=>{})
  canStart=true
  api.list.mockResolvedValue({items:[row],total:42,page:1})
  api.projects.mockResolvedValue([{id:'p1',name:'Project A',defaultBranch:'trunk'},{id:'p2',name:'Read only project'}])
  api.trigger.mockResolvedValue({id:'manual'})
  vi.mocked(access.decisions).mockImplementation(async(id,queries)=>({
    teamId:id,userId:'u',revision:1,modeEpoch:1,catalogVersion:1,
    allowed:queries.map(q=>id==='team-a'&&(
      (q.action==='run.read'&&q.resourceId==='r1')||
      (q.action==='run.start'&&q.resourceId==='p1'&&q.create===true&&canStart)
    )),
  }))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;vi.restoreAllMocks()})

describe('actual run list filters and trigger dismissal',()=>{
  it('retries the exact applied run-list query through its load-error retry button',async()=>{
    api.list.mockRejectedValueOnce(new Error('temporary'))
    const {view}=await render('/runs?status=failed&q=read-only&page=2')
    const retry=view.get('.runs-root .banner--error button.banner-retry')
    await retry.trigger('click')
    await settle()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(api.list).toHaveBeenLastCalledWith({page:2,pageSize:20,status:'failed',q:'read-only'},team())
    expect(view.get('a.run-row').attributes('data-run-id')).toBe('r1')
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it('retries loading scoped project options without giving the second project a trigger action',async()=>{
    api.projects.mockRejectedValueOnce(new Error('temporary'))
    const {view}=await render()
    await view.get('.query-warning button').trigger('click')
    await settle()
    expect(api.projects).toHaveBeenCalledTimes(2)
    expect(api.projects).toHaveBeenLastCalledWith(team())
    await view.get('.btn-run').trigger('click')
    await settle()
    expect(view.findAll('#trigger-project option').map(el=>el.attributes('value'))).toEqual(['','p1'])
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it('clicks the actual status tab and resets paging in the current scoped read query',async()=>{
    const {view,router}=await render('/runs?q=lookup&page=2')
    await view.findAll('.filter-tab')[3]!.trigger('click')
    await settle()
    expect(router.currentRoute.value.query).toEqual({status:'failed',q:'lookup'})
    expect(api.list).toHaveBeenLastCalledWith({page:1,pageSize:20,status:'failed',q:'lookup'},team())
    expect(view.findAll('.filter-tab')[3]!.attributes('aria-pressed')).toBe('true')
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it('clears the actual empty-state filters and reads only the first unfiltered page',async()=>{
    api.list.mockResolvedValueOnce({items:[],total:0,page:1})
    const {view,router}=await render('/runs?status=failed&q=lookup&projectId=p1')
    await view.get('.empty-state button').trigger('click')
    await settle()
    expect(router.currentRoute.value.query).toEqual({})
    expect(api.list).toHaveBeenLastCalledWith({page:1,pageSize:20},team())
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it.each(['header','backdrop','Escape'] as const)('dismisses the actual trigger form via %s without dispatching the abandoned project draft',async kind=>{
    const {view,router}=await render()
    await view.get('.btn-run').trigger('click')
    await view.get('#trigger-project').setValue('p1')
    await view.get('#trigger-branch').setValue('explicit-test')
    const retained=view.get('form.modal-form')
    if(kind==='header')await view.get('.modal-close').trigger('click')
    else if(kind==='backdrop'){
      await view.get('.modal--sm').trigger('click')
      expect(view.find('.modal-scrim').exists()).toBe(true)
      await view.get('.modal-scrim').trigger('click')
    }else await view.get('.modal-scrim').trigger('keydown',{key:'Escape'})
    await settle()
    expect(view.find('.modal-scrim').exists()).toBe(false)
    await retained.trigger('submit')
    await settle()
    expect(api.trigger).not.toHaveBeenCalled()
    expect(router.currentRoute.value.name).toBe('runs')
  })

  it('does not apply another team\'s retained run list filter to the current URL',async()=>{
    const {view,router}=await render()
    const retained=view.findAll('.filter-tab')[3]!
    usePermissionStore().setTeam('team-b')
    await settle()
    const count=api.list.mock.calls.length
    await retained.trigger('click')
    await settle()
    expect(router.currentRoute.value.query).toEqual({})
    expect(api.list).toHaveBeenCalledTimes(count)
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it('keeps filters but hides manual trigger for a user without run.start',async()=>{
    canStart=false
    const {view}=await render()
    expect(view.find('.btn-run').exists()).toBe(false)
    expect(view.find('.modal-scrim').exists()).toBe(false)
    await view.findAll('.filter-tab')[3]!.trigger('click')
    await settle()
    expect(api.list).toHaveBeenLastCalledWith({page:1,pageSize:20,status:'failed'},team())
    expect(api.trigger).not.toHaveBeenCalled()
  })

  it('submits only the current native form after reopening an abandoned manual project draft',async()=>{
    const {view,router}=await render()
    await view.get('.btn-run').trigger('click')
    await view.get('#trigger-project').setValue('p1')
    await view.get('#trigger-branch').setValue('old-branch')
    const retained=view.get('form.modal-form')
    await view.get('.modal-close').trigger('click')
    await view.get('.btn-run').trigger('click')
    await view.get('#trigger-project').setValue('p1')
    await view.get('#trigger-branch').setValue('new-explicit-branch')
    await retained.trigger('submit')
    await settle()
    expect(api.trigger).not.toHaveBeenCalled()
    await view.get('form.modal-form').trigger('submit')
    await settle()
    expect(api.trigger).toHaveBeenCalledExactlyOnceWith('p1',{branch:'new-explicit-branch'},team())
    expect(router.currentRoute.value.params.id).toBe('manual')
  })
})

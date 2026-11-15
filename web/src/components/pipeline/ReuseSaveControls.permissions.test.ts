import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { access } from '../../api/access'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import JobDrawer from './JobDrawer.vue'
import TemplatePickerModal from './TemplatePickerModal.vue'

const api=vi.hoisted(()=>({ custom:vi.fn(), templates:vi.fn(), save:vi.fn(), apply:vi.fn() }))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/customNodes',()=>({createCustomNode:api.custom}))
vi.mock('../../api/templates',()=>({listTemplates:api.templates,createTemplate:api.save,applyTemplate:api.apply}))

const job:PipelineJob={id:'job-a',name:'Build fixture',type:'script',summary:'Test summary',config:{commands:'go test ./...'}}
const stage:PipelineStage={id:'stage-a',name:'Build',kind:'build',jobs:[job]}
const targetRead='pipeline.read:project-a'
const templateCreate='template.create:'
const customCreate='custom_node.create:'
const opts=()=>expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal),throwOnUnauthorized:true})
let allowed:Set<string>
let wrapper:VueWrapper|undefined

async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function drawer(readOnly=false){
  const view=mount(JobDrawer,{props:{job,stage,readOnly}})
  wrapper=view
  await settle()
  return view
}
async function template(){
  const view=mount(TemplatePickerModal,{props:{projectId:'project-a',stages:[stage],baseRevision:'cfg-3'}})
  wrapper=view
  await settle()
  await view.findAll('[role=tab]')[1]!.trigger('click')
  await settle()
  await view.get('#tpl-name').setValue('  Fixture template  ')
  await view.get('#tpl-desc').setValue('  Snapshot design  ')
  return view
}
function reject(action:string,resourceId=''){
  usePermissionStore().reject('team-a',{action,resourceId,...(resourceId?{}:{create:true})})
}

beforeEach(()=>{
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true})
  usePermissionStore().setTeam('team-a')
  allowed=new Set([targetRead,templateCreate,customCreate])
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({
    userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>team==='team-a'&&allowed.has(q.action+':'+q.resourceId)&&(!q.action.endsWith('.create')||q.create===true)),
  }))
  api.custom.mockResolvedValue({id:'fixture-custom'})
  api.templates.mockResolvedValue([])
  api.save.mockResolvedValue({id:'fixture-template'})
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined})

describe('actual custom-node save drawer controls',()=>{
  it('opens and confirms the exact source node snapshot with the scoped create action',async()=>{
    const w=await drawer()
    expect(w.find('.reuse-panel').exists()).toBe(false)
    await w.get('.reuse-btn').trigger('click')
    expect(w.get('.reuse-panel input').element).toHaveProperty('value','Build fixture')
    const [name,description]=w.findAll('.reuse-panel input')
    await name!.setValue('  Reusable build  ')
    await description!.setValue('  Local fixture  ')
    await w.get('.reuse-confirm').trigger('click')
    await settle()
    expect(api.custom).toHaveBeenCalledExactlyOnceWith({
      name:'Reusable build',description:'Local fixture',nodeType:'script',summary:'Test summary',
      config:{commands:'go test ./...'},
    },opts())
    expect(w.find('.reuse-panel').exists()).toBe(false)
    expect(w.find('.reuse-ok').exists()).toBe(true)
  })

  it('cancels the current draft and blocks its retained confirm and Enter controls',async()=>{
    const w=await drawer()
    await w.get('.reuse-btn').trigger('click')
    const name=w.get('.reuse-panel input')
    await name.setValue('old draft')
    const confirm=w.get('.reuse-confirm')
    await w.get('.reuse-cancel').trigger('click')
    expect(w.find('.reuse-panel').exists()).toBe(false)
    await confirm.trigger('click')
    await name.trigger('keydown',{key:'Enter'})
    await settle()
    expect(api.custom).not.toHaveBeenCalled()
    await w.get('.reuse-btn').trigger('click')
    expect(w.get('.reuse-panel input').element).toHaveProperty('value','Build fixture')
  })

  it('never submits a reopened custom-node draft from the previous native controls',async()=>{
    const w=await drawer()
    await w.get('.reuse-btn').trigger('click')
    const oldName=w.get('.reuse-panel input')
    const oldSave=w.get('.reuse-confirm')
    await w.get('.reuse-cancel').trigger('click')
    await w.get('.reuse-btn').trigger('click')
    await w.get('.reuse-panel input').setValue('New explicit draft')
    await oldSave.trigger('click')
    await oldName.trigger('keydown',{key:'Enter'})
    await settle()
    expect(api.custom).not.toHaveBeenCalled()
    await w.get('.reuse-confirm').trigger('click')
    await settle()
    expect(api.custom).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({name:'New explicit draft'}),opts())
  })

  it('never applies the open custom-node draft to a newly selected node',async()=>{
    const w=await drawer()
    await w.get('.reuse-btn').trigger('click')
    await w.get('.reuse-panel input').setValue('Draft for node A')
    const confirm=w.get('.reuse-confirm')
    await w.setProps({job:{...job,id:'job-b',type:'script',name:'Node B',config:{commands:'printf fixture'}}})
    expect(w.find('.reuse-panel').exists()).toBe(false)
    await confirm.trigger('click')
    await settle()
    expect(api.custom).not.toHaveBeenCalled()
    await w.get('.reuse-btn').trigger('click')
    expect(w.get('.reuse-panel input').element).toHaveProperty('value','Node B')
  })

  it('blocks native open/confirm when create permission is withdrawn but preserves other controls',async()=>{
    const w=await drawer()
    const open=w.get('.reuse-btn')
    await open.trigger('click')
    const confirm=w.get('.reuse-confirm')
    reject('custom_node.create')
    await settle()
    await confirm.trigger('click')
    await open.trigger('click')
    await settle()
    expect(w.find('.reuse-btn').exists()).toBe(false)
    expect(w.find('.job-drawer').exists()).toBe(true)
    expect(api.custom).not.toHaveBeenCalled()
  })

  it('discards the old custom-node save response across create revocation and restoration',async()=>{
    let done!:(value:unknown)=>void
    api.custom.mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
    const w=await drawer()
    await w.get('.reuse-btn').trigger('click')
    await w.get('.reuse-confirm').trigger('click')
    await settle()
    expect(api.custom).toHaveBeenCalledOnce()
    allowed.delete(customCreate)
    reject('custom_node.create')
    await settle()
    allowed.add(customCreate)
    usePermissionStore().invalidate()
    await settle()
    done({id:'old-saved-custom'})
    await settle()
    expect(w.find('.reuse-btn').exists()).toBe(true)
    expect(w.find('.reuse-ok').exists()).toBe(false)
  })

  it('does not allow read-only node editing mode to use a separately granted library action',async()=>{
    const w=await drawer(true)
    expect(w.get('fieldset.drawer-fields').attributes('disabled')).toBeDefined()
    await w.get('.reuse-btn').trigger('click')
    await settle()
    expect(w.find('.reuse-panel').exists()).toBe(false)
    expect(api.custom).not.toHaveBeenCalled()
  })
})

describe('actual pipeline template saving controls',()=>{
  it('saves the exact readable pipeline snapshot using the actual create-template button',async()=>{
    const w=await template()
    await w.get('button.btn-primary').trigger('click')
    await settle()
    expect(api.save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
      name:'Fixture template',description:'Snapshot design',
      stages:[expect.objectContaining({id:undefined,name:'Build',kind:'build',jobs:[
        {id:undefined,name:job.name,type:job.type,summary:job.summary,config:job.config},
      ]})],
    }),opts())
    expect(api.templates).toHaveBeenCalledTimes(2)
    expect(w.find('.ok').exists()).toBe(true)
    expect(api.apply).not.toHaveBeenCalled()
  })

  it('blocks the detached template-save button after returning to the apply panel',async()=>{
    const w=await template()
    const save=w.get('button.btn-primary')
    await w.findAll('[role=tab]')[0]!.trigger('click')
    await settle()
    expect(w.find('#tpl-name').exists()).toBe(false)
    await save.trigger('click')
    await settle()
    expect(api.save).not.toHaveBeenCalled()
  })

  it('only submits a reopened template save tab from the current native button',async()=>{
    const w=await template()
    const oldSave=w.get('button.btn-primary')
    await w.findAll('[role=tab]')[0]!.trigger('click')
    await w.findAll('[role=tab]')[1]!.trigger('click')
    await w.get('#tpl-name').setValue('New explicit snapshot')
    await oldSave.trigger('click')
    await settle()
    expect(api.save).not.toHaveBeenCalled()
    await w.get('button.btn-primary').trigger('click')
    await settle()
    expect(api.save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({name:'New explicit snapshot'}),opts())
  })

  it('blocks an old template save after closing the modal without waiting for unmount',async()=>{
    const w=await template()
    const save=w.get('button.btn-primary')
    await w.get('header .close').trigger('click')
    await settle()
    expect(w.emitted('close')).toHaveLength(1)
    await save.trigger('click')
    await settle()
    expect(api.save).not.toHaveBeenCalled()
    expect(w.find('[role=dialog]').exists()).toBe(false)
  })

  it('drops stale template success and refresh after independent create permission is withdrawn',async()=>{
    let done!:(value:unknown)=>void
    api.save.mockImplementationOnce(()=>new Promise(resolve=>{done=resolve}))
    const w=await template()
    const save=w.get('button.btn-primary')
    await save.trigger('click')
    await settle()
    expect(api.save).toHaveBeenCalledOnce()
    reject('template.create')
    await settle()
    done({id:'old-template'})
    await settle()
    await save.trigger('click')
    expect(api.save).toHaveBeenCalledOnce()
    expect(api.templates).toHaveBeenCalledOnce()
    expect(w.find('.ok').exists()).toBe(false)
    expect(w.find('[role=dialog]').exists()).toBe(true)
  })

  it('does not carry the open template save draft to another readable pipeline project',async()=>{
    allowed.add('pipeline.read:project-b')
    const w=await template()
    await w.setProps({projectId:'project-b',stages:[{...stage,id:'stage-b',name:'Other stage'}]})
    await settle()
    await w.get('button.btn-primary').trigger('click')
    await settle()
    expect(api.save).not.toHaveBeenCalled()
  })
})

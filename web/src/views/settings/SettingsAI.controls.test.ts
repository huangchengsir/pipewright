import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { access } from '../../api/access'
import type { AISettings, AITestResult } from '../../api/aiSettings'
import { usePermissionStore } from '../../stores/permissions'
import { useSessionStore } from '../../stores/session'
import AppSelect from '../../components/ui/AppSelect.vue'
import SettingsAI from './SettingsAI.vue'

const api=vi.hoisted(()=>({ get:vi.fn(), models:vi.fn(), test:vi.fn(), save:vi.fn(), success:vi.fn(), error:vi.fn() }))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/aiSettings',()=>({getAISettings:api.get,listAIModels:api.models,testAIConnection:api.test,saveAISettings:api.save}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:api.success,error:api.error})}))

const saved:AISettings={
  provider:'openai',baseUrl:'https://catalog.example/v1',model:'model-1',configured:true,enabled:true,
  apiKeyMasked:'fixture-mask',budget:{monthlyTokenLimit:null},updatedAt:null,
}
const result=(detail:string):AITestResult=>({ok:true,latencyMs:10,detail,error:null})
const scope=(team='team-a')=>expect.objectContaining({headers:{'X-Pipewright-Team':team},signal:expect.any(AbortSignal),throwOnUnauthorized:true})
const catalog='button[title="获取模型"]'
const connection='.test-row button'
const saveButton='.save-bar-actions button.app-btn--primary'
const discard='.save-bar-actions button.app-btn--ghost'
const toggle='button[role=switch]'
const providerCard=(w:VueWrapper,name:string)=>w.findAll('button.prov-card').find(card=>card.text().includes(name))!
let wrapper:VueWrapper|undefined
let allow:boolean
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function render(){
  const view=mount(SettingsAI,{attachTo:document.body})
  wrapper=view
  await settle()
  return view
}
function withdraw(){
  allow=false
  usePermissionStore().reject('team-a',{action:'system.manage',resourceId:'instance'})
}
beforeEach(()=>{
  vi.useFakeTimers()
  vi.resetAllMocks()
  setActivePinia(createPinia())
  useSessionStore().setUser({username:'instance admin',userId:'u',multiTenant:true,instanceAdmin:true})
  usePermissionStore().setTeam('team-a')
  allow=true
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({
    userId:useSessionStore().user?.userId??'',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,
    allowed:queries.map(q=>allow&&q.action==='system.manage'&&q.resourceId==='instance'),
  }))
  api.get.mockResolvedValue(saved)
  api.models.mockResolvedValue({ok:true,items:[{id:'fixture-selected',name:'Fixture selected'}]})
  api.test.mockResolvedValue(result('successful fixture'))
  api.save.mockResolvedValue(saved)
})
afterEach(()=>{
  wrapper?.unmount()
  wrapper=undefined
  vi.useRealTimers()
})

describe('actual instance AI settings permissions',()=>{
  it('withholds instance configuration and all operations from a denied member',async()=>{
    allow=false
    useSessionStore().setUser({username:'member',userId:'regular',multiTenant:true,instanceAdmin:false})
    const w=await render()
    expect(w.find('.prov-card').exists()).toBe(false)
    expect(api.get).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(700)
    expect(api.models).not.toHaveBeenCalled()
    expect(api.test).not.toHaveBeenCalled()
    expect(api.save).not.toHaveBeenCalled()
  })

  it('checks the actual provider card and model button against the current destination and instance permission',async()=>{
    const w=await render()
    await w.get('#ai-apikey').setValue('synthetic-openai-draft')
    const ollama=providerCard(w,'Ollama')
    await ollama.trigger('click')
    expect(ollama.attributes('aria-pressed')).toBe('true')
    expect(w.find('#ai-apikey').exists()).toBe(false)
    await w.get('#ai-baseurl').setValue('http://ollama.example:11434')
    const getModels=w.get(catalog)
    await getModels.trigger('click')
    await settle()
    expect(api.models).toHaveBeenCalledExactlyOnceWith({provider:'ollama',baseUrl:'http://ollama.example:11434'},expect.any(AbortSignal),scope())
    expect(w.findAllComponents(AppSelect)[1]!.props('options')).toEqual([{value:'fixture-selected',label:'Fixture selected · fixture-selected'}])
    withdraw()
    await settle()
    await getModels.trigger('click')
    await ollama.trigger('click')
    await settle()
    expect(w.find('.prov-grid').exists()).toBe(false)
    expect(api.models).toHaveBeenCalledOnce()
    expect(api.test).not.toHaveBeenCalled()
    expect(api.save).not.toHaveBeenCalled()
  })

  it('aborts prior catalog and connection requests on identity change and discards their late results',async()=>{
    let finishModels!:(value:unknown)=>void,finishTest!:(value:AITestResult)=>void
    api.models.mockImplementationOnce(()=>new Promise(resolve=>{finishModels=resolve}))
    api.test.mockImplementationOnce(()=>new Promise(resolve=>{finishTest=resolve}))
    const w=await render()
    await w.get(catalog).trigger('click')
    await w.get(connection).trigger('click')
    await settle()
    expect(api.models).toHaveBeenCalledOnce()
    expect(api.test).toHaveBeenCalledOnce()
    const modelSignal=api.models.mock.calls[0]![1] as AbortSignal
    const testSignal=api.test.mock.calls[0]![1].signal as AbortSignal
    api.get.mockResolvedValue({...saved,baseUrl:'https://new-admin.example/v1',model:'new-admin-model'})
    useSessionStore().setUser({username:'another admin',userId:'v',multiTenant:true,instanceAdmin:true})
    await settle()
    expect(modelSignal.aborted).toBe(true)
    expect(testSignal.aborted).toBe(true)
    finishModels({ok:true,items:[{id:'old-identity-catalog',name:'Old'}]})
    finishTest(result('old-identity-probe'))
    await settle()
    expect(w.text()).not.toContain('old-identity-probe')
    expect(w.findAllComponents(AppSelect)[1]!.props('options')).toEqual([])
    expect(w.get('#ai-baseurl').element).toHaveProperty('value','https://new-admin.example/v1')
  })

  it('tests the captured destination and invalidates the result when only the model changes',async()=>{
    let finish!:(value:AITestResult)=>void
    api.test.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    await w.get(connection).trigger('click')
    await settle()
    expect(api.test).toHaveBeenNthCalledWith(1,{provider:'openai',baseUrl:saved.baseUrl,model:'model-1'},scope())
    const signal=api.test.mock.calls[0]![1].signal as AbortSignal
    await w.get('#ai-model').setValue('model-2')
    finish(result('previous-model-result'))
    await settle()
    expect(w.find('.test-result').exists()).toBe(false)
    expect(signal.aborted).toBe(true)
    await w.get(connection).trigger('click')
    await settle()
    expect(api.test).toHaveBeenNthCalledWith(2,{provider:'openai',baseUrl:saved.baseUrl,model:'model-2'},scope())
    expect(w.get('.test-result').text()).toContain('successful fixture')
  })

  it('changes enabled locally and discards it while dropping an in-flight test for the same destination',async()=>{
    let finish!:(value:AITestResult)=>void
    api.test.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    await w.get(toggle).trigger('click')
    expect(w.get(toggle).attributes('aria-checked')).toBe('false')
    await w.get(connection).trigger('click')
    await settle()
    const signal=api.test.mock.calls[0]![1].signal as AbortSignal
    await w.get(discard).trigger('click')
    expect(w.get(toggle).attributes('aria-checked')).toBe('true')
    finish(result('discarded-draft-test'))
    await settle()
    expect(w.find('.test-result').exists()).toBe(false)
    expect(signal.aborted).toBe(true)
    expect(api.save).not.toHaveBeenCalled()
  })

  it('saves the exact provider fields and prevents duplicate native submit while that save is in flight',async()=>{
    let finish!:(value:AISettings)=>void
    api.save.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    await w.get('#ai-model').setValue('new-explicit-model')
    await w.get(toggle).trigger('click')
    await w.get(saveButton).trigger('click')
    await settle()
    expect(api.save).toHaveBeenCalledExactlyOnceWith({
      provider:saved.provider,baseUrl:saved.baseUrl,model:'new-explicit-model',budget:saved.budget,enabled:false,
    },scope())
    await w.get('form.config-form').trigger('submit')
    await settle()
    expect(api.save).toHaveBeenCalledOnce()
    finish({...saved,model:'new-explicit-model',enabled:false})
    await settle()
    expect(w.get('#ai-model').element).toHaveProperty('value','new-explicit-model')
    expect(api.success).toHaveBeenCalledOnce()
  })

  it('holds the provider and preset steady while the actual native save is in flight',async()=>{
    let finish!:(value:AISettings)=>void
    api.save.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    await w.get('#ai-model').setValue('captured')
    await w.get('form.config-form').trigger('submit')
    await settle()
    expect(api.save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({provider:'openai',model:'captured'}),scope())
    const claude=providerCard(w,'Anthropic')
    const preset=w.findAllComponents(AppSelect)[0]!
    expect(claude.attributes('disabled')).toBeDefined()
    expect(preset.props('disabled')).toBe(true)
    await claude.trigger('click')
    expect(providerCard(w,'OpenAI').attributes('aria-pressed')).toBe('true')
    finish({...saved,model:'captured'})
    await settle()
    expect(w.get('#ai-model').element).toHaveProperty('value','captured')
  })

  it('aborts an accepted settings write and never presents its result after the instance action is withdrawn',async()=>{
    let finish!:(value:AISettings)=>void
    api.save.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    await w.get('#ai-model').setValue('uncommitted')
    const retained=w.get(saveButton)
    await retained.trigger('click')
    await settle()
    const signal=api.save.mock.calls[0]![1].signal as AbortSignal
    withdraw()
    await settle()
    finish({...saved,model:'late-private-result'})
    await settle()
    await retained.trigger('click')
    expect(signal.aborted).toBe(true)
    expect(api.save).toHaveBeenCalledOnce()
    expect(w.text()).not.toContain('late-private-result')
    expect(api.success).not.toHaveBeenCalled()
  })

  it('retries the instance load once through the error button and ignores its retained control after revoke',async()=>{
    let finish!:(value:AISettings)=>void
    api.get.mockRejectedValueOnce(new Error('temporary')).mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}))
    const w=await render()
    const retry=w.get('.banner-retry')
    await retry.trigger('click')
    await settle()
    await retry.trigger('click')
    await settle()
    expect(api.get).toHaveBeenCalledTimes(2)
    expect(api.get).toHaveBeenLastCalledWith(scope())
    const signal=api.get.mock.calls[1]![0].signal as AbortSignal
    withdraw()
    await settle()
    finish({...saved,model:'late-private-settings'})
    await settle()
    expect(signal.aborted).toBe(true)
    expect(w.find('.prov-card').exists()).toBe(false)
    await retry.trigger('click')
    expect(api.get).toHaveBeenCalledTimes(2)
  })
})

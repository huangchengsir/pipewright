import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import AI from './SettingsAI.vue'
import AIContent from './SettingsAIContent.vue'
import OAuth from './SettingsOAuth.vue'
import OAuthContent from './SettingsOAuthContent.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {HttpError} from '../../api/http'
const api=vi.hoisted(()=>({ai:vi.fn(),save:vi.fn(),test:vi.fn(),models:vi.fn(),apps:vi.fn(),saveApp:vi.fn(),decisions:vi.fn(),toast:vi.fn()}))
vi.mock('../../api/aiSettings',()=>({getAISettings:api.ai,saveAISettings:api.save,testAIConnection:api.test,listAIModels:api.models}))
vi.mock('../../api/oauth',()=>({getOAuthApps:api.apps,saveOAuthApp:api.saveApp}))
vi.mock('../../api/access',()=>({access:{decisions:api.decisions}}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:api.toast,error:api.toast})}))
let wrapper:VueWrapper|undefined,allow:boolean
const settings={provider:'ollama',baseUrl:'http://localhost:11434',model:'local',configured:true,enabled:true,apiKeyMasked:'',budget:{monthlyTokenLimit:null},updatedAt:null}
const app={provider:'github',clientId:'client',baseUrl:'',enabled:true,maskedSecret:'masked',configured:true,updatedAt:null}
beforeEach(()=>{vi.resetAllMocks();vi.useFakeTimers();setActivePinia(createPinia());useSessionStore().setUser({username:'u',userId:'u',multiTenant:true,instanceAdmin:true});usePermissionStore().setTeam('a');allow=true
 api.ai.mockResolvedValue(settings);api.save.mockResolvedValue(settings);api.test.mockResolvedValue({ok:true,latencyMs:1,detail:'ok',error:null});api.models.mockResolvedValue({ok:true,items:[]});api.apps.mockResolvedValue([app]);api.saveApp.mockResolvedValue(app)
 api.decisions.mockImplementation(async(team:string,qs:unknown[])=>({userId:useSessionStore().user?.userId,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>allow)}))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;vi.useRealTimers()})
async function render(kind:'ai'|'oauth'){wrapper=mount(kind==='ai'?AI:OAuth);await flushPromises();return wrapper}
it.each(['ai','oauth'] as const)('%s denies employee initial data requests',async kind=>{allow=false;useSessionStore().setUser({username:'u',userId:'u',multiTenant:true});await render(kind);expect(api.ai).not.toHaveBeenCalled();expect(api.apps).not.toHaveBeenCalled();expect(wrapper!.find('form').exists()).toBe(false)})
it.each(['ai','oauth'] as const)('%s rejects a late result after identity change',async kind=>{let resolve!:(value:unknown)=>void;const get=kind==='ai'?api.ai:api.apps;get.mockImplementationOnce(()=>new Promise(r=>{resolve=r}));await render(kind);const options=get.mock.calls[0]![0];useSessionStore().clearSession();await flushPromises();resolve(kind==='ai'?settings:[app]);await flushPromises();expect(options.signal.aborted).toBe(true);expect(wrapper!.find('form').exists()).toBe(false)})
it('AI save/test/model controls use the fixed request team', async () => {
  const wrapper = await render('ai')
  const vm = wrapper.getComponent(AIContent).vm as unknown as {
    handleSave: () => Promise<void>
    handleTest: () => Promise<void>
    fetchModels: () => Promise<void>
  }
  const changedBaseUrl = 'http://fixture.invalid:11434'
  await wrapper.get('#ai-baseurl').setValue(changedBaseUrl)
  const context = expect.objectContaining({ headers: { 'X-Pipewright-Team': 'a' } })
  await vm.handleSave()
  expect(api.save).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({ baseUrl: changedBaseUrl }),
    context,
  )
  await vm.handleTest()
  expect(api.test).toHaveBeenCalledWith(expect.anything(), context)
  await vm.fetchModels()
  expect(api.models).toHaveBeenCalledWith(expect.anything(), expect.any(AbortSignal), context)
})
it('AI revoked scope clears draft key and blocks captured methods',async()=>{const w=await render('ai');const vm=w.getComponent(AIContent).vm as unknown as {apiKey:string;handleSave:()=>Promise<void>;handleTest:()=>Promise<void>;fetchModels:()=>Promise<void>};vm.apiKey='draft-only';allow=false;usePermissionStore().invalidate();await flushPromises();expect(vm.apiKey).toBe('');await vm.handleSave();await vm.handleTest();await vm.fetchModels();expect(api.save).not.toHaveBeenCalled();expect(api.test).not.toHaveBeenCalled();expect(api.models).not.toHaveBeenCalled()})
it('OAuth each provider saves independently under the common configuration permission',async()=>{api.apps.mockResolvedValue(['github','gitlab','gitee','gitea','custom'].map(provider=>({...app,provider,baseUrl:'https://git.invalid/'})));const w=await render('oauth');for(const provider of ['github','gitlab','gitee','gitea','custom']){api.saveApp.mockResolvedValue({...app,provider,baseUrl:'https://git.invalid/'});await w.get(`[data-provider="${provider}"] form`).trigger('submit');await flushPromises()}expect(api.saveApp).toHaveBeenCalledTimes(5);for(const call of api.saveApp.mock.calls)expect(call[2].headers['X-Pipewright-Team']).toBe('a')})
it('OAuth clears all draft secrets on team change and blocks the old submit method',async()=>{const w=await render('oauth');const vm=w.getComponent(OAuthContent).vm as unknown as {cards:Record<string,{clientSecret:string}>;handleSave:(provider:string)=>Promise<void>};vm.cards.github!.clientSecret='draft-only';usePermissionStore().setTeam('b');await flushPromises();expect(vm.cards.github!.clientSecret).toBe('');await vm.handleSave('github');expect(api.saveApp).not.toHaveBeenCalled()})
it.each(['ai', 'oauth'] as const)('%s denied save does not display a stale success', async kind => {
  const wrapper = await render(kind)
  if (kind === 'ai') {
    api.save.mockRejectedValue(new HttpError(403, null, 'Denied'))
    await wrapper.get('#ai-baseurl').setValue('http://fixture.invalid:11434')
    const vm = wrapper.getComponent(AIContent).vm as unknown as { handleSave: () => Promise<void> }
    await vm.handleSave()
    expect(api.save).toHaveBeenCalledOnce()
  } else {
    api.saveApp.mockRejectedValue(new HttpError(403, null, 'Denied'))
    const vm = wrapper.getComponent(OAuthContent).vm as unknown as { handleSave: (provider: string) => Promise<void> }
    await vm.handleSave('github')
    expect(api.saveApp).toHaveBeenCalledOnce()
  }
  await flushPromises()
  expect(api.toast).not.toHaveBeenCalled()
})

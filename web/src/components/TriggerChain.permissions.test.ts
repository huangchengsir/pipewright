import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {access} from '../api/access'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {HttpError} from '../api/http'
import TriggersPanel from './TriggersPanel.vue'
import ChainPanel from './ChainPanel.vue'

vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
const mocks=vi.hoisted(()=>({getTrigger:vi.fn(),saveTrigger:vi.fn(),reset:vi.fn(),getChain:vi.fn(),saveChain:vi.fn(),projects:vi.fn()}))
vi.mock('../api/triggers',()=>({getTrigger:mocks.getTrigger,saveTrigger:mocks.saveTrigger,resetSecret:mocks.reset}))
vi.mock('../api/chain',()=>({getChain:mocks.getChain,saveChain:mocks.saveChain}))
vi.mock('../api/projects',()=>({listProjects:mocks.projects}))
const trigger={webhookUrl:'/api/webhooks/synthetic-endpoint',webhookSecretMasked:'masked',events:{push:false,tag:false,pullRequest:false,release:false},branchMappings:[{branchPattern:'main',environment:'dev',targetServerIds:[]}],unmatchedPolicy:'record',pathFilters:[]}
const chain={downstream:[{downstreamProjectId:'p2',branch:'main',enabled:false}]}
const wrappers:VueWrapper[]=[]
let read=true,edit=true,start=true
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
function render(component:typeof TriggersPanel|typeof ChainPanel){const w=mount(component,{props:{projectId:'p1'},attachTo:document.body,global:{stubs:{CronPanel:true,EnvironmentsPanel:true,ConcurrencyPanel:true,ParametersPanel:true,ChainPanel:component===TriggersPanel,RunnerPanel:true,Teleport:true}}});wrappers.push(w);return w}
function deferred<T>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(r=>resolve=r);return{promise,resolve}}
beforeEach(()=>{
 vi.resetAllMocks();setActivePinia(createPinia());read=true;edit=true;start=true
 useSessionStore().setUser({username:'member',userId:'u1',multiTenant:true});usePermissionStore().setTeam('team-a')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>q.action==='pipeline.edit'?edit:q.action==='run.start'?start:read)}))
 mocks.getTrigger.mockImplementation(async()=>structuredClone(trigger));mocks.saveTrigger.mockImplementation(async()=>structuredClone(trigger));mocks.reset.mockResolvedValue({webhookUrl:'/api/webhooks/new-endpoint',webhookSecret:'one-time-secret',webhookSecretMasked:'new-masked'})
 mocks.getChain.mockImplementation(async()=>structuredClone(chain));mocks.saveChain.mockImplementation(async()=>structuredClone(chain));mocks.projects.mockResolvedValue([{id:'p2',name:'Downstream'}])
})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()})
describe('trigger and chain permission boundaries',()=>{
 it.each([TriggersPanel,ChainPanel])('does not mount or read a denied configuration',async component=>{read=false;render(component);await settle();expect(mocks.getTrigger).not.toHaveBeenCalled();expect(mocks.getChain).not.toHaveBeenCalled()})
 it('keeps every trigger editing control disabled for a reader and blocks exposed save',async()=>{
  edit=false;const w=render(TriggersPanel);await settle()
  expect(w.findAll('.ev-toggle:not(.ev-toggle--always-on)')).toHaveLength(4)
  for(const control of w.findAll('.ev-toggle,.seg-btn,.row-del-btn,.add-row-btn,.map-input,textarea,.secret-wrap>.btn-mini'))expect(control.attributes('disabled')).toBeDefined()
  expect(w.find('.btn-primary').exists()).toBe(false)
  expect(await (w.vm as unknown as {save:()=>Promise<boolean>}).save()).toBe(false)
  await w.get('.secret-wrap>.btn-mini').trigger('click');await settle();expect(mocks.reset).not.toHaveBeenCalled();expect(mocks.saveTrigger).not.toHaveBeenCalled()
 })
 it('edits, saves and resets using the original team and an abortable request',async()=>{
  const w=render(TriggersPanel);await settle()
  await w.get('.ev-toggle').trigger('click');await w.get('.map-input').setValue('release/*')
  await w.get('.btn-primary').trigger('click');await settle()
  expect(mocks.saveTrigger).toHaveBeenCalledWith('p1',expect.objectContaining({events:expect.objectContaining({push:true})}),undefined,expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)}))
  await w.get('.secret-wrap>.btn-mini').trigger('click');await w.get('.btn-danger').trigger('click');await settle()
  expect(w.text()).toContain('one-time-secret');expect(w.text()).toContain('new-endpoint')
  const options=mocks.reset.mock.calls[0]![2] as {signal:AbortSignal}
  useSessionStore().clearSession();await settle();expect(options.signal.aborted).toBe(true);expect(w.text()).not.toContain('one-time-secret')
 })
 it('drops a late reset response after changing teams',async()=>{
  const pending=deferred<{webhookSecret:string;webhookSecretMasked:string}>();mocks.reset.mockReturnValue(pending.promise)
  const w=render(TriggersPanel);await settle();await w.get('.secret-wrap>.btn-mini').trigger('click');await w.get('.btn-danger').trigger('click');await settle()
  const options=mocks.reset.mock.calls[0]![2] as {signal:AbortSignal};usePermissionStore().setTeam('team-b');await settle()
  pending.resolve({webhookSecret:'late-private-secret',webhookSecretMasked:'late-mask'});await settle()
  expect(options.signal.aborted).toBe(true);expect(w.text()).not.toContain('late-private-secret')
 })
 it('clears data after backend denies a trigger write',async()=>{
  mocks.saveTrigger.mockImplementation(async()=>{read=false;edit=false;throw new HttpError(403,{code:'forbidden',message:'Denied'},'Denied')});const w=render(TriggersPanel);await settle()
  await w.get('.btn-primary').trigger('click');await settle();expect(w.text()).not.toContain('synthetic-endpoint');expect(w.find('.secret-reveal').exists()).toBe(false)
 })
 it('edits the downstream draft while leaving writes to the explicit save control', async () => {
  const wrapper = render(ChainPanel)
  await settle()
  const toggle = wrapper.get('button[role="switch"]')
  expect(toggle.attributes('aria-checked')).toBe('false')
  await toggle.trigger('click')
  expect(toggle.attributes('aria-checked')).toBe('true')
  const count = wrapper.findAll('.row-del-btn').length
  await wrapper.get('.add-row-btn').trigger('click')
  expect(wrapper.findAll('.row-del-btn')).toHaveLength(count + 1)
  await wrapper.findAll('.row-del-btn').at(-1)!.trigger('click')
  expect(wrapper.findAll('.row-del-btn')).toHaveLength(count)
  expect(mocks.saveChain).not.toHaveBeenCalled()
 })

 it('keeps chain add/remove, selection and toggle disabled for a reader',async()=>{
  edit=false;const w=render(ChainPanel);await settle()
  for(const control of w.findAll('select,input,.chain-toggle,.row-del-btn,.add-row-btn'))expect(control.attributes('disabled')).toBeDefined()
  expect(w.find('.btn-primary').exists()).toBe(false);expect(mocks.saveChain).not.toHaveBeenCalled()
 })
 it('allows disabling config without a downstream start grant but prevents enabling',async()=>{
  start=false;const w=render(ChainPanel);await settle()
  expect(w.get('.chain-toggle').attributes('disabled')).toBeDefined()
  await w.get('.btn-primary').trigger('click');await settle();expect(mocks.saveChain).toHaveBeenCalledOnce()
  expect(mocks.saveChain.mock.calls[0]![1].downstream[0].enabled).toBe(false)
 })
 it('requires downstream start permission for an enabled row but allows turning it off',async()=>{
  start=false;mocks.getChain.mockResolvedValue({downstream:[{...chain.downstream[0],enabled:true}]})
  const w=render(ChainPanel);await settle()
  expect(w.get('.btn-primary').attributes('disabled')).toBeDefined()
  await w.get('.btn-primary').trigger('click');expect(mocks.saveChain).not.toHaveBeenCalled()
  await w.get('.chain-toggle').trigger('click');await settle()
  expect(w.get('.btn-primary').attributes('disabled')).toBeUndefined()
  await w.get('.btn-primary').trigger('click');await settle();expect(mocks.saveChain.mock.calls[0]![1].downstream[0].enabled).toBe(false)
 })
 it('drops a late chain load after the employee session ends',async()=>{
  const pending=deferred<typeof chain>();mocks.getChain.mockReturnValue(pending.promise)
  const w=render(ChainPanel);await settle();const options=mocks.getChain.mock.calls[0]![1] as {signal:AbortSignal}
  useSessionStore().clearSession();pending.resolve(chain);await settle()
  expect(options.signal.aborted).toBe(true);expect(w.text()).not.toContain('Downstream');expect(w.find('.chain-row').exists()).toBe(false)
 })
 it('shows a required authorization without automatically saving or rotating a secret',async()=>{
  mocks.getTrigger.mockResolvedValue({...trigger,authorizationState:'required'})
  const w=render(TriggersPanel);await settle();expect(w.text()).toContain('需要重新授权');expect(w.text()).toContain('授权前不会自动触发')
  expect(mocks.saveTrigger).not.toHaveBeenCalled();expect(mocks.reset).not.toHaveBeenCalled()
 })
 it('identifies the downstream target that needs authorization without retrying it',async()=>{
  mocks.getChain.mockResolvedValue({downstream:[{...chain.downstream[0],enabled:true,authorizationState:'required'}]})
  const w=render(ChainPanel);await settle();expect(w.get('.automation-authorization').text()).toContain('需要重新授权');expect(w.get('.automation-authorization').attributes('title')).toContain('执行配置或权限已变化')
  expect(mocks.saveChain).not.toHaveBeenCalled()
 })
 it('enables and saves a permitted downstream and prevents duplicate writes after logout',async()=>{
  const w=render(ChainPanel);await settle();await w.get('.chain-toggle').trigger('click');await settle();await w.get('.btn-primary').trigger('click');await settle()
  expect(mocks.saveChain).toHaveBeenCalledWith('p1',{downstream:[{downstreamProjectId:'p2',branch:'main',enabled:true}]},undefined,expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'}}))
  const options=mocks.saveChain.mock.calls[0]![3] as {signal:AbortSignal};useSessionStore().clearSession();await settle();expect(options.signal.aborted).toBe(true);expect(w.text()).not.toContain('Downstream');expect(mocks.saveChain).toHaveBeenCalledOnce()
 })
})

it('cancels a reset before any request and never lets its old button confirm a reopened dialog', async () => {
 const w=render(TriggersPanel);
 await settle();
 for (const dismiss of [
  () => w.get('.modal-close').trigger('click'),
  () => w.get('.modal-footer .btn-secondary').trigger('click'),
  () => w.get('.modal-scrim').trigger('click'),
  () => w.get('.modal-scrim').trigger('keydown',{key:'Escape'}),
 ]) {
  await w.get('.secret-wrap>.btn-mini').trigger('click');
  const old=w.get('.btn-danger').element;
  await dismiss();
  old.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  await w.get('.secret-wrap>.btn-mini').trigger('click');
  old.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  expect(mocks.reset).not.toHaveBeenCalled();
  await w.get('.modal-close').trigger('click');
 }
});
it('aborts an in-flight reset and drops its secret after edit revocation while keeping read access', async () => {
 const pending=deferred<typeof trigger & {webhookSecret:string}>();
 mocks.reset.mockReturnValueOnce(pending.promise);
 const w=render(TriggersPanel);
 await settle();
 await w.get('.secret-wrap>.btn-mini').trigger('click');
 await w.get('.btn-danger').trigger('click');
 const options=mocks.reset.mock.calls[0]![2] as {signal:AbortSignal};
 usePermissionStore().reject('team-a',{action:'pipeline.edit',resourceId:'p1'});
 expect(options.signal.aborted).toBe(true);
 pending.resolve({...trigger,webhookSecret:'stale-secret'});
 await settle();
 expect(w.text()).not.toContain('stale-secret');
 expect(w.find('.modal-scrim').exists()).toBe(false);
 expect(w.text()).toContain('synthetic-endpoint');
});

it('clicks every trigger draft option and writes only on explicit save for its project',async()=>{
  const w=render(TriggersPanel);
  await settle();
  for(const sw of w.findAll('.ev-toggle:not(.ev-toggle--always-on)')){
    await sw.trigger('click');
    expect(sw.attributes('aria-checked')).toBe('true');
  }
  const forced=w.get('.ev-toggle--always-on');
  expect(forced.attributes('aria-checked')).toBe('true');
  await forced.trigger('click');
  expect(forced.attributes('aria-checked')).toBe('true');
  await w.get('.add-row-btn').trigger('click');
  expect(w.findAll('.map-row')).toHaveLength(2);
  await w.findAll('.row-del-btn')[1]!.trigger('click');
  expect(w.findAll('.map-row')).toHaveLength(1);
  const policy=w.findAll('.seg-btn');
  await policy[1]!.trigger('click');
  expect(policy[1]!.attributes('aria-checked')).toBe('true');
  await policy[0]!.trigger('click');
  expect(policy[0]!.attributes('aria-checked')).toBe('true');
  expect(mocks.saveTrigger).not.toHaveBeenCalled();
  await w.get('.btn-primary').trigger('click');
  await settle();
  expect(mocks.saveTrigger).toHaveBeenCalledOnce();
  expect(mocks.saveTrigger).toHaveBeenCalledWith('p1', expect.objectContaining({
    events:{push:true,tag:true,pullRequest:true,release:true},unmatchedPolicy:'record',
    branchMappings:[expect.objectContaining({branchPattern:'main',environment:'dev'})],
  }), undefined, expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'}}));
});
it('retries failed trigger reads only for the captured project and denies retained retry on logout',async()=>{
 mocks.getTrigger.mockRejectedValueOnce(new Error('fixture unavailable'));
 const w=render(TriggersPanel);
 await settle();
 const retry=w.get('.banner-retry').element;
 await w.get('.banner-retry').trigger('click');
 await settle();
 expect(mocks.getTrigger).toHaveBeenCalledTimes(2);
 expect(mocks.getTrigger).toHaveBeenLastCalledWith('p1',expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'}}));
 useSessionStore().clearSession();
 retry.dispatchEvent(new MouseEvent('click',{bubbles:true}));
 await settle();
 expect(mocks.getTrigger).toHaveBeenCalledTimes(2);
});
it('copies the scoped webhook URL and one-time result only while the corresponding access is active',async()=>{
 const writeText=vi.fn().mockResolvedValue(undefined);
 vi.stubGlobal('navigator',Object.assign(Object.create(navigator),{clipboard:{writeText}}));
 try{
  const w=render(TriggersPanel);
  await settle();
  await w.get('.url-action-btn').trigger('click');
  expect(writeText).toHaveBeenLastCalledWith('/api/webhooks/synthetic-endpoint');
  await w.get('.secret-wrap>.btn-mini').trigger('click');
  await w.get('.btn-danger').trigger('click');
  await settle();
  const copy=w.get('.secret-reveal .btn-mini');
  await copy.trigger('click');
  expect(writeText).toHaveBeenLastCalledWith('one-time-secret');
  usePermissionStore().reject('team-a',{action:'pipeline.edit',resourceId:'p1'});
  copy.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  await settle();
  expect(writeText).toHaveBeenCalledTimes(2);
  expect(w.find('.secret-reveal').exists()).toBe(false);
 }finally{vi.unstubAllGlobals()}
});

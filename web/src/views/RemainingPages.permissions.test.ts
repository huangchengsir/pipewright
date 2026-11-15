import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createMemoryHistory,createRouter,RouterView} from 'vue-router'
import Runs from './Runs.vue'
import ServerStatus from './ServerStatus.vue'
import DiagnosisStats from './settings/SettingsDiagnosisStats.vue'
import {HttpError} from '../api/http'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
const api=vi.hoisted(()=>({decisions:vi.fn(),runs:vi.fn(),projects:vi.fn(),trigger:vi.fn(),parameters:vi.fn(),servers:vi.fn(),metrics:vi.fn(),stats:vi.fn()}))
vi.mock('../api/access',()=>({access:{decisions:api.decisions}}))
vi.mock('../api/runs',()=>({listRuns:api.runs,triggerManual:api.trigger}))
vi.mock('../api/projects',()=>({listProjects:api.projects}))
vi.mock('../api/parameters',()=>({getParameters:api.parameters,validateParamValues:()=>null}))
vi.mock('../api/servers',()=>({listServers:api.servers,getAllServerMetrics:api.metrics}))
vi.mock('../api/settings',()=>({getDiagnosisStats:api.stats}))
vi.mock('../components/ops/ContainerAiPanel.vue',()=>({default:{props:['initialServerIds'],template:'<div class="ai-test" :data-targets="JSON.stringify(initialServerIds)">AI</div>'}}))
let w:VueWrapper|undefined,allow:Set<string>
const row={id:'r1',projectId:'p1',projectName:'Project',status:'success',createdAt:'2026-11-13T00:00:00Z',durationMs:1000,trigger:{type:'manual',branch:'main',commit:'',actor:'member'}}
const metric={serverId:'s1',reachable:true,cpu:null,memory:null,disk:null}
const stats={totalFeedback:1,thumbsUp:1,thumbsDown:0,accuracy:1,recentTrend:[],recentCorrections:[]}
async function settle(){for(let i=0;i<6;i++)await flushPromises()}
beforeEach(()=>{vi.resetAllMocks();setActivePinia(createPinia());useSessionStore().setUser({userId:'u',username:'member',multiTenant:true});usePermissionStore().setTeam('t');allow=new Set(['run.read:r1','pipeline.read:p1','run.start:p1','server.inspect:s1','opschat.create:new-resource']);api.decisions.mockImplementation(async(team:string,qs:{action:string;resourceId:string}[])=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>allow.has(q.action+':'+q.resourceId))}));api.runs.mockResolvedValue({items:[row],total:1,page:1});api.projects.mockResolvedValue([{id:'p1',name:'Project',defaultBranch:'main'}]);api.trigger.mockResolvedValue({id:'created'});api.parameters.mockResolvedValue([]);api.servers.mockResolvedValue([{id:'s1',name:'Private server'}]);api.metrics.mockResolvedValue([metric]);api.stats.mockResolvedValue(stats);vi.spyOn(window,'scrollTo').mockImplementation(()=>{})})
afterEach(()=>{w?.unmount();w=undefined;vi.useRealTimers()})
async function runs(){const router=createRouter({history:createMemoryHistory(),routes:[{path:'/runs',name:'runs',component:Runs},{path:'/runs/:id',name:'run-detail',component:{template:'<div>detail</div>'}}]});await router.push('/runs');await router.isReady();w=mount(RouterView,{attachTo:document.body,global:{plugins:[router],stubs:{Teleport:true}}});await settle();return router}
it('sends run list with fixed team and hides unauthorized manual trigger',async()=>{allow.delete('run.start:p1');await runs();expect(api.runs.mock.calls[0][1].headers['X-Pipewright-Team']).toBe('t');expect(w!.find('a.run-row').exists()).toBe(true);expect(w!.find('header .btn-run').exists()).toBe(false)})
it('submits an allowed manual run with fixed identity and opens its new detail',async()=>{const router=await runs();await w!.get('header .btn-run').trigger('click');await settle();await w!.get('#trigger-project').setValue('p1');await settle();await w!.get('#trigger-branch').setValue('main');await settle();(w!.get('.modal-form button[type=submit]').element as HTMLButtonElement).click();await settle();expect(api.trigger).toHaveBeenCalledExactlyOnceWith('p1',{branch:'main'},expect.objectContaining({headers:{'X-Pipewright-Team':'t'},signal:expect.any(AbortSignal)}));expect(router.currentRoute.value.params.id).toBe('created')})
it('blocks a forced form submit after run.start is revoked',async()=>{await runs();await w!.get('header .btn-run').trigger('click');await settle();await w!.get('#trigger-project').setValue('p1');await w!.get('#trigger-branch').setValue('main');allow.delete('run.start:p1');usePermissionStore().invalidate();await settle();await w!.get('.modal-form').trigger('submit');await settle();expect(api.trigger).not.toHaveBeenCalled()})
it('removes revoked run rows without exposing their native links',async()=>{await runs();allow.delete('run.read:r1');usePermissionStore().invalidate();await settle();expect(w!.find('a.run-row').exists()).toBe(false)})
it('discards old-team run responses and cancels their transport',async()=>{let finish!:(v:unknown)=>void;api.runs.mockImplementationOnce(()=>new Promise(r=>{finish=r}));await runs();const options=api.runs.mock.calls[0][1];usePermissionStore().setTeam('other');await settle();finish({items:[{...row,id:'old-secret'}],total:1});await settle();expect(options.signal.aborted).toBe(true);expect(w!.html()).not.toContain('old-secret')})
it('keeps server cards and AI launch under independent decisions',async()=>{allow.delete('opschat.create:new-resource');w=mount(ServerStatus);await settle();expect(api.metrics.mock.calls[0][0].headers['X-Pipewright-Team']).toBe('t');expect(w.text()).toContain('Private server');expect(w.find('button.app-btn--ai').exists()).toBe(false);allow.delete('server.inspect:s1');usePermissionStore().invalidate();await settle();expect(w.text()).not.toContain('Private server')})
it('stops server polling and clears data on logout',async()=>{vi.useFakeTimers();w=mount(ServerStatus);await settle();const signal=api.metrics.mock.calls[0][0].signal;useSessionStore().clearSession();await settle();await vi.advanceTimersByTimeAsync(24000);expect(api.metrics).toHaveBeenCalledOnce();expect(signal.aborted).toBe(true);expect(w.text()).not.toContain('Private server')})
it('does not display late server names or metrics in another team',async()=>{let finish!:(v:unknown)=>void;api.metrics.mockImplementationOnce(()=>new Promise(r=>{finish=r}));w=mount(ServerStatus);await settle();usePermissionStore().setTeam('other');api.metrics.mockResolvedValue([]);await settle();finish([{...metric,serverId:'old-private'}]);await settle();expect(w.html()).not.toContain('old-private')})
it('refreshes scoped diagnosis statistics with a fixed team and aborts logout',async()=>{vi.useFakeTimers();w=mount(DiagnosisStats);await settle();const options=api.stats.mock.calls[0][0];expect(options.headers['X-Pipewright-Team']).toBe('t');await vi.advanceTimersByTimeAsync(5000);await settle();expect(api.stats).toHaveBeenCalledTimes(2);useSessionStore().clearSession();await settle();await vi.advanceTimersByTimeAsync(10000);expect(api.stats).toHaveBeenCalledTimes(2);expect(options.signal.aborted).toBe(true);expect(w.find('.ds').exists()).toBe(false)})
it('discards a diagnosis response after switching employee',async()=>{let finish!:(v:unknown)=>void;api.stats.mockImplementationOnce(()=>new Promise(r=>{finish=r}));w=mount(DiagnosisStats);await settle();useSessionStore().setUser({userId:'other',username:'other',multiTenant:true});await settle();finish({...stats,recentCorrections:[{runId:'private-run',correctRootCause:'private-old-correction',at:'2026-11-13T00:00:00Z'}]});await settle();expect(w.text()).not.toContain('private-old-correction')})

it('loads typed run parameters after their independent read decision resolves',async()=>{
 api.parameters.mockResolvedValue([{name:'TARGET',type:'string',required:true,defaultValue:'test'}]);
 await runs();await w!.get('header .btn-run').trigger('click');await settle();await w!.get('#trigger-project').setValue('p1');await settle();
 expect(api.parameters).toHaveBeenCalledOnce();expect(api.parameters.mock.calls[0][0]).toBe('p1');expect(api.parameters.mock.calls[0][1].headers['X-Pipewright-Team']).toBe('t');
});

it('recovers a forbidden project filter only through a new scoped query',async()=>{
 const router=await runs();api.runs.mockRejectedValueOnce(new HttpError(403,null,'denied'));
 await router.push('/runs?projectId=forbidden');await settle();
 expect(w!.find('[role="alert"]').exists()).toBe(true);expect(w!.find('[aria-busy="true"]').exists()).toBe(false);
 expect(w!.find('a.run-row').exists()).toBe(false);
 const calls=api.runs.mock.calls.length;await settle();expect(api.runs).toHaveBeenCalledTimes(calls);
 await router.push('/runs');await settle();expect(w!.find('a.run-row').exists()).toBe(true);
});

it('blocks a stale run confirmation after losing run.start', async () => {
 await runs()
 await w!.get('header .btn-run').trigger('click')
 await settle()
 await w!.get('#trigger-project').setValue('p1')
 await w!.get('#trigger-branch').setValue('main')
 await settle()
 const confirm = w!.get('.modal-form button[type=submit]').element as HTMLButtonElement
 expect(confirm.disabled).toBe(false)
 allow.delete('run.start:p1')
 usePermissionStore().invalidate()
 await settle()
 expect(confirm.disabled || !confirm.isConnected).toBe(true)
 confirm.click()
 await settle()
 expect(api.trigger).not.toHaveBeenCalled()
})

it('closes the manual run dialog without submitting when cancelled', async () => {
 await runs()
 await w!.get('header .btn-run').trigger('click')
 await settle()
 await w!.get('.modal-form .modal-footer button[type=button]').trigger('click')
 await settle()
 expect(w!.find('.modal-form').exists()).toBe(false)
 expect(api.trigger).not.toHaveBeenCalled()
})

it('opens the overview assistant without a server and closes it when create is revoked',async()=>{
  w=mount(ServerStatus)
  await settle()
  await w.get('.view-header__actions button.app-btn--ai').trigger('click')
  expect(w.get('.ai-test').attributes('data-targets')).toBe('[]')
  usePermissionStore().reject('t',{action:'opschat.create',resourceId:'new-resource',create:true})
  await settle()
  expect(w.find('.ai-test').exists()).toBe(false)
})
it('opens the selected server assistant only for that host and closes it on inspect revocation',async()=>{
  w=mount(ServerStatus)
  await settle()
  const button=w.get('.metrics-grid button.app-btn--ai')
  await button.trigger('click')
  expect(w.get('.ai-test').attributes('data-targets')).toBe('["s1"]')
  usePermissionStore().reject('t',{action:'server.inspect',resourceId:'s1'})
  button.element.dispatchEvent(new MouseEvent('click',{bubbles:true}))
  await settle()
  expect(w.find('.ai-test').exists()).toBe(false)
})

it('opens the selected run link only while read permission is current',async()=>{
  const router=await runs()
  const old=w!.get('a.run-row').element
  usePermissionStore().reject('t',{action:'run.read',resourceId:'r1'})
  old.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,button:0}))
  await settle()
  expect(router.currentRoute.value.name).toBe('runs')
  expect(w!.find('a.run-row').exists()).toBe(false)
  usePermissionStore().invalidate()
  await settle()
  await w!.get('a.run-row').trigger('click',{button:0})
  await settle()
  expect(router.currentRoute.value.params.id).toBe('r1')
})
it('clicks both run history page buttons and keeps the scoped query',async()=>{
  api.runs.mockImplementation(async (params)=>({items:[row],total:45,page:params.page}))
  const router=await runs()
  const buttons=()=>w!.findAll('.run-pagination button')
  const before=api.runs.mock.calls.length
  await buttons()[1]!.trigger('click')
  await settle()
  expect(router.currentRoute.value.query.page).toBe('2')
  expect(api.runs).toHaveBeenNthCalledWith(before+1,expect.objectContaining({page:2}),expect.objectContaining({headers:{'X-Pipewright-Team':'t'}}))
  await buttons()[0]!.trigger('click')
  await settle()
  expect(router.currentRoute.value.query.page).toBeUndefined()
  expect(api.runs).toHaveBeenLastCalledWith(expect.objectContaining({page:1}),expect.objectContaining({headers:{'X-Pipewright-Team':'t'}}))
})
it('submits keyword filter and clears it without carrying an old team scope',async()=>{
  const router=await runs()
  await w!.get('.run-query input[type=search]').setValue('fixture')
  ;(w!.get('.run-query button[type=submit]').element as HTMLButtonElement).click()
  await settle()
  expect(router.currentRoute.value.query.q).toBe('fixture')
  expect(api.runs.mock.calls.at(-1)![1].headers['X-Pipewright-Team']).toBe('t')
  await w!.get('.run-query .banner-retry').trigger('click')
  await settle()
  expect(router.currentRoute.value.query.q).toBeUndefined()
  expect(api.runs.mock.calls.at(-1)![1].headers['X-Pipewright-Team']).toBe('t')
})

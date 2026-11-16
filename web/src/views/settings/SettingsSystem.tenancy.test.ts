import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import Page from './SettingsSystem.vue'
import Content from './SettingsSystemContent.vue'
import {t} from '../../i18n'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
const api=vi.hoisted(()=>({version:vi.fn(),check:vi.fn(),source:vi.fn(),sourceSave:vi.fn(),current:vi.fn(),get:vi.fn(),start:vi.fn(),resume:vi.fn(),retention:vi.fn(),retentionSave:vi.fn(),decisions:vi.fn()}))
vi.mock('../../api/version',()=>({getVersion:api.version,checkUpdate:api.check,getReleaseSourceSettings:api.source,setReleaseSourceSettings:api.sourceSave,getCurrentUpdateTask:api.current,getUpdateTask:api.get,startUpdateTask:api.start,resumeUpdateTask:api.resume}))
vi.mock('../../api/retention',()=>({getRetentionConfig:api.retention,setRetentionConfig:api.retentionSave}))
vi.mock('../../api/access',()=>({access:{decisions:api.decisions}}))
let wrapper:VueWrapper|undefined,allow:boolean
const source={source:'github',revision:0,sources:[{id:'github',name:'GitHub',repository:'fixture',available:true},{id:'gitee',name:'Gitee',repository:'fixture',available:true}]}
function user(id='u'){useSessionStore().setUser({username:id,userId:id,multiTenant:true,instanceAdmin:true})}
beforeEach(()=>{vi.resetAllMocks();vi.useFakeTimers();sessionStorage.clear();setActivePinia(createPinia());user();usePermissionStore().setTeam('a');allow=true
 api.version.mockResolvedValue({version:'2.3.0',commit:'fixture',date:'',goVersion:'',platform:''});api.check.mockResolvedValue({source:'github',current:'2.3.0',latest:'2.4.0',updateAvailable:true,notes:''});api.source.mockResolvedValue(source);api.current.mockResolvedValue(null);api.retention.mockResolvedValue({enabled:false,keepPerProject:0,maxAgeDays:0});api.retentionSave.mockImplementation(async x=>x)
 api.start.mockResolvedValue({status:'manual',command:'docker pull fixture',reason:'fixture'});api.sourceSave.mockImplementation(async x=>({...source,source:x.source,revision:1}))
 api.decisions.mockImplementation(async(team:string,qs:unknown[])=>({userId:useSessionStore().user?.userId,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>allow)}))
})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;vi.useRealTimers();sessionStorage.clear()})
async function render(){wrapper=mount(Page);await flushPromises();return wrapper}
function vm(){return wrapper!.getComponent(Content).vm as unknown as {runCheck:()=>Promise<void>;runUpdate:()=>Promise<void>;saveRetention:()=>Promise<void>;saveSource:()=>Promise<boolean>;sourceDraft:string;resumeTask:()=>Promise<void>;recoverOriginal:()=>Promise<void>}}
it('an employee cannot mount system settings or start background polling',async()=>{allow=false;useSessionStore().setUser({username:'u',userId:'u',multiTenant:true});await render();expect(wrapper!.findComponent(Content).exists()).toBe(false);for(const mock of [api.version,api.source,api.current,api.retention])expect(mock).not.toHaveBeenCalled()})
it('authorized check, update and retention keep the captured team',async()=>{await render();await vm().runCheck();await vm().runUpdate();await vm().saveRetention();expect(api.check).toHaveBeenCalledWith('github',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));expect(api.start).toHaveBeenCalledWith(expect.objectContaining({target:'2.4.0',source:'github'}),expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));expect(api.retentionSave).toHaveBeenCalledWith(expect.anything(),expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}))})
it('revoked methods cannot dispatch check, update or retention writes',async()=>{await render();await vm().runCheck();const old=vm();allow=false;usePermissionStore().invalidate();await flushPromises();api.check.mockClear();await old.runCheck();await old.runUpdate();await old.saveRetention();expect(api.check).not.toHaveBeenCalled();expect(api.start).not.toHaveBeenCalled();expect(api.retentionSave).not.toHaveBeenCalled();expect(wrapper!.findComponent(Content).exists()).toBe(false)})
it('an old check response cannot populate a new employee page',async()=>{await render();let resolve!:(x:unknown)=>void;api.check.mockImplementationOnce(()=>new Promise(r=>{resolve=r}));const pending=vm().runCheck();await flushPromises();const options=api.check.mock.calls[0]![1];user('other');await flushPromises();resolve({source:'github',latest:'old-user-result',updateAvailable:true});await pending;await flushPromises();expect(options.signal.aborted).toBe(true);expect(wrapper!.text()).not.toContain('old-user-result')})
it('pending original-request recovery is separated by employee identity',async()=>{sessionStorage.setItem('pipewright.update.pending.u',JSON.stringify({requestId:'a'.repeat(32),target:'2.4.0',source:'github',taskId:'task-u'}));await render();expect(api.get).toHaveBeenCalledWith('task-u',expect.anything());api.get.mockClear();user('other');await flushPromises();expect(api.get).not.toHaveBeenCalled();expect(api.start).not.toHaveBeenCalled();expect(api.resume).not.toHaveBeenCalled()})

it('clicks check upgrade and retention only under the active system authorization', async () => {
  const w=await render();
  const find=(message:string)=>w.findAll('button').find(button=>button.text()===t(message))!;
  const check=find('settingsSystem.checkUpdate');
  await check.trigger('click');
  await flushPromises();
  const upgrade=find('settingsSystem.updateNow');
  const retention=find('settingsSystem.retentionSave');
  expect(upgrade).toBeDefined();
  expect(retention).toBeDefined();
  await upgrade.trigger('click');
  await flushPromises();
  await retention.trigger('click');
  await flushPromises();
  const options=expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)});
  expect(api.check).toHaveBeenCalledExactlyOnceWith('github',options);
  expect(api.start).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({target:'2.4.0',source:'github'}),options);
  expect(api.retentionSave).toHaveBeenCalledExactlyOnceWith(expect.anything(),options);
  allow=false;
  usePermissionStore().invalidate();
  for(const old of [check,upgrade,retention]) old.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  await flushPromises();
  expect(api.check).toHaveBeenCalledTimes(1);
  expect(api.start).toHaveBeenCalledTimes(1);
  expect(api.retentionSave).toHaveBeenCalledTimes(1);
});

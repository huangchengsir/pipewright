import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createI18n} from 'vue-i18n'
import {afterEach,beforeEach,describe,it,expect,vi} from 'vitest'
import ServerCard from './ServerCard.vue'
import ReverseProxyPanel from './ReverseProxyPanel.vue'
import Containers from '../../views/Containers.vue'
import {listServers} from '../../api/servers'
import ContainerInspectModal from './ContainerInspectModal.vue'
import AiDiagnosisModal from './AiDiagnosisModal.vue'
import ContainerLogsDrawer from './ContainerLogsDrawer.vue'
import {getServerLogs,subscribeServerLogs} from '../../api/servers'
import CreateContainerModal from './CreateContainerModal.vue'
import {usePermissionStore} from '../../stores/permissions'
import {useSessionStore} from '../../stores/session'
import {access} from '../../api/access'
import * as api from '../../api/containers'
import {serviceAction} from '../../api/servers'
const {confirm}=vi.hoisted(()=>({confirm:vi.fn()}))
vi.mock('../../composables/useConfirm',()=>({useConfirm:()=>({open:confirm})}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:vi.fn(),error:vi.fn(),warn:vi.fn()})}))
vi.mock('vue-router',()=>({useRouter:()=>({resolve:()=>({href:'/terminal'})})}))
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/servers',()=>({listServers:vi.fn(),serviceAction:vi.fn(),getServerLogs:vi.fn(),subscribeServerLogs:vi.fn()}))
vi.mock('../../api/aiOps',()=>({generateCompose:vi.fn(),suggestCommand:vi.fn()}))
vi.mock('../../api/containers',()=>({getAllContainers:vi.fn(),getServerContainerStats:vi.fn(),getServerImages:vi.fn(),pullImage:vi.fn(),removeImage:vi.fn(),getServerStacks:vi.fn(),getStackDetail:vi.fn(),saveStackCompose:vi.fn(),deployStack:vi.fn(),stackAction:vi.fn(),getServerVolumes:vi.fn(),createVolume:vi.fn(),removeVolume:vi.fn(),getServerNetworks:vi.fn(),createNetwork:vi.fn(),removeNetwork:vi.fn(),getNetworkContainers:vi.fn(),connectNetwork:vi.fn(),disconnectNetwork:vi.fn(),createContainer:vi.fn(),getContainerInspect:vi.fn(),diagnoseContainer:vi.fn()}))
let allowed=new Set<string>()
let wrappers:ReturnType<typeof mount>[]=[]
const i18n=()=>createI18n({legacy:false,locale:'en',messages:{en:{}},missingWarn:false,fallbackWarn:false})
const group={serverId:'server',reachable:true,runtime:'docker',error:'',total:1,running:1,collectedAt:'',containers:[{id:'cid',names:'app',image:'image',state:'running' as const,status:'running',ports:'',createdAt:''}]}
beforeEach(()=>{
 setActivePinia(createPinia());vi.clearAllMocks();allowed=new Set(['container.inspect'])
 useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('a')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:1,allowed:queries.map(q=>team==='a' && q.resourceId==='server' && allowed.has(q.action))}))
 vi.mocked(api.getServerContainerStats).mockResolvedValue({...group,stats:[]})
 vi.mocked(api.getServerImages).mockResolvedValue({...group,images:[]})
 vi.mocked(api.getServerStacks).mockResolvedValue({...group,stacks:[]})
 vi.mocked(api.getServerVolumes).mockResolvedValue({...group,volumes:[]})
 vi.mocked(api.getServerNetworks).mockResolvedValue({...group,networks:[]})
 confirm.mockResolvedValue(true)
})
afterEach(()=>{wrappers.forEach(w=>w.unmount());wrappers=[]})
function card(){const w=mount(ServerCard,{props:{group,name:'server',host:'host',stateFilter:'all',search:'',bulkMode:false,selectedSet:new Set<string>()},global:{plugins:[i18n()],stubs:{ReverseProxyPanel:true,NIcon:true}}});wrappers.push(w);return w}
describe('container controls use resource permissions',()=>{
 it('keeps inspect/logs but hides operation, terminal and model buttons independently',async()=>{
  const w=card();await flushPromises()
  expect(w.find('[title="opsServer.card.inspectTitle"]').exists()).toBe(true)
  expect(w.find('[title="opsServer.card.logsTitle"]').exists()).toBe(true)
  expect(w.find('[title="opsServer.card.terminalTitle"]').exists()).toBe(false)
  expect(w.find('[title="opsServer.card.aiDiagnoseTitle"]').exists()).toBe(false)
  expect(w.find('.crow__actions .op--default').exists()).toBe(false)
  allowed.add('container.terminal');usePermissionStore().invalidate();await flushPromises()
  await w.get('[title="opsServer.card.terminalTitle"]').trigger('click');expect(w.emitted('terminal')).toHaveLength(1)
  usePermissionStore().setTeam('b');await flushPromises();expect(w.find('[title="opsServer.card.terminalTitle"]').exists()).toBe(false)
 })
 it('rechecks an operation after confirmation, with zero API writes after revocation',async()=>{
  allowed.add('container.operate');const w=card();await flushPromises()
  let answer!:(ok:boolean)=>void;confirm.mockReturnValue(new Promise(resolve=>{answer=resolve}))
  await w.get('.crow__actions .op--default').trigger('click');await flushPromises()
  allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises();answer(true);await flushPromises()
  expect(serviceAction).not.toHaveBeenCalled();expect(w.find('.crow__actions .op--default').exists()).toBe(false)
 })
 it('guards Enter-key image pull as well as the rendered button',async()=>{
  const w=card();await flushPromises();await w.findAll('[role="tab"]')[1]!.trigger('click');await flushPromises()
  await w.get('.pull__in').setValue('nginx:stable');await w.get('.pull__in').trigger('keyup.enter');expect(api.pullImage).not.toHaveBeenCalled()
  allowed.add('container.operate');usePermissionStore().invalidate();await flushPromises();vi.mocked(api.pullImage).mockResolvedValue({ok:true,error:''} as Awaited<ReturnType<typeof api.pullImage>>)
  await w.get('.pull__in').trigger('keyup.enter');await flushPromises();expect(api.pullImage).toHaveBeenCalledTimes(1)
 })
 it('checks selected server independently in the create dialog',async()=>{
  const w=mount(CreateContainerModal,{props:{servers:[{id:'server',name:'A'},{id:'other',name:'B'}]},global:{plugins:[i18n()]}});wrappers.push(w);await flushPromises()
  expect(w.find('.btn--primary').exists()).toBe(false)
  allowed.add('container.operate');usePermissionStore().invalidate();await flushPromises();expect(w.find('.btn--primary').exists()).toBe(true)
  await w.get('select').setValue('other');await flushPromises();expect(w.find('.btn--primary').exists()).toBe(false);expect(api.createContainer).not.toHaveBeenCalled()
 })
})

it('clears revealed container values and discards old identity responses',async()=>{
 allowed.add('container.reveal')
 const info={serverId:'server',containerId:'cid',reachable:true,env:['KEY=private-value'],labels:{},mounts:[],networks:[],ports:[]} as Awaited<ReturnType<typeof api.getContainerInspect>>
 vi.mocked(api.getContainerInspect).mockResolvedValue(info)
 const w=mount(ContainerInspectModal,{props:{serverId:'server',containerName:'app'},global:{plugins:[i18n()]}});wrappers.push(w);await flushPromises()
 expect(w.text()).toContain('private-value')
 let finish!:(value:typeof info)=>void;vi.mocked(api.getContainerInspect).mockReturnValue(new Promise(resolve=>{finish=resolve}))
 allowed.delete('container.reveal');usePermissionStore().invalidate();await flushPromises();expect(w.text()).not.toContain('private-value')
 usePermissionStore().setTeam('b');await flushPromises();finish(info);await flushPromises();expect(w.text()).not.toContain('private-value')
})
it('stops a live log stream and drops buffered callbacks after revocation',async()=>{
 const stop=vi.fn();vi.mocked(subscribeServerLogs).mockReturnValue(stop)
 vi.mocked(getServerLogs).mockResolvedValue({lines:[{text:'private-log'}]} as Awaited<ReturnType<typeof getServerLogs>>)
 const w=mount(ContainerLogsDrawer,{props:{serverId:'server',containerName:'app'},global:{plugins:[i18n()]}});wrappers.push(w);await flushPromises();expect(w.text()).toContain('private-log')
 const button=w.findAll('button').find(b=>b.text().includes('opsContainer.logs.follow'));await button!.trigger('click')
 const callback=vi.mocked(subscribeServerLogs).mock.calls[0]![2]
 allowed.clear();usePermissionStore().invalidate();await flushPromises();callback.onLine?.('late-private-log');await flushPromises()
 expect(stop).toHaveBeenCalled();expect(w.text()).not.toContain('private-log');expect(w.text()).not.toContain('late-private-log')
})
it('drops a model result after logout and cannot copy or rerun it',async()=>{
 allowed.add('container.diagnose');let finish!:(value:Awaited<ReturnType<typeof api.diagnoseContainer>>)=>void
 vi.mocked(api.diagnoseContainer).mockReturnValue(new Promise(resolve=>{finish=resolve}))
 const w=mount(AiDiagnosisModal,{props:{serverId:'server',containerName:'app'},global:{plugins:[i18n()]}});wrappers.push(w);await flushPromises();expect(api.diagnoseContainer).toHaveBeenCalledTimes(1)
 useSessionStore().clearSession();finish({status:'ready',reason:'',confidence:'high',generatedAt:'2026-11-13T00:00:00Z',hypothesis:'private-model-result',fixScript:'private-script',fixSuggestions:[],alternateCauses:[],evidence:[]} as Awaited<ReturnType<typeof api.diagnoseContainer>>);await flushPromises()
 expect(w.text()).not.toContain('private-model-result');expect(w.find('.copy').exists()).toBe(false);expect(w.find('.btn--ghost').exists()).toBe(false)
})

it('does not retarget a pending confirmed operation to another server',async()=>{allowed.add('container.operate');const w=card();await flushPromises();let answer!:(v:boolean)=>void;confirm.mockReturnValue(new Promise(r=>answer=r));await w.get('.crow__actions .op--default').trigger('click');vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'u',teamId:team,modeEpoch:1,revision:2,catalogVersion:1,allowed:qs.map(()=>true)}));await w.setProps({group:{...group,serverId:'other'}});await flushPromises();answer(true);await flushPromises();expect(serviceAction).not.toHaveBeenCalled()});
it('sends the fixed team and abort signal with a successful container action',async()=>{allowed.add('container.operate');vi.mocked(serviceAction).mockResolvedValue({serverId:'server',type:'docker',target:'app',action:'restart',ok:true,output:'',error:''});const w=card();await flushPromises();await w.get('.crow__actions .op--default').trigger('click');await flushPromises();expect(serviceAction).toHaveBeenCalledWith('server',expect.objectContaining({type:'docker',target:'app'}),expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}))});

it('discards late image data after target changes',async()=>{let done!:(v:Awaited<ReturnType<typeof api.getServerImages>>)=>void;vi.mocked(api.getServerImages).mockReturnValue(new Promise(r=>done=r));const w=card();await flushPromises();await w.findAll('[role="tab"]')[1]!.trigger('click');await flushPromises();await w.setProps({group:{...group,serverId:'other'}});done({...group,images:[{id:'image',repository:'old-private',tag:'secret',createdSince:'',size:''}]});await flushPromises();expect(w.text()).not.toContain('old-private')});
it('does not resume a pending confirmation after unmount',async()=>{allowed.add('container.operate');const w=card();await flushPromises();let answer!:(v:boolean)=>void;confirm.mockReturnValue(new Promise(r=>answer=r));await w.get('.crow__actions .op--default').trigger('click');w.unmount();answer(true);await flushPromises();expect(serviceAction).not.toHaveBeenCalled()});

it('recreates real server cards and permits fresh actions after employee replacement',async()=>{
 allowed.add('container.operate');vi.mocked(listServers).mockResolvedValue([]);vi.mocked(api.getAllContainers).mockResolvedValue([group]);vi.mocked(serviceAction).mockResolvedValue({serverId:'server',type:'docker',target:'app',action:'restart',ok:true,output:'',error:''});
 vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({teamId:team,userId:useSessionStore().user!.userId!,catalogVersion:1,modeEpoch:1,revision:1,allowed:qs.map(q=>allowed.has(q.action))}));
 const w=mount(Containers,{global:{plugins:[i18n()],stubs:{ReverseProxyPanel:true,NIcon:true,ContainerLogsDrawer:true,CreateContainerModal:true,AiDiagnosisModal:true,ContainerAiPanel:true,ContainerInspectModal:true,SystemPruneModal:true}}});wrappers.push(w);for(let i=0;i<4;i++)await flushPromises();
 const previous=w.getComponent(ServerCard).vm;useSessionStore().setUser({username:'new',userId:'new',multiTenant:true});for(let i=0;i<4;i++)await flushPromises();expect(w.getComponent(ServerCard).vm).not.toBe(previous);expect(w.find('.crow__actions .op--default').exists()).toBe(true);await w.get('.crow__actions .op--default').trigger('click');await flushPromises();expect(serviceAction).toHaveBeenCalledWith('server',expect.objectContaining({target:'app'}),expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));
});
it('does not place a late network A connect refresh under network B',async()=>{
 allowed.add('container.operate');vi.mocked(api.getServerNetworks).mockResolvedValue({...group,networks:['A','B'].map(name=>({id:name,name,driver:'bridge',scope:'local'}))});
 const result=(network:string,name:string):api.NetworkContainers=>({serverId:'server',network,reachable:true,error:'',containers:name?[{id:name,name,ipv4:''}]:[]});let finish!:(v:api.NetworkContainers)=>void;
 vi.mocked(api.getNetworkContainers).mockResolvedValueOnce(result('A','')).mockImplementationOnce(()=>new Promise(r=>finish=r)).mockResolvedValueOnce(result('B','B-only'));
 vi.mocked(api.connectNetwork).mockResolvedValue({ok:true} as Awaited<ReturnType<typeof api.connectNetwork>>);
 const w=card();await flushPromises();await w.findAll('[role="tab"]')[4]!.trigger('click');await flushPromises();await w.findAll('.netexp')[0]!.trigger('click');await flushPromises();await w.get('.connadd__sel').setValue('app');await w.get('.connadd button').trigger('click');await flushPromises();await w.findAll('.netexp')[1]!.trigger('click');await flushPromises();expect(w.get('.netdetail').text()).toContain('B-only');finish(result('A','A-only'));await flushPromises();expect(w.get('.netdetail').text()).toContain('B-only');expect(w.get('.netdetail').text()).not.toContain('A-only');
});
it.each(['disconnect','read-error'] as const)('keeps B details after late A %s',async(kind)=>{
 allowed.add('container.operate');vi.mocked(api.getServerNetworks).mockResolvedValue({...group,networks:['A','B'].map(name=>({id:name,name,driver:'bridge',scope:'local'}))});const result=(network:string):api.NetworkContainers=>({serverId:'server',network,reachable:true,error:'',containers:[{id:'same-container',name:'app',ipv4:''}]});let finish!:(v:never)=>void;
 if(kind==='read-error')vi.mocked(api.getNetworkContainers).mockImplementationOnce(()=>new Promise(r=>finish=r)).mockResolvedValueOnce(result('B'));else{vi.mocked(api.getNetworkContainers).mockResolvedValueOnce(result('A')).mockResolvedValueOnce(result('B'));vi.mocked(api.disconnectNetwork).mockImplementationOnce(()=>new Promise(r=>finish=r))}
 const w=card();await flushPromises();await w.findAll('[role="tab"]')[4]!.trigger('click');await flushPromises();await w.findAll('.netexp')[0]!.trigger('click');await flushPromises();if(kind==='disconnect'){await w.get('.connrow button').trigger('click');await flushPromises()}await w.findAll('.netexp')[1]!.trigger('click');await flushPromises();expect(w.find('.connrow__name').text()).toBe('app');finish((kind==='disconnect'?{ok:true}:{...result('A'),reachable:false,error:'A private error'}) as never);await flushPromises();expect(w.find('.connrow__name').text()).toBe('app');expect(w.get('.netdetail').text()).not.toContain('A private error');
});
const lifecycleCases=[['running',0,'restart',true],['running',1,'stop',true],['running',2,'pause',false],['running',3,'kill',true],['paused',0,'unpause',false],['exited',0,'start',false],['exited',1,'rm',true]] as const;
it.each(lifecycleCases)('clicks %s lifecycle %s (%s) with permit, revoke and cancel boundaries',async(state,index,action,danger)=>{
 for(const mode of ['allow','revoke',...(danger?['cancel']:[])]){
  allowed.add('container.operate');confirm.mockResolvedValue(mode!=='cancel');vi.mocked(serviceAction).mockClear();vi.mocked(serviceAction).mockResolvedValue({ok:true} as Awaited<ReturnType<typeof serviceAction>>);const w=card();await w.setProps({group:{...group,containers:[{...group.containers[0]!,state}]}});await flushPromises();const button=w.findAll('.crow__actions button')[index]!;
  if(mode==='revoke'){allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}
  await button.trigger('click');await flushPromises();if(mode==='allow')expect(serviceAction).toHaveBeenCalledWith('server',expect.objectContaining({action,target:'app'}),expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));else expect(serviceAction).not.toHaveBeenCalled();w.unmount();
 }
});
it.each(['update','start','restart','stop','down'] as const)('clicks stack %s with target authorization and cancellation',async(action)=>{
 for(const mode of ['allow','revoke',...(action==='start'?[]:['cancel'])]){
  allowed.add('container.operate');confirm.mockResolvedValue(mode!=='cancel');vi.mocked(api.stackAction).mockClear();vi.mocked(api.stackAction).mockResolvedValue({ok:true} as Awaited<ReturnType<typeof api.stackAction>>);vi.mocked(api.getServerStacks).mockResolvedValue({...group,stacks:[{name:'stack-a',status:'running',configFiles:'/synthetic/compose.yml'}]});const w=card();await flushPromises();await w.findAll('[role="tab"]')[2]!.trigger('click');await flushPromises();const button=w.findAll('.srow__act button')[['update','start','restart','stop','down'].indexOf(action)]!;
  if(mode==='revoke'){allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}await button.trigger('click');await flushPromises();if(mode==='allow')expect(api.stackAction).toHaveBeenCalledWith('server','stack-a',action,'/synthetic/compose.yml',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));else expect(api.stackAction).not.toHaveBeenCalled();w.unmount();
 }
});
it.each([['image',1],['volume',3],['network',4]] as const)('clicks %s removal with fixed resource, cancel and revoked stale button',async(kind,tab)=>{
 for(const mode of ['allow','cancel','revoke']){
  allowed.add('container.operate');confirm.mockResolvedValue(mode!=='cancel');const fn=kind==='image'?api.removeImage:kind==='volume'?api.removeVolume:api.removeNetwork;vi.mocked(fn).mockClear();vi.mocked(fn).mockResolvedValue({serverId:'server',action:'synthetic',name:'synthetic',image:'synthetic',ok:true,output:'',error:''} as api.ImageActionResult & api.VolNetActionResult);
  vi.mocked(api.getServerImages).mockResolvedValue({...group,images:[{id:'image-a',repository:'repo',tag:'tag',createdSince:'',size:''}]});vi.mocked(api.getServerVolumes).mockResolvedValue({...group,volumes:[{name:'volume-a',driver:'local'}]});vi.mocked(api.getServerNetworks).mockResolvedValue({...group,networks:[{id:'network-a',name:'custom-a',driver:'bridge',scope:'local'}]});const w=card();await flushPromises();await w.findAll('[role="tab"]')[tab]!.trigger('click');await flushPromises();const button=w.get(kind==='image'?'.irow__act button':'.srow__act button');if(mode==='revoke'){allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}await button.trigger('click');await flushPromises();if(mode==='allow'){expect(fn).toHaveBeenCalledOnce();expect(vi.mocked(fn).mock.calls[0]![0]).toBe('server');expect(vi.mocked(fn).mock.calls[0]!.at(-1)).toMatchObject({headers:{'X-Pipewright-Team':'a'}})}else expect(fn).not.toHaveBeenCalled();w.unmount();
 }
});
it.each([['image',1],['volume',3],['network',4]] as const)('clicks %s creation/pull button and rejects retained revoked button',async(kind,tab)=>{
 for(const mode of ['allow','revoke']){
  allowed.add('container.operate');const fn=kind==='image'?api.pullImage:kind==='volume'?api.createVolume:api.createNetwork;vi.mocked(fn).mockClear();vi.mocked(fn).mockResolvedValue({serverId:'server',action:'synthetic',name:'synthetic',image:'synthetic',ok:true,output:'',error:''} as api.ImageActionResult & api.VolNetActionResult);const w=card();await flushPromises();await w.findAll('[role="tab"]')[tab]!.trigger('click');await flushPromises();await w.get('.pull__in').setValue('synthetic-new');const button=w.get('.pull .pull__btn');if(mode==='revoke'){allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}await button.trigger('click');await flushPromises();if(mode==='allow')expect(fn).toHaveBeenCalledWith('server','synthetic-new',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));else expect(fn).not.toHaveBeenCalled();w.unmount();
 }
});
it.each(['deploy','save'] as const)('clicks compose %s with allowed and revoked controls',async(kind)=>{
 for(const mode of ['allow','revoke',...(kind==='save'?['cancel']:[])]){
 allowed.add('container.operate');allowed.add('container.reveal');confirm.mockResolvedValue(mode!=='cancel');const fn=kind==='save'?api.saveStackCompose:api.deployStack;vi.mocked(fn).mockClear();vi.mocked(fn).mockResolvedValue({ok:true} as Awaited<ReturnType<typeof fn>>);vi.mocked(api.getServerStacks).mockResolvedValue({...group,stacks:[{name:'stack-a',status:'running',configFiles:'/synthetic/compose.yml'}]});vi.mocked(api.getStackDetail).mockResolvedValue({...group,name:'stack-a',services:[],configFiles:'/synthetic/compose.yml',compose:'services: {}',composeSource:'file',editable:true});const w=card();await flushPromises();await w.findAll('[role="tab"]')[2]!.trigger('click');await flushPromises();if(kind==='save'){await w.get('.srow__toggle').trigger('click');await flushPromises()}else{await w.get('.img-toolbar .pull__btn').trigger('click');await w.get('.deploy__name').setValue('stack-a')}await w.get('.deploy__yaml').setValue('services: {new: {image: synthetic}}');const button=w.get('.deploy__act button');if(mode==='revoke'){allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}await button.trigger('click');await flushPromises();if(mode==='allow'){expect(fn).toHaveBeenCalledOnce();expect(vi.mocked(fn).mock.calls[0]!.slice(0,3)).toEqual(['server','stack-a','services: {new: {image: synthetic}}']);expect(vi.mocked(fn).mock.calls[0]!.at(-1)).toMatchObject({headers:{'X-Pipewright-Team':'a'}})}else expect(fn).not.toHaveBeenCalled();w.unmount();
 }
});
it('clicks stats and every resource list refresh with fixed context',async()=>{
 const w=card();await flushPromises();const count=vi.mocked(api.getServerContainerStats).mock.calls.length;await w.get('.cstats-bar button').trigger('click');await flushPromises();expect(api.getServerContainerStats).toHaveBeenCalledTimes(count+1);
 for(const [tab,fn] of [[1,api.getServerImages],[2,api.getServerStacks],[3,api.getServerVolumes],[4,api.getServerNetworks]] as const){await w.findAll('[role="tab"]')[tab]!.trigger('click');await flushPromises();const before=vi.mocked(fn).mock.calls.length;await w.get('.refresh').trigger('click');await flushPromises();expect(fn).toHaveBeenCalledTimes(before+1);expect(fn).toHaveBeenLastCalledWith('server',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}))}
});
it('clicks AI compose generation and denies retained button after diagnose revocation',async()=>{
 const {generateCompose}=await import('../../api/aiOps');allowed.add('container.operate');allowed.add('container.diagnose');vi.mocked(generateCompose).mockResolvedValue({available:true,yaml:'services: {synthetic: {image: test}}'});const w=card();await flushPromises();await w.findAll('[role="tab"]')[2]!.trigger('click');await flushPromises();await w.get('.img-toolbar .pull__btn').trigger('click');await w.get('.deploy__ainl').setValue('synthetic service');const b=w.get('.deploy__ai button');await b.trigger('click');await flushPromises();expect(generateCompose).toHaveBeenCalledWith('synthetic service');expect((w.get('.deploy__yaml').element as HTMLTextAreaElement).value).toContain('synthetic');vi.mocked(generateCompose).mockClear();allowed.delete('container.diagnose');usePermissionStore().invalidate();await flushPromises();await b.trigger('click');expect(generateCompose).not.toHaveBeenCalled();
});
it.each(['connect-revoke','disconnect-cancel','disconnect-revoke'] as const)('checks network %s actual control boundary',async(mode)=>{
 allowed.add('container.operate');vi.mocked(api.getServerNetworks).mockResolvedValue({...group,networks:[{id:'net-a',name:'A',driver:'bridge',scope:'local'}]});vi.mocked(api.getNetworkContainers).mockResolvedValue({serverId:'server',network:'A',reachable:true,error:'',containers:mode==='connect-revoke'?[]:[{id:'cid',name:'app',ipv4:''}]});vi.mocked(api.connectNetwork).mockClear();vi.mocked(api.disconnectNetwork).mockClear();const w=card();await flushPromises();await w.findAll('[role="tab"]')[4]!.trigger('click');await flushPromises();await w.get('.netexp').trigger('click');await flushPromises();if(mode==='connect-revoke')await w.get('.connadd__sel').setValue('app');const b=w.get(mode==='connect-revoke'?'.connadd button':'.connrow button');if(mode==='disconnect-cancel')confirm.mockResolvedValue(false);else{allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises()}await b.trigger('click');await flushPromises();expect(api.connectNetwork).not.toHaveBeenCalled();expect(api.disconnectNetwork).not.toHaveBeenCalled();
});
it.each([['inspect','inspectTitle','container.inspect'],['diagnose','aiDiagnoseTitle','container.diagnose'],['logs','logsTitle','container.inspect']] as const)('clicks parent %s with exact container and hides denied entry',async(event,title,action)=>{
 allowed.add(action);const w=card();await flushPromises();const selector=`[title="opsServer.card.${title}"]`;const button = w.get(selector);
 await button.trigger('click');
 const args = event === 'diagnose' ? [group.containers[0], button.element] : [group.containers[0]];
 expect(w.emitted(event)).toEqual([args]);
 allowed.delete(action);usePermissionStore().invalidate();await flushPromises();expect(w.find(selector).exists()).toBe(false);expect(w.emitted(event)).toHaveLength(1);
});
it('clicks containers tab to refresh only with inspect permission',async()=>{
 const w=card();await flushPromises();await w.findAll('[role="tab"]')[1]!.trigger('click');await flushPromises();vi.mocked(api.getServerContainerStats).mockClear();await w.findAll('[role="tab"]')[0]!.trigger('click');await flushPromises();expect(api.getServerContainerStats).toHaveBeenCalledWith('server',expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));
 allowed.delete('container.inspect');usePermissionStore().invalidate();await flushPromises();await w.findAll('[role="tab"]')[1]!.trigger('click');vi.mocked(api.getServerContainerStats).mockClear();await w.findAll('[role="tab"]')[0]!.trigger('click');await flushPromises();expect(api.getServerContainerStats).not.toHaveBeenCalled();
});
it('clicks domains tab and forwards the fixed server to its permission-owning panel',async()=>{
 const w=card();await flushPromises();expect(w.find('reverse-proxy-panel-stub').exists()).toBe(false);await w.findAll('[role="tab"]')[5]!.trigger('click');await flushPromises();expect(w.getComponent(ReverseProxyPanel).props('serverId')).toBe('server');expect(w.getComponent(ReverseProxyPanel).props('host')).toBe('host');expect(w.emitted('inspect')).toBeUndefined();expect(serviceAction).not.toHaveBeenCalled();
});
it('changes bulk selection with exact name and hides selection without operate permission',async()=>{
 allowed.add('container.operate');const w=card();await w.setProps({bulkMode:true});await flushPromises();await w.get('.crow__check input').setValue(true);expect(w.emitted('toggle-select')).toEqual([['app']]);await w.setProps({selectedSet:new Set(['app'])});expect((w.get('.crow__check input').element as HTMLInputElement).checked).toBe(true);await w.get('.crow__check input').setValue(false);expect(w.emitted('toggle-select')).toEqual([['app'],['app']]);allowed.delete('container.operate');usePermissionStore().invalidate();await flushPromises();expect(w.find('.crow__check input').exists()).toBe(false);expect(w.emitted('toggle-select')).toHaveLength(2);
});

it('rejects stale inspect logs and bulk controls before DOM removal', async () => {
  allowed.add('container.operate')
  const w = card()
  await w.setProps({bulkMode: true})
  await flushPromises()
  const inspect = w.get('[title="opsServer.card.inspectTitle"]').element
  const logs = w.get('[title="opsServer.card.logsTitle"]').element
  const select = w.get('.crow__check input').element
  const p = usePermissionStore()
  p.reject('a', {action: 'container.inspect', resourceId: 'server'})
  p.reject('a', {action: 'container.operate', resourceId: 'server'})
  inspect.dispatchEvent(new Event('click', {bubbles: true}))
  logs.dispatchEvent(new Event('click', {bubbles: true}))
  select.dispatchEvent(new Event('change', {bubbles: true}))
  expect(w.emitted('inspect')).toBeUndefined()
  expect(w.emitted('logs')).toBeUndefined()
  expect(w.emitted('toggle-select')).toBeUndefined()
})

it('cannot save old stack content if reveal is withdrawn during confirmation', async () => {
  allowed.add('container.operate')
  allowed.add('container.reveal')
  vi.mocked(api.getServerStacks).mockResolvedValue({...group, stacks: [{name: 'stack-a', status: 'running', configFiles: '/synthetic/compose.yml'}]})
  vi.mocked(api.getStackDetail).mockResolvedValue({...group, name: 'stack-a', services: [], configFiles: '/synthetic/compose.yml', compose: 'services: {}', composeSource: 'file', editable: true})
  let confirmSave!: (answer: boolean) => void
  confirm.mockReturnValue(new Promise(resolve => { confirmSave = resolve }))
  const w = card()
  await flushPromises()
  await w.findAll('[role="tab"]')[2]!.trigger('click')
  await flushPromises()
  await w.get('.srow__toggle').trigger('click')
  await flushPromises()
  await w.get('.deploy__yaml').setValue('services: {fixed: {image: fixture}}')
  await w.get('.deploy__act button').trigger('click')
  expect(confirm).toHaveBeenCalledTimes(1)
  usePermissionStore().reject('a', {action: 'container.reveal', resourceId: 'server'})
  confirmSave(true)
  await flushPromises()
  expect(api.saveStackCompose).not.toHaveBeenCalled()
  expect(w.find('.deploy__yaml').exists()).toBe(false)
})

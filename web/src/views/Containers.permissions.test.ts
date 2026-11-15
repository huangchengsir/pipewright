import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import Containers from './Containers.vue'
import {getAllContainers,type ServerContainers} from '../api/containers'
import {listServers,serviceAction} from '../api/servers'
import {access} from '../api/access'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
vi.mock('vue-router',()=>({useRouter:()=>({resolve:()=>({href:'/terminal'})})}))
vi.mock('../api/containers',()=>({getAllContainers:vi.fn()}))
vi.mock('../api/servers',()=>({listServers:vi.fn(),serviceAction:vi.fn()}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../composables/useToast',()=>({useToast:()=>({success:vi.fn(),error:vi.fn(),warn:vi.fn()})}))
const confirmation=vi.hoisted(()=>vi.fn());
vi.mock('../composables/useConfirm',()=>({useConfirm:()=>({open:confirmation})}))
const releases:Array<()=>void>=[]
let wrapper:ReturnType<typeof mount>|undefined
const group=(id:string):ServerContainers=>({serverId:id,reachable:true,runtime:'docker',error:'',total:1,running:1,collectedAt:'',containers:[{id:'cid',names:'private-'+id,image:'img',state:'running',status:'running',ports:'',createdAt:''}]})
beforeEach(()=>{
 setActivePinia(createPinia());vi.clearAllMocks();useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('a')
 vi.mocked(listServers).mockResolvedValue([])
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:1,allowed:queries.map(q=>q.action==='container.inspect' && q.resourceId===team)}))
})
afterEach(()=>{releases.splice(0).forEach(release=>release());wrapper?.unmount();wrapper=undefined})
function render(){ wrapper=mount(Containers,{global:{stubs:{ServerCard:{props:['group'],emits:['toggle-select'],template:'<div class="server-card">{{group.containers[0].names}}<button class="pick" @click="$emit(\'toggle-select\',group.containers[0].names)">pick</button></div>'},ContainerLogsDrawer:true,CreateContainerModal:true,AiDiagnosisModal:true,ContainerAiPanel:true,ContainerInspectModal:true,SystemPruneModal:true}}});return wrapper }
it('discards old-team inventory responses and clears cards immediately on team/logout',async()=>{
 let finish!:(items:ServerContainers[])=>void
 vi.mocked(getAllContainers).mockReturnValueOnce(new Promise(resolve=>{finish=resolve}))
 const w=render();await flushPromises()
 vi.mocked(getAllContainers).mockResolvedValue([group('b')]);usePermissionStore().setTeam('b');await flushPromises()
 expect(w.text()).toContain('private-b')
 finish([group('a')]);await flushPromises();expect(w.text()).not.toContain('private-a');expect(w.text()).toContain('private-b')
 useSessionStore().clearSession();await flushPromises();expect(w.find('.server-card').exists()).toBe(false);expect(w.text()).not.toContain('private-b')
})
it('removes stale cards/counts on failed refresh and hides operation modals for inspect-only users',async()=>{
 vi.mocked(getAllContainers).mockResolvedValue([group('a')]);const w=render();await flushPromises();expect(w.text()).toContain('private-a')
 expect(w.findAll('button').some(b=>b.text()==='新增容器')).toBe(false)
 vi.mocked(getAllContainers).mockRejectedValue(new Error('offline'))
 const refresh=w.findAll('.header-actions button').find(b=>b.text().includes('刷新'));expect(refresh).toBeDefined();await refresh!.trigger('click');await flushPromises()
 expect(w.find('.server-card').exists()).toBe(false);expect(w.text()).not.toContain('private-a')
})
it('hides an already-loaded server when its resource permission is withdrawn',async()=>{
 vi.mocked(getAllContainers).mockResolvedValue([group('a')]);const w=render();await flushPromises();expect(w.find('.server-card').exists()).toBe(true)
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:2,allowed:queries.map(()=>false)}));usePermissionStore().invalidate();await flushPromises();expect(w.find('.server-card').exists()).toBe(false)
})

async function settleBulk(){for(let i=0;i<4;i++)await flushPromises()}
async function selectBulk(){vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({teamId:team,userId:useSessionStore().user!.userId!,catalogVersion:1,modeEpoch:1,revision:1,allowed:qs.map(()=>true)}));vi.mocked(getAllContainers).mockResolvedValue([group('a'),group('b')]);const w=render();releases.push(...['a','b'].map(id=>usePermissionStore().retain('a',{action:'container.operate',resourceId:id})));await settleBulk();await w.findAll('.header-actions button').find(x=>x.text().includes('批量'))!.trigger('click');await w.findAll('.pick')[0]!.trigger('click');await settleBulk();return w}
it('rejects a bulk confirmation if selection changed to different authorized targets',async()=>{const w=await selectBulk();let answer!:(v:boolean)=>void;confirmation.mockReturnValue(new Promise(r=>answer=r));await w.findAll('.bulk-btn')[1]!.trigger('click');await w.findAll('.pick')[0]!.trigger('click');await w.findAll('.pick')[1]!.trigger('click');await settleBulk();answer(true);await settleBulk();expect(serviceAction).not.toHaveBeenCalled()});
it('uses fixed team context for every confirmed bulk target',async()=>{const w=await selectBulk();confirmation.mockResolvedValue(true);vi.mocked(serviceAction).mockResolvedValue({serverId:'a',type:'docker',target:'private-a',action:'stop',ok:true,output:'',error:''});await w.findAll('.bulk-btn')[1]!.trigger('click');await settleBulk();expect(serviceAction).toHaveBeenCalledWith('a',{type:'docker',target:'private-a',action:'stop'},expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}))});
it('rejects a bulk confirmation after employee changes even when the new employee has authority',async()=>{const w=await selectBulk();let answer!:(v:boolean)=>void;confirmation.mockReturnValue(new Promise(r=>answer=r));await w.findAll('.bulk-btn')[1]!.trigger('click');useSessionStore().setUser({userId:'v',username:'v',multiTenant:true});await settleBulk();answer(true);await settleBulk();expect(serviceAction).not.toHaveBeenCalled()});
it.each(['start','stop','restart','rm'] as const)('clicks bulk %s with fixed selected target and denies cancellation or revocation',async(action)=>{
 for(const mode of ['allow',...(action==='start'?[]:['cancel']),'revoke']){
 const w=await selectBulk();confirmation.mockResolvedValue(mode!=='cancel');vi.mocked(serviceAction).mockClear();vi.mocked(serviceAction).mockResolvedValue({serverId:'a',type:'docker',target:'private-a',action,ok:true,output:'',error:''});const button=w.findAll('.bulk-btn')[['start','stop','restart','rm'].indexOf(action)]!;
 if(mode==='revoke'){vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:2,allowed:qs.map(q=>q.action==='container.inspect')}));usePermissionStore().invalidate();await settleBulk()}await button.trigger('click');await settleBulk();if(mode==='allow')expect(serviceAction).toHaveBeenCalledWith('a',{type:'docker',target:'private-a',action},expect.objectContaining({headers:{'X-Pipewright-Team':'a'}}));else expect(serviceAction).not.toHaveBeenCalled();w.unmount();releases.splice(0).forEach(r=>r());
 }
});
it('clicks all status filters, clears search, selected bulk and exits bulk mode',async()=>{
 const w=await selectBulk();for(const b of w.findAll('.filter-chip')){await b.trigger('click');expect(b.attributes('aria-pressed')).toBe('true');expect(w.findAll('.filter-chip').filter(x=>x.attributes('aria-pressed')==='true')).toHaveLength(1)}await w.get('.search-box__input').setValue('synthetic');await w.get('.search-box__clear').trigger('click');expect((w.get('.search-box__input').element as HTMLInputElement).value).toBe('');await w.get('.bulk-btn--ghost').trigger('click');expect(w.findAll('.bulk-btn')[0]!.attributes('disabled')).toBeDefined();await w.findAll('.header-actions button').find(x=>x.text().includes('批量'))!.trigger('click');expect(w.find('.bulk-btn').exists()).toBe(false);
});
it.each(['AI','清理','新增'] as const)('opens %s header surface and closes only that surface without writes',async(label)=>{
 const w=await selectBulk();const before=vi.mocked(serviceAction).mock.calls.length;const names={AI:'ContainerAiPanel',清理:'SystemPruneModal',新增:'CreateContainerModal'};const b=w.findAll('.header-actions button').find(x=>x.text().includes(label));expect(b).toBeDefined();await b!.trigger('click');await settleBulk();const modal=w.findComponent({name:names[label]});expect(modal.exists()).toBe(true);modal.vm.$emit('close');await settleBulk();expect(w.findComponent({name:names[label]}).exists()).toBe(false);expect(serviceAction).toHaveBeenCalledTimes(before);
});

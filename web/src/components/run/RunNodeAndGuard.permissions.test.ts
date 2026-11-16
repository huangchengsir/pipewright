import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import Guard from './ReleaseGuardPanel.vue'
import Plan from './ReleaseGuardPlan.vue'
import Dag from './RunDagView.vue'
import Steps from './RunStepList.vue'
import {deploymentAttempt,guardReport} from '../../test/releaseGuardFixtures'
import {retryContext} from '../../test/runRetryFixtures'
const m=vi.hoisted(()=>({decisions:vi.fn(),list:vi.fn(),push:vi.fn()}))
vi.mock('../../api/access',()=>({access:{decisions:m.decisions}}))
vi.mock('../../api/releaseGuard',async original=>({...await original<typeof import('../../api/releaseGuard')>(),listDeploymentAttempts:m.list}))
vi.mock('vue-router',()=>({useRouter:()=>({push:m.push})}))
let w:VueWrapper|undefined, grants:Set<string>
async function settle(){for(let n=0;n<4;n++)await flushPromises()}
beforeEach(()=>{
 setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u',multiTenant:true});usePermissionStore().setTeam('team-a');grants=new Set();vi.clearAllMocks()
 m.decisions.mockImplementation(async(team:string,qs:{action:string;resourceId?:string}[])=>({userId:useSessionStore().user?.userId,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>grants.has(q.action+'/'+q.resourceId))}))
 m.list.mockResolvedValue({items:[deploymentAttempt()]})
})
afterEach(()=>{w?.unmount();w=undefined;vi.useRealTimers()})
function renderGuard(){w=mount(Guard,{props:{runId:'run-1',projectId:'project-1'},global:{stubs:{ReleaseGuardPlan:true,RouterLink:true}}});return w}
function fromNow(){return w!.findAll('button').find(b=>b.text()==='从现在观察')}
it('guard reader without ordinary run.read loads only with fixed team and cannot observe',async()=>{
 grants.add('guard.read/run-1');renderGuard();await settle();expect(m.list).toHaveBeenCalledExactlyOnceWith('run-1',expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal),throwOnUnauthorized:true}));expect(w!.find('[data-attempt-id]').exists()).toBe(true);expect(fromNow()).toBeUndefined()
})
it('guard observation permission on another run does not expose this run evidence',async()=>{
 grants.add('guard.read/other-run');grants.add('guard.observe/other-run');renderGuard();await settle();expect(m.list).not.toHaveBeenCalled();expect(w!.find('section').exists()).toBe(false)
})
it.each(['team','employee','permission'] as const)('drops late guard attempts after %s change',async(kind)=>{
 grants.add('guard.read/run-1');let finish!:(v:unknown)=>void;m.list.mockImplementation(()=>new Promise(resolve=>finish=resolve));renderGuard();await settle();const signal=m.list.mock.calls[0][1].signal as AbortSignal
 if(kind==='team')usePermissionStore().setTeam('team-b');else if(kind==='employee')useSessionStore().setUser({username:'other',userId:'other',multiTenant:true});else{grants.clear();usePermissionStore().invalidate()}
 await settle();expect(signal.aborted).toBe(true);finish({items:[deploymentAttempt()]});await settle();expect(w!.find('[data-attempt-id]').exists()).toBe(false)
})
it('closes observation plan on revoke and ignores its late started callback',async()=>{
 grants.add('guard.read/run-1');grants.add('guard.observe/run-1');renderGuard();await settle();await fromNow()!.trigger('click');const plan=w!.getComponent(Plan);grants.delete('guard.observe/run-1');usePermissionStore().invalidate();await settle();expect(w!.findComponent(Plan).exists()).toBe(false);plan.vm.$emit('started',guardReport());await settle();expect(m.push).not.toHaveBeenCalled()
})
it('navigates only for the selected attempt and exact run',async()=>{
 grants.add('guard.read/run-1');grants.add('guard.observe/run-1');renderGuard();await settle();await fromNow()!.trigger('click');const plan=w!.getComponent(Plan);plan.vm.$emit('started',guardReport({runId:'other'}));plan.vm.$emit('started',guardReport({attemptId:'other'}));await settle();expect(m.push).not.toHaveBeenCalled();plan.vm.$emit('started',guardReport());await settle();expect(m.push).toHaveBeenCalledOnce()
})
it('aborts guard polling after identity switch and never rebinds the existing panel',async()=>{
 vi.useFakeTimers();grants.add('guard.read/run-1');renderGuard();await w!.setProps({runActive:true});await settle();const before=m.list.mock.calls.length;usePermissionStore().setTeam('team-b');await settle();await vi.advanceTimersByTimeAsync(12000);expect(m.list).toHaveBeenCalledTimes(before)
})
const step={id:'step-b',name:'B',status:'failed' as const,startedAt:null,finishedAt:null,durationMs:null}
function renderNode(kind:'dag'|'steps',legacy=false){
 const recovery=legacy?undefined:retryContext({attemptRunId:'exact-run'});w=kind==='dag'?mount(Dag,{props:{runId:'exact-run',projectId:'project',runStatus:'failed',steps:[step],recovery}}):mount(Steps,{props:{runId:'exact-run',steps:[step],recovery}});return w
}
it.each(['dag','steps'] as const)('%s only enables retry for the exact attempt',async(kind)=>{
 grants.add('run.retry/other-run');renderNode(kind);await settle();expect(w!.find('.node-retry,.step-retry').exists()).toBe(false)
 grants.add('run.retry/exact-run');usePermissionStore().invalidate();await settle();await w!.get('.node-retry,.step-retry').trigger('click');expect(w!.emitted('retry')).toEqual([[['b']]])
})
it.each(['dag','steps'] as const)('%s retains legacy recovery button under exact run permission',async(kind)=>{
 grants.add('run.retry/exact-run');renderNode(kind,true);await settle();await w!.get('.node-retry,.step-retry').trigger('click');expect(w!.emitted('retry')).toEqual([[[]]])
})
it.each(['dag','steps'] as const)('%s refuses captured retry click after revocation',async(kind)=>{
 grants.add('run.retry/exact-run');renderNode(kind);await settle();const old=w!.get('.node-retry,.step-retry');grants.clear();usePermissionStore().invalidate();await old.trigger('click');await settle();expect(w!.emitted('retry')).toBeUndefined();expect(w!.find('.node-retry,.step-retry').exists()).toBe(false)
})
it.each(['dag','steps'] as const)('%s does not reuse old run controls after team switch even if new team permits',async(kind)=>{
 grants.add('run.retry/exact-run');renderNode(kind);await settle();const old=w!.get('.node-retry,.step-retry');usePermissionStore().setTeam('team-b');await settle();await old.trigger('click');expect(w!.emitted('retry')).toBeUndefined();expect(w!.find('.node-retry,.step-retry').exists()).toBe(false)
})
it.each(['dag','steps'] as const)('%s requires concrete run identity even if navigation permission allows empty resource',async(kind)=>{
 grants.add('run.retry/');renderNode(kind,true);await w!.setProps({runId:undefined});await settle();expect(w!.find('.node-retry,.step-retry').exists()).toBe(false)
})
it.each(['dag','steps'] as const)('%s rejects captured click after component teardown',async(kind)=>{
 grants.add('run.retry/exact-run');renderNode(kind);await settle();const old=w!.get('.node-retry,.step-retry');w!.unmount();await old.trigger('click');expect(w!.emitted('retry')).toBeUndefined()
})

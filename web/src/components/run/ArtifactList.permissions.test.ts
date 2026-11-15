import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import ArtifactList from './ArtifactList.vue'
const m=vi.hoisted(()=>({decisions:vi.fn()}))
vi.mock('../../api/access',()=>({access:{decisions:m.decisions}}))
let w:VueWrapper|undefined, allowed=true
const fetcher=vi.fn(),create=vi.fn(()=> 'blob:test'),click=vi.fn()
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
beforeEach(()=>{
 setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('team-a');allowed=true
 m.decisions.mockImplementation(async(team:string,qs:{action:string}[])=>({userId:'u1',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(q=>allowed&&q.action==='artifact.download')}))
 fetcher.mockReset();create.mockClear();click.mockClear();vi.stubGlobal('fetch',fetcher);vi.stubGlobal('URL',Object.assign(URL,{createObjectURL:create,revokeObjectURL:vi.fn()}));vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(click)
})
afterEach(()=>{w?.unmount();w=undefined;vi.restoreAllMocks();vi.unstubAllGlobals()})
async function render(){w=mount(ArtifactList,{props:{runId:'r1',artifacts:[{id:'a1',type:'archive',name:'result',reference:'sha256:fixture',sizeBytes:4,metadata:{stored:true},createdAt:''}]}});await settle()}
it('hides download for metadata-only readers',async()=>{allowed=false;await render();expect(w!.find('.download-btn').exists()).toBe(false);expect(fetcher).not.toHaveBeenCalled()})
it('downloads with fixed team and abort signal',async()=>{fetcher.mockResolvedValue(new Response('data',{headers:{'content-disposition':'attachment; filename="result.tar.gz"'}}));await render();await w!.get('.download-btn').trigger('click');await settle();expect(fetcher).toHaveBeenCalledWith('/api/runs/r1/artifacts/a1/download',expect.objectContaining({headers:{'X-Pipewright-Team':'team-a'},signal:expect.any(AbortSignal)}));expect(create).toHaveBeenCalledOnce();expect(click).toHaveBeenCalledOnce();expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe('result.tar.gz')})
it.each(['team','employee','permission'] as const)('discards late bytes after %s change',async(kind)=>{
 let finish!:(r:Response)=>void;fetcher.mockImplementation(()=>new Promise<Response>(resolve=>{finish=resolve}));await render();await w!.get('.download-btn').trigger('click');const signal=fetcher.mock.calls[0]![1].signal as AbortSignal
 if(kind==='team')usePermissionStore().setTeam('team-b')
 else if(kind==='employee')useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true,instanceAdmin:false})
 else{allowed=false;usePermissionStore().invalidate()}
 await settle();expect(signal.aborted).toBe(true);finish(new Response('private'));await settle();expect(create).not.toHaveBeenCalled();expect(click).not.toHaveBeenCalled()
})

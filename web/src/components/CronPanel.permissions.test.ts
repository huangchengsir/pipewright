import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest'
import {access} from '../api/access'
import {http,HttpError} from '../api/http'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import CronPanel from './CronPanel.vue'
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/http',async original=>({...await original<typeof import('../api/http')>(),http:{get:vi.fn(),put:vi.fn()}}))
let read=true,edit=true,start=true
const wrappers:VueWrapper[]=[]
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
function render(){const w=mount(CronPanel,{props:{projectId:'p1'},attachTo:document.body});wrappers.push(w);return w}
beforeEach(()=>{
 vi.resetAllMocks();setActivePinia(createPinia());read=true;edit=true;start=true
 useSessionStore().setUser({username:'member',userId:'u1',multiTenant:true});usePermissionStore().setTeam('t1')
 vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:useSessionStore().user!.userId!,teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>q.action==='pipeline.edit'?edit:q.action==='run.start'?start:read)}))
 vi.mocked(http.get).mockResolvedValue({enabled:true,expression:'* * * * *',branch:'main',nextRun:'',authorizationState:'required'})
 vi.mocked(http.put).mockImplementation(async(_url,body)=>({...body as object,nextRun:'',authorizationState:'ready'}))
})
afterEach(()=>{for(const w of wrappers.splice(0))w.unmount();vi.restoreAllMocks()})
describe('scheduled trigger permission boundaries',()=>{
 it('denies configuration reads without issuing a request',async()=>{read=false;const w=render();await settle();expect(http.get).not.toHaveBeenCalled();expect(w.find('section').exists()).toBe(false)})
 it('reader sees required status but cannot edit or save',async()=>{edit=false;const w=render();await settle();expect(w.text()).toContain('授权');for(const control of w.findAll('input'))expect(control.attributes('disabled')).toBeDefined();expect(w.find('.btn-primary').exists()).toBe(false);expect(http.put).not.toHaveBeenCalled()})
 it('requires run.start for enabled schedule and permits disabling with edit',async()=>{start=false;const w=render();await settle();expect(w.get('.btn-primary').attributes('disabled')).toBeDefined();await w.get('.btn-primary').trigger('click');expect(http.put).not.toHaveBeenCalled();await w.get('input[type=checkbox]').setValue(false);await settle();await w.get('.btn-primary').trigger('click');await settle();expect(http.put).toHaveBeenCalledOnce()})
 it('saves only explicitly and keeps captured team options',async()=>{const w=render();await settle();expect(http.put).not.toHaveBeenCalled();await w.get('#cron-expr').setValue('0 2 * * *');await w.get('.btn-primary').trigger('click');await settle();expect(new Headers(vi.mocked(http.put).mock.calls[0]![2]?.headers).get('X-Pipewright-Team')).toBe('t1');expect(http.put).toHaveBeenCalledWith('/api/projects/p1/cron',{expression:'0 2 * * *',branch:'main',enabled:true},expect.objectContaining({signal:expect.any(AbortSignal),throwOnUnauthorized:true}))})
 it('logout aborts reads and discards stale responses',async()=>{let resolve!:(value:unknown)=>void;vi.mocked(http.get).mockReturnValue(new Promise(r=>{resolve=r}));const w=render();await settle();const options=vi.mocked(http.get).mock.calls[0]![1]!;useSessionStore().clearSession();await settle();resolve({enabled:true,expression:'private-stale',branch:'secret',nextRun:''});await settle();expect(options.signal?.aborted).toBe(true);expect(w.find('section').exists()).toBe(false);expect(w.text()).not.toContain('private-stale')})
})

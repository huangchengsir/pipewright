import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import Panel from './SystemPruneModal.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
const m=vi.hoisted(()=>({decisions:vi.fn(),df:vi.fn(),prune:vi.fn(),confirm:vi.fn(),success:vi.fn(),error:vi.fn()}))
vi.mock('../../api/access',()=>({access:{decisions:m.decisions}}))
vi.mock('../../api/containers',()=>({getSystemDf:m.df,systemPrune:m.prune}))
vi.mock('../../composables/useConfirm',()=>({useConfirm:()=>({open:m.confirm})}))
vi.mock('../../composables/useToast',()=>({useToast:()=>({success:m.success,error:m.error})}))
let w:VueWrapper|undefined,grants:Set<string>
const usage={serverId:'s1',reachable:true,dockerAvailable:true,entries:[],error:''}
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
async function open(){w=mount(Panel,{props:{servers:[{id:'s1',name:'First'},{id:'s2',name:'Second'}]}});await settle()}
beforeEach(()=>{vi.clearAllMocks();setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true});usePermissionStore().setTeam('a');grants=new Set(['container.inspect','container.operate']);m.decisions.mockImplementation(async(team,queries)=>({userId:'u1',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map((q:{action:string})=>grants.has(q.action))}));m.df.mockResolvedValue(usage);m.confirm.mockResolvedValue(true);m.prune.mockResolvedValue({ok:true,output:'done',error:''})})
afterEach(()=>{w?.unmount();w=undefined})
it('clicks positive disk refresh once with the selected fixed server and team',async()=>{await open();const before=m.df.mock.calls.length;await w!.get('.usage-head button').trigger('click');await settle();expect(m.df).toHaveBeenCalledTimes(before+1);expect(m.df).toHaveBeenLastCalledWith('s1',expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}));expect(m.prune).not.toHaveBeenCalled()});

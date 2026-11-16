import {defineComponent,h,onMounted,ref} from 'vue'
import {createPinia,setActivePinia} from 'pinia'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {afterEach,beforeEach,describe,it,expect,vi} from 'vitest'
import AuthorizedPage from './AuthorizedPage.vue'
import {useAuthorizedRequestScope} from '../composables/useAuthorizedRequestScope'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {HttpError} from '../api/http'
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
let wrapper:VueWrapper|undefined,revision=1
const query={action:'pipeline.read',resourceId:'p'}
const request=vi.fn()
async function settle(){for(let i=0;i<5;i++)await flushPromises()}
const Content=defineComponent({setup(){const text=ref('');const scope=useAuthorizedRequestScope(query,()=>{text.value=''});onMounted(async()=>{try{text.value=await scope.run(request)}catch{}});return()=>h('div',{class:'private'},text.value)}})
beforeEach(()=>{vi.useFakeTimers();vi.resetAllMocks();setActivePinia(createPinia());revision=1;useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('t');vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'u',teamId:team,modeEpoch:1,revision,catalogVersion:1,allowed:qs.map(()=>true)}));request.mockRejectedValue(new HttpError(403,{code:'forbidden',message:'Denied'},'Denied'))})
afterEach(()=>{wrapper?.unmount();wrapper=undefined;vi.useRealTimers()})
describe('server denial versus an allowed permission hint',()=>{
 it('does not repeatedly remount and fetch while the same policy keeps allowing',async()=>{wrapper=mount(AuthorizedPage,{props:{component:Content,query}});await settle();expect(request).toHaveBeenCalledOnce();await vi.advanceTimersByTimeAsync(20000);await settle();expect(request).toHaveBeenCalledOnce();expect(wrapper.find('.private').exists()).toBe(false)})
 it('permits a retry after a real policy revision changes',async()=>{wrapper=mount(AuthorizedPage,{props:{component:Content,query}});await settle();request.mockResolvedValue('authorized result');revision=2;await vi.advanceTimersByTimeAsync(6000);await settle();expect(request).toHaveBeenCalledTimes(2);expect(wrapper.text()).toContain('authorized result')})
 it('allows explicit refresh but suppresses another contradictory denial',async()=>{wrapper=mount(AuthorizedPage,{props:{component:Content,query}});await settle();usePermissionStore().invalidate();await settle();expect(request).toHaveBeenCalledTimes(2);await vi.advanceTimersByTimeAsync(10000);await settle();expect(request).toHaveBeenCalledTimes(2)})
 it('clears an invalid session on 401 instead of remounting under stale hints',async()=>{request.mockRejectedValue(new HttpError(401,null,'Expired'));wrapper=mount(AuthorizedPage,{props:{component:Content,query}});await settle();expect(request).toHaveBeenCalledOnce();expect(useSessionStore().user).toBeNull();await vi.advanceTimersByTimeAsync(10000);await settle();expect(request).toHaveBeenCalledOnce()})
})

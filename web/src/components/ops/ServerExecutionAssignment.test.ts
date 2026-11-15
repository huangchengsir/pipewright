import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createI18n} from 'vue-i18n'
import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import Panel from './ServerExecutionAssignment.vue'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import {getExecutionAssignment,setExecutionAssignment} from '../../api/servers'
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/servers',()=>({getExecutionAssignment:vi.fn(),setExecutionAssignment:vi.fn()}))
let wrappers:ReturnType<typeof mount>[]=[]
const key='SHA256:'+'A'.repeat(43)
beforeEach(()=>{setActivePinia(createPinia());vi.clearAllMocks();useSessionStore().setUser({username:'admin',userId:'u',multiTenant:true,instanceAdmin:true});usePermissionStore().setTeam('a');vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:1,allowed:queries.map(q=>team==='a'&&q.action==='server.assign'&&q.resourceId==='s')}));vi.mocked(getExecutionAssignment).mockResolvedValue(null);vi.mocked(setExecutionAssignment).mockResolvedValue({teamId:'a',hostKey:key,assignedAt:1})})
afterEach(()=>wrappers.splice(0).forEach(w=>w.unmount()))
function panel(){const w=mount(Panel,{props:{serverId:'s'},global:{plugins:[createI18n({legacy:false,locale:'en',messages:{en:{}},missingWarn:false,fallbackWarn:false})]}});wrappers.push(w);return w}
it('submits only a valid public fingerprint and supports revocation',async()=>{const w=panel();await flushPromises();expect(w.get('button').attributes('disabled')).toBeDefined();await w.get('input').setValue(key);await w.get('button').trigger('click');await flushPromises();expect(setExecutionAssignment).toHaveBeenCalledWith('s',key);await w.findAll('button')[1]!.trigger('click');await flushPromises();expect(setExecutionAssignment).toHaveBeenLastCalledWith('s','')})
it('does not expose assignment in off mode or to employees',async()=>{useSessionStore().setUser({username:'admin',multiTenant:false});const w=panel();await flushPromises();expect(w.find('section').exists()).toBe(false);expect(getExecutionAssignment).not.toHaveBeenCalled();useSessionStore().setUser({username:'member',userId:'u',multiTenant:true,instanceAdmin:false});await flushPromises();expect(w.find('section').exists()).toBe(false);expect(getExecutionAssignment).not.toHaveBeenCalled()})
it('drops old team responses and clears on logout',async()=>{let finish!:(v:null)=>void;vi.mocked(getExecutionAssignment).mockReturnValue(new Promise(resolve=>{finish=resolve}));const w=panel();await flushPromises();usePermissionStore().setTeam('b');await flushPromises();finish(null);await flushPromises();expect(w.find('section').exists()).toBe(false);expect(setExecutionAssignment).not.toHaveBeenCalled();useSessionStore().clearSession();await flushPromises();expect(w.find('input').exists()).toBe(false)})
it('fails closed on lookup failure',async()=>{vi.mocked(getExecutionAssignment).mockRejectedValue(new Error('network'));const w=panel();await flushPromises();expect(w.find('[role=alert]').exists()).toBe(true);expect(w.get('button').attributes('disabled')).toBeDefined();expect(setExecutionAssignment).not.toHaveBeenCalled()})

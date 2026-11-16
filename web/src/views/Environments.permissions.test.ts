import {reactive} from 'vue'
import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../stores/session'
import {usePermissionStore} from '../stores/permissions'
import {access} from '../api/access'
import {listEnvironmentDeployments,listEnvironmentProjects,rollbackEnvironment,type EnvDeployment} from '../api/environments'
import {useConfirm} from '../composables/useConfirm'
import Environments from './Environments.vue'
const route=vi.hoisted(()=>({value:null as {query:{projectId:string}} | null}))
vi.mock('vue-router',()=>({useRoute:()=>route.value,useRouter:()=>({replace:vi.fn((next:{query:{projectId:string}})=>{route.value!.query=next.query})})}))
vi.mock('naive-ui',()=>({useMessage:()=>({success:vi.fn(),error:vi.fn()})}))
vi.mock('../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../api/environments',()=>({listEnvironmentDeployments:vi.fn(),listEnvironmentProjects:vi.fn(),rollbackEnvironment:vi.fn()}))
let allowed=new Set<string>();let wrappers:ReturnType<typeof mount>[]=[]
function render(multiTenant=true){const pinia=createPinia();setActivePinia(pinia);useSessionStore().setUser({userId:'u',username:'u',multiTenant});usePermissionStore().setTeam('team');const w=mount(Environments,{global:{plugins:[pinia],stubs:{ReleaseGuardPanel:true,RouterLink:true}}});wrappers.push(w);return w}
beforeEach(()=>{route.value=reactive({query:{projectId:'project'}});vi.resetAllMocks();allowed=new Set(['env-test']);vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>allowed.has(q.resourceId??'')&&q.action==='release.rollback'&&q.environmentId===q.resourceId)}));vi.mocked(listEnvironmentProjects).mockResolvedValue([{id:'project',name:'Project'}]);vi.mocked(listEnvironmentDeployments).mockResolvedValue(['test','prod'].map(environment=>{const deployments:EnvDeployment[]=[0,1].map(i=>({runId:environment+i,active:i===0,status:'success',commit:'abcdef',branch:'main',triggeredBy:'u',deployedAt:'2026-11-01T00:00:00Z',targets:[],artifacts:[]}));return {environment,environmentId:'env-'+environment,active:deployments[0]!,deployments}}));vi.mocked(rollbackEnvironment).mockResolvedValue({environment:'test',fromRunId:'test0',toRunId:'test1',artifactId:'a',targets:[]})})
afterEach(()=>{useConfirm()._resolve(false);wrappers.forEach(w=>w.unmount());wrappers=[]})
it('uses stable environment identity and only shows permitted rollback',async()=>{const w=render();await flushPromises();const cards=w.findAll('.env-card');expect(cards[0]!.find('.env-card__head button').exists()).toBe(true);expect(cards[1]!.find('.env-card__head button').exists()).toBe(false);await cards[0]!.get('.env-card__head button').trigger('click');useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).toHaveBeenCalledExactlyOnceWith('project','test',expect.objectContaining({headers:{'X-Pipewright-Team':'team'},signal:expect.any(AbortSignal)}),{expectedCurrentRunId:'test0',expectedTargetRunId:'test1'});expect(listEnvironmentProjects).toHaveBeenCalled()})
it('revocation while confirmation is open cancels the action without request',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');allowed.clear();usePermissionStore().invalidate();await flushPromises();useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).not.toHaveBeenCalled();expect(w.find('.env-card__head button').exists()).toBe(false)})
it('preserves explicitly disabled tenancy rollback controls',async()=>{const w=render(false);await flushPromises();expect(w.findAll('.env-card__head button')).toHaveLength(2);expect(access.decisions).not.toHaveBeenCalled()})

it('does not use a previous employee rollback confirmation after identity changes',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');vi.mocked(access.decisions).mockImplementation(async(team,qs)=>({userId:'v',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>true)}));useSessionStore().setUser({userId:'v',username:'v',multiTenant:true});await flushPromises();useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).not.toHaveBeenCalled()});

it('discards old environment timelines after identity changes',async()=>{let done!:(v:Awaited<ReturnType<typeof listEnvironmentDeployments>>)=>void;vi.mocked(listEnvironmentDeployments).mockReturnValueOnce(new Promise(r=>done=r)).mockResolvedValue([]);const w=render();await flushPromises();useSessionStore().setUser({userId:'v',username:'v',multiTenant:true});await flushPromises();done([{environment:'old-private-environment',environmentId:'old',active:null,deployments:[]}]);await flushPromises();expect(w.text()).not.toContain('old-private-environment')});

it('loads and operates the newly selected team while rejecting its predecessor confirmation',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');usePermissionStore().setTeam('new-team');await flushPromises();useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).not.toHaveBeenCalled();expect(listEnvironmentProjects).toHaveBeenLastCalledWith(expect.objectContaining({headers:{'X-Pipewright-Team':'new-team'}}));expect(w.find('.env-card__head button').exists()).toBe(true);await w.get('.env-card__head button').trigger('click');useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).toHaveBeenCalledWith('project','test',expect.objectContaining({headers:{'X-Pipewright-Team':'new-team'}}),{expectedCurrentRunId:'test0',expectedTargetRunId:'test1'})});

it('rejects an old rollback confirmation after project A to B to A',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');route.value!.query.projectId='other';await flushPromises();route.value!.query.projectId='project';await flushPromises();useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).not.toHaveBeenCalled()});
it('pins the active and previous run in the confirmed rollback request',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).toHaveBeenCalledWith('project','test',expect.anything(),{expectedCurrentRunId:'test0',expectedTargetRunId:'test1'})});
it('rejects confirmation after refreshed history changes the previous deployment',async()=>{const w=render();await flushPromises();await w.get('.env-card__head button').trigger('click');vi.mocked(listEnvironmentDeployments).mockResolvedValue([]);await w.get('.view-header button').trigger('click');await flushPromises();useConfirm()._resolve(true);await flushPromises();expect(rollbackEnvironment).not.toHaveBeenCalled()});

it('clicks the real project dropdown option and loads its fixed-team data',async()=>{vi.mocked(listEnvironmentProjects).mockResolvedValue([{id:'project',name:'Project'},{id:'other',name:'Other Project'}]);const w=render();await flushPromises();await w.get('[role="combobox"]').trigger('click');await flushPromises();const option=w.findAll('[role="option"]').find(x=>x.text().includes('Other Project'));expect(option).toBeDefined();await option!.trigger('click');await flushPromises();expect(route.value!.query.projectId).toBe('other');expect(listEnvironmentDeployments).toHaveBeenLastCalledWith('other',expect.any(AbortSignal),expect.objectContaining({headers:{'X-Pipewright-Team':'team'}}));expect(rollbackEnvironment).not.toHaveBeenCalled()});

it('cancels the displayed rollback before dispatch and keeps its timeline readable', async () => {
  const wrapper = render();
  await flushPromises();
  const rollback = wrapper.get('.env-card__head button');
  await rollback.trigger('click');
  useConfirm()._resolve(false);
  await flushPromises();
  expect(rollbackEnvironment).not.toHaveBeenCalled();
  expect(wrapper.findAll('.env-card')).toHaveLength(2);
  expect(wrapper.find('.env-card__head button').exists()).toBe(true);
});

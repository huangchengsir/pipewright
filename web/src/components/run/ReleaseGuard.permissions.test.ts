import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import {guardReport,guardPlan} from '../../test/releaseGuardFixtures'
import Investigation from './ReleaseGuardInvestigation.vue'
import Analysis from './ReleaseGuardAnalysis.vue'
import Plan from './ReleaseGuardPlan.vue'
import {investigateReleaseGuard,listGuardAnalyses,previewGuardAnalysis,previewGuardPlan,startReleaseGuard} from '../../api/releaseGuard'
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/releaseGuard',async original=>({...await original<typeof import('../../api/releaseGuard')>(),investigateReleaseGuard:vi.fn(),listGuardAnalyses:vi.fn(),previewGuardAnalysis:vi.fn(),previewGuardPlan:vi.fn(),startReleaseGuard:vi.fn()}))
let allowed=new Set<string>();let wrappers:ReturnType<typeof mount>[]=[]
function session(){const pinia=createPinia();setActivePinia(pinia);useSessionStore().setUser({userId:'u',username:'u',multiTenant:true});usePermissionStore().setTeam('team');return pinia}
beforeEach(()=>{vi.resetAllMocks();allowed=new Set();vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({userId:'u',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:queries.map(q=>allowed.has(q.action+'/'+q.resourceId))}));vi.mocked(investigateReleaseGuard).mockResolvedValue(guardReport());vi.mocked(listGuardAnalyses).mockResolvedValue({items:[]});vi.mocked(previewGuardPlan).mockResolvedValue(guardPlan());vi.mocked(startReleaseGuard).mockResolvedValue(guardReport())})
afterEach(()=>{wrappers.forEach(w=>w.unmount());wrappers=[]})
it('keeps report evidence visible but denies investigation without the exact action and run',async()=>{const pinia=session();allowed.add('guard.investigate/other-run');const w=mount(Investigation,{props:{report:guardReport()},global:{plugins:[pinia]}});wrappers.push(w);await flushPromises();expect(w.find('.guard-investigation-update__action button').exists()).toBe(false);expect(investigateReleaseGuard).not.toHaveBeenCalled();allowed.add('guard.investigate/run-1');usePermissionStore().invalidate();await flushPromises();await w.get('.guard-investigation-update__action button').trigger('click');await flushPromises();expect(investigateReleaseGuard).toHaveBeenCalledExactlyOnceWith('project-1','guard-1',expect.any(Object));allowed.clear();usePermissionStore().invalidate();await flushPromises();expect(w.find('.guard-investigation-update__action button').exists()).toBe(false)})
it('allows reading analysis history without granting prepare/send/revoke',async()=>{const pinia=session();const w=mount(Analysis,{props:{report:guardReport()},global:{plugins:[pinia]}});wrappers.push(w);await flushPromises();expect(listGuardAnalyses).toHaveBeenCalled();expect(w.findAll('button').map(b=>b.text())).not.toContain('准备分析');await w.get('input[type=checkbox]').setValue(true);await flushPromises();expect(previewGuardAnalysis).not.toHaveBeenCalled();expect(w.findAllComponents({name:'PermissionGate'}).every(g=>g.find('button').exists()===false)).toBe(true)})
it('does not prepare observation until authorized and rejects start after revocation',async()=>{const pinia=session();const w=mount(Plan,{props:{projectId:'project-1',runId:'run-1',attemptId:'attempt-1'},global:{plugins:[pinia]}});wrappers.push(w);await flushPromises();expect(previewGuardPlan).not.toHaveBeenCalled();allowed.add('guard.observe/run-1');usePermissionStore().invalidate();await flushPromises();expect(previewGuardPlan).toHaveBeenCalledTimes(1);const start=w.findAll('button').find(b=>b.text()==='开始观察');expect(start).toBeDefined();allowed.clear();usePermissionStore().invalidate();await flushPromises();expect(w.findAll('button').some(b=>b.text()==='开始观察')).toBe(false);expect(startReleaseGuard).not.toHaveBeenCalled()})

it('manually re-previews only the selected attempt and blocks its retained button on revoke', async () => {
  const pinia = session();
  allowed.add('guard.observe/run-1');
  const w = mount(Plan, {props:{projectId:'project-1',runId:'run-1',attemptId:'attempt-1'},global:{plugins:[pinia]}});
  wrappers.push(w);
  await flushPromises();
  expect(previewGuardPlan).toHaveBeenCalledTimes(1);
  const get = () => w.findAll('button').find(b => b.text() === '重新预览')!;
  await get().trigger('click');
  await flushPromises();
  expect(previewGuardPlan).toHaveBeenCalledTimes(2);
  expect(previewGuardPlan).toHaveBeenLastCalledWith('project-1',expect.objectContaining({attemptId:'attempt-1'}),expect.objectContaining({headers:{'X-Pipewright-Team':'team'}}));
  const retained = get().element;
  usePermissionStore().reject('team', {action:'guard.observe', resourceId:'run-1'});
  retained.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  await flushPromises();
  expect(previewGuardPlan).toHaveBeenCalledTimes(2);
  expect(startReleaseGuard).not.toHaveBeenCalled();
});

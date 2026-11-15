import {mount,flushPromises} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {createI18n} from 'vue-i18n'
import {it,expect,beforeEach,afterEach,vi} from 'vitest'
import Panel from './DiagnosisPanel.vue'
import {usePermissionStore} from '../../stores/permissions'
import {useSessionStore} from '../../stores/session'
import {access} from '../../api/access'
import {diagnoseRun,submitDiagnosisFeedback,type DiagnosisDTO} from '../../api/runs'
vi.mock('../../api/access',()=>({access:{decisions:vi.fn()}}))
vi.mock('../../api/runs',()=>({diagnoseRun:vi.fn(),submitDiagnosisFeedback:vi.fn()}))
let allowed:Set<string>,wrappers:ReturnType<typeof mount>[]=[]
const fixedOptions=expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal),throwOnUnauthorized:true})
const ready:DiagnosisDTO={status:'ready',reason:'',hypothesis:'Scoped finding',confidence:'high',alternateCauses:[],fixSuggestions:[],fixScript:'echo safe',evidence:[],generatedAt:'2026-11-13T00:00:00Z'}
beforeEach(()=>{setActivePinia(createPinia());vi.clearAllMocks();allowed=new Set(['run.read']);useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});usePermissionStore().setTeam('a');vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',catalogVersion:1,modeEpoch:1,revision:1,allowed:queries.map(q=>team==='a'&&q.resourceId==='r'&&allowed.has(q.action))}));vi.mocked(diagnoseRun).mockResolvedValue(ready);vi.mocked(submitDiagnosisFeedback).mockResolvedValue({ok:true})})
afterEach(()=>wrappers.splice(0).forEach(w=>w.unmount()))
function panel(diagnosis:DiagnosisDTO|null=ready){const w=mount(Panel,{props:{runId:'r',diagnosis},global:{plugins:[createI18n({legacy:false,locale:'en',messages:{en:{}},missingWarn:false,fallbackWarn:false})]}});wrappers.push(w);return w}
it('read-only users see existing findings but no feedback or model action',async()=>{const w=panel();await flushPromises();expect(w.text()).toContain('Scoped finding');expect(w.find('.dp-feedback').exists()).toBe(false);await w.setProps({diagnosis:null});await flushPromises();expect(w.find('button').exists()).toBe(false);expect(diagnoseRun).not.toHaveBeenCalled();expect(submitDiagnosisFeedback).not.toHaveBeenCalled()})
it('permits diagnosis without changing layout and accepts the completed result',async()=>{allowed.add('run.diagnose');const w=panel(null);await flushPromises();await w.get('button').trigger('click');await flushPromises();expect(diagnoseRun).toHaveBeenCalledWith('r',fixedOptions);expect(w.emitted('diagnosed')?.[0]).toEqual([ready])})
it('permits thumbs-up independently of viewing and submits the correct run',async()=>{allowed.add('run.diagnose');const w=panel();await flushPromises();await w.get('.dp-fb-btn--up').trigger('click');await flushPromises();expect(submitDiagnosisFeedback).toHaveBeenCalledWith('r',{verdict:'up'},fixedOptions)})
it('permits root-cause feedback and clears the input when permission is withdrawn',async()=>{allowed.add('run.diagnose');const w=panel();await flushPromises();await w.get('.dp-fb-btn--down').trigger('click');await w.get('textarea').setValue('corrected reason');await w.get('.dp-fb-actions button').trigger('click');await flushPromises();expect(submitDiagnosisFeedback).toHaveBeenCalledWith('r',{verdict:'down',correctRootCause:'corrected reason'},fixedOptions);allowed.delete('run.diagnose');usePermissionStore().invalidate();await flushPromises();expect(w.find('textarea').exists()).toBe(false);expect(w.find('.dp-feedback').exists()).toBe(false)})
it('does not emit a diagnosis returned after team change or logout',async()=>{allowed.add('run.diagnose');let finish!:(r:DiagnosisDTO)=>void;vi.mocked(diagnoseRun).mockReturnValue(new Promise(resolve=>{finish=resolve}));const w=panel(null);await flushPromises();await w.get('button').trigger('click');usePermissionStore().setTeam('b');await flushPromises();finish(ready);await flushPromises();expect(w.emitted('diagnosed')).toBeUndefined();expect(w.find('section').exists()).toBe(false);useSessionStore().clearSession();await flushPromises();expect(w.text()).not.toContain('Scoped finding')})
it('keeps unknown identity denied and preserves explicit legacy mode',async()=>{useSessionStore().setUser({username:'unknown'});const w=panel(null);await flushPromises();expect(w.find('section').exists()).toBe(false);useSessionStore().setUser({username:'admin',multiTenant:false});await flushPromises();expect(w.find('button').exists()).toBe(true)})

it('uses the exact run for the error-state retry and rejects a revoked retry button', async () => {
  allowed.add('run.diagnose');
  const w=panel({...ready,status:'unavailable',reason:'fixture'});
  await flushPromises();
  const button=w.get('.dp-unavail-inner button');
  await button.trigger('click');
  await flushPromises();
  expect(diagnoseRun).toHaveBeenCalledExactlyOnceWith('r',fixedOptions);
  usePermissionStore().reject('a',{action:'run.diagnose',resourceId:'r'});
  button.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  await flushPromises();
  expect(diagnoseRun).toHaveBeenCalledTimes(1);
});

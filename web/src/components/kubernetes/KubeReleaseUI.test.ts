import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { h } from 'vue'
import { i18n, setLocale, SUPPORTED_LOCALES } from '../../i18n'
import Legacy from './KubeLegacyOperationPanel.vue'
import Release from './KubeReleasePanel.vue'
import Operation from './KubeOperationPanel.vue'
import Password from './KubePassword.vue'
import Editor from '../pipeline/KubernetesReleaseJobConfig.vue'
import { newReleaseConfig } from '../pipeline/kubernetesReleaseConfig'
import type { KubeRelease } from '../../api/kubernetes'
const m=vi.hoisted(()=>({operations:vi.fn(),release:vi.fn(),releases:vi.fn(),releaseAction:vi.fn(),rollbackRelease:vi.fn(),rollbackRequest:vi.fn(),reauth:vi.fn(),connections:vi.fn(),releaseGrants:vi.fn(),confirm:vi.fn()}))
vi.mock('../../api/kubernetes',async()=>({...await vi.importActual('../../api/kubernetes'),kubeAPI:m}))
vi.mock('../../composables/useConfirm',()=>({useConfirm:()=>({open:m.confirm})}))
const wrappers:VueWrapper[]=[]
const resource={group:'apps',version:'v1',resource:'deployments',kind:'Deployment',namespace:'app',name:'web'}
function row():KubeRelease { return {id:'release',runId:'original-run',nodeKey:'kube',revision:3,state:'paused',cleanup:'verified',ownerFenced:true,cursor:0,createdAt:1,updatedAt:2,preview:{planId:'sealed-plan',mode:'progressive',resources:[resource],batchReplicas:[1,2]},results:[{resource,state:'verified',health:{state:'ready',reason:'workload_ready',containers:[{pod:'web-1',name:'web',ready:true,restarts:1}]},observedAt:2}]} }
function render(release=row()) { const w=mount(Release,{props:{release},global:{stubs:{KubePassword:true,RouterLink:{props:['to'],template:'<a><slot/></a>'}}},slots:{default:(p:any)=>h('p',{'data-current-phase':p.phase},'original-run / '+p.phase)}}); wrappers.push(w); return w }
const button=(w:VueWrapper,key:string)=>w.findAll('button').find(x=>x.text()===i18n.global.t(key))!
async function authenticate(w:VueWrapper) { const state=w.getComponent(Password).props('state'); state.password.value='synthetic-password'; await state.verify(); await flushPromises() }
beforeEach(()=>{ for(const fn of Object.values(m))fn.mockReset(); sessionStorage.clear(); m.release.mockImplementation(async()=>row());m.releases.mockResolvedValue([]);m.reauth.mockResolvedValue({reauthenticatedUntil:new Date(Date.now()+300000).toISOString()});m.confirm.mockResolvedValue(true);m.connections.mockResolvedValue([]);m.releaseGrants.mockResolvedValue([]) })
afterEach(()=>{ for(const w of wrappers.splice(0))w.unmount();vi.useRealTimers();vi.restoreAllMocks();setLocale('zh-CN') })
describe('Kubernetes v2 UI integration',()=>{
 it('continues the same release only after reauthentication and stops polling on unmount',async()=>{
  vi.useFakeTimers();m.releaseAction.mockResolvedValue({});const w=render();await flushPromises();expect(w.text()).toContain('web-1 / web');expect(w.emitted('blocked')?.at(-1)).toEqual([true])
  await button(w,'kubernetes.release.continue').trigger('click');await flushPromises();expect(m.releaseAction).not.toHaveBeenCalled();await authenticate(w)
  expect(m.releaseAction).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({id:'release',runId:'original-run',revision:3}),'continue',expect.any(AbortSignal))
  await w.findAll('[role="tab"]')[0].trigger('keydown',{key:'End'});expect(w.get('[data-current-phase]').text()).toContain('original-run / cleanup')
  w.unmount();wrappers.pop();const calls=m.release.mock.calls.length;await vi.advanceTimersByTimeAsync(8000);expect(m.release.mock.calls.length).toBe(calls)
 })
 it('preserves a lost rollback request across remount and uses read-only lookup',async()=>{
  m.rollbackRelease.mockRejectedValue(new Error('lost response'));m.rollbackRequest.mockRejectedValue(new Error('not found'))
  const w=render();await flushPromises();await button(w,'kubernetes.release.rollback').trigger('click');await flushPromises();await authenticate(w)
  expect(m.rollbackRelease).toHaveBeenCalledTimes(1);const request=m.rollbackRelease.mock.calls[0][1];expect(sessionStorage.getItem('pipewright:kubernetes-rollback:release')).toBe(request)
  w.unmount();wrappers.pop();const next=render();await flushPromises();await button(next,'kubernetes.release.findRollback').trigger('click');await flushPromises()
  expect(m.rollbackRequest).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({id:'release'}),request,expect.any(AbortSignal));expect(m.rollbackRelease).toHaveBeenCalledTimes(1);expect(sessionStorage.getItem('pipewright:kubernetes-rollback:release')).toBe(request)
 })
 it('shows a pinned missing release without falling back to a legacy operation',async()=>{
  const w=mount(Operation,{props:{runId:'original-run',nodeId:'kube',releaseId:'missing',version:2},global:{stubs:{KubeLegacyOperationPanel:true}}});wrappers.push(w);await flushPromises()
  expect(w.find('[role="alert"]').exists()).toBe(true);expect(w.find('kube-legacy-operation-panel-stub').exists()).toBe(false);expect(m.releaseAction).not.toHaveBeenCalled()
 })
 it('enables and removes explicit CoreDNS maintenance without replacing the source',async()=>{
  const job={id:'kube',name:'Kubernetes',type:'deploy_kubernetes',summary:'',config:{}};const stage={id:'deploy',name:'Deploy',kind:'deploy' as const,jobs:[job]};const cfg={...newReleaseConfig(),mode:'manifest',source:'{"kind":"inline","content":"apiVersion: v1"}',privateRef:'kcfg_bound'}
  const w=mount(Editor,{props:{config:cfg,stages:[stage],stage,job,projectId:'project'}});wrappers.push(w);await flushPromises()
  const toggle=()=>w.findAll('label.kube-check').find(x=>x.text()===i18n.global.t('kubernetes.release.coreDNSEnable'))!.get('input')
  await toggle().setValue(true);let next=w.emitted('update')!.at(-1)![0] as Record<string,string>;expect(next.source).toBe(cfg.source);expect(JSON.parse(next.coreDNS).configMap).toBe('coredns');await w.setProps({config:next})
  await w.get('#kr-dns-candidateServiceIP').setValue('10.43.0.50');next=w.emitted('update')!.at(-1)![0] as Record<string,string>;expect(JSON.parse(next.coreDNS).candidateServiceIP).toBe('10.43.0.50');await w.setProps({config:next})
  await toggle().setValue(false);next=w.emitted('update')!.at(-1)![0] as Record<string,string>;expect(next.coreDNS).toBeUndefined();expect(next.privateRef).toBe('kcfg_bound');expect(next.source).toBe(cfg.source)
 })
 it('edits all four delivery modes while preserving private references and variable bindings',async()=>{
  const job={id:'kube',name:'Kubernetes',type:'deploy_kubernetes',summary:'',config:{}};const stage={id:'deploy',name:'Deploy',kind:'deploy' as const,jobs:[job]};const cfg={...newReleaseConfig(),privateRef:'kcfg_bound',variables:'[{"name":"COUNT","type":"number","source":"fixed","value":"pwkeep_bound"}]'}
  const w=mount(Editor,{props:{config:cfg,stages:[stage],stage,job,projectId:'project'}});wrappers.push(w);await flushPromises()
  for(const mode of ['manifest','helm','progressive','image']) { await w.get('#kr-mode').setValue(mode); const next=w.emitted('update')!.at(-1)![0] as Record<string,string>;expect(next.privateRef).toBe('kcfg_bound');expect(next.variables).toBe(cfg.variables);await w.setProps({config:next});expect(w.get('#kr-mode').element).toHaveProperty('value',mode) }
 })
})

describe('mode-accurate Kubernetes phase labels',()=>{
 for(const {code} of SUPPORTED_LOCALES) it(code+' uses generic apply for v2 and keeps the legacy image label and phase key',async()=>{
  setLocale(code);const release=row();release.preview.mode='manifest';const modern=render(release);await flushPromises();
  const modernApply=modern.findAll('[role="tab"]')[1];
  expect(i18n.global.te('kubernetes.release.applyResources')).toBe(true);
  expect(modernApply.text()).toBe(i18n.global.t('kubernetes.release.applyResources'));
  expect(modernApply.text()).not.toBe(i18n.global.t('kubernetes.phase.update'));
  await modernApply.trigger('click');expect(modern.get('[data-current-phase]').text()).toContain('/ update');
  m.operations.mockResolvedValue([{id:'legacy',runId:'legacy-run',nodeId:'legacy-node',attempt:1,revision:1,connectionId:'connection',projectId:'project',environment:'dev',namespace:'app',deployment:'web',container:'app',executor:{kind:'local'},image:'registry.invalid/app@sha256:fixture',intentHash:'opaque',business:'ready',cleanup:'verified',retired:false,reasonCode:'',canReconcile:true,canRetire:false}]);
  const legacy=mount(Legacy,{props:{runId:'legacy-run',nodeId:'legacy-node'},global:{stubs:{KubePassword:true}},slots:{default:(p:any)=>h('p',{'data-legacy-phase':p.phase},p.phase)}});wrappers.push(legacy);await flushPromises();
  const legacyUpdate=legacy.findAll('[role="tab"]')[1];expect(legacyUpdate.text()).toContain(i18n.global.t('kubernetes.phase.update'));
  await legacyUpdate.trigger('click');expect(legacy.get('[data-legacy-phase]').text()).toBe('update');expect(m.releaseAction).not.toHaveBeenCalled();
 });
});

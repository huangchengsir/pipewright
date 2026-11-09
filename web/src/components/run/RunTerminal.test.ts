import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import RunTerminal from './RunTerminal.vue'
import {i18n,setLocale} from '../../i18n'
const api = vi.hoisted(() => ({ logs: vi.fn(), subscribe: vi.fn() }))
vi.mock('../../api/runs', () => ({ getRunLogs: api.logs, subscribeRunEvents: api.subscribe }))
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
const page = (text: string) => ({ lines: [{ seq: 1, ts: '2026-10-28T00:00:00Z', stream: 'stdout', stepOrdinal: 0, text }], nextSeq: 1, complete: true })
describe('run log identity lifecycle', () => {
  beforeEach(() => { vi.clearAllMocks(); api.logs.mockResolvedValue(page('current log')); api.subscribe.mockReturnValue(vi.fn()) })
  it('ignores a previous run’s late historical page', async () => {
    const old = deferred<ReturnType<typeof page>>(); api.logs.mockReturnValueOnce(old.promise)
    const wrapper = mount(RunTerminal, { props: { runId: 'old', live: false } }); await wrapper.setProps({ runId: 'new' }); await flushPromises()
    old.resolve(page('wrong old evidence')); await flushPromises()
    expect(wrapper.text()).toContain('current log'); expect(wrapper.text()).not.toContain('wrong old evidence'); wrapper.unmount()
  })
  it('disposes the old subscription and ignores late callback delivery', async () => {
    const stop = vi.fn(); api.subscribe.mockReturnValue(stop)
    const wrapper = mount(RunTerminal, { props: { runId: 'old', live: true } }); await flushPromises()
    const oldHandlers = api.subscribe.mock.calls[0][1]
    await wrapper.setProps({ runId: 'new' }); await flushPromises(); expect(stop).toHaveBeenCalled()
    oldHandlers.onLog({ seq: 99, ts: '', stream: 'stdout', stepOrdinal: 0, text: 'late old SSE' }); await flushPromises()
    expect(wrapper.text()).not.toContain('late old SSE'); wrapper.unmount(); expect(stop).toHaveBeenCalledTimes(2)
  })
})

describe('Kubernetes preparation user feedback',()=>{
 it('shows a localized validation failure in the preflight view and changes locale without refetching',async()=>{
  api.logs.mockResolvedValue(page('kubernetes preparation: phase=dry_run reason=kubernetes_resource_conflict'));
  setLocale('zh-CN');
  const wrapper=mount(RunTerminal,{props:{runId:'diagnostic',live:false,filterOrdinal:0,kubernetesPhase:'preflight'}});
  await flushPromises();
  expect(wrapper.text()).toContain(i18n.global.t('kubernetes.errors.resourceConflict'));
  expect(wrapper.text()).not.toContain('kubernetes_resource_conflict');
  const requests=api.logs.mock.calls.length;
  setLocale('en');await flushPromises();
  expect(wrapper.text()).toContain(i18n.global.t('kubernetes.errors.resourceConflict'));
  expect(api.logs.mock.calls.length).toBe(requests);
  wrapper.unmount();setLocale('zh-CN');
 });
});

describe('v2 Kubernetes phase log projection',()=>{
 it('renders only current-node actual observe events and preserves pending or declared states',async()=>{
  setLocale('en');
  const row=(seq:number,stepOrdinal:number,text:string)=>({seq,ts:'2026-10-28T00:00:00Z',stream:'stdout' as const,stepOrdinal,text});
  api.logs.mockResolvedValue({lines:[
   row(1,0,'kubernetes dispatch: resource_write_fenced'),
   row(2,0,'kubernetes observe: kind=Deployment state=pending reason=current_template_pods_not_observed'),
   row(3,0,'kubernetes observe: kind=ConfigMap state=declared reason=configuration_verified_no_application_health'),
   row(4,1,'kubernetes observe: kind=Pod state=ready reason=pod_ready'),
   row(5,0,'kubernetes observe: kind=Pod state=ready reason=PrivateCanary'),
  ],nextSeq:5,complete:true});
  const w=mount(RunTerminal,{props:{runId:'v2',live:false,filterOrdinal:0,kubernetesPhase:'rollout'}});await flushPromises();
  expect(w.text()).toContain('Deployment: '+i18n.global.t('kubernetes.release.states.pending'));
  expect(w.text()).toContain('ConfigMap: '+i18n.global.t('kubernetes.release.states.declared'));
  expect(w.text()).not.toContain('Pod:');expect(w.text()).not.toContain('PrivateCanary');expect(w.text()).not.toContain('state=pending');
  await w.setProps({kubernetesPhase:'update'});expect(w.text()).toContain(i18n.global.t('kubernetes.phaseCode.resource_write_fenced'));
  await w.setProps({kubernetesPhase:'cleanup'});expect(w.text()).toContain(i18n.global.t('kubernetes.release.noPhaseEvents'));
  expect(w.text()).not.toContain(i18n.global.t('run.noLogRecords'));w.unmount();setLocale('zh-CN');
 });
});

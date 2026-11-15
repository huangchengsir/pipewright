import {beforeEach,afterEach,it,expect,vi} from 'vitest'
import {mount,flushPromises,type VueWrapper} from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import Logs from './ServiceLogViewer.vue'
import Ops from './ServiceOpsPanel.vue'
const m=vi.hoisted(()=>({decisions:vi.fn(),logs:vi.fn(),stream:vi.fn(),action:vi.fn()}))
vi.mock('../../api/access',()=>({access:{decisions:m.decisions}}))
vi.mock('../../api/servers',()=>({getServerLogs:m.logs,subscribeServerLogs:m.stream,serviceAction:m.action}))
let w:VueWrapper|undefined,allowed=true
async function settle(){for(let i=0;i<4;i++)await flushPromises()}
beforeEach(()=>{setActivePinia(createPinia());useSessionStore().setUser({username:'employee',userId:'u1',multiTenant:true,instanceAdmin:false});usePermissionStore().setTeam('a');allowed=true;vi.clearAllMocks();m.decisions.mockImplementation(async(team:string,qs:unknown[])=>({userId:'u1',teamId:team,modeEpoch:1,revision:1,catalogVersion:1,allowed:qs.map(()=>allowed)}));m.stream.mockReturnValue(vi.fn())})
afterEach(()=>w?.unmount())
it('disables every service mutation when permission is denied',async()=>{allowed=false;w=mount(Ops,{props:{serverId:'s1'}});await settle();await w.get('input').setValue('app');for(const b of w.findAll('.ops-actions button'))expect(b.attributes('disabled')).toBeDefined();expect(m.action).not.toHaveBeenCalled()})
it('uses docker operation permission and cancels stale confirmation on target change',async()=>{w=mount(Ops,{props:{serverId:'s1'}});await settle();await w.get('select').setValue('docker');await w.get('input').setValue('app');await settle();await w.get('.ops-btn--warn').trigger('click');expect(w.find('.ops-confirm').exists()).toBe(true);await w.get('input').setValue('other');expect(w.find('.ops-confirm').exists()).toBe(false);expect(m.decisions.mock.calls.flatMap(c=>c[1]).some(q=>q.action==='container.operate'&&q.resourceId==='s1')).toBe(true);expect(m.action).not.toHaveBeenCalled()})
it.each(['team','employee','permission','target'])('discards late service result after %s change',async(kind)=>{let finish!:(x:unknown)=>void;m.action.mockImplementation(()=>new Promise(r=>{finish=r}));w=mount(Ops,{props:{serverId:'s1'}});await settle();await w.get('input').setValue('app');await w.get('.ops-btn--ok').trigger('click');expect(m.action.mock.calls[0]![2].headers).toEqual({'X-Pipewright-Team':'a'});if(kind==='team')usePermissionStore().setTeam('b');else if(kind==='employee')useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true,instanceAdmin:false});else if(kind==='permission'){allowed=false;usePermissionStore().invalidate()}else await w.setProps({serverId:'s2'});await settle();finish({ok:true,output:'private-output',error:''});await settle();expect(w.text()).not.toContain('private-output')})
it.each(['team','employee','permission','target'])('discards late history after %s change',async(kind)=>{let finish!:(x:unknown)=>void;m.logs.mockImplementation(()=>new Promise(r=>{finish=r}));w=mount(Logs,{props:{serverId:'s1'}});await settle();await w.get('input[type="text"]').setValue('/var/log/app');await w.get('.lv-actions button').trigger('click');expect(m.logs.mock.calls[0]![2].headers).toEqual({'X-Pipewright-Team':'a'});if(kind==='team')usePermissionStore().setTeam('b');else if(kind==='employee')useSessionStore().setUser({username:'other',userId:'u2',multiTenant:true,instanceAdmin:false});else if(kind==='permission'){allowed=false;usePermissionStore().invalidate()}else await w.get('input[type="text"]').setValue('/var/log/other');await settle();expect(m.logs.mock.calls[0]![2].signal.aborted).toBe(true);finish({lines:[{text:'private-log'}]});await settle();expect(w.text()).not.toContain('private-log')})

it.each(['restart','stop','start'])('submits authorized %s with fixed team',async(action)=>{m.action.mockResolvedValue({ok:true,output:'done',error:''});w=mount(Ops,{props:{serverId:'s1'}});await settle();await w.get('input').setValue('app');const buttons=w.findAll('.ops-actions button');await buttons[['restart','stop','start'].indexOf(action)]!.trigger('click');if(action!=='start')await w.get('.ops-confirm-actions button:last-child').trigger('click');await settle();expect(m.action).toHaveBeenCalledWith('s1',{type:'systemd',target:'app',action},expect.objectContaining({headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)}));expect(w.text()).toContain('done')})
it('closes live stream and rejects queued callbacks on revocation',async()=>{const stop=vi.fn();m.stream.mockReturnValue(stop);w=mount(Logs,{props:{serverId:'s1'}});await settle();await w.get('select').setValue('docker');await w.get('input[type="text"]').setValue('app');await settle();await w.findAll('.lv-actions button')[1]!.trigger('click');const handlers=m.stream.mock.calls[0]![2];expect(m.stream.mock.calls[0]![3].headers).toEqual({'X-Pipewright-Team':'a'});handlers.onLine('visible-before');await settle();expect(w.text()).toContain('visible-before');allowed=false;usePermissionStore().invalidate();await settle();handlers.onLine('private-after');await settle();expect(stop).toHaveBeenCalledOnce();expect(w.text()).not.toContain('visible-before');expect(w.text()).not.toContain('private-after')})

it('preserves single administrator service operations with tenancy disabled',async()=>{useSessionStore().setUser({username:'admin',userId:'admin',multiTenant:false,instanceAdmin:true});m.action.mockResolvedValue({ok:true,output:'legacy',error:''});w=mount(Ops,{props:{serverId:'s1'}});await settle();await w.get('input').setValue('app');await w.get('.ops-btn--ok').trigger('click');await settle();expect(m.action.mock.calls[0]![2].headers).toBeUndefined();expect(w.text()).toContain('legacy')})


it('dismisses a pending service mutation through either cancel control without dispatching', async () => {
  w = mount(Ops, { props: { serverId: 's1' } })
  await settle()
  await w.get('input').setValue('app')
  for (const dismiss of ['.ops-confirm-actions button:first-child', '.ops-confirm-backdrop']) {
    await w.get('.ops-btn--warn').trigger('click')
    expect(w.find('.ops-confirm').exists()).toBe(true)
    await w.get(dismiss).trigger('click')
    expect(w.find('.ops-confirm').exists()).toBe(false)
    expect(m.action).not.toHaveBeenCalled()
  }
})


it('jumps to the latest visible log and clears only the current local display', async () => {
  m.logs.mockResolvedValue({ lines: [{ text: 'synthetic service log' }] })
  w = mount(Logs, { props: { serverId: 's1' } })
  await settle()
  await w.get('input[type="text"]').setValue('/var/log/app')
  await w.findAll('.lv-actions button')[0]!.trigger('click')
  await settle()
  expect(w.get('.term-text').text()).toBe('synthetic service log')

  const scroll = w.get<HTMLElement>('.term-scroll')
  Object.defineProperties(scroll.element, {
    scrollHeight: { configurable: true, value: 1200 },
    clientHeight: { configurable: true, value: 300 },
  })
  scroll.element.scrollTop = 100
  await scroll.trigger('scroll')
  await w.get('.term-follow').trigger('click')
  expect(scroll.element.scrollTop).toBe(1200)
  expect(w.find('.term-follow').exists()).toBe(false)
  await w.get('.lv-btn--ghost').trigger('click')
  expect(w.find('.term-text').exists()).toBe(false)
  expect(m.logs).toHaveBeenCalledTimes(1)
  expect(m.stream).not.toHaveBeenCalled()
})

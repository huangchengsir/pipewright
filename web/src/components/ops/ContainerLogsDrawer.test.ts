import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import {createPinia,setActivePinia} from 'pinia'
import {useSessionStore} from '../../stores/session'
import {usePermissionStore} from '../../stores/permissions'
import {access} from '../../api/access'
import ContainerLogsDrawer from './ContainerLogsDrawer.vue'
import AppSelect from '../ui/AppSelect.vue'
import CopyTextDialog from './CopyTextDialog.vue'
import { getServerLogs, subscribeServerLogs } from '../../api/servers'
import { useToast } from '../../composables/useToast'
import { i18n } from '../../i18n'

vi.mock('../../api/access', () => ({ access: { decisions: vi.fn() } }))
vi.mock('../../api/servers', () => ({ getServerLogs: vi.fn(), subscribeServerLogs: vi.fn() }))
let wrapper: VueWrapper | undefined
const exec = vi.fn()
const stop = vi.fn()
const toast = useToast()
beforeEach(() => {
 setActivePinia(createPinia());useSessionStore().setUser({username:'admin',multiTenant:false})
  toast.clear()
  exec.mockReset().mockReturnValue(false)
  stop.mockReset()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  Object.defineProperty(document, 'execCommand', { configurable: true, value: exec })
  vi.mocked(getServerLogs).mockResolvedValue({ source: 'docker', target: 'app', lines: [{ text: 'line 1' }, { text: 'line 2' }] } as Awaited<ReturnType<typeof getServerLogs>>)
  vi.mocked(subscribeServerLogs).mockReturnValue(stop)
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  toast.clear()
  document.body.replaceChildren()
  vi.restoreAllMocks()
})
async function open() {
  wrapper = mount(ContainerLogsDrawer, { attachTo: document.body, props: { serverId: 'server-a', containerName: 'app' } })
  await flushPromises()
  return wrapper
}
function button(label: string) {
  return wrapper!.findAll('.tool-btn').find((b) => b.text().includes(label))!
}

describe('container log controls', () => {
  it('ignores older history after a rapid tail change', async () => {
    let resolve!: (value: Awaited<ReturnType<typeof getServerLogs>>) => void
    vi.mocked(getServerLogs).mockReturnValueOnce(new Promise(r => { resolve = r }))
    wrapper = mount(ContainerLogsDrawer, { props: { serverId:'a', containerName:'app' } })
    wrapper.getComponent(AppSelect).vm.$emit('update:modelValue','500')
    await flushPromises()
    resolve({lines:[{text:'stale'}]} as Awaited<ReturnType<typeof getServerLogs>>)
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale')
    expect(wrapper.text()).toContain('line 1')
  })
  it('invalidates history and stream callbacks when target changes or unmounts', async () => {
    const view = await open()
    await button('跟随').trigger('click')
    const old = vi.mocked(subscribeServerLogs).mock.calls[0]![2]
    await view.setProps({serverId:'new',containerName:'other'})
    await flushPromises()
    old.onLine?.('stale stream')
    expect(view.text()).not.toContain('stale stream')
    expect(getServerLogs).toHaveBeenLastCalledWith('new',{source:'docker',target:'other',lines:200},expect.objectContaining({signal:expect.any(AbortSignal)}))
    view.unmount(); wrapper=undefined
    old.onLine?.('after unmount'); old.onTransportError?.(new Event('error'))
    expect(stop).toHaveBeenCalled()
  })
  it('defaults to 200 with a string model, numeric options and fixed portal dimensions', async () => {
    const view = await open()
    expect(getServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: 200 }, expect.objectContaining({signal:expect.any(AbortSignal)}))
    const select = view.getComponent(AppSelect)
    expect(select.props()).toMatchObject({ modelValue: '200', portal: true, minWidth: '88px', height: '36px' })
    expect(select.props('options')).toEqual([100, 200, 500, 1000].map((n) => ({ value: String(n), label: String(n) })))
    expect(view.find('select').exists()).toBe(false)
  })

  it.each([100, 500, 1000, 200])('requests numeric %s through the actual portal menu', async (n) => {
    const view = await open()
    await view.get('[role="combobox"]').trigger('click')
    const menu = document.body.querySelector('[role="listbox"]')!
    expect(view.element.contains(menu)).toBe(false)
    const option = Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="option"]')).find((el) => el.textContent?.trim() === String(n))!
    option.click()
    await flushPromises()
    expect(getServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: n }, expect.objectContaining({signal:expect.any(AbortSignal)}))
    expect(document.querySelector('[role="listbox"]')).toBeNull()
  })

  it('stops the old stream on line-count change and uses numeric lines when following again', async () => {
    const view = await open()
    await button('跟随').trigger('click')
    view.getComponent(AppSelect).vm.$emit('update:modelValue', '500')
    await flushPromises()
    expect(stop).toHaveBeenCalledTimes(1)
    await button('跟随').trigger('click')
    expect(subscribeServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: 500 }, expect.any(Object), expect.objectContaining({signal:expect.any(AbortSignal)}))
  })

  it('ignores unsupported string values without issuing another request', async () => {
    const view = await open()
    const calls = vi.mocked(getServerLogs).mock.calls.length
    view.getComponent(AppSelect).vm.$emit('update:modelValue', 'invalid')
    await flushPromises()
    expect(getServerLogs).toHaveBeenCalledTimes(calls)
  })

  it.each(['api', 'fallback'])('reports copied only for real %s success', async (mode) => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    if (mode === 'api') Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    else exec.mockReturnValue(true)
    const view = await open()
    await button('复制').trigger('click')
    await flushPromises()
    expect(toast.toasts.value).toEqual([expect.objectContaining({ type: 'success' })])
    expect(view.findComponent(CopyTextDialog).exists()).toBe(false)
    if (mode === 'api') expect(writeText).toHaveBeenCalledWith('line 1\nline 2')
  })

  it.each(['missing', 'denied', 'throws'])('opens manual copy without success toast when %s and fallback fails', async (mode) => {
    if (mode === 'denied') Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockRejectedValue(new Error('Denied')) } })
    if (mode === 'throws') exec.mockImplementation(() => { throw new Error('Unsupported') })
    const view = await open()
    await button('复制').trigger('click')
    await flushPromises()
    expect(toast.toasts.value).toHaveLength(0)
    expect(view.getComponent(CopyTextDialog).props('text')).toBe('line 1\nline 2')
    expect((document.querySelector('.copy-text') as HTMLTextAreaElement).value).toBe('line 1\nline 2')
    view.getComponent(CopyTextDialog).vm.$emit('close')
    await flushPromises()
    expect(document.querySelector('.copy-dialog')).toBeNull()
    expect(subscribeServerLogs).not.toHaveBeenCalled()
  })

  it('disables copy for empty logs', async () => {
    vi.mocked(getServerLogs).mockResolvedValue({ lines: [] } as unknown as Awaited<ReturnType<typeof getServerLogs>>)
    await open()
    expect(button('复制').attributes('disabled')).toBeDefined()
  })
})

it('pins actual refresh follow and copy to the selected team and ignores them after revocation', async ()=>{
  useSessionStore().setUser({username:'member',userId:'u',multiTenant:true});
  const permissions=usePermissionStore();
  permissions.setTeam('a');
  vi.mocked(access.decisions).mockImplementation(async(team,queries)=>({teamId:team,userId:'u',modeEpoch:1,catalogVersion:1,revision:1,allowed:queries.map(q=>team==='a'&&q.action==='container.inspect'&&q.resourceId==='server-a')}));
  await open();
  const expected={headers:{'X-Pipewright-Team':'a'},signal:expect.any(AbortSignal)};
  const refresh=button('刷新');
  const follow=button('跟随');
  const copy=button('复制');
  await refresh.trigger('click');
  await flushPromises();
  expect(getServerLogs).toHaveBeenLastCalledWith('server-a',{source:'docker',target:'app',lines:200},expect.objectContaining(expected));
  let completeCopy!:()=>void;
  const writer=vi.fn(()=>new Promise<void>(r=>{completeCopy=r}));
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:writer}});
  await copy.trigger('click');
  expect(writer).toHaveBeenCalledWith('line 1\nline 2');
  await follow.trigger('click');
  expect(subscribeServerLogs).toHaveBeenLastCalledWith('server-a',{source:'docker',target:'app',lines:200},expect.any(Object),expect.objectContaining(expected));
  const signal=vi.mocked(subscribeServerLogs).mock.calls.at(-1)![3]!.signal;
  const reads=vi.mocked(getServerLogs).mock.calls.length;
  const streams=vi.mocked(subscribeServerLogs).mock.calls.length;
  permissions.reject('a',{action:'container.inspect',resourceId:'server-a'});
  refresh.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  follow.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  copy.element.dispatchEvent(new MouseEvent('click',{bubbles:true}));
  completeCopy();
  await flushPromises();
  expect(signal?.aborted).toBe(true);
  expect(getServerLogs).toHaveBeenCalledTimes(reads);
  expect(subscribeServerLogs).toHaveBeenCalledTimes(streams);
  expect(writer).toHaveBeenCalledTimes(1);
  expect(wrapper!.text()).not.toContain('line 1');
  expect(toast.toasts.value).toHaveLength(0);
});

it('clears only loaded container logs and closes from each actual drawer control', async () => {
  const view = await open()
  expect(view.text()).toContain('line 1')
  const historyCalls = vi.mocked(getServerLogs).mock.calls.length
  await button(i18n.global.t('opsContainer.logs.clear')).trigger('click')
  expect(view.text()).not.toContain('line 1')
  expect(vi.mocked(getServerLogs).mock.calls).toHaveLength(historyCalls)
  await view.get('.drawer__close').trigger('click')
  await view.get('.drawer-scrim').trigger('click')
  expect(view.emitted('close')).toHaveLength(2)
})

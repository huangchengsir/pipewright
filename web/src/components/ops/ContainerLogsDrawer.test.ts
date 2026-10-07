import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import ContainerLogsDrawer from './ContainerLogsDrawer.vue'
import AppSelect from '../ui/AppSelect.vue'
import CopyTextDialog from './CopyTextDialog.vue'
import { getServerLogs, subscribeServerLogs } from '../../api/servers'
import { useToast } from '../../composables/useToast'

vi.mock('../../api/servers', () => ({ getServerLogs: vi.fn(), subscribeServerLogs: vi.fn() }))
let wrapper: VueWrapper | undefined
const exec = vi.fn()
const stop = vi.fn()
const toast = useToast()
beforeEach(() => {
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
    expect(getServerLogs).toHaveBeenLastCalledWith('new',{source:'docker',target:'other',lines:200})
    view.unmount(); wrapper=undefined
    old.onLine?.('after unmount'); old.onTransportError?.(new Event('error'))
    expect(stop).toHaveBeenCalled()
  })
  it('defaults to 200 with a string model, numeric options and fixed portal dimensions', async () => {
    const view = await open()
    expect(getServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: 200 })
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
    expect(getServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: n })
    expect(document.querySelector('[role="listbox"]')).toBeNull()
  })

  it('stops the old stream on line-count change and uses numeric lines when following again', async () => {
    const view = await open()
    await button('跟随').trigger('click')
    view.getComponent(AppSelect).vm.$emit('update:modelValue', '500')
    await flushPromises()
    expect(stop).toHaveBeenCalledTimes(1)
    await button('跟随').trigger('click')
    expect(subscribeServerLogs).toHaveBeenLastCalledWith('server-a', { source: 'docker', target: 'app', lines: 500 }, expect.any(Object))
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

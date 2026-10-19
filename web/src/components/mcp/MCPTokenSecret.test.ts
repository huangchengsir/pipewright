import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import MCPTokenSecret from './MCPTokenSecret.vue'

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('../../composables/useToast', () => ({ useToast: () => toast }))
vi.mock('naive-ui', () => ({ NModal: { props: ['show'], template: '<div v-if="show"><slot name="header-extra"/><slot/><slot name="footer"/></div>' } }))

const wrappers: VueWrapper[] = []
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
const execDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')
const exec = vi.fn()
const writeText = vi.fn()
function deferred() {
  let resolve!: () => void
  let reject!: (error: Error) => void
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function render() {
  const wrapper = mount(MCPTokenSecret, {
    props: { token: 'fictional-one-time-secret' },
    attachTo: document.body,
  })
  wrappers.push(wrapper)
  return wrapper
}

describe('MCP one-time secret clipboard lifecycle', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    exec.mockReturnValue(true)
    writeText.mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: exec })
  })
  afterEach(() => {
    for (const wrapper of wrappers.splice(0)) wrapper.unmount()
    vi.restoreAllMocks()
    document.body.replaceChildren()
    window.getSelection()?.removeAllRanges()
    if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
    else Reflect.deleteProperty(navigator, 'clipboard')
    if (execDescriptor) Object.defineProperty(document, 'execCommand', execDescriptor)
    else Reflect.deleteProperty(document, 'execCommand')
  })
  it('copies the visible secret and emits the existing close event', async () => {
    const wrapper = render()
    await wrapper.get('.mcp-secret-copy').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenCalledWith('fictional-one-time-secret')
    expect(exec).not.toHaveBeenCalled()
    expect(toast.success).toHaveBeenCalledWith('已复制')
    await wrapper.get('.mcp-secret-close').trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
  })
  it('keeps the live HTTP fallback available and removes its temporary secret', async () => {
    writeText.mockRejectedValue(new Error('Denied'))
    exec.mockImplementation(() => {
      expect((document.activeElement as HTMLTextAreaElement).value).toBe('fictional-one-time-secret')
      return true
    })
    const wrapper = render()
    await wrapper.get('.mcp-secret-copy').trigger('click'); await flushPromises()
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
    expect(toast.success).toHaveBeenCalledWith('已复制')
  })
  it('reports a live copy failure without claiming success', async () => {
    writeText.mockRejectedValue(new Error('Denied'))
    exec.mockReturnValue(false)
    const wrapper = render()
    await wrapper.get('.mcp-secret-copy').trigger('click'); await flushPromises()
    expect(toast.error).toHaveBeenCalledWith('复制失败')
    expect(toast.success).not.toHaveBeenCalled()
    expect(document.querySelector('textarea')).toBeNull()
  })
  it.each(['resolved', 'rejected'])('does not copy, restore focus, or toast on another page after a late %s clipboard result', async (outcome) => {
    const pending = deferred()
    writeText.mockReturnValueOnce(pending.promise)
    const wrapper = render()
    await wrapper.get('.mcp-secret-copy').trigger('click')
    expect(writeText).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    const destination = document.createElement('input')
    destination.value = 'different page draft'
    document.body.append(destination)
    destination.focus()
    destination.setSelectionRange(2, 5)
    const append = vi.spyOn(document.body, 'appendChild')
    const focus = vi.spyOn(destination, 'focus')
    if (outcome === 'resolved') pending.resolve()
    else pending.reject(new Error('Clipboard permission denied'))
    await flushPromises()
    expect(append).not.toHaveBeenCalled()
    expect(exec).not.toHaveBeenCalled()
    expect(document.querySelector('textarea')).toBeNull()
    expect(document.body.textContent).not.toContain('fictional-one-time-secret')
    expect(document.activeElement).toBe(destination)
    expect([destination.selectionStart, destination.selectionEnd]).toEqual([2, 5])
    expect(focus).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })
  it.each(['', 'replacement-fictional-secret'])('ignores a delayed clipboard rejection after the secret becomes %s', async (token) => {
    const pending = deferred()
    writeText.mockReturnValueOnce(pending.promise)
    const wrapper = render()
    await wrapper.get('.mcp-secret-copy').trigger('click')
    await wrapper.setProps({ token })
    const append = vi.spyOn(document.body, 'appendChild')
    pending.reject(new Error('Denied')); await flushPromises()
    expect(append).not.toHaveBeenCalled()
    expect(exec).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import CopyTextDialog from './CopyTextDialog.vue'

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.replaceChildren()
  vi.restoreAllMocks()
})
function open(text = 'docker logs app\n<untrusted>') {
  wrapper = mount(CopyTextDialog, { attachTo: document.body, props: { text } })
  return document.body.querySelector('.copy-dialog') as HTMLElement
}

describe('manual copy dialog', () => {
  it('renders visible readonly text in a portal, focuses and selects it without reporting success', () => {
    const dialog = open()
    const textarea = dialog.querySelector('textarea')!
    expect(wrapper!.element.contains(dialog)).toBe(false)
    expect(dialog.getAttribute('role')).toBe('dialog')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(textarea.value).toBe('docker logs app\n<untrusted>')
    expect(textarea.readOnly).toBe(true)
    expect(document.activeElement).toBe(textarea)
    expect(textarea.selectionEnd).toBe(textarea.value.length)
    expect(dialog.querySelector('untrusted')).toBeNull()
    expect(dialog.textContent).toContain('尚未确认')
    expect(wrapper!.emitted('copied')).toBeUndefined()
  })

  it('allows reselecting text and traps Tab in both directions', () => {
    const dialog = open()
    const textarea = dialog.querySelector('textarea')!
    const buttons = dialog.querySelectorAll('button')
    buttons[1].click()
    expect(document.activeElement).toBe(textarea)
    buttons[1].focus()
    buttons[1].dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(buttons[0])
    buttons[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    expect(document.activeElement).toBe(buttons[1])
  })

  it.each(['escape', 'close', 'backdrop'])('emits only close for %s', (mode) => {
    const dialog = open()
    if (mode === 'escape') dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    else if (mode === 'close') (dialog.querySelector('.copy-close') as HTMLButtonElement).click()
    else (document.querySelector('.copy-overlay') as HTMLElement).click()
    expect(wrapper!.emitted('close')).toHaveLength(1)
    expect(wrapper!.emitted('copied')).toBeUndefined()
  })

  it('restores the prior focus and input selection when closed', () => {
    const input = document.createElement('textarea')
    input.value = 'draft command'
    document.body.append(input)
    input.focus()
    input.setSelectionRange(1, 5, 'backward')
    open()
    wrapper!.unmount()
    wrapper = undefined
    expect(document.activeElement).toBe(input)
    expect([input.selectionStart, input.selectionEnd, input.selectionDirection]).toEqual([1, 5, 'backward'])
    expect(document.querySelector('.copy-dialog')).toBeNull()
  })

  it('restores an existing document selection on unmount', () => {
    const p = document.createElement('p')
    p.textContent = 'selected logs'
    document.body.append(p)
    const selection = window.getSelection()!
    selection.setBaseAndExtent(p.firstChild!, 8, p.firstChild!, 1)
    open()
    wrapper!.unmount()
    wrapper = undefined
    expect(selection.anchorNode).toBe(p.firstChild)
    expect([selection.anchorOffset, selection.focusOffset]).toEqual([8, 1])
  })
})

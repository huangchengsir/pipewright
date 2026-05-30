import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ConfirmDialog from './ConfirmDialog.vue'
import { useConfirm } from '../../composables/useConfirm'

// ConfirmDialog teleports to body; query the document directly for rendered nodes.
function dialogEl(): HTMLElement | null {
  return document.querySelector('.cfm-dialog')
}

describe('ConfirmDialog', () => {
  beforeEach(() => {
    const c = useConfirm()
    if (c.pending.value) c._resolve(false)
  })

  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('is not rendered when there is no pending request', () => {
    mount(ConfirmDialog, { attachTo: document.body })
    expect(dialogEl()).toBeNull()
  })

  it('renders title, body and confirm label when a request opens', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    confirm.open({ title: '回滚到 #126?', body: '切回上一稳定版本', confirmLabel: '确认回滚' })
    await nextTick()

    const el = dialogEl()
    expect(el).not.toBeNull()
    expect(el!.textContent).toContain('回滚到 #126?')
    expect(el!.textContent).toContain('切回上一稳定版本')
    expect(el!.querySelector('.cfm-dialog__confirm')!.textContent).toContain('确认回滚')
    expect(el!.getAttribute('role')).toBe('dialog')
    expect(el!.getAttribute('aria-modal')).toBe('true')
  })

  it('clicking 确认 resolves the promise with true', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    const p = confirm.open({ title: 't', body: 'b' })
    await nextTick()

    ;(document.querySelector('.cfm-dialog__confirm') as HTMLButtonElement).click()
    await expect(p).resolves.toBe(true)
  })

  it('clicking 取消 resolves the promise with false', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    const p = confirm.open({ title: 't', body: 'b' })
    await nextTick()

    ;(document.querySelector('.cfm-dialog__cancel') as HTMLButtonElement).click()
    await expect(p).resolves.toBe(false)
  })

  it('type-to-confirm keeps confirm disabled until the exact text is typed', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    confirm.open({ title: '重置', body: '不可恢复', confirmText: 'acme' })
    await nextTick()

    const confirmBtn = document.querySelector('.cfm-dialog__confirm') as HTMLButtonElement
    const input = document.querySelector('.cfm-dialog__type-input') as HTMLInputElement
    expect(confirmBtn.disabled).toBe(true)

    input.value = 'wrong'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(confirmBtn.disabled).toBe(true)

    input.value = 'acme'
    input.dispatchEvent(new Event('input'))
    await nextTick()
    expect(confirmBtn.disabled).toBe(false)
  })

  it('type-to-confirm does not resolve true while text does not match', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    const p = confirm.open({ title: '重置', body: 'x', confirmText: 'acme' })
    await nextTick()

    let settled: boolean | 'pending' = 'pending'
    p.then((v) => (settled = v))

    // Force-click confirm while mismatched — doConfirm() must guard.
    ;(document.querySelector('.cfm-dialog__confirm') as HTMLButtonElement).click()
    await nextTick()
    expect(settled).toBe('pending')

    // Now type correctly and cancel to settle the promise for cleanup.
    confirm._resolve(false)
    await expect(p).resolves.toBe(false)
  })

  it('Escape key cancels (resolves false)', async () => {
    const confirm = useConfirm()
    mount(ConfirmDialog, { attachTo: document.body })
    const p = confirm.open({ title: 't', body: 'b' })
    await nextTick()

    const el = dialogEl()!
    el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await expect(p).resolves.toBe(false)
  })
})

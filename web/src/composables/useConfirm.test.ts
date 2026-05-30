import { describe, it, expect, beforeEach } from 'vitest'
import { useConfirm } from './useConfirm'

describe('useConfirm', () => {
  beforeEach(() => {
    // Settle any leftover pending request so state is clean between tests.
    const c = useConfirm()
    if (c.pending.value) c._resolve(false)
  })

  it('exposes a pending request once open() is called', () => {
    const confirm = useConfirm()
    expect(confirm.pending.value).toBeNull()
    confirm.open({ title: '回滚?', body: '切回上一版本' })
    expect(confirm.pending.value).not.toBeNull()
    expect(confirm.pending.value?.options.title).toBe('回滚?')
  })

  it('resolves true when confirmed', async () => {
    const confirm = useConfirm()
    const p = confirm.open({ title: 't', body: 'b' })
    confirm._resolve(true)
    await expect(p).resolves.toBe(true)
    expect(confirm.pending.value).toBeNull()
  })

  it('resolves false when cancelled', async () => {
    const confirm = useConfirm()
    const p = confirm.open({ title: 't', body: 'b' })
    confirm._resolve(false)
    await expect(p).resolves.toBe(false)
  })

  it('settles a prior pending promise with false when a new open() supersedes it', async () => {
    const confirm = useConfirm()
    const first = confirm.open({ title: '第一', body: 'b' })
    const second = confirm.open({ title: '第二', body: 'b' })

    // The superseded first promise must not hang forever.
    await expect(first).resolves.toBe(false)
    // The new request is now the pending one.
    expect(confirm.pending.value?.options.title).toBe('第二')

    confirm._resolve(true)
    await expect(second).resolves.toBe(true)
  })

  it('carries type-to-confirm options through to pending', () => {
    const confirm = useConfirm()
    confirm.open({ title: '重置', body: '不可恢复', confirmText: 'acme', variant: 'danger' })
    expect(confirm.pending.value?.options.confirmText).toBe('acme')
    expect(confirm.pending.value?.options.variant).toBe('danger')
  })
})

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useToast } from './useToast'

// useToast 是单例(模块级 ref),每个用例先 clear 隔离状态。
describe('useToast', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useToast().clear()
  })

  afterEach(() => {
    useToast().clear()
    vi.useRealTimers()
  })

  it('stacks multiple toasts in insertion order', () => {
    const toast = useToast()
    toast.success('一')
    toast.error('二')
    toast.info('三')

    expect(toast.toasts.value).toHaveLength(3)
    expect(toast.toasts.value.map((t) => t.title)).toEqual(['一', '二', '三'])
  })

  it('returns a unique increasing id per toast', () => {
    const toast = useToast()
    const a = toast.success('a')
    const b = toast.success('b')
    expect(b).toBeGreaterThan(a)
  })

  it('auto-dismisses success after 4s', () => {
    const toast = useToast()
    toast.success('成功')
    expect(toast.toasts.value).toHaveLength(1)

    vi.advanceTimersByTime(3999)
    expect(toast.toasts.value).toHaveLength(1)

    vi.advanceTimersByTime(1)
    expect(toast.toasts.value).toHaveLength(0)
  })

  it('auto-dismisses warn after 6s (longer than success)', () => {
    const toast = useToast()
    toast.warn('警告')
    vi.advanceTimersByTime(4000)
    expect(toast.toasts.value).toHaveLength(1) // still alive past 4s
    vi.advanceTimersByTime(2000)
    expect(toast.toasts.value).toHaveLength(0)
  })

  it('error toasts never auto-dismiss (manual only)', () => {
    const toast = useToast()
    toast.error('失败')
    vi.advanceTimersByTime(60_000)
    expect(toast.toasts.value).toHaveLength(1)
    expect(toast.toasts.value[0].duration).toBeUndefined()
  })

  it('duration:0 forces manual-only even for success', () => {
    const toast = useToast()
    toast.success('钉住', { duration: 0 })
    vi.advanceTimersByTime(60_000)
    expect(toast.toasts.value).toHaveLength(1)
  })

  it('custom duration overrides the type default', () => {
    const toast = useToast()
    toast.success('快', { duration: 1000 })
    vi.advanceTimersByTime(1000)
    expect(toast.toasts.value).toHaveLength(0)
  })

  it('dismiss removes a specific toast and cancels its timer', () => {
    const toast = useToast()
    const id = toast.success('一')
    toast.success('二')
    toast.dismiss(id)

    expect(toast.toasts.value).toHaveLength(1)
    expect(toast.toasts.value[0].title).toBe('二')

    // Advancing time must not throw / re-fire the cancelled timer
    vi.advanceTimersByTime(10_000)
    expect(toast.toasts.value).toHaveLength(0) // only the remaining auto-dismiss fired
  })

  it('clear removes everything and cancels all timers', () => {
    const toast = useToast()
    toast.success('一')
    toast.warn('二')
    toast.clear()
    expect(toast.toasts.value).toHaveLength(0)
    vi.advanceTimersByTime(10_000) // no dangling timers should re-add
    expect(toast.toasts.value).toHaveLength(0)
  })

  it('carries detail and action through to the item', () => {
    const toast = useToast()
    const onClick = vi.fn()
    toast.error('部署失败', { detail: '健康检查超时', action: { label: '查看', onClick } })
    const item = toast.toasts.value[0]
    expect(item.detail).toBe('健康检查超时')
    expect(item.action?.label).toBe('查看')
    item.action?.onClick()
    expect(onClick).toHaveBeenCalledOnce()
  })
})

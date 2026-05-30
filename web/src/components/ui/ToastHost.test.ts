import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import ToastHost from './ToastHost.vue'
import { useToast } from '../../composables/useToast'

describe('ToastHost', () => {
  beforeEach(() => {
    useToast().clear()
  })

  afterEach(() => {
    useToast().clear()
    vi.restoreAllMocks()
  })

  it('renders one item per active toast with title + detail', async () => {
    const toast = useToast()
    const wrapper = mount(ToastHost)
    toast.success('部署成功', { detail: 'acme-web #127' })
    toast.error('部署失败')
    await nextTick()

    const items = wrapper.findAll('.toast-item')
    expect(items).toHaveLength(2)
    expect(wrapper.text()).toContain('部署成功')
    expect(wrapper.text()).toContain('acme-web #127')
    expect(wrapper.text()).toContain('部署失败')
  })

  it('applies a per-type modifier class', async () => {
    const toast = useToast()
    const wrapper = mount(ToastHost)
    toast.error('x')
    await nextTick()
    expect(wrapper.find('.toast-item--error').exists()).toBe(true)
  })

  it('the close button dismisses its toast', async () => {
    const toast = useToast()
    const wrapper = mount(ToastHost)
    toast.error('删不掉?')
    await nextTick()
    expect(wrapper.findAll('.toast-item')).toHaveLength(1)

    await wrapper.find('.toast-item__close').trigger('click')
    expect(wrapper.findAll('.toast-item')).toHaveLength(0)
  })

  it('an action toast fires onClick and then auto-dismisses', async () => {
    const toast = useToast()
    const onClick = vi.fn()
    const wrapper = mount(ToastHost)
    toast.error('部署失败', { action: { label: '查看运行', onClick } })
    await nextTick()

    await wrapper.find('.toast-item__action').trigger('click')
    expect(onClick).toHaveBeenCalledOnce()
    // action click dismisses the toast per ToastHost handler
    expect(wrapper.findAll('.toast-item')).toHaveLength(0)
  })

  it('exposes an aria-live status region for screen readers', () => {
    const wrapper = mount(ToastHost)
    const host = wrapper.find('.toast-host')
    expect(host.attributes('role')).toBe('status')
    expect(host.attributes('aria-live')).toBe('polite')
  })
})

import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ProgressBar from './ProgressBar.vue'

describe('ProgressBar', () => {
  it('renders a determinate bar with aria-valuenow when value is given', () => {
    const wrapper = mount(ProgressBar, { props: { value: 42, label: '部署进度' } })
    const bar = wrapper.find('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('42')
    expect(bar.attributes('aria-valuemin')).toBe('0')
    expect(bar.attributes('aria-valuemax')).toBe('100')
    expect(wrapper.find('.progress-bar__fill').attributes('style')).toContain('width: 42%')
  })

  it('clamps the fill width to 0..100', () => {
    const over = mount(ProgressBar, { props: { value: 150 } })
    expect(over.find('.progress-bar__fill').attributes('style')).toContain('width: 100%')

    const under = mount(ProgressBar, { props: { value: -20 } })
    expect(under.find('.progress-bar__fill').attributes('style')).toContain('width: 0%')
  })

  it('renders an indeterminate bar (no aria-valuenow) when value is omitted', () => {
    const wrapper = mount(ProgressBar, { props: { label: '加载中' } })
    const bar = wrapper.find('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBeUndefined()
    expect(wrapper.find('.progress-bar__indeterminate').exists()).toBe(true)
    expect(wrapper.find('.progress-bar__fill').exists()).toBe(false)
  })

  it('applies the variant modifier to the fill', () => {
    const wrapper = mount(ProgressBar, { props: { value: 50, variant: 'error' } })
    expect(wrapper.find('.progress-bar__fill--error').exists()).toBe(true)
  })
})

import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AppButton from './AppButton.vue'

describe('AppButton', () => {
  it('renders slot content', () => {
    const wrapper = mount(AppButton, { slots: { default: '部署' } })
    expect(wrapper.text()).toContain('部署')
  })

  it('emits click when enabled', async () => {
    const wrapper = mount(AppButton)
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toHaveLength(1)
  })

  it('does NOT emit click when disabled', async () => {
    const wrapper = mount(AppButton, { props: { disabled: true } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
    expect(wrapper.attributes('disabled')).toBeDefined()
    expect(wrapper.attributes('aria-disabled')).toBe('true')
  })

  it('does NOT emit click when loading, and exposes aria-busy + spinner', async () => {
    const wrapper = mount(AppButton, { props: { loading: true } })
    await wrapper.trigger('click')
    expect(wrapper.emitted('click')).toBeUndefined()
    expect(wrapper.attributes('aria-busy')).toBe('true')
    expect(wrapper.find('.app-btn__spinner').exists()).toBe(true)
    // loading also disables the native button
    expect(wrapper.attributes('disabled')).toBeDefined()
  })

  it('no spinner is rendered in the resting state', () => {
    const wrapper = mount(AppButton)
    expect(wrapper.find('.app-btn__spinner').exists()).toBe(false)
    expect(wrapper.attributes('aria-busy')).toBeUndefined()
  })

  it.each(['primary', 'default', 'ghost', 'danger', 'ai'] as const)(
    'applies the %s variant modifier',
    (variant) => {
      const wrapper = mount(AppButton, { props: { variant } })
      expect(wrapper.classes()).toContain(`app-btn--${variant}`)
    },
  )

  it('defaults to type=button (avoids accidental form submit)', () => {
    const wrapper = mount(AppButton)
    expect(wrapper.attributes('type')).toBe('button')
  })

  it('honours type=submit when requested', () => {
    const wrapper = mount(AppButton, { props: { type: 'submit' } })
    expect(wrapper.attributes('type')).toBe('submit')
  })
})

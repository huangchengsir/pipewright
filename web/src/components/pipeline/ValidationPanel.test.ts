import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ValidationPanel from './ValidationPanel.vue'

describe('ValidationPanel certainty', () => {
  it('does not claim readiness or all passed before a result is available', () => {
    const wrapper = mount(ValidationPanel, { props: { loading: false, ready: false, issues: [], pendingMessage: 'Save the pending draft first' } })
    expect(wrapper.text()).toContain('Save the pending draft first')
    expect(wrapper.find('.vp-ready-badge').exists()).toBe(false)
    expect(wrapper.find('.vp-empty-icon').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('所有检查通过')
    wrapper.unmount()
  })
  it('shows all passed only for a confirmed ready result', () => {
    const wrapper = mount(ValidationPanel, { props: { loading: false, ready: true, issues: [] } })
    expect(wrapper.find('.vp-ready-badge--ok').exists()).toBe(true)
    expect(wrapper.find('.vp-empty-icon').exists()).toBe(true)
    wrapper.unmount()
  })
})

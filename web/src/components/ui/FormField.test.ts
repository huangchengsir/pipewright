import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { h } from 'vue'
import FormField from './FormField.vue'

describe('FormField', () => {
  it('renders the label wired to the field id', () => {
    const wrapper = mount(FormField, {
      props: { label: '账号', fieldId: 'username' },
    })
    const label = wrapper.find('label')
    expect(label.text()).toContain('账号')
    expect(label.attributes('for')).toBe('username')
  })

  it('shows a required asterisk only when required', () => {
    const plain = mount(FormField, { props: { label: 'x', fieldId: 'f' } })
    expect(plain.find('.form-field__required').exists()).toBe(false)

    const req = mount(FormField, { props: { label: 'x', fieldId: 'f', required: true } })
    expect(req.find('.form-field__required').exists()).toBe(true)
  })

  it('renders a hint when there is no error', () => {
    const wrapper = mount(FormField, {
      props: { label: 'x', fieldId: 'f', hint: '至少 8 位' },
    })
    const hint = wrapper.find('.form-field__hint')
    expect(hint.text()).toBe('至少 8 位')
    expect(hint.attributes('id')).toBe('f-hint')
  })

  it('shows the error (role=alert) and hides the hint when an error is present', () => {
    const wrapper = mount(FormField, {
      props: { label: 'x', fieldId: 'f', hint: '提示', error: '不能为空' },
    })
    expect(wrapper.find('.form-field__hint').exists()).toBe(false)
    const err = wrapper.find('.form-field__error')
    expect(err.text()).toContain('不能为空')
    expect(err.attributes('role')).toBe('alert')
    expect(err.attributes('id')).toBe('f-err')
    expect(wrapper.classes()).toContain('form-field--error')
  })

  it('passes aria wiring down to a slotted control', () => {
    const wrapper = mount(FormField, {
      props: { label: 'x', fieldId: 'pw', error: '错误', required: true, hint: 'h' },
      slots: {
        // Render-function slot so we can read the scoped slot props the
        // component exposes (fieldId + aria wiring) and reflect them onto a control.
        // Vue camelCases scoped slot prop keys (ariaInvalid, ariaDescribedby, …).
        default: (s: Record<string, unknown>) =>
          h('input', {
            id: s.fieldId as string,
            'aria-invalid': s.ariaInvalid,
            'aria-describedby': s.ariaDescribedby,
            'aria-required': s.ariaRequired,
          }),
      },
    })
    const input = wrapper.find('input')
    expect(input.attributes('id')).toBe('pw')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(input.attributes('aria-required')).toBe('true')
    // describedby points at the error id (hint is suppressed while erroring)
    expect(input.attributes('aria-describedby')).toContain('pw-err')
  })
})

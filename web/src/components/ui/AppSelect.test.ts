import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AppSelect from './AppSelect.vue'

let wrapper: VueWrapper | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('AppSelect portal menu', () => {
  it('clamps refreshed options and blocks choices while disabled', async () => {
    wrapper = mount(AppSelect, {
      props: {
        modelValue: '',
        options: [
          { value: 'a', label: 'A' },
          { value: 'b', label: 'B' },
          { value: 'c', label: 'C' },
        ],
      },
    })
    const trigger = wrapper.get('.app-select__trigger')
    await trigger.trigger('click')
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await wrapper.setProps({ options: [{ value: 'new', label: 'New' }] })
    await trigger.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['new'])
    await trigger.trigger('click')
    const option = wrapper.get('[role="option"]')
    await wrapper.setProps({ disabled: true })
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)
    await option.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
  })
  it('anchors upward menus by the bottom edge for wrapped labels', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 20,
      y: window.innerHeight - 50,
      top: window.innerHeight - 50,
      bottom: window.innerHeight - 12,
      left: 20,
      right: 200,
      width: 180,
      height: 38,
      toJSON: () => ({}),
    })
    wrapper = mount(AppSelect, {
      attachTo: document.body,
      props: {
        modelValue: '',
        portal: true,
        options: [{ value: 'long', label: 'Long model label '.repeat(30) }],
      },
    })
    await wrapper.get('.app-select__trigger').trigger('click')
    const menu = document.body.querySelector(
      '.app-select__menu--portal',
    ) as HTMLElement
    expect(menu.style.top).toBe('auto')
    expect(menu.style.bottom).toBe('56px')
  })
  it('ignores IME confirmation and resets filtering when reopening', async () => {
    wrapper = mount(AppSelect, {
      props: {
        modelValue: 'beta',
        searchable: true,
        options: [
          { value: 'alpha', label: 'Alpha' },
          { value: 'beta', label: 'Beta' },
        ],
      },
    })
    await wrapper.get('.app-select__trigger').trigger('click')
    await wrapper.get('input').setValue('Beta')
    await wrapper
      .get('input')
      .trigger('keydown', { key: 'Enter', isComposing: true })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('[role="listbox"]').exists()).toBe(true)
    await wrapper.get('.app-select__trigger').trigger('click')
    await wrapper.get('.app-select__trigger').trigger('click')
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['beta'])
  })
  it('filters by label or exact ID and chooses filtered options by keyboard', async () => {
    wrapper = mount(AppSelect, {
      props: {
        modelValue: '',
        searchable: true,
        options: [
          { value: 'alpha', label: 'Alpha' },
          { value: 'provider/beta:1', label: 'Custom' },
        ],
      },
    })
    await wrapper.get('.app-select__trigger').trigger('click')
    await wrapper.get('input').setValue('beta:1')
    expect(wrapper.findAll('[role="option"]')).toHaveLength(1)
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([
      'provider/beta:1',
    ])
  })
  it('keeps options outside a scrolling parent and emits the selected value', async () => {
    wrapper = mount(AppSelect, {
      attachTo: document.body,
      props: {
        modelValue: 'auto',
        options: [
          { value: 'auto', label: '自动' },
          { value: 'image', label: '容器镜像' },
        ],
        portal: true,
        ariaLabel: '部署产物类型',
      },
    })

    await wrapper.find('.app-select__trigger').trigger('click')
    const menu = document.body.querySelector('.app-select__menu--portal')
    expect(menu).not.toBeNull()
    expect(wrapper.element.contains(menu)).toBe(false)
    expect(menu?.querySelectorAll('[role="option"]')).toHaveLength(2)
    ;(menu?.querySelectorAll('[role="option"]')[1] as HTMLButtonElement).click()
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['image'])
    expect(wrapper.emitted('change')).toHaveLength(1)
    expect(document.body.querySelector('.app-select__menu--portal')).toBeNull()
  })

  it('closes when clicking outside the menu', async () => {
    wrapper = mount(AppSelect, {
      attachTo: document.body,
      props: {
        modelValue: '',
        options: [{ value: 'one', label: 'One' }],
        portal: true,
      },
    })
    await wrapper.find('.app-select__trigger').trigger('click')
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await wrapper.vm.$nextTick()
    expect(document.body.querySelector('.app-select__menu--portal')).toBeNull()
  })
})

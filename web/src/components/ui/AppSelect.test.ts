import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import AppSelect from './AppSelect.vue'

let wrapper: VueWrapper | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('AppSelect portal menu', () => {
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
      props: { modelValue: '', options: [{ value: 'one', label: 'One' }], portal: true },
    })
    await wrapper.find('.app-select__trigger').trigger('click')
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await wrapper.vm.$nextTick()
    expect(document.body.querySelector('.app-select__menu--portal')).toBeNull()
  })
})

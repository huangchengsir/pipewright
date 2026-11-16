import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import LocaleSwitcher from '../LocaleSwitcher.vue'
import ThemeToggle from '../ThemeToggle.vue'
import ErrorState from './ErrorState.vue'
import ResourcePager from './ResourcePager.vue'
import { i18n, setLocale } from '../../i18n'
import { useThemeStore } from '../../stores/theme'

it('opens the actual language menu and selects its English option', async () => {
  const previous = i18n.global.locale.value
  setLocale('zh-CN')
  const wrapper = mount(LocaleSwitcher, { attachTo: document.body })
  try {
    await wrapper.get('button.locale-switcher').trigger('click')
    await flushPromises()
    const options = [...document.body.querySelectorAll<HTMLElement>('.n-dropdown-option-body')]
    expect(options).toHaveLength(8)
    const english = options.find(option => option.textContent?.trim().includes('English'))
    expect(english).toBeDefined()
    english!.click()
    await flushPromises()
    expect(i18n.global.locale.value).toBe('en')
  } finally {
    wrapper.unmount()
    setLocale(previous)
  }
})

it('switches between the two theme labels using the actual button', async () => {
  setActivePinia(createPinia())
  const theme = useThemeStore()
  const previous = theme.current
  const wrapper = mount(ThemeToggle)
  try {
    const button = wrapper.get('button')
    const label = button.attributes('aria-label')
    await button.trigger('click')
    expect(theme.current).not.toBe(previous)
    expect(button.attributes('aria-label')).not.toBe(label)
    await button.trigger('click')
    expect(theme.current).toBe(previous)
  } finally {
    wrapper.unmount()
  }
})

it('retries one visible error and hides its action when retry is disabled', async () => {
  const wrapper = mount(ErrorState)
  try {
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    await wrapper.setProps({ showRetry: false })
    expect(wrapper.find('button').exists()).toBe(false)
  } finally {
    wrapper.unmount()
  }
})

it('uses both page direction buttons and refuses out-of-bound jumps', async () => {
  const wrapper = mount(ResourcePager, { props: { page: 2, pages: 3 } })
  try {
    const [previous, next] = wrapper.findAll<HTMLButtonElement>('button')
    await previous!.trigger('click')
    await next!.trigger('click')
    expect(wrapper.emitted('change')).toEqual([[1], [3]])
    await wrapper.setProps({ page: 1 })
    expect(previous!.element.disabled).toBe(true)
    await previous!.trigger('click')
    await wrapper.setProps({ page: 3 })
    expect(next!.element.disabled).toBe(true)
    await next!.trigger('click')
    expect(wrapper.emitted('change')).toHaveLength(2)
  } finally {
    wrapper.unmount()
  }
})

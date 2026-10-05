import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import ContinueOnboarding from './ContinueOnboarding.vue'
import { dismissOnboarding, rereadOnboardingPreferences, resetOnboarding, selectOnboardingProject, useOnboardingPreferences } from '../../composables/useOnboarding'

let wrapper: VueWrapper | undefined
const global = { stubs: { RouterLink: { props: ['to'], template: '<a :href="to" @click.prevent><slot /></a>' } } }
beforeEach(() => { localStorage.clear(); resetOnboarding() })
afterEach(() => { wrapper?.unmount(); wrapper = undefined; dismissOnboarding(); vi.restoreAllMocks() })

describe('global continue entry', () => {
  it.each(['/dashboard', '/library', '/metrics/dora', '/settings/account', '/projects/a/pipeline'])('is available on %s without changing preferences', path => {
    const version = useOnboardingPreferences().preferences.version
    wrapper = mount(ContinueOnboarding, { props: { currentPath: path }, global })
    expect(wrapper.get('a').attributes('href')).toBe('/onboarding')
    expect(wrapper.get('a').attributes('title')).toBe('继续上手')
    expect(useOnboardingPreferences().preferences.version).toBe(version)
  })
  it.each([false, true])('hides on onboarding and reacts to navigation when dismissed is %s', async dismissed => {
    if (dismissed) dismissOnboarding()
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/onboarding' }, global })
    expect(wrapper.find('a').exists()).toBe(false)
    await wrapper.setProps({ currentPath: '/dashboard' })
    expect(wrapper.find('a').exists()).toBe(true)
    dismissOnboarding(); await wrapper.vm.$nextTick()
    expect(wrapper.find('a').exists()).toBe(true)
  })
  it('keeps the skipped entry visible without resetting preferences on mount', () => {
    dismissOnboarding()
    const preferences = useOnboardingPreferences().preferences
    const beforeMount = { ...preferences }
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/dashboard' }, global })
    expect(wrapper.get('a').attributes('href')).toBe('/onboarding')
    expect({ ...preferences }).toEqual(beforeMount)
    expect(localStorage.getItem('onboarding_dismissed')).toBe('1')
    expect(localStorage.getItem('onboarding_active')).toBeNull()
  })
  it('resets a skipped journey before route navigation and keeps the entry after leaving', async () => {
    selectOnboardingProject('project-a')
    dismissOnboarding()
    const router = createRouter({
      history: createMemoryHistory(),
      routes: ['/dashboard', '/onboarding', '/projects'].map(path => ({ path, component: { template: '<div />' } })),
    })
    await router.push('/dashboard')
    await router.isReady()
    const push = router.push.bind(router)
    const navigation = vi.spyOn(router, 'push').mockImplementation(to => {
      expect(useOnboardingPreferences().preferences).toMatchObject({ dismissed: false, active: true, completed: false, projectId: 'project-a' })
      expect(localStorage.getItem('onboarding_dismissed')).toBeNull()
      expect(localStorage.getItem('onboarding_active')).toBe('1')
      return push(to)
    })
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/dashboard' }, global: { plugins: [router] } })
    await wrapper.get('a').trigger('click')
    await flushPromises()
    expect(navigation).toHaveBeenCalledExactlyOnceWith('/onboarding')
    expect(router.currentRoute.value.path).toBe('/onboarding')
    await wrapper.setProps({ currentPath: '/onboarding' })
    expect(wrapper.find('a').exists()).toBe(false)
    await router.push('/projects')
    await wrapper.setProps({ currentPath: '/projects' })
    expect(wrapper.find('a').exists()).toBe(true)
    expect(useOnboardingPreferences().continuing.value).toBe(true)
  })
  it('does not reset an active journey on click', async () => {
    const preferences = useOnboardingPreferences().preferences
    const beforeMount = { ...preferences }
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/dashboard' }, global })
    await wrapper.get('a').trigger('click')
    expect({ ...preferences }).toEqual(beforeMount)
  })
  it.each([false, true])('hides completed onboarding even when dismissed is %s', dismissed => {
    if (dismissed) dismissOnboarding()
    localStorage.setItem('onboarding_completed', '1')
    rereadOnboardingPreferences()
    const preferences = useOnboardingPreferences().preferences
    const beforeMount = { ...preferences }
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/dashboard' }, global })
    expect(wrapper.find('a').exists()).toBe(false)
    expect({ ...preferences }).toEqual(beforeMount)
  })
  it('does not start an inactive, never-skipped journey on mount', () => {
    localStorage.removeItem('onboarding_active')
    rereadOnboardingPreferences()
    const preferences = useOnboardingPreferences().preferences
    const beforeMount = { ...preferences }
    wrapper = mount(ContinueOnboarding, { props: { currentPath: '/dashboard' }, global })
    expect(wrapper.find('a').exists()).toBe(false)
    expect({ ...preferences }).toEqual(beforeMount)
  })
})

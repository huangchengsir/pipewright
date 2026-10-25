import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AppShell from './AppShell.vue'
import productRouter from '../router'

describe('explicit report navigation ownership', () => {
  it('declares runs ownership on the unchanged product report URL', () => {
    const record = productRouter
      .getRoutes()
      .find((item) => item.name === 'release-guard-report')!
    expect(record.path).toBe('/projects/:projectId/release-guards/:guardId')
    expect(record.meta.navSection).toBe('runs')
  })
  it('highlights only runs on a report and keeps path fallback for other pages and invalid ownership', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/projects/:projectId/release-guards/:guardId',
          component: { template: '<div />' },
          meta: { navSection: 'runs' },
        },
        { path: '/projects/:id', component: { template: '<div />' } },
        {
          path: '/runs/:id',
          component: { template: '<div />' },
          meta: { navSection: 'future_section' },
        },
        { path: '/:pathMatch(.*)*', component: { template: '<div />' } },
      ],
    })
    await router.push('/projects/p/release-guards/g')
    const wrapper = mount(AppShell, {
      global: {
        plugins: [router, createPinia()],
        stubs: { ThemeToggle: true, ContinueOnboarding: true },
      },
    })
    await flushPromises()
    const selected = () =>
      wrapper
        .findAll('.nav-item[aria-current="page"]')
        .map((item) => item.attributes('href'))
    expect(selected()).toEqual(['/runs'])
    expect(wrapper.findAll('.nav-item--active')).toHaveLength(1)
    await router.push('/projects/p')
    expect(selected()).toEqual(['/projects'])
    await router.push('/runs/r')
    expect(selected()).toEqual(['/runs'])
    await router.push('/settings/notifications')
    expect(selected()).toEqual(['/settings/notifications'])
    // Preserve the existing visual path fallback while exposing one current page.
    expect(wrapper.findAll('.nav-item--active')).toHaveLength(2)
    await router.push('/settings/ai')
    expect(selected()).toEqual(['/settings'])
    wrapper.unmount()
  })
})

import { afterEach, describe, expect, it } from 'vitest'
import { h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import Settings from './Settings.vue'
import { i18n, setLocale, SUPPORTED_LOCALES } from '../i18n'

const paths = ['ai', 'oauth', 'notifications', 'vault', 'dns-providers', 'account', 'mcp', 'servers', 'diagnosis-stats', 'system']
let wrapper: VueWrapper | undefined

afterEach(() => { wrapper?.unmount(); setLocale('zh-CN') })
async function setup(initial = 'ai') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{
      path: '/settings', component: Settings,
      children: paths.map(path => ({ path, component: { render: () => h('p', { 'data-page': path }, path) } })),
    }],
  })
  await router.push(`/settings/${initial}`)
  await router.isReady()
  wrapper = mount({ render: () => h(RouterView) }, { global: { plugins: [router] } })
  await flushPromises()
  return { w: wrapper, router }
}

describe('Settings navigation', () => {
  it.each(['vault/', 'VAULT/'])('matches the selected mobile section for supported route %s', async path => {
    const { w, router } = await setup(path)
    expect((w.get('.settings-nav-select').element as HTMLSelectElement).value).toBe('/settings/vault')
    await w.get('.settings-nav-select').setValue('/settings/ai'); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/settings/ai')
    expect(w.get('.settings-content [data-page]').attributes('data-page')).toBe('ai')
  })
  it('retains all ten sections and uses the current route for both navigation controls', async () => {
    const { w, router } = await setup('vault')
    expect(w.findAll('.settings-nav a').map(link => link.attributes('href'))).toEqual(paths.map(path => `/settings/${path}`))
    expect(w.findAll('.settings-nav-select option').map(option => option.attributes('value'))).toEqual(paths.map(path => `/settings/${path}`))
    expect((w.get('.settings-nav-select').element as HTMLSelectElement).value).toBe('/settings/vault')
    await router.push('/settings/servers'); await flushPromises()
    expect((w.get('.settings-nav-select').element as HTMLSelectElement).value).toBe('/settings/servers')
    expect(w.get('.settings-content [data-page]').attributes('data-page')).toBe('servers')
  })

  it('uses the router for a mobile selection and honours existing navigation guards', async () => {
    const { w, router } = await setup()
    await w.get('.settings-nav-select').setValue('/settings/vault'); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/settings/vault')
    const removeGuard = router.beforeEach(to => to.path === '/settings/account' ? false : true)
    await w.get('.settings-nav-select').setValue('/settings/account'); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/settings/vault')
    expect((w.get('.settings-nav-select').element as HTMLSelectElement).value).toBe('/settings/vault')
    expect(w.get('.settings-content [data-page]').attributes('data-page')).toBe('vault')
    removeGuard()
  })

  it('ignores values outside the known settings routes', async () => {
    const { w, router } = await setup()
    const control = w.get('.settings-nav-select')
    ;(control.element as HTMLSelectElement).value = '/unrelated-route'
    await control.trigger('change'); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/settings/ai')
    expect((control.element as HTMLSelectElement).value).toBe('/settings/ai')
  })

  it('keeps every section accessible and translated in all eight locales', async () => {
    const { w } = await setup()
    for (const { code } of SUPPORTED_LOCALES) {
      setLocale(code); await flushPromises()
      expect(w.get('.settings-nav-select').attributes('aria-label')).toBe(i18n.global.t('settingsHub.navAria'))
      expect(w.findAll('.settings-nav-select option')).toHaveLength(10)
      expect(w.findAll('.settings-nav a')).toHaveLength(10)
      expect(w.text()).not.toContain('settingsHub.')
      expect(w.get('.settings-nav a').text()).toBe(i18n.global.t('settingsHub.navAi'))
    }
  })
})

import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { deriveOnboarding } from '../../composables/onboardingState'
import { snapshot, run } from '../../composables/onboardingFixtures.test-helper'
import type { Snapshot } from '../../api/onboarding'
import { setLocale } from '../../i18n'
import OnboardingFlow from './OnboardingFlow.vue'
import ContinueOnboarding from './ContinueOnboarding.vue'
import { resetOnboarding, dismissOnboarding } from '../../composables/useOnboarding'
let wrapper: VueWrapper | undefined
const global = { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } }
function render(data: Snapshot | null, loading = false, error = false) {
  wrapper = mount(OnboardingFlow, { props: { snapshot: data, flow: deriveOnboarding(data, loading, error), loading, error }, global })
  return wrapper
}
afterEach(() => { wrapper?.unmount(); wrapper = undefined; setLocale('zh-CN') })
describe('presentational three-step flow', () => {
  it.each(['loading', 'error', 'create', 'ready', 'failed', 'success'])('%s always keeps skip/dashboard and at most one primary', async kind => {
    const data = kind === 'loading' || kind === 'error' ? null : snapshot(kind === 'create' ? { projects: [], projectCount: 0, selectedProject: null }
      : kind === 'success' ? { success: run() } : kind === 'failed' ? { latestRun: run('failed') } : {})
    const view = render(data, kind === 'loading', kind === 'error')
    expect(view.findAll('.flow-step')).toHaveLength(3)
    expect(view.findAll('.app-btn--primary')).toHaveLength(kind === 'loading' ? 0 : 1)
    expect(view.get('.dashboard-link').attributes('href')).toBe('/')
    await view.get('[data-testid="onboarding-skip"]').trigger('click')
    expect(view.emitted('skip')).toHaveLength(1)
  })
  it('uses global success B, hides selected A, and all contextual links belong to B', async () => {
    const view = render(snapshot({ success: run('success', 'real', 'b') }))
    expect(view.find('[role="combobox"]').exists()).toBe(false)
    expect(view.get('.project-name').text()).toBe('Project B')
    expect(view.text()).not.toContain('Project A')
    await view.findAll('.step-button')[1]!.trigger('click')
    await view.get('.step-detail .app-btn').trigger('click')
    expect(view.emitted('navigate')?.at(-1)).toEqual(['/projects/b/pipeline'])
    await view.findAll('.step-button')[2]!.trigger('click')
    await view.get('.step-detail .app-btn').trigger('click')
    expect(view.emitted('navigate')?.at(-1)).toEqual(['/runs/run-b'])
    expect(view.find('.runtime-help').exists()).toBe(false)
  })
  it('keeps orphan run accessible without claiming deletion or offering configuration', async () => {
    const view = render(snapshot({ success: { ...run('success', 'legacy_unknown', 'b'), projectName: '', projectExists: false } }))
    expect(view.text()).toContain('项目名称暂不可用')
    expect(view.text()).not.toContain('已删除')
    await view.findAll('.step-button')[1]!.trigger('click')
    expect(view.find('.step-detail .app-btn').exists()).toBe(false)
    await view.get('[data-testid="onboarding-primary"]').trigger('click')
    expect(view.emitted('navigate')?.at(-1)).toEqual(['/runs/run-b'])
  })
  it('uses fixed-height project selection and independently wrapped full name', async () => {
    const name = '超长项目名'.repeat(35)
    const view = render(snapshot({ selectedProject: { id: 'a', name, pacEnabled: false }, projects: [{ id: 'a', name }, { id: 'b', name: 'B' }] }))
    expect(view.get('.app-select').attributes('style')).toContain('44px')
    expect(view.get('.project-name').text()).toBe(name)
    const trigger = view.get('[role="combobox"]')
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'ArrowDown' })
    await trigger.trigger('keydown', { key: 'Enter' })
    expect(view.emitted('select')?.[0]).toEqual(['b'])
  })
  it('shows saved preparation separately from unresolved tasks and runtime', () => {
    const view = render(snapshot({ runtime: 'stub', pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'no_tasks', scope: 'canvas' }, { code: 'runtime_stub', scope: 'vars' }] } }))
    const preparation = view.findAll('.flow-step')[1]!
    expect(preparation.attributes('data-state')).toBe('saved')
    expect(preparation.get('.step-copy').text()).toContain('已保存')
    expect(preparation.get('.step-button').attributes('aria-current')).toBe('step')
    expect(view.get('.flow-issues').text()).toContain('当前分支没有可执行的实际任务')
    expect(view.get('.flow-issues').text()).toContain('演示模式')
    expect(view.findAll('.flow-step')[2]!.attributes('data-state')).toBe('pending')
  })
  it('localizes unknown codes safely and exposes server link only on actual missing server', async () => {
    const view = render(snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'SECRET_UNKNOWN_CODE', scope: 'canvas' }] } }))
    expect(view.text()).not.toContain('SECRET_UNKNOWN_CODE')
    expect(view.find('a[href="/settings/servers"]').exists()).toBe(false)
    await view.setProps({ snapshot: snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'now', issues: [{ code: 'server_missing', scope: 'vars' }] } }) })
    expect(view.find('a[href="/settings/servers"]').exists()).toBe(true)
  })
  it.each(['zh-CN', 'zh-TW', 'en', 'ja', 'ko', 'es', 'fr', 'de'] as const)('renders %s without raw localization keys', locale => {
    setLocale(locale)
    const view = render(snapshot({ latestRun: run('success', 'mixed') }))
    expect(view.text()).not.toMatch(/onboarding(?:Flow)?\./)
    expect(view.element.tagName).toBe('SECTION')
  })
  it('continue link uses preferences without requesting status', async () => {
    resetOnboarding()
    wrapper = mount(ContinueOnboarding, { global })
    expect(wrapper.find('a').exists()).toBe(true)
    dismissOnboarding(); await wrapper.vm.$nextTick()
    expect(wrapper.find('a').exists()).toBe(true)
  })
})

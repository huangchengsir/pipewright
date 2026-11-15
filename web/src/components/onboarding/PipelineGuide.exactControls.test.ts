import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { getOnboardingStatus, type Snapshot } from '../../api/onboarding'
import type { PipelineStage } from '../../api/pipeline'
import { snapshot } from '../../composables/onboardingFixtures.test-helper'
import PipelineGuide from './PipelineGuide.vue'

vi.mock('../../api/onboarding', () => ({ getOnboardingStatus: vi.fn() }))
const status = vi.mocked(getOnboardingStatus)
let wrapper: VueWrapper | undefined
let host: HTMLElement | undefined
let transport: ReturnType<typeof vi.fn>
const stage: PipelineStage = {
  id: 'build', name: 'Build', kind: 'build',
  jobs: [{ id: 'job', name: 'Task', type: 'custom', summary: '', config: {} }],
}
const checkNeeded = (issues: Snapshot['pipeline']['issues'] = []) =>
  snapshot({ pipeline: { state: 'needs_configuration', savedAt: 'old', issues } })
const emptyPage = { template: '<main>Fixture destination</main>' }
const withTarget = '/projects/a/pipeline?tab=canvas&onboardingGuide=1&keep=fixture'

beforeEach(() => {
  status.mockReset()
  status.mockResolvedValue(snapshot())
  transport = vi.fn(() => { throw new Error('Unexpected transport from guide fixture') })
  vi.stubGlobal('fetch', transport)
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  host?.remove()
  host = undefined
  vi.unstubAllGlobals()
})

async function open(
  props: Partial<InstanceType<typeof PipelineGuide>['$props']> = {},
  withCanvasTarget = false,
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: ['/projects/:id/pipeline', '/projects', '/settings/servers', '/settings/vault'].map(path => ({ path, component: emptyPage })),
  })
  await router.push(withTarget)
  await router.isReady()
  const guideProps = {
    projectId: 'a', stages: [], dirty: false, context: { selectedJobId: null, pickerOpen: false },
    activeTab: 'canvas', ...props,
  }
  if (withCanvasTarget) {
    host = document.createElement('div')
    document.body.append(host)
    const frame = defineComponent({
      setup: () => () => h('div', { class: 'pipeline-root' }, [
        h(PipelineGuide, guideProps),
        h('button', { 'data-onboarding-target': 'add-stage' }, 'Fixture stage control'),
      ]),
    })
    wrapper = mount(frame, { attachTo: host, global: { plugins: [router] } })
  } else wrapper = mount(PipelineGuide, { props: guideProps, global: { plugins: [router] } })
  await flushPromises()
  return { view: withCanvasTarget ? wrapper.findComponent(PipelineGuide) : wrapper, router }
}
function control(view: VueWrapper, id: string) {
  return view.get(`[data-testid="${id}"]`)
}
const selected = { stages: [stage], context: { selectedJobId: 'job', pickerOpen: false } }

it('locates and focuses the stage control on the actual local canvas without fetching or saving', async () => {
  status.mockResolvedValueOnce(checkNeeded())
  const { view } = await open({}, true)
  expect(view.attributes('data-phase')).toBe('stage')
  const target = wrapper!.get('[data-onboarding-target="add-stage"]')
  const scrollIntoView = vi.fn()
  Object.defineProperty(target.element, 'scrollIntoView', { value: scrollIntoView })
  await control(view, 'guide-locate').trigger('click')
  await flushPromises()
  expect(view.emitted('locate')).toEqual([['stage']])
  expect(target.classes()).toContain('onboarding-target')
  expect(document.activeElement).toBe(target.element)
  expect(scrollIntoView).toHaveBeenCalledExactlyOnceWith({ block: 'center', inline: 'nearest' })
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

it('reviews the exact selected task and moves a dirty local draft to save without committing it', async () => {
  status.mockResolvedValueOnce(checkNeeded())
  const { view } = await open({ ...selected, dirty: true })
  expect(view.attributes('data-phase')).toBe('configure')
  await control(view, 'guide-review').trigger('click')
  expect(view.attributes('data-phase')).toBe('save')
  expect(view.find('[data-testid="guide-review"]').exists()).toBe(false)
  expect(view.emitted('locate')).toBeUndefined()
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

it('refreshes only the selected project from the rendered guide button after a read failure', async () => {
  status.mockRejectedValueOnce(new Error('fixture unavailable')).mockResolvedValueOnce(snapshot())
  const { view } = await open()
  expect(view.attributes('data-phase')).toBe('unknown')
  const first = status.mock.calls[0]![1]!
  await control(view, 'guide-refresh').trigger('click')
  await flushPromises()
  expect(first.aborted).toBe(true)
  expect(status).toHaveBeenNthCalledWith(2, 'a', expect.any(AbortSignal))
  expect(status).toHaveBeenCalledTimes(2)
  expect(view.attributes('data-phase')).toBe('ready')
  expect(transport).not.toHaveBeenCalled()
})

it.each([
  ['no_tasks', 'conditions'], ['script_incomplete', 'configure'],
] as const)('uses the %s canvas issue to locate %s without guessing a task or navigating away', async (code, phase) => {
  status.mockResolvedValueOnce(checkNeeded([{ code, scope: 'canvas' }]))
  const { view, router } = await open(selected)
  await control(view, 'guide-review').trigger('click')
  expect(view.attributes('data-phase')).toBe('issues')
  await view.get('.guide-issues a').trigger('click')
  await flushPromises()
  expect(view.emitted('locate')).toEqual([[phase]])
  expect(router.currentRoute.value.fullPath).toBe(withTarget)
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

it('follows the external server settings issue link without executing a local canvas action', async () => {
  status.mockResolvedValueOnce(checkNeeded([{ code: 'server_missing', scope: 'vars' }]))
  const { view, router } = await open(selected)
  await control(view, 'guide-review').trigger('click')
  const issue = view.get('.guide-issues a')
  expect(issue.attributes('href')).toBe('/settings/servers')
  await issue.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/servers')
  expect(view.emitted('locate')).toBeUndefined()
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

it('routes the ready button to the exact encoded project run intent without starting a run', async () => {
  const id = 'project / + fixture'
  status.mockResolvedValueOnce(snapshot({ selectedProject: { id, name: 'Fixture', pacEnabled: false } }))
  const { view, router } = await open({ projectId: id })
  expect(view.attributes('data-phase')).toBe('ready')
  await control(view, 'guide-run').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/projects')
  expect(router.currentRoute.value.query.onboardingRun).toBe(id)
  expect(status).toHaveBeenCalledExactlyOnceWith(id, expect.any(AbortSignal))
  expect(transport).not.toHaveBeenCalled()
})

it('closes the guide using its own button without a new status read or changing the route directly', async () => {
  const { view, router } = await open()
  await view.get('button.guide-close').trigger('click')
  expect(view.emitted('close')).toEqual([[]])
  expect(router.currentRoute.value.fullPath).toBe(withTarget)
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})


it.each([
  { ctrlKey: true }, { metaKey: true }, { altKey: true }, { shiftKey: true }, { button: 1 },
])('leaves modified clicks on a canvas issue to normal anchor behavior: %j', async init => {
  status.mockResolvedValueOnce(checkNeeded([{ code: 'no_tasks', scope: 'canvas' }]))
  const { view, router } = await open(selected)
  await control(view, 'guide-review').trigger('click')
  const issue = view.get('.guide-issues a')
  expect(issue.attributes('href')).toBe('/projects/a/pipeline?tab=canvas&onboardingGuide=1')
  const clicks: boolean[] = []
  const stopNativeNavigation = (event: MouseEvent) => {
    clicks.push(event.defaultPrevented)
    // jsdom does not implement opening a browser tab.
    event.preventDefault()
  }
  issue.element.addEventListener('click', stopNativeNavigation as EventListener, { once: true })
  issue.element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ...init }))
  await flushPromises()
  expect(clicks).toEqual([false])
  expect(view.emitted('locate')).toBeUndefined()
  expect(router.currentRoute.value.fullPath).toBe(withTarget)
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

it('keeps normal same-project routing for an issue outside canvas scope', async () => {
  status.mockResolvedValueOnce(checkNeeded([{ code: 'environment_undefined', scope: 'envs' }]))
  const { view, router } = await open(selected)
  await control(view, 'guide-review').trigger('click')
  await view.get('.guide-issues a').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/projects/a/pipeline?tab=envs&onboardingGuide=1')
  expect(view.emitted('locate')).toBeUndefined()
  expect(status).toHaveBeenCalledOnce()
  expect(transport).not.toHaveBeenCalled()
})

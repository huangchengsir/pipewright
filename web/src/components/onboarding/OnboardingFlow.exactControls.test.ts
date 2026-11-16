import { afterEach, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { deriveOnboarding } from '../../composables/onboardingState'
import { snapshot } from '../../composables/onboardingFixtures.test-helper'
import type { Snapshot } from '../../api/onboarding'
import { t } from '../../i18n'
import OnboardingFlow from './OnboardingFlow.vue'

let view: VueWrapper | undefined
function open(data: Snapshot | null) {
  view = mount(OnboardingFlow, {
    props: { snapshot: data, flow: deriveOnboarding(data, false, !data), loading: false, error: !data },
    global: { stubs: { RouterLink: true } },
  })
  return view
}
afterEach(() => { view?.unmount(); view = undefined })

it('emits retry only when the unknown-state primary button is clicked', async () => {
  const wrapper = open(null)
  await wrapper.get('[data-testid="onboarding-primary"]').trigger('click')
  expect(wrapper.emitted('retry')).toEqual([[]])
  expect(wrapper.emitted('navigate')).toBeUndefined()
  expect(wrapper.emitted('skip')).toBeUndefined()
  expect(wrapper.emitted('select')).toBeUndefined()
})

it('opens help locally and navigates only when the exact current project secondary action is chosen', async () => {
  const wrapper = open(snapshot({
    selectedProject: { id: 'selected / a', name: 'Selected', pacEnabled: false },
    projects: [{ id: 'a', name: 'Other' }, { id: 'selected / a', name: 'Selected' }],
    runtime: 'stub',
  }))
  const secondary = (label: string) => wrapper.findAll('.flow-actions button.app-btn--ghost')
    .find(button => button.text() === t('onboardingFlow.actions.' + label))!
  await secondary('runtimeHelp').trigger('click')
  expect((wrapper.get('.runtime-help').element as HTMLDetailsElement).open).toBe(true)
  expect(wrapper.emitted('navigate')).toBeUndefined()
  await secondary('goRun').trigger('click')
  expect(wrapper.emitted('navigate')).toEqual([['/projects?onboardingRun=selected%20%2F%20a']])
  expect(wrapper.emitted('retry')).toBeUndefined()
  expect(wrapper.emitted('skip')).toBeUndefined()
})

it('expands and collapses only the chosen onboarding step without a navigation side effect', async () => {
  const wrapper = open(snapshot())
  const [project, pipeline] = wrapper.findAll('button.step-button')
  expect(project!.attributes('aria-expanded')).toBe('false')
  await project!.trigger('click')
  expect(project!.attributes('aria-expanded')).toBe('true')
  expect(wrapper.findAll('.step-detail')).toHaveLength(1)
  expect(wrapper.find('#onboarding-step-0').exists()).toBe(true)
  await pipeline!.trigger('click')
  expect(project!.attributes('aria-expanded')).toBe('false')
  expect(pipeline!.attributes('aria-expanded')).toBe('true')
  expect(wrapper.find('#onboarding-step-0').exists()).toBe(false)
  await pipeline!.trigger('click')
  expect(pipeline!.attributes('aria-expanded')).toBe('false')
  expect(wrapper.find('.step-detail').exists()).toBe(false)
  expect(wrapper.emitted('navigate')).toBeUndefined()
  expect(wrapper.emitted('retry')).toBeUndefined()
})

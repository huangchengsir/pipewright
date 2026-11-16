import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import type { ValidationIssue } from '../../api/pipelineValidation'
import { useSessionStore } from '../../stores/session'
import JobDrawer from './JobDrawer.vue'
import PipelineCanvas from './PipelineCanvas.vue'
import StageDrawer from './StageDrawer.vue'
import StagePostEditor from './StagePostEditor.vue'
import StageServicesEditor from './StageServicesEditor.vue'
import ValidationPanel from './ValidationPanel.vue'
import { encodeStudioMeta, STUDIO_META_KEY } from './studioCompile'

const fixtureStage: PipelineStage = {
  id: 'current-stage', name: 'Fixture stage', kind: 'build', jobs: [],
  services: [
    { name: 'db', image: 'postgres:16', ports: ['5432'] },
    { name: 'cache', image: 'redis:7', env: ['MODE=test'] },
  ],
  post: [
    { condition: 'always', image: 'busybox', commands: ['echo cleanup'] },
    { condition: 'on_failure', image: 'alpine', commands: ['echo fixture'], workDir: '/tmp' },
  ],
}
const cleanup: Array<() => void> = []
beforeEach(() => {
  setActivePinia(createPinia())
  useSessionStore().setUser({ username: 'admin', multiTenant: false })
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  })
})
afterEach(() => {
  cleanup.splice(0).forEach(unmount => unmount())
  vi.unstubAllGlobals()
})

function stageDrawer() {
  const wrapper = mount(StageDrawer, {
    props: { stage: fixtureStage, stageIndex: 1 },
    attachTo: document.body,
  })
  cleanup.push(() => wrapper.unmount())
  return wrapper
}

describe('pipeline stage draft controls', () => {
  it('closes the actual read-only stage drawer without changing its draft', async () => {
    const wrapper = stageDrawer()
    await wrapper.setProps({ readOnly: true })
    expect(wrapper.get<HTMLButtonElement>('button.drawer-close').element.matches(':disabled')).toBe(false)
    await wrapper.get('button.drawer-close').trigger('click')
    expect(wrapper.emitted()).toMatchObject({ close: [[]] })
    expect(wrapper.emitted('update-post')).toBeUndefined()
    expect(wrapper.emitted('update-services')).toBeUndefined()
  })

  it('adds a post step and removes only the selected one while preserving the other row', async () => {
    const wrapper = stageDrawer()
    const editor = wrapper.getComponent(StagePostEditor)
    await editor.get('button.settings-add-link').trigger('click')
    expect(wrapper.emitted('update-post')?.at(-1)).toEqual([
      [...fixtureStage.post!, { condition: 'always', image: '', commands: [] }],
    ])
    await editor.findAll('button.post-del')[0].trigger('click')
    expect(wrapper.emitted('update-post')?.at(-1)).toEqual([[fixtureStage.post![1]]])
  })

  it('adds an empty service and removes only the chosen draft service with other fields intact', async () => {
    const wrapper = stageDrawer()
    const editor = wrapper.getComponent(StageServicesEditor)
    await editor.get('button.settings-add-link').trigger('click')
    expect(wrapper.emitted('update-services')?.at(-1)).toEqual([
      [...fixtureStage.services!, { image: '', name: '' }],
    ])
    await editor.findAll('button.svc-del')[1].trigger('click')
    expect(wrapper.emitted('update-services')?.at(-1)).toEqual([[fixtureStage.services![0]]])
  })

  it('disables all post and service draft buttons once stage editing is withdrawn', async () => {
    const wrapper = stageDrawer()
    const controls = wrapper.findAll<HTMLButtonElement>('.post-del, .svc-del, button.settings-add-link').map(button => button.element)
    expect(controls).toHaveLength(6)
    expect(controls.every(button => !button.matches(':disabled'))).toBe(true)
    await wrapper.setProps({ readOnly: true })
    for (const button of controls) {
      expect(button.matches(':disabled')).toBe(true)
      button.click()
    }
    await nextTick()
    expect(wrapper.emitted('update-post')).toBeUndefined()
    expect(wrapper.emitted('update-services')).toBeUndefined()
  })

  it('dismisses an already open post condition menu when stage edit permission is withdrawn', async () => {
    const wrapper = stageDrawer()
    const post = wrapper.getComponent(StagePostEditor)
    await post.findAll('[role=combobox]')[0].trigger('click')
    const menuOptions = [...document.querySelectorAll<HTMLButtonElement>('button[role=option]')]
    expect(menuOptions).toHaveLength(3)
    await wrapper.setProps({ readOnly: true })
    expect(document.querySelector('button[role=option]')).toBeNull()
    menuOptions[1].click()
    await nextTick()
    expect(wrapper.emitted('update-post')).toBeUndefined()
  })

  it('adds one canvas stage to the current draft and stops after editing is withdrawn', async () => {
    const wrapper = mount(PipelineCanvas, { props: { stages: [fixtureStage], yaml: '' }, attachTo: document.body })
    cleanup.push(() => wrapper.unmount())
    const button = wrapper.get<HTMLButtonElement>('button.add-stage-btn')
    await button.trigger('click')
    expect(wrapper.emitted('update')).toEqual([
      [[fixtureStage, expect.objectContaining({ id: expect.stringMatching(/^stg_/), kind: 'deploy', jobs: [] })]],
    ])
    await wrapper.setProps({ readOnly: true })
    expect(button.element.disabled).toBe(true)
    button.element.click()
    await nextTick()
    expect(wrapper.emitted('update')).toHaveLength(1)
  })
})

describe('pipeline instance and result controls', () => {
  it('changes just the studio boolean draft and preserves studio definitions on revocation', async () => {
    const meta = encodeStudioMeta([
      { key: 'cache', label: 'Cache', type: 'toggle', default: 'false' },
      { key: 'directory', label: 'Directory', type: 'text', default: 'service' },
    ])
    const job: PipelineJob = {
      id: 'studio-job', name: 'Studio fixture', type: 'templated', summary: '',
      config: { commandTemplate: 'echo {{cache}}', params: 'cache=false\ndirectory=service', [STUDIO_META_KEY]: meta },
    }
    const wrapper = mount(JobDrawer, { props: { stage: fixtureStage, job }, attachTo: document.body })
    cleanup.push(() => wrapper.unmount())
    const toggle = wrapper.get<HTMLButtonElement>('#si-cache[role=switch]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      config: { params: 'cache=true\ndirectory=service', [STUDIO_META_KEY]: meta },
    })
    const count = wrapper.emitted('update')?.length
    await wrapper.setProps({ readOnly: true })
    expect(toggle.element.matches(':disabled')).toBe(true)
    toggle.element.click()
    await nextTick()
    expect(wrapper.emitted('update')).toHaveLength(count!)
  })

  it('locates only the selected issue through click and keyboard and closes validation', async () => {
    const issues: ValidationIssue[] = [
      { severity: 'error', code: 'missing', scope: 'vars', field: 'build.vars', message: 'Missing fixture variable' },
      { severity: 'warning', code: 'env-warning', scope: 'envs', field: 'targets', message: 'Check fixture environment' },
    ]
    const wrapper = mount(ValidationPanel, { props: { loading: false, ready: false, issues } })
    cleanup.push(() => wrapper.unmount())
    const [vars, envs] = wrapper.findAll('li[role=button]')
    await envs.trigger('click')
    await vars.trigger('keydown', { key: 'Enter' })
    await envs.trigger('keydown', { key: ' ' })
    expect(wrapper.emitted('locate')).toEqual([['envs'], ['vars'], ['envs']])
    await wrapper.get('button.vp-close').trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
    expect(wrapper.emitted('locate')).toHaveLength(3)
  })
})

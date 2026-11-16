import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import type { Credential } from '../../api/credentials'
import type { PipelineJob, PipelineStage } from '../../api/pipeline'
import type { BuildConfig } from '../../api/pipelineSettings'
import { useSessionStore } from '../../stores/session'
import JobDrawer from './JobDrawer.vue'
import StepBuilder from './StepBuilder.vue'
import VarsCacheTab from './VarsCacheTab.vue'

const stage: PipelineStage = { id: 'build', name: 'Build', kind: 'build', jobs: [] }
const job: PipelineJob = {
  id: 'fixture-build', name: 'Test build', type: 'build_frontend', summary: '',
  config: { commands: 'echo first\necho second\necho third', image: 'node:20' },
}

const build: BuildConfig = {
  model: 'dockerfile',
  dockerfilePath: 'Dockerfile',
  toolchain: { language: '', version: '' },
  artifactType: 'image',
  vars: [
    { id: 'mode', key: 'MODE', secret: false, value: 'test' },
    { id: 'reference', key: 'TOKEN', secret: true, credentialId: 'first', maskedValue: '••••_old' },
  ],
  cache: { enabled: false, paths: ['node_modules'] },
}

const credentials: Credential[] = [
  { id: 'first', name: 'First fixture', type: 'git_token', scope: 'test', username: '', maskedValue: '••••_old', lastUsedAt: null, createdAt: '' },
  { id: 'second', name: 'Second fixture', type: 'git_token', scope: 'test', username: '', maskedValue: '••••_new', lastUsedAt: null, createdAt: '' },
]

const cleanup: Array<() => void> = []
beforeEach(() => {
  setActivePinia(createPinia())
  useSessionStore().setUser({ username: 'admin', multiTenant: false })
})
afterEach(() => {
  for (const unmount of cleanup.splice(0)) unmount()
})

function mountSteps(config = job.config) {
  const wrapper = mount(StepBuilder, { props: { config } })
  cleanup.push(() => wrapper.unmount())
  return wrapper
}

function mountVars() {
  const wrapper = mount(VarsCacheTab, { props: { build, credentials }, attachTo: document.body })
  cleanup.push(() => wrapper.unmount())
  return wrapper
}

describe('pipeline visual draft step controls', () => {
  it('moves the chosen step up and down and removes only that step', async () => {
    const wrapper = mountSteps()
    const rowControls = (n: number) => wrapper.get(`.sb-step:nth-child(${n})`)
    expect(rowControls(1).get<HTMLButtonElement>('.sb-move:first-child').element.disabled).toBe(true)
    expect(rowControls(3).get<HTMLButtonElement>('.sb-move:nth-child(2)').element.disabled).toBe(true)

    await rowControls(2).get('.sb-move:first-child').trigger('click')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      commands: 'echo second\necho first\necho third',
    })
    await rowControls(1).get('.sb-move:nth-child(2)').trigger('click')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({ commands: job.config.commands })
    await rowControls(2).get('.sb-del').trigger('click')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({ commands: 'echo first\necho third' })
    expect(wrapper.findAll('.sb-step')).toHaveLength(2)
  })

  it('opens and closes the add menu without editing, then adds the chosen draft step', async () => {
    const wrapper = mountSteps()
    const menu = wrapper.get('.sb-add-btn')
    await menu.trigger('click')
    expect(menu.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[role=menu]').findAll('[role=menuitem]')).toHaveLength(5)
    await menu.trigger('click')
    expect(menu.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('[role=menu]').exists()).toBe(false)
    expect(wrapper.emitted('update')).toBeUndefined()

    await menu.trigger('click')
    await wrapper.get('.sb-add-item--workDir[role=menuitem]').trigger('click')
    expect(wrapper.find('[role=menu]').exists()).toBe(false)
    const directory = wrapper.get('.sb-step--workDir input')
    await directory.setValue('frontend')
    await directory.trigger('blur')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      commands: `${job.config.commands}\ncd 'frontend'`,
    })
  })

  it('does not move another draft step through a removed row button', async () => {
    const wrapper = mountSteps()
    const row = wrapper.get('.sb-step:first-child')
    const removedDown = row.get<HTMLButtonElement>('.sb-move:nth-child(2)').element
    row.get<HTMLButtonElement>('.sb-del').element.click()
    removedDown.click()
    await nextTick()
    expect(wrapper.emitted('update')).toEqual([[{ commands: 'echo second\necho third', artifactPath: '' }]])
  })

  it('keeps the job drawer step controls read-only after editing is withdrawn', async () => {
    const wrapper = mount(JobDrawer, { props: { job, stage }, attachTo: document.body })
    cleanup.push(() => wrapper.unmount())
    const builder = wrapper.getComponent(StepBuilder)
    builder.get<HTMLButtonElement>('.sb-add-btn').element.click()
    await nextTick()
    const selectors = [
      '.sb-step:nth-child(2) .sb-move:first-child',
      '.sb-step:first-child .sb-move:nth-child(2)',
      '.sb-step:first-child .sb-del',
      '.sb-add-btn',
      '.sb-add-item--workDir[role=menuitem]',
    ]
    const controls = selectors.map(selector => builder.get<HTMLButtonElement>(selector).element)
    expect(controls.every(button => !button.matches(':disabled'))).toBe(true)
    await wrapper.setProps({ readOnly: true })
    controls.forEach(button => {
      expect(button.matches(':disabled')).toBe(true)
      button.click()
    })
    await nextTick()
    expect(builder.findAll('.sb-step')).toHaveLength(3)
    expect(builder.get('.sb-add-btn').attributes('aria-expanded')).toBe('true')
    expect(builder.emitted('update')).toBeUndefined()
    expect(wrapper.emitted('update')).toBeUndefined()
  })
})

describe('pipeline variables and cache draft controls', () => {
  it('adds plain and vault variables separately and deletes only the selected draft row', async () => {
    const wrapper = mountVars()
    await wrapper.get('.addbtn:not(.addbtn--vault)').trigger('click')
    expect(wrapper.findAll('.vrow').at(-1)?.find('.vv').exists()).toBe(true)
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      vars: [...build.vars, { secret: false, key: '', value: '' }],
    })

    await wrapper.get('.addbtn--vault').trigger('click')
    expect(wrapper.findAll('.vrow').at(-1)?.find('[role=combobox]').exists()).toBe(true)
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      vars: [...build.vars, { secret: false }, { secret: true, key: '', credentialId: '' }],
    })

    await wrapper.findAll('.vrow')[0].get('.vdel').trigger('click')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      vars: [build.vars[1], { secret: false }, { secret: true }],
    })
  })

  it('switches dependency cache on and back off while retaining paths in the draft', async () => {
    const wrapper = mountVars()
    const cache = wrapper.get('button[role=switch]')
    expect(cache.attributes('aria-checked')).toBe('false')
    expect(wrapper.get<HTMLTextAreaElement>('#cache-paths').element.disabled).toBe(true)
    await cache.trigger('click')
    expect(cache.attributes('aria-checked')).toBe('true')
    expect(wrapper.get<HTMLTextAreaElement>('#cache-paths').element.disabled).toBe(false)
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toMatchObject({
      cache: { enabled: true, paths: ['node_modules'] },
    })
    await cache.trigger('click')
    expect(cache.attributes('aria-checked')).toBe('false')
    expect(wrapper.get<HTMLTextAreaElement>('#cache-paths').element.disabled).toBe(true)
    expect(wrapper.get<HTMLTextAreaElement>('#cache-paths').element.value).toBe('node_modules')
  })

  it('clears the old mask when toggling the credential source and shows only the new mask', async () => {
    const wrapper = mountVars()
    const row = wrapper.findAll('.vrow')[1]
    const source = row.get('.vfrom')
    expect(source.get('.mask').text()).toBe('••••_old')
    await source.trigger('click')
    expect(row.get<HTMLInputElement>('.vv').element.value).toBe('')
    const plain = (wrapper.emitted('update')?.at(-1)?.[0] as BuildConfig).vars[1]
    expect(plain).toMatchObject({ secret: false, value: '', credentialId: undefined })
    expect(plain.maskedValue).toBeUndefined()

    await source.trigger('click')
    expect(source.get('.mask').text()).toBe('••••')
    await row.get('[role=combobox]').trigger('click')
    const option = [...document.querySelectorAll<HTMLButtonElement>('[role=option]')]
      .find(button => button.textContent?.includes('Second fixture'))
    expect(option).toBeDefined()
    option!.click()
    await nextTick()
    expect(source.get('.mask').text()).toBe('••••_new')
    expect((wrapper.emitted('update')?.at(-1)?.[0] as BuildConfig).vars[1])
      .toMatchObject({ secret: true, credentialId: 'second', value: undefined })
  })

  it('clears a server mask when changing credentials without switching to plain text', async () => {
    const wrapper = mountVars()
    const row = wrapper.findAll('.vrow')[1]
    await row.get('[role=combobox]').trigger('click')
    const option = [...document.querySelectorAll<HTMLButtonElement>('[role=option]')]
      .find(button => button.textContent?.includes('Second fixture'))
    expect(option).toBeDefined()
    option!.click()
    await nextTick()
    expect(row.get('.mask').text()).toBe('••••_new')
    const selected = (wrapper.emitted('update')?.at(-1)?.[0] as BuildConfig).vars[1]
    expect(selected.credentialId).toBe('second')
    expect(selected.maskedValue).toBeUndefined()
  })

  it('disables each variable and cache action and ignores native clicks after editing is withdrawn', async () => {
    const wrapper = mountVars()
    const selectors = ['.vfrom', '.vdel', '.addbtn:not(.addbtn--vault)', '.addbtn--vault', 'button[role=switch]']
    const controls = selectors.map(selector => wrapper.get<HTMLButtonElement>(selector).element)
    await wrapper.setProps({ disabled: true })
    controls.forEach(button => {
      expect(button.disabled).toBe(true)
      button.click()
    })
    await nextTick()
    expect(wrapper.findAll('.vrow')).toHaveLength(2)
    expect(wrapper.get('[role=switch]').attributes('aria-checked')).toBe('false')
    expect(wrapper.emitted('update')).toBeUndefined()
  })
})

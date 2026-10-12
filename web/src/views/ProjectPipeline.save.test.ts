import { defineComponent, h } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView, type Router } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ProjectPipeline from './ProjectPipeline.vue'
import VarsCacheTab from '../components/pipeline/VarsCacheTab.vue'
import EnvCredsTab from '../components/pipeline/EnvCredsTab.vue'
import TriggersPanel from '../components/TriggersPanel.vue'
import ProjectPreviewConfig from '../components/pipeline/ProjectPreviewConfig.vue'
import { http, HttpError } from '../api/http'
import type { PipelineDTO, PipelineStage } from '../api/pipeline'
import type { SettingsDTO } from '../api/pipelineSettings'
import type { TriggerConfig } from '../api/triggers'
import type { PreviewConfig } from '../api/previewEnvs'

vi.mock('../api/http', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api/http')>(),
  http: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
}))
vi.mock('../composables/useToast', () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}))
vi.mock('../composables/useConfirm', () => ({
  useConfirm: () => ({ open: async () => window.confirm('Discard draft?') }),
}))

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const prefix = '/api/projects/p1'
let storedPipeline: PipelineDTO
let storedSettings: SettingsDTO
let storedTrigger: TriggerConfig
let storedPreview: PreviewConfig
let wrapper: VueWrapper | undefined
let router: Router

const CanvasStub = defineComponent({
  name: 'PipelineCanvas',
  props: ['stages'],
  emits: ['update'],
  setup: () => () => h('div', { class: 'canvas-stub' }),
})

async function render(tab = 'canvas', realCanvas = false): Promise<VueWrapper> {
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/projects/:id/pipeline', component: ProjectPipeline },
      { path: '/projects', component: { template: '<div>Projects</div>' } },
    ],
  })
  await router.push(`/projects/p1/pipeline?tab=${tab}`)
  await router.isReady()
  wrapper = mount(defineComponent({ setup: () => () => h(RouterView) }), {
    attachTo: document.body,
    global: {
      plugins: [router],
      stubs: {
        ...(realCanvas ? {} : { PipelineCanvas: CanvasStub }),
        PipelineGuide: true, RiskAnnotationPanel: true, ValidationPanel: true,
        CronPanel: true, EnvironmentsPanel: true, ParametersPanel: true,
        ConcurrencyPanel: true, ChainPanel: true, RunnerPanel: true,
        AppSelect: true,
      },
    },
  })
  await flushPromises()
  return wrapper
}

async function switchTab(tab: string): Promise<void> {
  await router.replace({ query: { tab } })
  await flushPromises()
}

async function editTrigger(pattern = 'acceptance/*'): Promise<void> {
  const panel = wrapper!.findComponent(TriggersPanel)
  await panel.find('.add-row-btn').trigger('click')
  await panel.find('.map-input--mono').setValue(pattern)
  await panel.find('.map-cell:nth-child(2) input').setValue('acceptance-test')
  await panel.find('textarea').setValue('backend/**\nshared/**\nbackend/**')
}

async function editEnvironment(): Promise<void> {
  const envs = wrapper!.findComponent(EnvCredsTab)
  await envs.find('button.addbtn').trigger('click')
  await envs.find('input.env-name').setValue('acceptance-test')
}

async function editVariable(): Promise<void> {
  const vars = wrapper!.findComponent(VarsCacheTab)
  await vars.find('button.addbtn').trigger('click')
  await vars.find('input[aria-label="变量键"]').setValue('NAME')
  await vars.find('input[aria-label="变量值"]').setValue('persisted')
}

async function clickSave(): Promise<void> {
  await wrapper!.find('.top-btn--save').trigger('click')
  await flushPromises()
}

function isGuarded(): boolean {
  const event = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(event)
  return event.defaultPrevented
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  storedPipeline = { stages: [], yaml: '', status: 'draft', updatedAt: 'initial' }
  storedSettings = {
    build: {
      model: 'dockerfile', dockerfilePath: 'Dockerfile',
      toolchain: { language: '', version: '' }, artifactType: 'image',
      vars: [], cache: { enabled: false, paths: [] },
    },
    environments: [], updatedAt: 'initial',
  }
  storedTrigger = {
    webhookUrl: '/api/webhooks/token', webhookSecretMasked: 'masked',
    events: { push: false, tag: false, pullRequest: false, release: false },
    branchMappings: [], unmatchedPolicy: 'record', pathFilters: [],
  }
  storedPreview = { projectId: 'p1', enabled: false, dnsProviderId: '', baseDomain: '' }
  vi.mocked(http.get).mockImplementation(async (url) => {
    if (url === `${prefix}/pipeline`) return clone(storedPipeline)
    if (url === `${prefix}/pipeline/settings`) return clone(storedSettings)
    if (url === `${prefix}/trigger`) return clone(storedTrigger)
    if (url === `${prefix}/preview-config`) return clone(storedPreview)
    if (url === '/api/projects') return [{ id: 'p1', name: 'Project' }]
    if (url === '/api/credentials') return []
    if (['/api/servers', '/api/notifications/channels', '/api/dns/providers'].includes(url)) return { items: [] }
    throw new Error(`Unexpected GET ${url}`)
  })
  vi.mocked(http.put).mockImplementation(async (url, input) => {
    if (url === `${prefix}/pipeline`) {
      storedPipeline = { ...storedPipeline, ...clone(input as { stages: PipelineStage[] }), updatedAt: 'saved' }
      return clone(storedPipeline)
    }
    if (url === `${prefix}/pipeline/settings`) {
      storedSettings = { ...clone(input as SettingsDTO), updatedAt: 'saved' }
      return clone(storedSettings)
    }
    if (url === `${prefix}/trigger`) {
      storedTrigger = { ...storedTrigger, ...clone(input as TriggerConfig) }
      return clone(storedTrigger)
    }
    if (url === `${prefix}/preview-config`) {
      storedPreview = clone(input as PreviewConfig)
      return clone(storedPreview)
    }
    throw new Error(`Unexpected PUT ${url}`)
  })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('ProjectPipeline draft save scope (#190/#193)', () => {
  it('preserves an unblurred drawer edit when an unchanged save is acknowledged', async () => {
    storedPipeline.stages = [{ id: 's', name: 'Build', kind: 'build', jobs: [{ id: 'j', name: 'Before', summary: '', type: 'script', config: { script: 'true' } }] }]
    await render('canvas', true)
    await wrapper!.find('.job-card').trigger('click')
    let finish!: (value: PipelineDTO) => void
    vi.mocked(http.put).mockImplementationOnce(() => new Promise<PipelineDTO>((resolve) => { finish = resolve }))
    await clickSave()
    const input = wrapper!.find('.job-drawer input.drawer-input')
    await input.setValue('Typed while saving')
    finish(clone(storedPipeline))
    await flushPromises()
    expect((input.element as HTMLInputElement).value).toBe('Typed while saving')
    await input.trigger('blur')
    expect(isGuarded()).toBe(true)
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
  })
  it('persists an explicit unchanged save without creating a dirty draft', async () => {
    await render()
    expect(isGuarded()).toBe(false)
    await clickSave()
    expect(http.put).toHaveBeenCalledExactlyOnceWith(`${prefix}/pipeline`, { stages: [] })
    expect(storedPipeline.updatedAt).toBe('saved')
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
    expect(isGuarded()).toBe(false)
  })
  it.each(['canvas', 'vars', 'triggers', 'envs'])('saves all changed scopes from %s and restores them on remount', async (tab) => {
    await render('envs')
    await editEnvironment()
    await switchTab('vars')
    await editVariable()
    await switchTab('triggers')
    await editTrigger()
    await wrapper!.findComponent(ProjectPreviewConfig).find('input.pvc__in').setValue('preview.example.com')
    const stages: PipelineStage[] = [{ id: 'stage-1', name: 'Build', kind: 'build', jobs: [] }]
    wrapper!.findComponent(CanvasStub).vm.$emit('update', stages)
    await switchTab(tab)
    await clickSave()

    expect(http.put).toHaveBeenCalledTimes(4)
    expect(storedPipeline.stages).toEqual(stages)
    expect(storedSettings.environments[0].name).toBe('acceptance-test')
    expect(storedSettings.build.vars[0]).toMatchObject({ key: 'NAME', value: 'persisted' })
    expect(storedTrigger.branchMappings[0]).toMatchObject({ branchPattern: 'acceptance/*', environment: 'acceptance-test' })
    expect(storedTrigger.pathFilters).toEqual(['backend/**', 'shared/**'])
    expect(storedPreview.baseDomain).toBe('preview.example.com')
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
    expect(isGuarded()).toBe(false)

    wrapper!.unmount()
    await render('triggers')
    expect((wrapper!.find('.map-input--mono').element as HTMLInputElement).value).toBe('acceptance/*')
    await switchTab('envs')
    expect((wrapper!.find('input.env-name').element as HTMLInputElement).value).toBe('acceptance-test')
    expect(http.put).toHaveBeenCalledTimes(4)
    expect(isGuarded()).toBe(false)
  })

  it('preserves live rows and focus while echoing edits and switching tabs', async () => {
    await render('vars')
    await editVariable()
    const input = wrapper!.find('input[aria-label="变量值"]').element as HTMLInputElement
    input.focus()
    await wrapper!.find('input[aria-label="变量值"]').setValue('changed')
    expect(document.activeElement).toBe(input)
    await switchTab('envs')
    await editEnvironment()
    const envInput = wrapper!.find('input.env-name').element
    await switchTab('canvas')
    await switchTab('vars')
    expect(wrapper!.find('input[aria-label="变量值"]').element).toBe(input)
    expect(wrapper!.find('input.env-name').element).toBe(envInput)
    expect(http.put).not.toHaveBeenCalled()
    await clickSave()
    expect(storedSettings.build.vars[0].value).toBe('changed')
    expect(storedSettings.environments[0].name).toBe('acceptance-test')
  })

  it.each(['pipeline', 'pipeline/settings', 'trigger', 'preview-config'])('does not claim success after %s failure; retry preserves acknowledged scopes', async (scope) => {
    await render()
    wrapper!.findComponent(CanvasStub).vm.$emit('update', [{ id: 's', name: 'Build', kind: 'build', jobs: [] }])
    await editVariable()
    await editTrigger()
    await wrapper!.findComponent(ProjectPreviewConfig).find('input.pvc__in').setValue('preview.example.com')
    const normalPut = vi.mocked(http.put).getMockImplementation()!
    let fail = true
    vi.mocked(http.put).mockImplementation(async (url, body) => {
      if (fail && url === `${prefix}/${scope}`) throw new HttpError(422, { code: 'invalid_build', message: 'save rejected' }, 'rejected')
      return normalPut(url, body)
    })
    await clickSave()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    expect(wrapper!.find('.pipeline-banner--error').exists()).toBe(true)
    expect(isGuarded()).toBe(true)
    const alreadySaved = vi.mocked(http.put).mock.calls.slice(0, -1).map(([url]) => url)
    fail = false
    await clickSave()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
    expect(isGuarded()).toBe(false)
    for (const url of alreadySaved) {
      expect(vi.mocked(http.put).mock.calls.filter(([called]) => called === url)).toHaveLength(url === `${prefix}/pipeline` ? 2 : 1)
    }
  })

  it('validates hidden trigger mappings before any writes and can recover', async () => {
    await render('canvas')
    await editVariable()
    await editTrigger('/invalid/')
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(wrapper!.find('.map-field-error').exists()).toBe(true)
    expect(router.currentRoute.value.query.tab).toBe('triggers')
    expect(wrapper!.find('.pipeline-banner--error').exists()).toBe(true)
    await wrapper!.find('.map-input--mono').setValue('valid/*')
    await clickSave()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
  })

  it.each(['pipeline/settings', 'trigger', 'preview-config'])('never skips an unloaded %s scope with false success', async (scope) => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    vi.mocked(http.get).mockImplementation(async (url) => {
      if (url === `${prefix}/${scope}`) throw new Error('load failed')
      return normalGet(url)
    })
    await render()
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    expect(wrapper!.find('.pipeline-banner--error').exists()).toBe(true)
  })

  it('guards leave/project changes, allows tab changes, and removes unload protection on unmount', async () => {
    await render('triggers')
    expect(isGuarded()).toBe(false)
    await editTrigger()
    expect(isGuarded()).toBe(true)
    await switchTab('canvas')
    expect(window.confirm).not.toHaveBeenCalled()
    await router.push('/projects')
    expect(router.currentRoute.value.params.id).toBe('p1')
    await router.push('/projects/p2/pipeline')
    expect(router.currentRoute.value.params.id).toBe('p1')
    expect(window.confirm).toHaveBeenCalledTimes(2)
    vi.mocked(window.confirm).mockReturnValue(true)
    await router.push('/projects')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/projects')
    expect(isGuarded()).toBe(false)
  })

  it('keeps other scopes pending while settings load, then saves after load resolves', async () => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    let finish!: (value: SettingsDTO) => void
    vi.mocked(http.get).mockImplementation(async (url) => {
      if (url === `${prefix}/pipeline/settings`) return new Promise<SettingsDTO>((resolve) => { finish = resolve })
      return normalGet(url)
    })
    await render()
    await editTrigger()
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    finish(clone(storedSettings))
    await flushPromises()
    await clickSave()
    expect(storedTrigger.branchMappings[0].branchPattern).toBe('acceptance/*')
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
  })

  it('recovers settings load through retry without resetting a pending trigger draft', async () => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    let fail = true
    vi.mocked(http.get).mockImplementation(async (url) => {
      if (fail && url === `${prefix}/pipeline/settings`) throw new Error('offline')
      return normalGet(url)
    })
    await render('triggers')
    await editTrigger()
    fail = false
    await wrapper!.find('.pipeline-banner--error .banner-retry').trigger('click')
    await flushPromises()
    expect((wrapper!.find('.map-input--mono').element as HTMLInputElement).value).toBe('acceptance/*')
    await clickSave()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(true)
  })

  it.each([0, 500])('preserves trigger draft after status %s failure and shows the child error', async (status) => {
    await render('canvas')
    await editTrigger()
    const normalPut = vi.mocked(http.put).getMockImplementation()!
    vi.mocked(http.put).mockRejectedValueOnce(new HttpError(status, null, 'unavailable'))
    await clickSave()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('triggers')
    expect(wrapper!.findComponent(TriggersPanel).find('.banner--error').exists()).toBe(true)
    expect(isGuarded()).toBe(true)
    vi.mocked(http.put).mockImplementation(normalPut)
    await clickSave()
    expect(isGuarded()).toBe(false)
  })

  it('blocks all writes for invalid enabled PR preview configuration and retains its local save', async () => {
    await render('canvas')
    await editVariable()
    const preview = wrapper!.findComponent(ProjectPreviewConfig)
    await preview.find('input.pvc__cb').setValue(true)
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(router.currentRoute.value.query.tab).toBe('triggers')
    await preview.find('input.pvc__cb').setValue(false)
    await preview.find('input.pvc__in').setValue('preview.example.com')
    await preview.find('.pvc__save').trigger('click')
    await flushPromises()
    expect(storedPreview.baseDomain).toBe('preview.example.com')
    expect(isGuarded()).toBe(true)
    await clickSave()
    expect(http.put).toHaveBeenCalledTimes(3)
    expect(isGuarded()).toBe(false)
  })

  it.each(['canvas', 'triggers', 'preview'])('retains newer %s edits while acknowledging the sent snapshot', async (scope) => {
    await render()
    const submitted: PipelineStage[] = [{ id: 's', name: 'Sent', kind: 'build', jobs: [] }]
    if (scope === 'canvas') wrapper!.findComponent(CanvasStub).vm.$emit('update', submitted)
    if (scope === 'triggers') await editTrigger()
    if (scope === 'preview') await wrapper!.findComponent(ProjectPreviewConfig).find('.pvc__in').setValue('sent.example.com')
    let finish!: (value: unknown) => void
    vi.mocked(http.put).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve }))
    await clickSave()
    if (scope === 'canvas') {
      wrapper!.findComponent(CanvasStub).vm.$emit('update', [{ ...submitted[0], name: 'Newer' }])
      finish({ ...storedPipeline, stages: submitted })
    } else if (scope === 'triggers') {
      await wrapper!.find('.map-input--mono').setValue('newer/*')
      const sent = vi.mocked(http.put).mock.calls[0][1] as TriggerConfig
      finish({ ...storedTrigger, ...sent })
    } else {
      await wrapper!.findComponent(ProjectPreviewConfig).find('.pvc__in').setValue('newer.example.com')
      finish(vi.mocked(http.put).mock.calls[0][1])
    }
    await flushPromises()
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    expect(isGuarded()).toBe(true)
    if (scope === 'canvas') expect(wrapper!.findComponent(CanvasStub).props('stages')[0].name).toBe('Newer')
    if (scope === 'triggers') expect((wrapper!.find('.map-input--mono').element as HTMLInputElement).value).toBe('newer/*')
    if (scope === 'preview') expect((wrapper!.find('.pvc__in').element as HTMLInputElement).value).toBe('newer.example.com')
  })

  it('saves events, existing target server IDs and unmatched policy without resetting the webhook secret', async () => {
    storedTrigger.branchMappings = [{ id: 'mapping', branchPattern: 'main', environment: 'prod', targetServerIds: ['server-1'] }]
    await render('triggers')
    const trigger = wrapper!.findComponent(TriggersPanel)
    await trigger.find('.ev-toggle').trigger('click')
    await trigger.findAll('.seg-btn')[1].trigger('click')
    await switchTab('envs')
    await clickSave()
    expect(storedTrigger.events.push).toBe(true)
    expect(storedTrigger.unmatchedPolicy).toBe('ignore')
    expect(storedTrigger.branchMappings[0].targetServerIds).toEqual(['server-1'])
    expect(http.post).not.toHaveBeenCalled()
    expect(isGuarded()).toBe(false)
  })

  it('does not overwrite edits made during a settings save or accept duplicate saves/departure', async () => {
    await render('vars')
    await editVariable()
    let finish!: (value: SettingsDTO) => void
    vi.mocked(http.put).mockImplementationOnce(() => new Promise<SettingsDTO>((resolve) => { finish = resolve }))
    await clickSave()
    await clickSave()
    expect(http.put).toHaveBeenCalledTimes(1)
    await router.push('/projects')
    expect(router.currentRoute.value.params.id).toBe('p1')
    expect(window.confirm).not.toHaveBeenCalled()
    const build = clone(wrapper!.findComponent(VarsCacheTab).props('build'))
    build.vars[0].value = 'newer'
    wrapper!.findComponent(VarsCacheTab).vm.$emit('update', build)
    finish({ ...storedSettings, build: { ...build, vars: [{ ...build.vars[0], value: 'persisted' }] } })
    await flushPromises()
    expect((wrapper!.find('input[aria-label="变量值"]').element as HTMLInputElement).value).toBe('newer')
    expect(wrapper!.find('.pipeline-banner--success').exists()).toBe(false)
    expect(isGuarded()).toBe(true)
  })

  it('retains local trigger save, leaves unrelated settings dirty, and sends no secrets or masks', async () => {
    storedSettings.build.vars = [{ id: 'secret', key: 'TOKEN', secret: true, credentialId: 'credential', maskedValue: 'masked' }]
    await render('triggers')
    await editEnvironment()
    await editTrigger()
    await wrapper!.findComponent(TriggersPanel).find('.save-bar button').trigger('click')
    await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(1)
    expect(isGuarded()).toBe(true)
    await clickSave()
    expect(http.put).toHaveBeenCalledTimes(3)
    expect(storedSettings.build.vars[0]).toEqual({ id: 'secret', key: 'TOKEN', secret: true, credentialId: 'credential' })
    expect(isGuarded()).toBe(false)
  })
})

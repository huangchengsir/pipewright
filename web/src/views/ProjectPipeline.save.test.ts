import { defineComponent, h } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView, type Router } from 'vue-router'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ProjectPipeline from './ProjectPipeline.vue'
import VarsCacheTab from '../components/pipeline/VarsCacheTab.vue'
import EnvCredsTab from '../components/pipeline/EnvCredsTab.vue'
import TriggersPanel from '../components/TriggersPanel.vue'
import ProjectPreviewConfig from '../components/pipeline/ProjectPreviewConfig.vue'
import PipelineSaveStatus from '../components/pipeline/PipelineSaveStatus.vue'
import ValidationPanel from '../components/pipeline/ValidationPanel.vue'
import { i18n } from '../i18n'
import { http, HttpError } from '../api/http'
import { bindConfigRevision } from '../api/configRevision'
import type { PipelineDTO, PipelineStage } from '../api/pipeline'
import type { SettingsDTO } from '../api/pipelineSettings'
import type { TriggerConfig } from '../api/triggers'
import type { PreviewConfig } from '../api/previewEnvs'
import type { ValidationDTO } from '../api/pipelineValidation'

vi.mock('../api/http', async (importOriginal) => ({
  ...await importOriginal<typeof import('../api/http')>(),
  http: { get: vi.fn(), put: vi.fn(), post: vi.fn(), patch: vi.fn() },
}))
vi.mock('../composables/useToast', () => ({
  useToast: () => ({ success: vi.fn(), error: vi.fn() }),
}))
vi.mock('../composables/useConfirm', () => ({
  useConfirm: () => ({ open: async () => window.confirm('Discard draft?') }),
}))

const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
// Locale files are owned by the parent change; keep pending copy isolated to this suite.
const originalMessages = clone(i18n.global.getLocaleMessage('zh-CN'))
i18n.global.mergeLocaleMessage('zh-CN', {
  projectPipeline: {
    saveConfiguration: '保存配置',
    replaceDraftTitle: '替换编排草稿？',
    replaceDraftBody: '未保存的编排修改将被替换，其他配置草稿保持不变。',
    replaceDraftConfirm: '替换草稿',
    saveStatus: {
      saved: '配置已保存', dirty: '配置未保存', loading: '正在加载配置',
      saving: '正在保存配置', error: '配置保存失败', partial: '配置已部分保存',
      unavailable: '部分配置暂不可用', newerEdits: '新编辑仍未保存',
      completedScopes: '已保存：{scopes}', pendingScopes: '待保存：{scopes}',
      failedScopes: '未保存成功：{scopes}', unavailableScopes: '暂不可用：{scopes}',
      dirtyTab: '{tab}，有未保存修改',
    },
  },
})
afterAll(() => i18n.global.setLocaleMessage('zh-CN', originalMessages))
const scopeLabel = (scope: string): string => scope === 'preview'
  ? i18n.global.t('previewEnvs.config.title')
  : i18n.global.t(`projectPipeline.tab${scope[0].toUpperCase()}${scope.slice(1)}`)
const prefix = '/api/projects/p1'
let storedPipeline: PipelineDTO
let storedSettings: SettingsDTO
let storedTrigger: TriggerConfig
let storedPreview: PreviewConfig
let wrapper: VueWrapper | undefined
let router: Router

const CanvasStub = defineComponent({
  name: 'PipelineCanvas',
  props: ['stages', 'projectName', 'project', 'environmentAutoPushPossible'],
  emits: ['update'],
  setup: () => () => h('div', { class: 'canvas-stub' }),
})
const ReplacementModalStub = defineComponent({
  props: ['beforeReplace'],
  emits: ['saved', 'applied'],
  setup: () => () => h('div'),
})

async function render(tab = 'canvas', realCanvas = false, realConfigCards = false, realReplacements = false): Promise<VueWrapper> {
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
        CronPanel: !realConfigCards, EnvironmentsPanel: true, ParametersPanel: true,
        ConcurrencyPanel: !realConfigCards, ChainPanel: true, RunnerPanel: true,
        AppSelect: true,
        ...(realReplacements ? {} : { YamlImportModal: ReplacementModalStub, TemplatePickerModal: ReplacementModalStub }),
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

function saveStatus() {
  return wrapper!.findComponent(PipelineSaveStatus)
}

function versionedRequests(lazyDefaults = false, alwaysChanging = false) {
  let revision = 1
  const reads = new Map<string, number>()
  const configPaths = ['pipeline', 'pipeline/settings', 'trigger', 'preview-config', 'cron', 'concurrency'].map(path => `${prefix}/${path}`)
  const normalGet = vi.mocked(http.get).getMockImplementation()!
  const normalPut = vi.mocked(http.put).getMockImplementation()!
  let project = { id: 'p1', name: 'Project', repoUrl: 'https://example.test/repo', pacEnabled: false, prStatusEnabled: false }
  vi.mocked(http.get).mockImplementation(async url => {
    if (url === '/api/projects') return [{ ...project, configRevision: `cfg-${revision}` }]
    const value = url === `${prefix}/cron` ? { enabled: false, expression: '', branch: '', nextRun: '' }
      : url === `${prefix}/concurrency` ? { maxConcurrent: 0 } : await normalGet(url)
    if (!configPaths.includes(url)) return value
    const count = (reads.get(url) ?? 0) + 1
    reads.set(url, count)
    if (alwaysChanging || (lazyDefaults && count === 1)) revision++
    return bindConfigRevision(value, `cfg-${revision}`)
  })
  function accept(options?: RequestInit) {
    expect(new Headers(options?.headers).get('If-Match')).toBe(`cfg-${revision}`)
    revision++
  }
  vi.mocked(http.put).mockImplementation(async (url, input, options) => {
    accept(options)
    const value = url === `${prefix}/cron` ? { ...(input as object), nextRun: '' }
      : url === `${prefix}/concurrency` ? input : await normalPut(url, input, options)
    return bindConfigRevision(value, `cfg-${revision}`)
  })
  vi.mocked(http.patch).mockImplementation(async (url, input, options) => {
    expect(url).toBe(prefix)
    accept(options)
    project = { ...project, ...(input as object) }
    return { ...project, configRevision: `cfg-${revision}` }
  })
  return { reads, current: () => `cfg-${revision}`, accept }
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
  vi.useRealTimers()
})

describe('ProjectPipeline draft save scope (#190/#193)', () => {
  it.each(['import', 'template', 'ai'])('loads the actual %s result before another versioned save', async source => {
    const versions = versionedRequests()
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    vi.mocked(http.get).mockImplementation((url, options) => url === '/api/templates'
      ? Promise.resolve({ templates: [{ id: 'tpl', name: 'Template', description: '', stageCount: 1 }] }) : normalGet(url, options))
    const replacement: PipelineStage[] = [{ id: 'replacement', name: 'Applied stages', kind: 'build', jobs: [] }]
    vi.mocked(http.post).mockImplementation(async (url, _input, options) => {
      if (url === `${prefix}/pipeline/ai-generate`) return {
        available: true, reason: '', analysis: { cloned: false, language: 'Go', languageVersion: '', buildTool: '', hasDockerfile: false, artifactHint: '', signals: [] },
        proposal: { stages: replacement, build: { model: 'dockerfile', toolchain: { language: '', version: '' }, artifactType: 'image', dockerfilePath: 'Generatedfile' }, branchMappings: [], rationale: 'Generated proposal' },
      }
      versions.accept(options)
      storedPipeline.stages = replacement
      if (url === `${prefix}/pipeline/ai-apply`) {
        storedSettings.build.dockerfilePath = 'Generatedfile'
        storedTrigger.events.push = true
        return bindConfigRevision({ applied: { spec: true, settings: true, triggers: true } }, versions.current())
      }
      expect([`${prefix}/pipeline/import`, `${prefix}/pipeline/apply-template`]).toContain(url)
      return bindConfigRevision(clone(storedPipeline), versions.current())
    })
    await render('canvas', false, false, true)
    const label = i18n.global.t(source === 'import' ? 'projectPipeline.importYaml' : source === 'template' ? 'projectPipeline.templates' : 'projectPipeline.aiGenerate')
    await wrapper!.findAll('.top-btn').find(button => button.text() === label)!.trigger('click'); await flushPromises()
    if (source === 'template') await wrapper!.get('.scrim .btn-primary').trigger('click')
    else if (source === 'import') {
      await wrapper!.get('.yi-modal textarea').setValue('version: 1')
      await wrapper!.findAll('.yi-modal button').find(button => button.text() === '导入并保存')!.trigger('click')
    } else {
      await wrapper!.findAll('.wiz-footer button').find(button => button.text().includes(i18n.global.t('pipelinePanels.wizGenerateConfig')))!.trigger('click'); await flushPromises()
      await wrapper!.findAll('.wiz-footer button').find(button => button.text() === i18n.global.t('pipelinePanels.wizApplySelected'))!.trigger('click')
    }
    await flushPromises()
    expect(wrapper!.findComponent(CanvasStub).props('stages')).toEqual(replacement)
    if (source === 'ai') {
      expect(wrapper!.findComponent(VarsCacheTab).props('build').dockerfilePath).toBe('Generatedfile')
      expect(versions.reads.get(`${prefix}/trigger`)).toBe(2)
      expect(wrapper!.findComponent(TriggersPanel).vm.dirty).toBe(false)
    }
    await wrapper!.findAll('.pac-switch')[1].trigger('click'); await flushPromises()
    await editVariable(); await clickSave()
    expect(storedPipeline.stages).toEqual(replacement)
    expect(http.put).toHaveBeenCalledTimes(2)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('re-reads lazy defaults once before the first pristine multi-block save', async () => {
    const versions = versionedRequests(true)
    await render('vars')
    for (const path of ['pipeline', 'pipeline/settings', 'trigger', 'preview-config']) expect(versions.reads.get(`${prefix}/${path}`)).toBe(2)
    await editVariable(); await clickSave()
    expect(http.put).toHaveBeenCalledTimes(2)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it.each([0, 1])('propagates acknowledged saves to project switch %s and its result back to drafts', async index => {
    versionedRequests()
    await render('vars'); await editVariable(); await clickSave()
    await switchTab('canvas')
    await wrapper!.findAll('.pac-switch')[index].trigger('click'); await flushPromises()
    expect(http.patch).toHaveBeenCalledTimes(1)
    expect(wrapper!.find('.pac-bar-err').exists()).toBe(false)
    wrapper!.findComponent(CanvasStub).vm.$emit('update', [{ id: 's', name: 'Changed', kind: 'build', jobs: [] }])
    await clickSave()
    expect(http.put).toHaveBeenCalledTimes(3)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('advances all same-baseline drafts after a project switch before their first save', async () => {
    versionedRequests()
    await render()
    await wrapper!.findAll('.pac-switch')[1].trigger('click'); await flushPromises()
    await editVariable(); await editTrigger()
    await wrapper!.findComponent(ProjectPreviewConfig).find('.pvc__in').setValue('preview.example.com')
    await clickSave()
    expect(http.put).toHaveBeenCalledTimes(4)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('retains a newer same-page commit when an overlapping project switch is rejected', async () => {
    versionedRequests()
    const normalPatch = vi.mocked(http.patch).getMockImplementation()!
    let failPac!: (reason: HttpError) => void
    vi.mocked(http.patch).mockImplementation((url, input, options) => (input as { pacEnabled?: boolean }).pacEnabled
      ? new Promise((_resolve, reject) => { failPac = reject }) : normalPatch(url, input, options))
    await render()
    await wrapper!.findAll('.pac-switch')[0].trigger('click')
    await wrapper!.findAll('.pac-switch')[1].trigger('click'); await flushPromises()
    failPac(new HttpError(409, { code: 'revision_conflict', message: 'Reload' }, 'Reload')); await flushPromises()
    expect(wrapper!.findAll('.pac-switch')[0].attributes('aria-checked')).toBe('false')
    expect(wrapper!.findAll('.pac-switch')[1].attributes('aria-checked')).toBe('true')
    await editVariable(); await clickSave()
    expect(http.put).toHaveBeenCalledTimes(2)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('propagates a child-card commit to sibling cards, project switches and draft saves', async () => {
    versionedRequests()
    await render('triggers', false, true)
    await wrapper!.get('#conc-input').setValue(2)
    await wrapper!.get('.conc-save button').trigger('click'); await flushPromises()
    await wrapper!.get('.cron-save button').trigger('click'); await flushPromises()
    expect(http.put).toHaveBeenCalledTimes(2)
    await switchTab('canvas')
    await wrapper!.findAll('.pac-switch')[1].trigger('click'); await flushPromises()
    await editVariable(); await clickSave()
    expect(http.patch).toHaveBeenCalledTimes(1)
    expect(http.put).toHaveBeenCalledTimes(4)
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('never replaces user edits with a late initialization re-read', async () => {
    versionedRequests(true)
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    let count = 0
    let finish!: () => void
    vi.mocked(http.get).mockImplementation(async (url, options) => {
      const value = await normalGet(url, options)
      if (url === `${prefix}/pipeline/settings` && ++count === 2) return new Promise(resolve => { finish = () => resolve(value) })
      return value
    })
    await render('vars')
    const build = clone(wrapper!.findComponent(VarsCacheTab).props('build'))
    build.vars = [{ id: 'new', key: 'NAME', value: 'persisted', secret: false }]
    wrapper!.findComponent(VarsCacheTab).vm.$emit('update', build)
    finish(); await flushPromises()
    expect((wrapper!.get('input[aria-label="变量值"]').element as HTMLInputElement).value).toBe('persisted')
    await clickSave()
    expect(storedSettings.build.vars[0].value).toBe('persisted')
    expect(saveStatus().props('state')).toBe('saved')
  })
  it('bounds pristine snapshot retries when configuration keeps changing', async () => {
    const versions = versionedRequests(false, true)
    await render()
    for (const path of ['pipeline', 'pipeline/settings', 'trigger', 'preview-config']) expect(versions.reads.get(`${prefix}/${path}`)).toBe(3)
    await editVariable()
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(wrapper!.text()).toContain('重新加载')
  })
  it('derives image auto-push uncertainty from edited environment registries', async () => {
    await render()
    const canvas = wrapper!.findComponent(CanvasStub)
    expect(canvas.props('environmentAutoPushPossible')).toBe(false)
    const env = { name: 'prod', targetServerIds: [], envVars: [], imageRegistry: { type: 'harbor', url: '   ', credentialId: '' } }
    wrapper!.findComponent(EnvCredsTab).vm.$emit('update', [env])
    await flushPromises()
    expect(canvas.props('environmentAutoPushPossible')).toBe(false)
    wrapper!.findComponent(EnvCredsTab).vm.$emit('update', [{ ...env, imageRegistry: { ...env.imageRegistry, url: ' http://harbor.example/team ' } }])
    await flushPromises()
    expect(canvas.props('environmentAutoPushPossible')).toBe(true)
  })
  it('does not assume builds are unpublished when environment settings are unavailable', async () => {
    const get = vi.mocked(http.get).getMockImplementation()!
    vi.mocked(http.get).mockImplementation((url, options) => url === `${prefix}/pipeline/settings`
      ? Promise.reject(new Error('settings unavailable')) : get(url, options))
    await render()
    expect(wrapper!.findComponent(CanvasStub).props('environmentAutoPushPossible')).toBe(true)
  })
  it('shows persistent saved/dirty state and exact per-tab pending scope', async () => {
    await render('vars')
    expect(saveStatus().props('state')).toBe('saved')
    expect(wrapper!.findComponent(CanvasStub).props('projectName')).toBe('Project')
    expect(wrapper!.findComponent(CanvasStub).props('project')).toMatchObject({ id: 'p1', name: 'Project' })
    await editVariable()
    expect(saveStatus().props('state')).toBe('dirty')
    expect(saveStatus().props('pendingScopes')).toEqual([scopeLabel('vars')])
    expect(wrapper!.find('#tab-vars').classes()).toContain('tab-btn--dirty')
    expect(wrapper!.find('#tab-envs').classes()).not.toContain('tab-btn--dirty')
    expect(wrapper!.find('#tab-vars').attributes('aria-label')).toContain('有未保存修改')
    await editEnvironment()
    await editTrigger()
    await wrapper!.findComponent(ProjectPreviewConfig).find('.pvc__in').setValue('preview.example.com')
    wrapper!.findComponent(CanvasStub).vm.$emit('update', [{ id: 's', name: 'Build', kind: 'build', jobs: [] }])
    await flushPromises()
    expect(saveStatus().props('pendingScopes')).toEqual(['canvas', 'vars', 'envs', 'triggers', 'preview'].map(scopeLabel))
    expect(wrapper!.findAll('.tab-btn--dirty')).toHaveLength(4)
    await switchTab('canvas')
    expect(saveStatus().find('.save-status-pending').text()).toContain(scopeLabel('preview'))
    await clickSave()
    expect(saveStatus().props('state')).toBe('saved')
    expect(saveStatus().props('pendingScopes')).toEqual([])
    expect(wrapper!.findAll('.tab-btn--dirty')).toHaveLength(0)
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    await vi.advanceTimersByTimeAsync(10000)
    expect(saveStatus().text()).toBe('配置已保存')
  })

  it('marks the trigger tab for preview-only edits and returns to saved after its local save', async () => {
    await render('triggers')
    const preview = wrapper!.findComponent(ProjectPreviewConfig)
    await preview.find('.pvc__in').setValue('preview.example.com')
    expect(saveStatus().props('pendingScopes')).toEqual([scopeLabel('preview')])
    expect(wrapper!.find('#tab-triggers').classes()).toContain('tab-btn--dirty')
    await preview.find('.pvc__save').trigger('click')
    await flushPromises()
    expect(saveStatus().props('state')).toBe('saved')
    expect(wrapper!.find('#tab-triggers').classes()).not.toContain('tab-btn--dirty')
  })
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
    expect(saveStatus().props('state')).toBe(scope === 'pipeline' ? 'error' : 'partial')
    const failedScopes = scope === 'pipeline/settings' ? ['vars'] : [scope === 'pipeline' ? 'canvas' : scope === 'trigger' ? 'triggers' : 'preview']
    expect(saveStatus().props('failedScopes')).toEqual(failedScopes.map(scopeLabel))
    expect(saveStatus().find('.save-status-failed').text()).toContain(scopeLabel(failedScopes[0]))
    expect(isGuarded()).toBe(true)
    const alreadySaved = vi.mocked(http.put).mock.calls.slice(0, -1).map(([url]) => url)
    const acknowledged = alreadySaved.map((url) => url.endsWith('/pipeline/settings') ? 'vars' : url.endsWith('/pipeline') ? 'canvas' : 'triggers')
    expect(saveStatus().props('completedScopes')).toEqual(acknowledged.map(scopeLabel))
    for (const label of saveStatus().props('completedScopes')) {
      expect(saveStatus().props('pendingScopes')).not.toContain(label)
    }
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
    expect(saveStatus().props('state')).toBe('error')
    expect(saveStatus().props('failedScopes')).toEqual([scopeLabel('triggers')])
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
    expect(saveStatus().props('state')).toBe(scope === 'pipeline/settings' ? 'error' : 'unavailable')
    expect(saveStatus().props('unavailableScopes')).toEqual((scope === 'pipeline/settings' ? ['vars', 'envs'] : [scope === 'trigger' ? 'triggers' : 'preview']).map(scopeLabel))
    expect(wrapper!.find('.top-btn--save').attributes('disabled')).toBeDefined()
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
    expect(saveStatus().props('state')).toBe('loading')
    await editTrigger()
    expect(saveStatus().props('pendingScopes')).toEqual([scopeLabel('triggers')])
    await clickSave()
    expect(saveStatus().props('state')).toBe('loading')
    expect(saveStatus().attributes('aria-busy')).toBe('true')
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
    expect(saveStatus().props('state')).toBe('saving')
    expect(saveStatus().attributes('aria-busy')).toBe('true')
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
    expect(saveStatus().props('state')).toBe('dirty')
    expect(saveStatus().find('.save-status-title').text()).toBe('新编辑仍未保存')
    expect(saveStatus().props('failedScopes')).toEqual([])
    expect(wrapper!.find('.pipeline-banner--error').exists()).toBe(false)
    expect(saveStatus().props('pendingScopes')).toEqual([scopeLabel(scope)])
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
    expect(saveStatus().props('state')).toBe('dirty')
    expect(saveStatus().props('pendingScopes')).toEqual([scopeLabel('vars')])
    expect(wrapper!.find('.pipeline-banner--error').exists()).toBe(false)
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

  it.each(['import', 'template'])('guards %s stage replacement without guarding unrelated drafts', async (source) => {
    await render()
    await editVariable()
    const label = i18n.global.t(source === 'import' ? 'projectPipeline.importYaml' : 'projectPipeline.templates')
    await wrapper!.findAll('.top-btn').find((button) => button.text() === label)!.trigger('click')
    const modal = wrapper!.findComponent(ReplacementModalStub)
    const beforeReplace = modal.props('beforeReplace') as () => Promise<boolean>
    expect(await beforeReplace()).toBe(true)
    expect(window.confirm).not.toHaveBeenCalled()
    const draft = [{ id: 's', name: 'Unsaved', kind: 'build', jobs: [] }]
    wrapper!.findComponent(CanvasStub).vm.$emit('update', draft)
    await flushPromises()
    expect(await beforeReplace()).toBe(false)
    expect(wrapper!.findComponent(CanvasStub).props('stages')).toEqual(draft)
    vi.mocked(window.confirm).mockReturnValue(true)
    expect(await beforeReplace()).toBe(true)
    expect(window.confirm).toHaveBeenCalledTimes(2)
    expect(http.put).not.toHaveBeenCalled()
  })

  it.each(['import', 'template'])('preserves variable, environment and trigger drafts after %s saves only stages', async (source) => {
    await render('vars')
    await editVariable()
    await editEnvironment()
    await editTrigger()
    const build = wrapper!.findComponent(VarsCacheTab).props('build')
    const environments = wrapper!.findComponent(EnvCredsTab).props('environments')
    const label = i18n.global.t(source === 'import' ? 'projectPipeline.importYaml' : 'projectPipeline.templates')
    await wrapper!.findAll('.top-btn').find((button) => button.text() === label)!.trigger('click')
    storedPipeline.stages = [{ id: 'replacement', name: 'Replaced', kind: 'build', jobs: [] }]
    wrapper!.findComponent(ReplacementModalStub).vm.$emit(source === 'import' ? 'saved' : 'applied')
    await flushPromises()
    expect(wrapper!.findComponent(CanvasStub).props('stages')).toEqual(storedPipeline.stages)
    expect(wrapper!.findComponent(VarsCacheTab).props('build')).toBe(build)
    expect(wrapper!.findComponent(EnvCredsTab).props('environments')).toBe(environments)
    expect(vi.mocked(http.get).mock.calls.filter(([url]) => url === `${prefix}/pipeline/settings`)).toHaveLength(1)
    expect(saveStatus().props('state')).toBe('dirty')
    expect(saveStatus().props('pendingScopes')).toEqual(['vars', 'envs', 'triggers'].map(scopeLabel))
    expect(wrapper!.find('#tab-canvas').classes()).not.toContain('tab-btn--dirty')
    expect(isGuarded()).toBe(true)
    await clickSave()
    expect(storedSettings.build.vars[0].value).toBe('persisted')
    expect(storedSettings.environments[0].name).toBe('acceptance-test')
    expect(saveStatus().props('state')).toBe('saved')
  })

  it.each(['canvas', 'vars', 'envs', 'triggers', 'preview'])('clears cached persisted validation on new %s edits', async (scope) => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    vi.mocked(http.get).mockImplementation(async (url) => url === `${prefix}/pipeline/validation`
      ? { ready: true, issues: [] } : normalGet(url))
    await render()
    await wrapper!.find('.top-btn--validate').trigger('click')
    await flushPromises()
    expect(wrapper!.find('.top-btn-badge--ok').exists()).toBe(true)
    if (scope === 'canvas') wrapper!.findComponent(CanvasStub).vm.$emit('update', [{ id: 's', name: 'Changed', kind: 'build', jobs: [] }])
    if (scope === 'vars') await editVariable()
    if (scope === 'envs') await editEnvironment()
    if (scope === 'triggers') await editTrigger()
    if (scope === 'preview') await wrapper!.findComponent(ProjectPreviewConfig).find('.pvc__in').setValue('changed.example.com')
    await flushPromises()
    expect(wrapper!.find('.top-btn-badge').exists()).toBe(false)
    expect(wrapper!.findComponent(ValidationPanel).props('ready')).toBe(false)
    expect(wrapper!.findComponent(ValidationPanel).props('issues')).toEqual([])
    expect(wrapper!.findComponent(ValidationPanel).props('pendingMessage')).toBe(i18n.global.t('pipelinePanels.vpSaveFirst'))
    const requests = vi.mocked(http.get).mock.calls.filter(([url]) => url === `${prefix}/pipeline/validation`).length
    await wrapper!.find('.top-btn--validate').trigger('click')
    await flushPromises()
    expect(vi.mocked(http.get).mock.calls.filter(([url]) => url === `${prefix}/pipeline/validation`)).toHaveLength(requests)
  })

  it('does not restore obsolete validation when a response arrives after a draft edit', async () => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    let finish!: (value: ValidationDTO) => void
    vi.mocked(http.get).mockImplementation(async (url) => url === `${prefix}/pipeline/validation`
      ? new Promise<ValidationDTO>((resolve) => { finish = resolve }) : normalGet(url))
    await render()
    await wrapper!.find('.top-btn--validate').trigger('click')
    await flushPromises()
    expect(wrapper!.findComponent(ValidationPanel).props('loading')).toBe(true)
    await editVariable()
    finish({ ready: true, issues: [] })
    await flushPromises()
    expect(wrapper!.find('.top-btn-badge').exists()).toBe(false)
    expect(wrapper!.findComponent(ValidationPanel).props('ready')).toBe(false)
  })

  it('clears readiness on unblurred drawer input without changing parent-state dirty semantics', async () => {
    const normalGet = vi.mocked(http.get).getMockImplementation()!
    vi.mocked(http.get).mockImplementation(async (url) => url === `${prefix}/pipeline/validation`
      ? { ready: true, issues: [] } : normalGet(url))
    storedPipeline.stages = [{ id: 's', name: 'Build', kind: 'build', jobs: [{ id: 'j', name: 'Before', summary: '', type: 'script', config: { script: 'true' } }] }]
    await render('canvas', true)
    await wrapper!.find('.top-btn--validate').trigger('click')
    await flushPromises()
    await wrapper!.find('.job-card').trigger('click')
    await wrapper!.find('.job-drawer input.drawer-input').setValue('Unblurred')
    expect(wrapper!.find('.top-btn-badge').exists()).toBe(false)
    expect(isGuarded()).toBe(false)
  })

  it('retains the existing all-scope validation of a clean invalid trigger draft', async () => {
    storedTrigger.branchMappings = [{ id: 'bad', branchPattern: '/invalid/', environment: 'prod', targetServerIds: [] }]
    await render('vars')
    await editVariable()
    expect(wrapper!.find('#tab-triggers').classes()).not.toContain('tab-btn--dirty')
    await clickSave()
    expect(http.put).not.toHaveBeenCalled()
    expect(saveStatus().props('state')).toBe('error')
    expect(saveStatus().props('failedScopes')).toEqual([scopeLabel('triggers')])
  })
})

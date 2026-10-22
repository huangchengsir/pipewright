<script setup lang="ts">
/**
 * ProjectPipeline — 4-tab pipeline configuration editor.
 * Tabs: 流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据
 * URL state: ?tab=canvas|vars|triggers|envs  (shareable)
 */
import { ref, shallowRef, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { DeviceFloppy, FileImport, ListCheck, Robot, Stack2 } from '@vicons/tabler'
import { getPipeline, savePipeline, type PipelineDTO, type PipelineStage } from '../api/pipeline'
import {
  getSettings,
  saveSettings,
  type SettingsDTO,
  type BuildConfig,
  type Environment,
  type SaveSettingsInput,
} from '../api/pipelineSettings'
import { listCredentials, type Credential } from '../api/credentials'
import { listProjects, updateProject, type Project } from '../api/projects'
import { listServers, type Server } from '../api/servers'
import { listChannels, type NotificationChannel } from '../api/notifications'
import { getValidation, type ValidationDTO, type IssueScope } from '../api/pipelineValidation'
import { HttpError } from '../api/http'
import type { AIApplyResponse } from '../api/aiGenerate'
import { bindConfigRevision, configRevisionOf } from '../api/configRevision'
import { provideProjectConfigRevisions } from '../composables/useProjectConfigRevisions'
import PipelineCanvas from '../components/pipeline/PipelineCanvas.vue'
import PipelineSaveStatus from '../components/pipeline/PipelineSaveStatus.vue'
import PipelineGuide from '../components/onboarding/PipelineGuide.vue'
import type { GuideContext, GuidePhase } from '../composables/pipelineGuideState'
import VarsCacheTab from '../components/pipeline/VarsCacheTab.vue'
import EnvCredsTab from '../components/pipeline/EnvCredsTab.vue'
import TriggersPanel from '../components/TriggersPanel.vue'
import ValidationPanel from '../components/pipeline/ValidationPanel.vue'
import AIGenerateWizard from '../components/pipeline/AIGenerateWizard.vue'
import RiskAnnotationPanel from '../components/pipeline/RiskAnnotationPanel.vue'
import YamlImportModal from '../components/pipeline/YamlImportModal.vue'
import TemplatePickerModal from '../components/pipeline/TemplatePickerModal.vue'
import PacPreviewModal from '../components/pipeline/PacPreviewModal.vue'
import ProjectPreviewConfig from '../components/pipeline/ProjectPreviewConfig.vue'
import { usePipelineDraftSave, type PipelineDraftParticipant, type PipelineSaveContext, type PipelineSaveState } from '../composables/usePipelineDraftSave'
import { useConfirm } from '../composables/useConfirm'

// ─── Route ────────────────────────────────────────────────────────────────────

const { t } = useI18n()

const route  = useRoute()
const router = useRouter()

const projectId = computed(() => route.params.id as string)
const revisionCoordinator = provideProjectConfigRevisions(() => projectId.value)
const initialTouched = ref(false)
function markInitialEdit(): void { initialTouched.value = true }

// ─── Tabs ─────────────────────────────────────────────────────────────────────

type TabKey = 'canvas' | 'vars' | 'triggers' | 'envs'

const TABS = computed<Array<{ key: TabKey; label: string }>>(() => [
  { key: 'canvas',   label: t('projectPipeline.tabCanvas') },
  { key: 'vars',     label: t('projectPipeline.tabVars') },
  { key: 'triggers', label: t('projectPipeline.tabTriggers') },
  { key: 'envs',     label: t('projectPipeline.tabEnvs') },
])

const activeTab = computed<TabKey>(() => {
  const q = route.query.tab
  if (q === 'canvas' || q === 'vars' || q === 'triggers' || q === 'envs') return q
  return 'canvas'
})

function setTab(key: TabKey): void {
  void router.replace({ query: { ...route.query, tab: key } })
}

// ─── Load state ───────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')

// ─── Pipeline data ────────────────────────────────────────────────────────────

const pipeline = ref<PipelineDTO | null>(null)

/** Local editable stages; written back via savePipeline */
const editStages = ref<PipelineStage[]>([])
const persistedStages = shallowRef('')
const guideEnabled = computed(() => route.query.onboardingGuide === '1')
const guideRef = ref<InstanceType<typeof PipelineGuide> | null>(null)
const canvasRef = ref<InstanceType<typeof PipelineCanvas> | null>(null)
const guideContext = ref<GuideContext>({ selectedJobId: null, pickerOpen: false })
const stagesDirty = computed(() => !!pipeline.value && JSON.stringify(editStages.value) !== persistedStages.value)

async function locateGuide(phase: GuidePhase | 'conditions'): Promise<void> {
  if (phase !== 'save') {
    await router.replace({ query: { ...route.query, tab: 'canvas' } })
    await nextTick()
  }
  if (phase === 'configure') canvasRef.value?.focusGuideJob()
  if (phase === 'conditions') canvasRef.value?.focusGuideStage()
}
function closeGuide(): void {
  const query = { ...route.query }
  delete query.onboardingGuide
  void router.replace({ query })
}

function applyPipeline(dto: PipelineDTO): void {
  pipeline.value = dto
  persistedStages.value = JSON.stringify(dto.stages)
  editStages.value = JSON.parse(JSON.stringify(dto.stages)) as PipelineStage[]
}

let pipelineLoadGeneration = 0
async function loadPipeline(): Promise<void> {
  const generation = ++pipelineLoadGeneration
  const id = projectId.value
  const initial = pipelineRevisionSync.isInitialRead(id)
  if (!initial) loadState.value = 'loading'
  loadError.value = ''
  try {
    const dto = await getPipeline(id)
    if (id !== projectId.value || generation !== pipelineLoadGeneration) return
    if (initial && !pipelineRevisionSync.canAcceptInitialRead()) return
    applyPipeline(dto)
    loadState.value = 'idle'
  } catch (err) {
    if (id !== projectId.value || generation !== pipelineLoadGeneration) return
    if (initial) return
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = t('projectPipeline.errNoServer')
      } else if (err.status === 404) {
        loadError.value = t('projectPipeline.errProjectNotFound')
      } else {
        loadError.value = err.apiError?.message ?? t('projectPipeline.errLoadFailedStatus', { status: err.status })
      }
    } else {
      loadError.value = t('projectPipeline.errLoadFailedRetry')
    }
    loadState.value = 'error'
  }
}

// ─── Build/deploy settings data (Story 2.4) ───────────────────────────────────

const settings    = ref<SettingsDTO | null>(null)
const editBuild    = ref<BuildConfig | null>(null)
const editEnvs     = ref<Environment[]>([])
const credentials  = ref<Credential[]>([])
const servers      = ref<Server[]>([])
const channels     = ref<NotificationChannel[]>([])
const settingsLoadState = shallowRef<LoadState>('loading')
const buildDirty = computed(() => !!settings.value && !!editBuild.value
  && JSON.stringify(editBuild.value) !== JSON.stringify(settings.value.build))
const environmentsDirty = computed(() => !!settings.value
  && JSON.stringify(editEnvs.value) !== JSON.stringify(settings.value.environments))
const settingsDirty = computed(() => buildDirty.value || environmentsDirty.value)
const environmentAutoPushPossible = computed(() => settingsLoadState.value !== 'idle'
  || editEnvs.value.some((env) => !!env.imageRegistry.type.trim() && !!env.imageRegistry.url.trim()))

function applySettings(dto: SettingsDTO): void {
  settings.value = dto
  editBuild.value = JSON.parse(JSON.stringify(dto.build)) as BuildConfig
  editEnvs.value  = JSON.parse(JSON.stringify(dto.environments)) as Environment[]
}

let settingsLoadGeneration = 0
async function loadSettings(): Promise<void> {
  const generation = ++settingsLoadGeneration
  const id = projectId.value
  const initial = settingsRevisionSync.isInitialRead(id)
  if (!initial) settingsLoadState.value = 'loading'
  try {
    const [dto, creds, srvs, chs] = await Promise.all([
      getSettings(id),
      listCredentials().catch(() => [] as Credential[]),
      listServers().catch(() => [] as Server[]),
      listChannels().catch(() => [] as NotificationChannel[]),
    ])
    if (id !== projectId.value || generation !== settingsLoadGeneration) return
    if (initial && !settingsRevisionSync.canAcceptInitialRead()) return
    applySettings(dto)
    credentials.value = creds
    servers.value = srvs
    channels.value = chs
    settingsLoadState.value = 'idle'
  } catch {
    if (id !== projectId.value || generation !== settingsLoadGeneration) return
    if (initial) return
    settingsLoadState.value = 'error'
  }
}

function handleBuildUpdate(build: BuildConfig): void {
  markInitialEdit()
  editBuild.value = build
}

function handleEnvsUpdate(envs: Environment[]): void {
  markInitialEdit()
  editEnvs.value = envs
}

watch(projectId, () => {
  initialTouched.value = false
  aiWizardOpen.value = false
  yamlImportOpen.value = false
  templateModalOpen.value = false
  project.value = null
  pipeline.value = null
  settings.value = null
  editBuild.value = null
  editEnvs.value = []
  saveBanner.value = ''
  draftSave.resetOutcome()
  void loadProject(); void loadPipeline(); void loadSettings()
})
onMounted(() => { void loadProject(); void loadPipeline(); void loadSettings() })

// ─── Canvas update ────────────────────────────────────────────────────────────

function handleCanvasUpdate(stages: PipelineStage[]): void {
  markInitialEdit()
  editStages.value = stages
}

// ─── Save pipeline ────────────────────────────────────────────────────────────

const triggersRef = ref<InstanceType<typeof TriggersPanel> | null>(null)
const previewRef = ref<InstanceType<typeof ProjectPreviewConfig> | null>(null)
const confirm = useConfirm()
const pipelineRevisionSync = revisionCoordinator.register({ projectId: () => projectId.value, read: () => configRevisionOf(pipeline.value), advance: (_from, to) => bindConfigRevision(pipeline.value, to), ready: () => loadState.value === 'idle' && !!pipeline.value, reload: loadPipeline })
const settingsRevisionSync = revisionCoordinator.register({ projectId: () => projectId.value, read: () => configRevisionOf(settings.value), advance: (_from, to) => bindConfigRevision(settings.value, to), ready: () => settingsLoadState.value === 'idle' && !!settings.value, reload: loadSettings })
const projectRevisionSync = revisionCoordinator.register({ projectId: () => projectId.value, read: () => configRevisionOf(project.value), advance: (_from, to) => bindConfigRevision(project.value, to), ready: () => !!project.value, reload: loadProject })

function triggerParticipant(part: PipelineDraftParticipant | null, scope: string): PipelineDraftParticipant | null {
  if (!part) return null
  return {
    scopes: [scope],
    ready: part.ready,
    dirty: part.dirty,
    saving: part.saving,
    get configRevision() { return part.configRevision },
    advanceConfigRevision: (from, to) => part.advanceConfigRevision?.(from, to),
    validate: () => {
      const valid = part.validate?.() ?? true
      if (!valid) setTab('triggers')
      return valid
    },
    save: async (context) => {
      const saved = await part.save(context)
      if (!saved) setTab('triggers')
      return saved
    },
  }
}

const draftSave = usePipelineDraftSave({
  participants: () => [
    {
      scopes: ['canvas'],
      ready: loadState.value === 'idle' && !!pipeline.value,
      dirty: stagesDirty.value,
      saving: false,
      saveEvenIfClean: true,
      get configRevision() { return configRevisionOf(pipeline.value) },
      advanceConfigRevision: (from, to) => { if (configRevisionOf(pipeline.value) === from) bindConfigRevision(pipeline.value, to) },
      save: saveStagesDraft,
    },
    {
      get scopes() {
        return [
          ...(buildDirty.value ? ['vars'] : []),
          ...(environmentsDirty.value ? ['envs'] : []),
        ]
      },
      ready: settingsLoadState.value === 'idle' && !!editBuild.value,
      dirty: settingsDirty.value,
      saving: false,
      get configRevision() { return configRevisionOf(settings.value) },
      advanceConfigRevision: (from, to) => { if (configRevisionOf(settings.value) === from) bindConfigRevision(settings.value, to) },
      save: saveSettingsDraft,
    },
    triggerParticipant(triggersRef.value, 'triggers'),
    triggerParticipant(previewRef.value, 'preview'),
  ],
  confirmDiscard: () => confirm.open({
    title: t('opsChat.unsaved'),
    body: t('opsChat.discardBody'),
    confirmLabel: t('opsChat.discard'),
    variant: 'danger',
  }),
})
const saveSubmitting = computed(() => draftSave.busy.value || revisionCoordinator.aligning.value)
const guideDirty = draftSave.dirty
const saveBanner     = ref('')
const tabDirty = computed<Record<TabKey, boolean>>(() => ({
  canvas: stagesDirty.value,
  vars: buildDirty.value,
  envs: environmentsDirty.value,
  triggers: !!triggersRef.value?.dirty || !!previewRef.value?.dirty,
}))
const scopeLabels = computed<Record<string, string>>(() => ({
  canvas: t('projectPipeline.tabCanvas'),
  vars: t('projectPipeline.tabVars'),
  envs: t('projectPipeline.tabEnvs'),
  triggers: t('projectPipeline.tabTriggers'),
  preview: t('previewEnvs.config.title'),
}))
const pendingScopeKeys = computed(() => [
  ...(stagesDirty.value ? ['canvas'] : []),
  ...(buildDirty.value ? ['vars'] : []),
  ...(environmentsDirty.value ? ['envs'] : []),
  ...(triggersRef.value?.dirty ? ['triggers'] : []),
  ...(previewRef.value?.dirty ? ['preview'] : []),
])
const unavailableScopeKeys = computed(() => [
  ...(loadState.value !== 'idle' || !pipeline.value ? ['canvas'] : []),
  ...(settingsLoadState.value !== 'idle' || !editBuild.value ? ['vars', 'envs'] : []),
  ...(!triggersRef.value?.ready ? ['triggers'] : []),
  ...(!previewRef.value?.ready ? ['preview'] : []),
])
function labelScopes(scopes: readonly string[]): string[] {
  return scopes.map((scope) => scopeLabels.value[scope] ?? scope)
}
const pendingScopeLabels = computed(() => labelScopes(pendingScopeKeys.value))
const unavailableScopeLabels = computed(() => labelScopes(unavailableScopeKeys.value))
const completedScopeLabels = computed(() => labelScopes(draftSave.outcome.value?.completedScopes ?? []))
const failedScopeLabels = computed(() => labelScopes(draftSave.outcome.value?.failedScopes ?? []))
const saveState = computed<PipelineSaveState>(() => {
  if (saveSubmitting.value) return 'saving'
  if (loadState.value === 'loading' || settingsLoadState.value === 'loading') return 'loading'
  if (loadState.value === 'error' || settingsLoadState.value === 'error') return 'error'
  if (!draftSave.ready.value) return 'unavailable'
  const outcome = draftSave.outcome.value
  if (outcome?.state === 'partial' || outcome?.state === 'error') return outcome.state
  return guideDirty.value ? 'dirty' : 'saved'
})

/** Maps 422 error codes to user-visible hints. */
function mapSaveError(err: HttpError): string {
  const code = err.apiError?.code
  switch (code) {
    case 'invalid_stage':        return t('projectPipeline.errInvalidStage')
    case 'invalid_job':          return t('projectPipeline.errInvalidJob')
    case 'duplicate_id':         return t('projectPipeline.errDuplicateId')
    case 'invalid_build':        return t('projectPipeline.errInvalidBuild')
    case 'invalid_var':          return t('projectPipeline.errInvalidVar')
    case 'invalid_environment':  return t('projectPipeline.errInvalidEnvironment')
    case 'credential_not_found': return t('projectPipeline.errCredentialNotFound')
    case 'vault_unconfigured':   return t('projectPipeline.errVaultUnconfigured')
    default:                     return err.apiError?.message ?? t('projectPipeline.errSaveFailedStatus', { status: err.status })
  }
}

/** Builds the settings save payload from local edit state. */
function buildSettingsPayload(): SaveSettingsInput | null {
  if (!editBuild.value) return null
  const b = editBuild.value
  return {
    build: {
      model: b.model,
      dockerfilePath: b.dockerfilePath,
      toolchain: b.toolchain,
      artifactType: b.artifactType,
      vars: b.vars.map((v) => ({
        id: v.id || undefined,
        key: v.key,
        secret: v.secret,
        value: v.secret ? undefined : v.value,
        credentialId: v.secret ? v.credentialId : undefined,
      })),
      cache: b.cache,
    },
    environments: editEnvs.value.map((e) => ({
      id: e.id || undefined,
      name: e.name,
      targetServerIds: e.targetServerIds,
      envVars: e.envVars.map((v) => ({
        id: v.id || undefined,
        key: v.key,
        secret: v.secret,
        value: v.secret ? undefined : v.value,
        credentialId: v.secret ? v.credentialId : undefined,
      })),
      imageRegistry: {
        type: e.imageRegistry.type,
        url: e.imageRegistry.url,
        credentialId: e.imageRegistry.credentialId || undefined,
      },
    })),
  }
}

async function handleSave(): Promise<void> {
  if (saveSubmitting.value) return
  saveBanner.value     = ''

  try {
    if (!await draftSave.save()) {
      // Acknowledged writes with newer local edits are pending, not a failed save.
      if (draftSave.outcome.value?.state === 'error' || draftSave.outcome.value?.state === 'partial') {
        saveBanner.value = draftSave.outcome.value.reason === 'revision_mismatch' ? t('mcpConfig.reloadRequired') : t('projectPipeline.errSaveFailedRetry')
      }
      return
    }
    void guideRef.value?.refresh()
    // Revalidate after a successful save (debounced, only when panel is open).
    scheduleValidation(400)
  } catch (err) {
    if (err instanceof HttpError) {
      saveBanner.value = err.status === 0
        ? t('projectPipeline.errNoServer')
        : mapSaveError(err)
    } else {
      saveBanner.value = t('projectPipeline.errSaveFailedRetry')
    }
  }
}

async function saveStagesDraft(context?: PipelineSaveContext): Promise<boolean> {
  const id = projectId.value
  const snapshot = JSON.stringify(editStages.value)
  const revision = context?.baseRevision ?? configRevisionOf(pipeline.value)
  const payload = { stages: JSON.parse(snapshot) as PipelineStage[] }
  const dto = revision ? await savePipeline(id, payload, revision) : await savePipeline(id, payload)
  if (id !== projectId.value) return false
  revisionCoordinator.commit(id, revision, dto)
  const committedRevision = configRevisionOf(dto)
  if (committedRevision) context?.onCommitted(committedRevision)
  if (JSON.stringify(editStages.value) === snapshot && JSON.stringify(dto.stages) !== snapshot) applyPipeline(dto)
  else {
    pipeline.value = dto
    persistedStages.value = JSON.stringify(dto.stages)
  }
  return true
}

async function saveSettingsDraft(context?: PipelineSaveContext): Promise<boolean> {
  const id = projectId.value
  const payload = buildSettingsPayload()
  if (!payload) return false
  const snapshot = JSON.stringify({ build: editBuild.value, environments: editEnvs.value })
  const revision = context?.baseRevision ?? configRevisionOf(settings.value)
  const input = JSON.parse(JSON.stringify(payload)) as SaveSettingsInput
  const dto = revision ? await saveSettings(id, input, revision) : await saveSettings(id, input)
  if (id !== projectId.value) return false
  revisionCoordinator.commit(id, revision, dto)
  const committedRevision = configRevisionOf(dto)
  if (committedRevision) context?.onCommitted(committedRevision)
  if (JSON.stringify({ build: editBuild.value, environments: editEnvs.value }) === snapshot) applySettings(dto)
  else settings.value = dto
  credentials.value = await listCredentials().catch(() => credentials.value)
  return true
}

onUnmounted(() => {
  if (validationDebounce) clearTimeout(validationDebounce)
})

// ─── AI wizard (Story 2-5) ────────────────────────────────────────────────────

const aiWizardOpen = ref(false)

function handleAI(): void {
  aiWizardOpen.value = true
}

// ─── YAML import (Pipeline-as-code, FR-8-12) ──────────────────────────────────

const yamlImportOpen = ref(false)

function handleImport(): void {
  yamlImportOpen.value = true
}

// ─── Pipeline templates (FR-8-13) ─────────────────────────────────────────────

const templateModalOpen = ref(false)

function handleTemplates(): void {
  templateModalOpen.value = true
}

async function confirmStageReplacement(): Promise<boolean> {
  if (saveSubmitting.value) return false
  if (!stagesDirty.value) return true
  const replace = await confirm.open({
    title: t('projectPipeline.replaceDraftTitle'),
    body: t('projectPipeline.replaceDraftBody'),
    confirmLabel: t('projectPipeline.replaceDraftConfirm'),
    variant: 'danger',
  })
  return replace && !saveSubmitting.value
}

/** Templates persist only stages; keep unrelated settings drafts intact. */
async function handleTemplateApplied(dto?: PipelineDTO): Promise<void> {
  const id = projectId.value
  if (dto) { pipelineLoadGeneration++; applyPipeline(dto); loadState.value = 'idle' }
  else await loadPipeline()
  if (id !== projectId.value) return
  void guideRef.value?.refresh()
  if (activeTab.value !== 'canvas') setTab('canvas')
  draftSave.resetOutcome()
  scheduleValidation(400)
}

/**
 * Preview (save=false): the modal parsed + validated the YAML and returned a DTO.
 * Reflect it on the canvas WITHOUT persisting — the user reviews, then clicks 保存草稿.
 * We jump to the canvas tab so the change is visible.
 */
function handleImportPreview(dto: PipelineDTO): void {
  // Parsing YAML is not a read of the persisted editing baseline.
  pipeline.value = bindConfigRevision({ ...dto }, configRevisionOf(pipeline.value))
  editStages.value = JSON.parse(JSON.stringify(dto.stages)) as PipelineStage[]
  if (activeTab.value !== 'canvas') setTab('canvas')
}

/** The modal persisted stages only; preserve variable and environment drafts. */
async function handleImportSaved(dto?: PipelineDTO): Promise<void> {
  const id = projectId.value
  if (dto) { pipelineLoadGeneration++; applyPipeline(dto); loadState.value = 'idle' }
  else await loadPipeline()
  if (id !== projectId.value) return
  void guideRef.value?.refresh()
  if (activeTab.value !== 'canvas') setTab('canvas')
  draftSave.resetOutcome()
  scheduleValidation(400)
}

/** Reload changed blocks before they can save; unchanged drafts retain their buffers. */
async function handleAIApplied(result?: AIApplyResponse): Promise<void> {
  const id = projectId.value
  await Promise.all([
    ...(!result || result.applied.spec ? [loadPipeline()] : []),
    ...(!result || result.applied.settings ? [loadSettings()] : []),
    ...(!result || result.applied.triggers ? [triggersRef.value?.reload()] : []),
  ])
  if (id !== projectId.value) return
  void guideRef.value?.refresh()
  scheduleValidation(400)
}

// ─── Validation (Story 2-6, FR-9) ────────────────────────────────────────────

const validationOpen    = ref(false)
const validationLoading = ref(false)
const validationData    = ref<ValidationDTO | null>(null)

/** Debounce handle for auto-revalidation after tab switch / save. */
let validationDebounce: ReturnType<typeof setTimeout> | null = null
let validationRequest = 0

function clearDraftValidation(): void {
  validationData.value = null
  validationLoading.value = false
  validationRequest += 1
  if (validationDebounce) clearTimeout(validationDebounce)
  validationDebounce = null
}

watch([
  editStages, editBuild, editEnvs,
  () => triggersRef.value?.dirty,
  () => previewRef.value?.dirty,
], clearDraftValidation, { deep: true })

async function fetchValidation(): Promise<void> {
  if (!projectId.value) return
  if (guideDirty.value) {
    clearDraftValidation()
    return
  }
  const id = projectId.value
  const request = ++validationRequest
  validationLoading.value = true
  try {
    const data = await getValidation(id)
    if (request === validationRequest && id === projectId.value) validationData.value = data
  } catch {
    // Non-fatal: panel stays open but shows stale data or nothing.
    // We intentionally swallow errors here — validation is a read-only aid.
  } finally {
    if (request === validationRequest) validationLoading.value = false
  }
}

function scheduleValidation(delay = 600): void {
  if (!validationOpen.value) return
  if (validationDebounce) clearTimeout(validationDebounce)
  validationDebounce = setTimeout(() => { void fetchValidation() }, delay)
}

function handleValidateClick(): void {
  validationOpen.value = true
  void fetchValidation()
}

function handleValidationClose(): void {
  validationOpen.value = false
  if (validationDebounce) clearTimeout(validationDebounce)
}

/** Emitted by ValidationPanel when user clicks an issue row. */
function handleLocate(scope: IssueScope): void {
  setTab(scope as TabKey)
}

// Re-fetch after tab switch (debounced, only when panel is open).
watch(activeTab, () => { scheduleValidation() })

// ─── Project name display (fetch real name; fall back to ID until loaded) ─────

const project = ref<Project | null>(null)
const projectName = computed(() => project.value?.name ?? String(route.params.id))

let projectLoadGeneration = 0
async function loadProject(): Promise<void> {
  const generation = ++projectLoadGeneration
  const id = projectId.value
  const initial = projectRevisionSync.isInitialRead(id)
  try {
    const all = await listProjects()
    if (id !== projectId.value || generation !== projectLoadGeneration) return
    if (initial && !projectRevisionSync.canAcceptInitialRead()) return
    project.value = all.find((p) => p.id === id) ?? null
  } catch {
    if (id !== projectId.value || generation !== projectLoadGeneration) return
    if (initial) return
    project.value = null // 面包屑回退显示 ID,不阻断编辑器
  }
}

// ─── Pipeline-as-code (GitOps) toggle (FR-8-12) ───────────────────────────────
// 开启后运行时按运行分支读仓库根 .pipewright.yml 驱动该 run(缺失/非法回退此处库内配置)。
const pacEnabled = computed(() => project.value?.pacEnabled ?? false)
const pacToggling = ref(false)
const pacError = ref('')

// 预览仓库配置:按 ref 拉取并校验仓库 .pipewright.yml(只读;依赖它驱动运行前先看清/捕错)。
const pacPreviewOpen = ref(false)
const pacHasRepo = computed(() => !!project.value?.repoUrl?.trim())
const pacDefaultBranch = computed(() => project.value?.defaultBranch ?? '')

async function togglePac(next: boolean): Promise<void> {
  if (!project.value || pacToggling.value) return
  const id = projectId.value
  pacToggling.value = true
  pacError.value = ''
  const previousProject = project.value
  const revision = configRevisionOf(previousProject)
  project.value = bindConfigRevision({ ...project.value, pacEnabled: next }, revision) // optimistic
  try {
    const updated = revision ? await updateProject(id, { pacEnabled: next }, revision) : await updateProject(id, { pacEnabled: next })
    if (id !== projectId.value) return
    revisionCoordinator.commit(id, revision, updated)
    project.value = updated
    void guideRef.value?.refresh()
  } catch {
    if (id !== projectId.value) return
    // Another same-page commit may have advanced the project while this request failed.
    project.value = bindConfigRevision({ ...project.value!, pacEnabled: previousProject.pacEnabled }, configRevisionOf(project.value))
    pacError.value = t('projectPipeline.pacToggleFailed')
  } finally {
    pacToggling.value = false
  }
}

// ─── PR status checks toggle (Story 8-9 / FR-8-9) ─────────────────────────────
// 开启后 run 终态据项目仓库识别 GitHub/Gitee,经项目凭据回写该 commit 的提交状态(PR 检查)。
const prStatusEnabled = computed(() => project.value?.prStatusEnabled ?? false)
const prStatusToggling = ref(false)
const prStatusError = ref('')

async function togglePrStatus(next: boolean): Promise<void> {
  if (!project.value || prStatusToggling.value) return
  const id = projectId.value
  prStatusToggling.value = true
  prStatusError.value = ''
  const previousProject = project.value
  const revision = configRevisionOf(previousProject)
  project.value = bindConfigRevision({ ...project.value, prStatusEnabled: next }, revision) // optimistic
  try {
    const updated = revision ? await updateProject(id, { prStatusEnabled: next }, revision) : await updateProject(id, { prStatusEnabled: next })
    if (id !== projectId.value) return
    revisionCoordinator.commit(id, revision, updated)
    project.value = updated
  } catch {
    if (id !== projectId.value) return
    project.value = bindConfigRevision({ ...project.value!, prStatusEnabled: previousProject.prStatusEnabled }, configRevisionOf(project.value))
    prStatusError.value = t('projectPipeline.prStatusToggleFailed')
  } finally {
    prStatusToggling.value = false
  }
}
revisionCoordinator.synchronizeInitial(() => !initialTouched.value && !stagesDirty.value && !settingsDirty.value && !triggersRef.value?.dirty && !previewRef.value?.dirty && !draftSave.busy.value)
</script>

<template>
  <div class="pipeline-root" @input.capture="markInitialEdit" @change.capture="markInitialEdit" @click.capture="markInitialEdit">

    <!-- ─── Top bar ────────────────────────────────────────────────────────── -->
    <header class="pipeline-top">
      <div class="pipeline-top-left">
        <nav class="breadcrumb" :aria-label="t('projectPipeline.breadcrumbAria')">
          <router-link to="/projects" class="crumb-link">{{ t('projectPipeline.breadcrumbProjects') }}</router-link>
          <span class="crumb-sep" aria-hidden="true">/</span>
          <span class="crumb-cur">{{ projectName }}</span>
        </nav>
        <h1 class="pipeline-title">
          {{ t('projectPipeline.title') }}
          <span class="pipeline-tag">{{ pipeline?.status ?? 'draft' }}</span>
        </h1>
      </div>

      <div class="pipeline-top-actions">
        <button class="top-btn top-btn--ai" :disabled="saveSubmitting" @click="handleAI">
          <Robot class="top-btn-icon" aria-hidden="true" />
          {{ t('projectPipeline.aiGenerate') }}
        </button>

        <button class="top-btn" :disabled="saveSubmitting" @click="handleImport">
          <FileImport class="top-btn-icon" aria-hidden="true" />
          {{ t('projectPipeline.importYaml') }}
        </button>

        <button class="top-btn" :disabled="saveSubmitting" @click="handleTemplates">
          <Stack2 class="top-btn-icon" aria-hidden="true" />
          {{ t('projectPipeline.templates') }}
        </button>

        <!-- ─── Validation button + ready badge (Story 2-6) ─────────────── -->
        <button
          class="top-btn top-btn--validate"
          :class="{ 'top-btn--validate-active': validationOpen }"
          :aria-pressed="validationOpen"
          :aria-label="validationOpen ? t('projectPipeline.closeValidationPanel') : t('projectPipeline.validate')"
          @click="validationOpen ? handleValidationClose() : handleValidateClick()"
        >
          <ListCheck class="top-btn-icon" aria-hidden="true" />
          {{ t('projectPipeline.validate') }}
          <!-- inline ready badge when data is available -->
          <span
            v-if="validationData !== null && !validationLoading && !guideDirty"
            class="top-btn-badge"
            :class="validationData.ready ? 'top-btn-badge--ok' : 'top-btn-badge--err'"
            aria-hidden="true"
          >
            {{ validationData.ready ? t('projectPipeline.badgeReady') : t('projectPipeline.badgeErrors', { n: validationData.issues.filter(i => i.severity === 'error').length }) }}
          </span>
        </button>

        <button
          class="top-btn top-btn--save"
          data-onboarding-target="save"
          :disabled="saveSubmitting || !draftSave.ready.value"
          :aria-busy="saveSubmitting"
          @click="handleSave"
        >
          <span v-if="saveSubmitting" class="spinner" aria-hidden="true"/>
          <DeviceFloppy v-else class="top-btn-icon" aria-hidden="true" />
          {{ saveSubmitting ? t('projectPipeline.saving') : t('projectPipeline.saveConfiguration') }}
        </button>
      </div>
    </header>

    <PipelineGuide v-if="guideEnabled && loadState === 'idle' && pipeline" ref="guideRef"
      :project-id="projectId" :stages="editStages" :dirty="guideDirty" :context="guideContext" :active-tab="activeTab"
      @locate="locateGuide" @close="closeGuide" />

    <PipelineSaveStatus
      :state="saveState"
      :pending-scopes="pendingScopeLabels"
      :completed-scopes="completedScopeLabels"
      :failed-scopes="failedScopeLabels"
      :unavailable-scopes="unavailableScopeLabels"
      :error="saveBanner"
    />

    <div v-if="settingsLoadState === 'error'" class="pipeline-banner pipeline-banner--error" role="alert">
      <span>{{ t('projectPipeline.errLoadFailedRetry') }}</span>
      <button class="banner-retry" :disabled="saveSubmitting" @click="loadSettings">{{ t('projectPanels.triggers.retry') }}</button>
    </div>

    <!-- ─── Tab strip ──────────────────────────────────────────────────────── -->
    <nav class="tab-strip" :aria-label="t('projectPipeline.tabStripAria')" role="tablist">
      <button
        v-for="tab in TABS"
        :key="tab.key"
        :id="`tab-${tab.key}`"
        class="tab-btn"
        :class="{ 'tab-btn--active': activeTab === tab.key, 'tab-btn--dirty': tabDirty[tab.key] }"
        role="tab"
        :aria-selected="activeTab === tab.key"
        :aria-controls="`tabpanel-${tab.key}`"
        :aria-label="tabDirty[tab.key] ? t('projectPipeline.saveStatus.dirtyTab', { tab: tab.label }) : tab.label"
        @click="setTab(tab.key)"
      >
        <span>{{ tab.label }}</span>
        <span class="tab-dirty-marker" :class="{ 'tab-dirty-marker--visible': tabDirty[tab.key] }" aria-hidden="true">*</span>
      </button>
    </nav>

    <!--
      Source-of-truth bars (PaC + PR status) are pipeline-definition concerns,
      so they belong to the 编排/canvas tab only — not above every tab body.
    -->
    <template v-if="activeTab === 'canvas'">
    <!-- ─── Pipeline-as-code (GitOps) toggle (FR-8-12) ─────────────────────── -->
    <div class="pac-bar" data-onboarding-target="repository" :class="{ 'pac-bar--on': pacEnabled }">
      <div class="pac-bar-text">
        <span class="pac-bar-title">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>
          </svg>
          {{ t('projectPipeline.pacTitle') }}
          <code class="pac-bar-file">.pipewright.yml</code>
        </span>
        <span class="pac-bar-desc">
          {{ pacEnabled ? t('projectPipeline.pacOnHint') : t('projectPipeline.pacOffHint') }}
        </span>
        <span v-if="pacError" class="pac-bar-err" role="alert">{{ pacError }}</span>
      </div>
      <div class="pac-bar-actions">
        <button
          v-if="pacHasRepo"
          type="button"
          class="pac-preview-btn"
          @click="pacPreviewOpen = true"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/>
          </svg>
          {{ t('projectPipeline.pacPreviewBtn') }}
        </button>
        <button
          type="button"
          class="pac-switch"
        :class="{ 'pac-switch--on': pacEnabled }"
        role="switch"
        :aria-checked="pacEnabled"
        :aria-label="t('projectPipeline.pacTitle')"
        :disabled="pacToggling || !project"
          @click="togglePac(!pacEnabled)"
        >
          <span class="pac-switch-knob" aria-hidden="true"/>
        </button>
      </div>
    </div>

    <!-- ─── PR status checks (commit status writeback · Story 8-9 / FR-8-9) ── -->
    <div class="pac-bar" :class="{ 'pac-bar--on': prStatusEnabled }">
      <div class="pac-bar-text">
        <span class="pac-bar-title">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 12l2 2 4-4"/><circle cx="12" cy="12" r="9"/>
          </svg>
          {{ t('projectPipeline.prStatusTitle') }}
        </span>
        <span class="pac-bar-desc">
          {{ prStatusEnabled ? t('projectPipeline.prStatusOnHint') : t('projectPipeline.prStatusOffHint') }}
        </span>
        <span v-if="prStatusError" class="pac-bar-err" role="alert">{{ prStatusError }}</span>
      </div>
      <button
        type="button"
        class="pac-switch"
        :class="{ 'pac-switch--on': prStatusEnabled }"
        role="switch"
        :aria-checked="prStatusEnabled"
        :aria-label="t('projectPipeline.prStatusTitle')"
        :disabled="prStatusToggling || !project"
        @click="togglePrStatus(!prStatusEnabled)"
      >
        <span class="pac-switch-knob" aria-hidden="true"/>
      </button>
    </div>
    </template>

    <!-- ─── Tab body: panels + optional validation side-drawer ─────────────── -->
    <!--
      The tab-body is a horizontal flex row.
      Tab panels fill the left area (flex: 1).
      The ValidationPanel occupies a fixed-width right column (360px) when open,
      visible regardless of which tab is active, enabling cross-tab locate jumps.
    -->
    <div class="tab-body">

      <!-- ─── Tab panels (left column) ─────────────────────────────────────── -->
      <div class="tab-panels" @input.capture="clearDraftValidation" @change.capture="clearDraftValidation">

        <!-- 流水线编排 -->
        <div
          v-show="activeTab === 'canvas'"
          id="tabpanel-canvas"
          class="tab-panel tab-panel--canvas"
          role="tabpanel"
          aria-labelledby="tab-canvas"
        >
          <!-- Load error -->
          <div v-if="loadState === 'error'" class="pipeline-banner pipeline-banner--error" role="alert">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
            </svg>
            <span>{{ loadError }}</span>
            <button class="banner-retry" @click="loadPipeline">↻ {{ t('projectPipeline.retry') }}</button>
          </div>

          <!-- Loading skeleton -->
          <div v-else-if="loadState === 'loading'" class="canvas-skeleton" aria-busy="true" :aria-label="t('projectPipeline.loading')">
            <div class="skel-flow">
              <div v-for="i in 4" :key="i" class="skel-stage" aria-hidden="true">
                <div class="skel skel--stage-head"/>
                <div class="skel skel--card"/>
                <div class="skel skel--card" style="opacity:.6"/>
              </div>
            </div>
          </div>

          <!-- Canvas -->
          <PipelineCanvas
            v-else-if="loadState === 'idle' && pipeline"
            ref="canvasRef"
            :guide-enabled="guideEnabled"
            :project-name="project?.name"
            :project="project ?? undefined"
            :environment-auto-push-possible="environmentAutoPushPossible"
            :stages="editStages"
            :yaml="pipeline.yaml"
            :credentials="credentials"
            :servers="servers"
            :channels="channels"
            @update="handleCanvasUpdate"
            @guide-context="guideContext = $event"
          />

          <!-- AI 脚本风险标注(护城河):对脚本步骤命令做风险体检,提交前先发现高危/泄漏/不可复现项 -->
          <RiskAnnotationPanel
            v-if="loadState === 'idle' && pipeline"
            :project-id="projectId"
          />
        </div>

        <!-- 变量与缓存 -->
        <div
          v-show="activeTab === 'vars'"
          id="tabpanel-vars"
          class="tab-panel"
          role="tabpanel"
          aria-labelledby="tab-vars"
        >
          <VarsCacheTab
            v-if="editBuild"
            :build="editBuild"
            :credentials="credentials"
            :disabled="saveSubmitting || settingsLoadState !== 'idle'"
            @update="handleBuildUpdate"
          />
        </div>

        <!-- 触发设置 -->
        <div
          v-show="activeTab === 'triggers'"
          id="tabpanel-triggers"
          class="tab-panel"
          role="tabpanel"
          aria-labelledby="tab-triggers"
        >
          <div class="triggers-stack">
            <TriggersPanel ref="triggersRef" :project-id="projectId" :disabled="saveSubmitting" />
            <!-- R4 / E4.1: 每项目 PR 预览环境配置 -->
            <ProjectPreviewConfig ref="previewRef" :project-id="projectId" :disabled="saveSubmitting" />
          </div>
        </div>

        <!-- 环境与凭据 -->
        <div
          v-show="activeTab === 'envs'"
          id="tabpanel-envs"
          class="tab-panel"
          role="tabpanel"
          aria-labelledby="tab-envs"
        >
          <EnvCredsTab
            :key="projectId"
            :environments="editEnvs"
            :credentials="credentials"
            :servers="servers"
            :disabled="saveSubmitting || settingsLoadState !== 'idle'"
            @update="handleEnvsUpdate"
          />
        </div>

      </div><!-- /tab-panels -->

      <!-- ─── Validation side-drawer (Story 2-6) ────────────────────────────── -->
      <!--
        Persists across tab switches so the user can see all cross-tab issues
        while editing. The 'locate' event triggers setTab() to jump to the
        relevant tab without closing the panel.
      -->
      <ValidationPanel
        v-if="validationOpen"
        :loading="validationLoading"
        :pending-message="guideDirty ? t('pipelinePanels.vpSaveFirst') : (!validationData ? t('pipelinePanels.vpUnavailable') : undefined)"
        :ready="validationData?.ready ?? false"
        :issues="validationData?.issues ?? []"
        @locate="handleLocate"
        @close="handleValidationClose"
      />

    </div><!-- /tab-body -->

    <!-- ─── AI Generate Wizard (Story 2-5) ──────────────────────────────── -->
    <AIGenerateWizard
      v-if="aiWizardOpen"
      :project-id="projectId"
      :base-revision="configRevisionOf(pipeline)"
      @close="aiWizardOpen = false"
      @applied="handleAIApplied"
    />

    <!-- ─── YAML Import (Pipeline-as-code, FR-8-12) ─────────────────────── -->
    <YamlImportModal
      v-if="yamlImportOpen"
      :project-id="projectId"
      :base-revision="configRevisionOf(pipeline)"
      :before-replace="confirmStageReplacement"
      @close="yamlImportOpen = false"
      @preview="handleImportPreview"
      @saved="handleImportSaved"
    />

    <!-- ─── Pipeline templates (FR-8-13) ────────────────────────────────── -->
    <TemplatePickerModal
      v-if="templateModalOpen"
      :project-id="projectId"
      :base-revision="configRevisionOf(pipeline)"
      :stages="editStages"
      :before-replace="confirmStageReplacement"
      @close="templateModalOpen = false"
      @applied="handleTemplateApplied"
    />

    <!-- ─── Preview repo .pipewright.yml at a ref (GitOps Slice 3) ───────── -->
    <PacPreviewModal
      v-if="pacPreviewOpen"
      :project-id="projectId"
      :default-branch="pacDefaultBranch"
      @close="pacPreviewOpen = false"
    />

  </div>
</template>

<style scoped>
/* ─── Root: full-height flex column ──────────────────────────────────────── */
.pipeline-root {
  display: flex;
  flex-direction: column;
  /* Keep inner scrolling bounded; short screens scroll the page to preserve usable drawer height. */
  height: calc(100vh - var(--main-pad-top) - 38px - 20px);
  margin-bottom: calc(20px - var(--main-pad-bottom));
  min-height: 850px;
  gap: 0;
}
.pipeline-root :deep(.onboarding-target) { outline: 2px solid var(--color-primary); outline-offset: 3px; box-shadow: 0 0 0 5px var(--color-primary-soft); }
.pipeline-root:has(.pipeline-guide) { min-height: 850px; }
@media (max-width: 900px) {
  .pipeline-root, .pipeline-root:has(.pipeline-guide) { height: auto; min-height: 0; margin-bottom: 0; }
  .pipeline-root .pipeline-top { flex-wrap: wrap; padding: 12px; }
  .pipeline-root .pipeline-top-left { flex-basis: 100%; }
  .pipeline-root .pipeline-top-actions { flex: 1 1 100%; min-width: 0; max-width: 100%; flex-wrap: wrap; }
  .pipeline-root .tab-body { flex: none; flex-direction: column; min-height: 400px; overflow: visible; }
  .pipeline-root .tab-panels { overflow: visible; }
  .pipeline-root .tab-panel { height: auto; min-height: 400px; overflow: visible; }
  .pipeline-root :deep(.vp) { width: 100%; border-left: 0; border-top: 1px solid var(--color-border); max-height: 420px; }
  .pipeline-root .tab-strip { overflow-x: auto; flex: none; }
  .pipeline-root .tab-btn { flex: none; white-space: nowrap; }
  .pipeline-root .pac-bar { flex-wrap: wrap; padding: 12px; }
  .pipeline-root .pac-bar-text { flex-basis: 100%; min-width: 0; }
  .pipeline-root .pac-bar-title { flex-wrap: wrap; }
}

/* ─── Top bar ────────────────────────────────────────────────────────────── */
.pipeline-top {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  padding: 14px 28px 12px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.pipeline-top-left {
  flex: 1 1 180px;
  min-width: 0;
}

.breadcrumb {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 0.76rem;
  color: var(--color-faint);
  margin-bottom: 3px;
}

.crumb-link {
  color: var(--color-dim);
  text-decoration: none;
  transition: color var(--duration-fast);
}

.crumb-link:hover { color: var(--color-primary); }
.crumb-link:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; border-radius: 2px; }
.crumb-sep { color: var(--color-faint); }
.crumb-cur { color: var(--color-text); font-weight: 500; overflow-wrap: anywhere; }

.pipeline-title {
  font-size: 1.06rem;
  font-weight: 700;
  letter-spacing: 0;
  color: var(--color-text);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.pipeline-tag {
  font-size: 0.68rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: 100px;
  padding: 2px 9px;
  font-weight: 500;
  letter-spacing: 0;
  text-transform: lowercase;
}

.pipeline-top-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  flex: 1 1 520px;
  min-width: 0;
}

.top-btn {
  min-height: 34px;
  max-width: 100%;
  font-size: 0.83rem;
  font-weight: 500;
  border: 1px solid var(--color-border-strong);
  background: var(--color-card-2);
  color: var(--color-text);
  border-radius: var(--rounded);
  padding: 0 14px;
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast), transform var(--duration-fast);
  display: inline-flex;
  align-items: center;
  gap: 7px;
  overflow-wrap: anywhere;
  text-align: start;
  font-family: var(--font-sans);
}

.top-btn-icon { width: 14px; height: 14px; flex: none; }

.top-btn:hover:not(:disabled) {
  border-color: var(--color-faint);
}

.top-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.top-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.top-btn--ai {
  color: var(--color-cyan);
  border-color: var(--color-cyan-line);
}

.top-btn--save {
  background: var(--color-primary);
  color: #fff;
  border-color: transparent;
  font-weight: 600;
  box-shadow: 0 5px 16px var(--color-primary-soft);
}

.top-btn--save:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}

.top-btn--save:disabled {
  box-shadow: none;
  transform: none;
}

/* ─── Validate button (Story 2-6) ────────────────────────────────────────── */
.top-btn--validate {
  color: var(--color-green);
  border-color: var(--color-green-line);
  background: var(--color-green-soft);
}

.top-btn--validate:hover:not(:disabled) {
  border-color: var(--color-green);
}

.top-btn--validate-active {
  background: var(--color-primary-soft);
  color: var(--color-primary);
  border-color: var(--color-primary);
}

/* ─── Inline ready badge inside validate button ───────────────────────────── */
.top-btn-badge {
  font-size: 0.68rem;
  font-weight: 700;
  padding: 2px 7px;
  border-radius: 100px;
  border: 1px solid transparent;
  line-height: 1;
}

.top-btn-badge--ok {
  background: var(--color-green-soft);
  color: var(--color-green);
  border-color: var(--color-green-line);
}

.top-btn-badge--err {
  background: var(--color-red-soft);
  color: var(--color-red);
  border-color: var(--color-red-line);
}

/* ─── Tab body: panels row ───────────────────────────────────────────────── */
/*
 * Wraps all tab panels + the optional ValidationPanel side-drawer.
 * Flex row: panels fill flex-1, drawer is fixed 360px on the right.
 */
.tab-body {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.tab-panels {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
}

/* ─── Tab strip ──────────────────────────────────────────────────────────── */
.tab-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 0 28px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.tab-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 0.84rem;
  color: var(--color-faint);
  padding: 12px 14px;
  cursor: pointer;
  border: none;
  background: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  font-family: var(--font-sans);
  transition: color var(--duration-fast), border-color var(--duration-fast);
}

.tab-dirty-marker { width: 8px; flex: none; visibility: hidden; color: var(--color-amber); font-weight: 700; }
.tab-dirty-marker--visible { visibility: visible; }

@media (max-width: 600px) {
  .pipeline-top { padding: 12px; }
  .pipeline-top-left { flex-basis: 100%; }
  .pipeline-top-actions { justify-content: flex-start; flex-basis: 100%; }
  .top-btn { min-height: 44px; padding: 6px 10px; }
  .tab-strip { padding: 0 12px; }
  .tab-btn { flex: 1 1 calc(50% - 4px); padding: 10px 6px; }
}

.tab-btn:hover { color: var(--color-dim); }
.tab-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: -2px; }

.tab-btn--active {
  color: var(--color-text);
  font-weight: 500;
  border-bottom-color: var(--color-primary);
}

/* ─── Pipeline-as-code (GitOps) toggle bar ───────────────────────────────── */
.pac-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 28px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface-2, var(--color-surface));
  flex: none;
}
.pac-bar--on { background: color-mix(in oklab, var(--color-primary) 8%, transparent); }
.pac-bar-text { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.pac-bar-title {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 0.86rem;
  font-weight: 500;
  color: var(--color-text);
}
.pac-bar-title svg { color: var(--color-primary); flex: none; }
.pac-bar-file {
  font-family: var(--font-mono, monospace);
  font-size: 0.76rem;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--color-bg-soft, rgba(127,127,127,0.12));
  color: var(--color-dim);
}
.pac-bar-desc { font-size: 0.78rem; color: var(--color-faint); }
.pac-bar-err { font-size: 0.78rem; color: var(--color-danger, #e5484d); }

.pac-bar-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
}

.pac-preview-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 12px;
  font-size: 0.8rem;
  font-weight: 500;
  font-family: var(--font-sans);
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.pac-preview-btn:hover { border-color: var(--color-faint); }
.pac-preview-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.pac-preview-btn svg { flex: none; color: var(--color-faint); }

.pac-switch {
  position: relative;
  flex: none;
  width: 40px;
  height: 22px;
  border-radius: 999px;
  border: none;
  cursor: pointer;
  background: var(--color-border);
  transition: background var(--duration-fast);
}
.pac-switch--on { background: var(--color-primary); }
.pac-switch:disabled { opacity: 0.55; cursor: default; }
.pac-switch:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.pac-switch-knob {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--duration-fast);
}
.pac-switch--on .pac-switch-knob { transform: translateX(18px); }

/* ─── Tab panels ─────────────────────────────────────────────────────────── */
/*
 * Each tab panel fills the .tab-panels column.
 * v-show toggles display; when visible, panel should fill all available height.
 */
.tab-panel {
  /* When shown, fill parent column height */
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

/* Canvas panel: own flex layout for canvas + drawer */
.tab-panel--canvas {
  display: flex;
  flex-direction: column;
}

/* Non-canvas panels: scrollable with standard padding */
.tab-panel:not(.tab-panel--canvas) {
  overflow-y: auto;
  padding: var(--main-pad-top) var(--main-pad) var(--main-pad-bottom);
}

/* 触发设置 tab:触发器面板 + PR 预览环境配置卡纵向堆叠 */
.triggers-stack {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

/* ─── Banners ────────────────────────────────────────────────────────────── */
.pipeline-banner {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 10px 28px;
  font-size: 0.83rem;
  line-height: 1.5;
  flex: none;
  border-bottom: 1px solid transparent;
}

.pipeline-banner--error {
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
  color: var(--color-red);
}

.pipeline-banner--success {
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
  color: var(--color-green);
}

.pipeline-banner--info {
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
  color: var(--color-cyan);
}

.banner-dismiss {
  margin-left: auto;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  border: none;
  background: none;
  cursor: pointer;
  color: inherit;
  border-radius: 4px;
  display: grid;
  place-items: center;
  opacity: 0.65;
  transition: opacity var(--duration-fast);
}

.banner-dismiss:hover { opacity: 1; }

.banner-retry {
  margin-left: auto;
  flex-shrink: 0;
  background: none;
  border: none;
  color: var(--color-red);
  font-size: 0.83rem;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
  text-decoration: underline;
  text-underline-offset: 2px;
}

/* ─── Canvas skeleton ────────────────────────────────────────────────────── */
.canvas-skeleton {
  flex: 1;
  overflow: auto;
  background-color: var(--color-canvas);
  padding: 34px 30px;
}

.skel-flow {
  display: flex;
  gap: 74px;
  min-width: max-content;
}

.skel-stage {
  width: 248px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skel {
  display: block;
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.06) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: shimmer 1.4s ease-in-out infinite;
}

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .skel { animation: none; background: var(--color-inset); }
}

.skel--stage-head { height: 18px; width: 80%; }
.skel--card       { height: 62px; width: 100%; border-radius: 12px; }

/* ─── Placeholder tabs ───────────────────────────────────────────────────── */
.placeholder-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 72px 32px;
  text-align: center;
  background: var(--color-card);
  border: 1.5px dashed var(--color-border-strong);
  border-radius: var(--rounded-card);
  max-width: 500px;
  margin: 0 auto;
}

.placeholder-icon {
  width: 56px;
  height: 56px;
  border-radius: var(--rounded-xl);
  background: var(--color-inset);
  border: 1.5px dashed var(--color-border-strong);
  display: grid;
  place-items: center;
  color: var(--color-dim);
  margin-bottom: 4px;
}

.placeholder-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
}

.placeholder-desc {
  font-size: 0.82rem;
  color: var(--color-faint);
  max-width: 44ch;
  line-height: 1.6;
}

.placeholder-badge {
  display: inline-flex;
  align-items: center;
  font-size: 0.73rem;
  font-weight: 600;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: 100px;
  padding: 3px 11px;
  margin-top: 4px;
}

/* ─── Spinner ────────────────────────────────────────────────────────────── */
.spinner {
  display: inline-block;
  width: 13px;
  height: 13px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}

@keyframes spin { to { transform: rotate(360deg); } }

@media (prefers-reduced-motion: reduce) {
  .spinner { animation: none; border-top-color: currentColor; }
}
</style>

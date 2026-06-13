<script setup lang="ts">
/**
 * ProjectPipeline — 4-tab pipeline configuration editor.
 * Tabs: 流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据
 * URL state: ?tab=canvas|vars|triggers|envs  (shareable)
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
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
import PipelineCanvas from '../components/pipeline/PipelineCanvas.vue'
import VarsCacheTab from '../components/pipeline/VarsCacheTab.vue'
import EnvCredsTab from '../components/pipeline/EnvCredsTab.vue'
import TriggersPanel from '../components/TriggersPanel.vue'
import ValidationPanel from '../components/pipeline/ValidationPanel.vue'
import AIGenerateWizard from '../components/pipeline/AIGenerateWizard.vue'
import RiskAnnotationPanel from '../components/pipeline/RiskAnnotationPanel.vue'
import YamlImportModal from '../components/pipeline/YamlImportModal.vue'
import TemplatePickerModal from '../components/pipeline/TemplatePickerModal.vue'
import PacPreviewModal from '../components/pipeline/PacPreviewModal.vue'

// ─── Route ────────────────────────────────────────────────────────────────────

const { t } = useI18n()

const route  = useRoute()
const router = useRouter()

const projectId = computed(() => route.params.id as string)

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

function applyPipeline(dto: PipelineDTO): void {
  pipeline.value = dto
  editStages.value = JSON.parse(JSON.stringify(dto.stages)) as PipelineStage[]
}

async function loadPipeline(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const dto = await getPipeline(projectId.value)
    applyPipeline(dto)
    loadState.value = 'idle'
  } catch (err) {
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

function applySettings(dto: SettingsDTO): void {
  settings.value = dto
  editBuild.value = JSON.parse(JSON.stringify(dto.build)) as BuildConfig
  editEnvs.value  = JSON.parse(JSON.stringify(dto.environments)) as Environment[]
}

async function loadSettings(): Promise<void> {
  try {
    const [dto, creds, srvs, chs] = await Promise.all([
      getSettings(projectId.value),
      listCredentials().catch(() => [] as Credential[]),
      listServers().catch(() => [] as Server[]),
      listChannels().catch(() => [] as NotificationChannel[]),
    ])
    applySettings(dto)
    credentials.value = creds
    servers.value = srvs
    channels.value = chs
  } catch {
    // Settings load failure is non-fatal for the canvas; the vars/envs tabs show
    // their own empty state until a successful save. Surfaced on save attempts.
  }
}

function handleBuildUpdate(build: BuildConfig): void {
  editBuild.value = build
}

function handleEnvsUpdate(envs: Environment[]): void {
  editEnvs.value = envs
}

watch(projectId, () => { void loadProject(); void loadPipeline(); void loadSettings() })
onMounted(() => { void loadProject(); void loadPipeline(); void loadSettings() })

// ─── Canvas update ────────────────────────────────────────────────────────────

function handleCanvasUpdate(stages: PipelineStage[]): void {
  editStages.value = stages
}

// ─── Save pipeline ────────────────────────────────────────────────────────────

const saveSubmitting = ref(false)
const saveBanner     = ref('')
const saveSuccess    = ref(false)
let saveSuccessTimer: ReturnType<typeof setTimeout> | null = null

function showSaveSuccess(): void {
  saveSuccess.value = true
  if (saveSuccessTimer) clearTimeout(saveSuccessTimer)
  saveSuccessTimer = setTimeout(() => { saveSuccess.value = false }, 3200)
}

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
  saveSubmitting.value = true
  saveBanner.value     = ''
  saveSuccess.value    = false

  try {
    // The vars/envs tabs persist build/deploy settings; canvas/triggers persist the spec.
    if (activeTab.value === 'vars' || activeTab.value === 'envs') {
      const payload = buildSettingsPayload()
      if (payload) {
        const dto = await saveSettings(projectId.value, payload)
        applySettings(dto)
        // Refresh credential masks in case a referenced credential changed.
        credentials.value = await listCredentials().catch(() => credentials.value)
      }
    } else {
      const dto = await savePipeline(projectId.value, { stages: editStages.value })
      applyPipeline(dto)
    }
    showSaveSuccess()
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
  } finally {
    saveSubmitting.value = false
  }
}

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

/** Apply-template succeeded: server already persisted; reload pipeline + settings + revalidate. */
async function handleTemplateApplied(): Promise<void> {
  await Promise.all([loadPipeline(), loadSettings()])
  if (activeTab.value !== 'canvas') setTab('canvas')
  showSaveSuccess()
  scheduleValidation(400)
}

/**
 * Preview (save=false): the modal parsed + validated the YAML and returned a DTO.
 * Reflect it on the canvas WITHOUT persisting — the user reviews, then clicks 保存草稿.
 * We jump to the canvas tab so the change is visible.
 */
function handleImportPreview(dto: PipelineDTO): void {
  pipeline.value = dto
  editStages.value = JSON.parse(JSON.stringify(dto.stages)) as PipelineStage[]
  if (activeTab.value !== 'canvas') setTab('canvas')
}

/** Save (save=true): the modal already persisted. Reload everything + revalidate. */
async function handleImportSaved(): Promise<void> {
  await Promise.all([loadPipeline(), loadSettings()])
  if (activeTab.value !== 'canvas') setTab('canvas')
  showSaveSuccess()
  scheduleValidation(400)
}

/** Called by AIGenerateWizard after a successful apply. Reload all data. */
async function handleAIApplied(): Promise<void> {
  await Promise.all([loadPipeline(), loadSettings()])
  // Trigger TriggersPanel to refresh — it is self-loading, close+reopen is the
  // simplest approach; alternatively emit a key-change or use a triggerKey ref.
  scheduleValidation(400)
}

// ─── Validation (Story 2-6, FR-9) ────────────────────────────────────────────

const validationOpen    = ref(false)
const validationLoading = ref(false)
const validationData    = ref<ValidationDTO | null>(null)

/** Debounce handle for auto-revalidation after tab switch / save. */
let validationDebounce: ReturnType<typeof setTimeout> | null = null

async function fetchValidation(): Promise<void> {
  if (!projectId.value) return
  validationLoading.value = true
  try {
    validationData.value = await getValidation(projectId.value)
  } catch {
    // Non-fatal: panel stays open but shows stale data or nothing.
    // We intentionally swallow errors here — validation is a read-only aid.
  } finally {
    validationLoading.value = false
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

async function loadProject(): Promise<void> {
  try {
    const all = await listProjects()
    project.value = all.find((p) => p.id === projectId.value) ?? null
  } catch {
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
  pacToggling.value = true
  pacError.value = ''
  const prev = project.value.pacEnabled
  project.value = { ...project.value, pacEnabled: next } // optimistic
  try {
    const updated = await updateProject(projectId.value, { pacEnabled: next })
    project.value = updated
  } catch {
    project.value = { ...project.value, pacEnabled: prev } // rollback
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
  prStatusToggling.value = true
  prStatusError.value = ''
  const prev = project.value.prStatusEnabled
  project.value = { ...project.value, prStatusEnabled: next } // optimistic
  try {
    const updated = await updateProject(projectId.value, { prStatusEnabled: next })
    project.value = updated
  } catch {
    project.value = { ...project.value, prStatusEnabled: prev } // rollback
    prStatusError.value = t('projectPipeline.prStatusToggleFailed')
  } finally {
    prStatusToggling.value = false
  }
}
</script>

<template>
  <div class="pipeline-root">

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
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
          </svg>
          {{ t('projectPipeline.aiGenerate') }}
        </button>

        <button class="top-btn" :disabled="saveSubmitting" @click="handleImport">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M7 10l5 5 5-5"/><path d="M12 15V3"/>
          </svg>
          {{ t('projectPipeline.importYaml') }}
        </button>

        <button class="top-btn" :disabled="saveSubmitting" @click="handleTemplates">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M12 3l9 4.5-9 4.5-9-4.5z"/><path d="M3 12l9 4.5 9-4.5"/><path d="M3 16.5l9 4.5 9-4.5"/>
          </svg>
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
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
          </svg>
          {{ t('projectPipeline.validate') }}
          <!-- inline ready badge when data is available -->
          <span
            v-if="validationData !== null && !validationLoading"
            class="top-btn-badge"
            :class="validationData.ready ? 'top-btn-badge--ok' : 'top-btn-badge--err'"
            aria-hidden="true"
          >
            {{ validationData.ready ? t('projectPipeline.badgeReady') : t('projectPipeline.badgeErrors', { n: validationData.issues.filter(i => i.severity === 'error').length }) }}
          </span>
        </button>

        <button
          class="top-btn top-btn--save"
          :disabled="saveSubmitting || loadState !== 'idle'"
          :aria-busy="saveSubmitting"
          @click="handleSave"
        >
          <span v-if="saveSubmitting" class="spinner" aria-hidden="true"/>
          {{ saveSubmitting ? t('projectPipeline.saving') : t('projectPipeline.saveDraft') }}
        </button>
      </div>
    </header>

    <!-- ─── Banners (save success / error) ────────────────────────────────── -->
    <div
      v-if="saveBanner"
      class="pipeline-banner pipeline-banner--error"
      role="alert"
      aria-live="assertive"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ saveBanner }}</span>
      <button class="banner-dismiss" :aria-label="t('projectPipeline.dismiss')" @click="saveBanner = ''">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
      </button>
    </div>

    <div v-if="saveSuccess" class="pipeline-banner pipeline-banner--success" role="status" aria-live="polite">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
        <path d="M20 6 9 17l-5-5"/>
      </svg>
      <span>{{ t('projectPipeline.draftSaved') }}</span>
    </div>

    <!-- ─── Tab strip ──────────────────────────────────────────────────────── -->
    <nav class="tab-strip" :aria-label="t('projectPipeline.tabStripAria')" role="tablist">
      <button
        v-for="tab in TABS"
        :key="tab.key"
        class="tab-btn"
        :class="{ 'tab-btn--active': activeTab === tab.key }"
        role="tab"
        :aria-selected="activeTab === tab.key"
        :aria-controls="`tabpanel-${tab.key}`"
        @click="setTab(tab.key)"
      >{{ tab.label }}</button>
    </nav>

    <!-- ─── Pipeline-as-code (GitOps) toggle (FR-8-12) ─────────────────────── -->
    <div class="pac-bar" :class="{ 'pac-bar--on': pacEnabled }">
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

    <!-- ─── Tab body: panels + optional validation side-drawer ─────────────── -->
    <!--
      The tab-body is a horizontal flex row.
      Tab panels fill the left area (flex: 1).
      The ValidationPanel occupies a fixed-width right column (360px) when open,
      visible regardless of which tab is active, enabling cross-tab locate jumps.
    -->
    <div class="tab-body">

      <!-- ─── Tab panels (left column) ─────────────────────────────────────── -->
      <div class="tab-panels">

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
            :stages="editStages"
            :yaml="pipeline.yaml"
            :credentials="credentials"
            :servers="servers"
            :channels="channels"
            @update="handleCanvasUpdate"
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
            :disabled="saveSubmitting"
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
          <TriggersPanel :project-id="projectId" />
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
            :environments="editEnvs"
            :credentials="credentials"
            :disabled="saveSubmitting"
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
      @close="aiWizardOpen = false"
      @applied="handleAIApplied"
    />

    <!-- ─── YAML Import (Pipeline-as-code, FR-8-12) ─────────────────────── -->
    <YamlImportModal
      v-if="yamlImportOpen"
      :project-id="projectId"
      @close="yamlImportOpen = false"
      @preview="handleImportPreview"
      @saved="handleImportSaved"
    />

    <!-- ─── Pipeline templates (FR-8-13) ────────────────────────────────── -->
    <TemplatePickerModal
      v-if="templateModalOpen"
      :project-id="projectId"
      :stages="editStages"
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
  /*
   * 流水线编辑器是「定高应用面板」(画布横向内滚 + 右侧检视抽屉纵向内滚 + 非画布 tab
   * 内滚),需要一个相对视口确定的高度。父级 .main-inner 是 min-height:100vh(随内容
   * 增高,height:100% 无法解析成确定值),会导致抽屉撑满内容、被 overflow:hidden 链裁掉
   * 底部(后置步骤够不到)。这里按视口减去 .main-inner 的上下内边距锁定高度,使内部
   * overflow:auto/hidden 的滚动真正生效。
   *
   * 底部只留一小段呼吸位(而非整段 --main-pad-bottom):编辑器是全幅工作面板,把原本
   * ~90px 的底部留白还给画布,缓解「画布高度太窄」。
   *
   * ⚠ 两处必须对齐父内边距,否则编辑器比视口高、整页滚动、底部风险面板划不到/被截:
   *  ① 顶部减 .main-inner 的**完整** padding-top = calc(--main-pad-top + 38px)(见 AppShell);
   *     原来只减了 --main-pad-top,漏掉 38px。
   *  ② 用负 margin-bottom 收掉 .main-inner 的 padding-bottom(--main-pad-bottom,默认 90px,
   *     是给普通页准备的),只留 20px 呼吸位 —— 否则那 90px 叠在定高面板下方又把文档顶出视口。
   */
  height: calc(100vh - var(--main-pad-top) - 38px - 20px);
  margin-bottom: calc(20px - var(--main-pad-bottom));
  min-height: 0;
  gap: 0;
}

/* ─── Top bar ────────────────────────────────────────────────────────────── */
.pipeline-top {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 28px 12px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.pipeline-top-left {
  flex: 1;
  min-width: 0;
}

.breadcrumb {
  display: flex;
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
.crumb-cur { color: var(--color-text); font-weight: 500; }

.pipeline-title {
  font-size: 1.06rem;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
  display: flex;
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
  align-items: center;
  gap: 8px;
  flex: none;
}

.top-btn {
  height: 34px;
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
  white-space: nowrap;
  font-family: var(--font-sans);
}

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
  gap: 4px;
  padding: 0 28px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.tab-btn {
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

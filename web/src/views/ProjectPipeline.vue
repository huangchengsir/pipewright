<script setup lang="ts">
/**
 * ProjectPipeline — 4-tab pipeline configuration editor.
 * Tabs: 流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据
 * URL state: ?tab=canvas|vars|triggers|envs  (shareable)
 */
import { ref, computed, onMounted, watch } from 'vue'
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
import { listServers, type Server } from '../api/servers'
import { getValidation, type ValidationDTO, type IssueScope } from '../api/pipelineValidation'
import { HttpError } from '../api/http'
import PipelineCanvas from '../components/pipeline/PipelineCanvas.vue'
import VarsCacheTab from '../components/pipeline/VarsCacheTab.vue'
import EnvCredsTab from '../components/pipeline/EnvCredsTab.vue'
import TriggersPanel from '../components/TriggersPanel.vue'
import ValidationPanel from '../components/pipeline/ValidationPanel.vue'
import AIGenerateWizard from '../components/pipeline/AIGenerateWizard.vue'
import YamlImportModal from '../components/pipeline/YamlImportModal.vue'

// ─── Route ────────────────────────────────────────────────────────────────────

const route  = useRoute()
const router = useRouter()

const projectId = computed(() => route.params.id as string)

// ─── Tabs ─────────────────────────────────────────────────────────────────────

type TabKey = 'canvas' | 'vars' | 'triggers' | 'envs'

const TABS: Array<{ key: TabKey; label: string }> = [
  { key: 'canvas',   label: '流水线编排' },
  { key: 'vars',     label: '变量与缓存' },
  { key: 'triggers', label: '触发设置' },
  { key: 'envs',     label: '环境与凭据' },
]

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
        loadError.value = '无法连接到服务器,请检查后端是否运行后重试。'
      } else if (err.status === 404) {
        loadError.value = '项目不存在,请确认项目 ID 正确。'
      } else {
        loadError.value = err.apiError?.message ?? `加载流水线失败(${err.status})`
      }
    } else {
      loadError.value = '加载流水线失败,请稍后重试。'
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

function applySettings(dto: SettingsDTO): void {
  settings.value = dto
  editBuild.value = JSON.parse(JSON.stringify(dto.build)) as BuildConfig
  editEnvs.value  = JSON.parse(JSON.stringify(dto.environments)) as Environment[]
}

async function loadSettings(): Promise<void> {
  try {
    const [dto, creds, srvs] = await Promise.all([
      getSettings(projectId.value),
      listCredentials().catch(() => [] as Credential[]),
      listServers().catch(() => [] as Server[]),
    ])
    applySettings(dto)
    credentials.value = creds
    servers.value = srvs
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

watch(projectId, () => { void loadPipeline(); void loadSettings() })
onMounted(() => { void loadPipeline(); void loadSettings() })

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
    case 'invalid_stage':        return '阶段名不能为空或 kind 不在允许值内,请检查后重试。'
    case 'invalid_job':          return '任务名称或类型不能为空,请补充后重试。'
    case 'duplicate_id':         return '阶段或任务 ID 重复,请删除重复项后重试。'
    case 'invalid_build':        return '构建模型须为 dockerfile/toolchain,产物类型须为 image/jar/dist。'
    case 'invalid_var':          return '变量键不能为空且同作用域内不可重复;secret 变量须选择保险库凭据。'
    case 'invalid_environment':  return '环境名不能为空,镜像仓库类型须为 harbor/acr/dockerhub/custom。'
    case 'credential_not_found': return '引用的保险库凭据不存在,请重新选择后重试。'
    case 'vault_unconfigured':   return '保险库未配置 master key,无法引用 secret 凭据。'
    default:                     return err.apiError?.message ?? `保存失败(${err.status})`
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
        ? '无法连接到服务器,请检查后端是否运行后重试。'
        : mapSaveError(err)
    } else {
      saveBanner.value = '保存失败,请稍后重试。'
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

// ─── Project name display (from route state or fallback to ID) ────────────────

const projectName = computed(() => String(route.params.id))
</script>

<template>
  <div class="pipeline-root">

    <!-- ─── Top bar ────────────────────────────────────────────────────────── -->
    <header class="pipeline-top">
      <div class="pipeline-top-left">
        <nav class="breadcrumb" aria-label="面包屑导航">
          <router-link to="/projects" class="crumb-link">项目</router-link>
          <span class="crumb-sep" aria-hidden="true">/</span>
          <span class="crumb-cur">{{ projectName }}</span>
        </nav>
        <h1 class="pipeline-title">
          流水线配置
          <span class="pipeline-tag">{{ pipeline?.status ?? 'draft' }}</span>
        </h1>
      </div>

      <div class="pipeline-top-actions">
        <button class="top-btn top-btn--ai" :disabled="saveSubmitting" @click="handleAI">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 12h3.5l2.2-6 3.6 12 2.4-7 1.3 1h4.5"/>
          </svg>
          AI 生成流水线
        </button>

        <button class="top-btn" :disabled="saveSubmitting" @click="handleImport">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M7 10l5 5 5-5"/><path d="M12 15V3"/>
          </svg>
          从 YAML 导入
        </button>

        <!-- ─── Validation button + ready badge (Story 2-6) ─────────────── -->
        <button
          class="top-btn top-btn--validate"
          :class="{ 'top-btn--validate-active': validationOpen }"
          :aria-pressed="validationOpen"
          :aria-label="validationOpen ? '关闭校验面板' : '校验配置'"
          @click="validationOpen ? handleValidationClose() : handleValidateClick()"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>
          </svg>
          校验配置
          <!-- inline ready badge when data is available -->
          <span
            v-if="validationData !== null && !validationLoading"
            class="top-btn-badge"
            :class="validationData.ready ? 'top-btn-badge--ok' : 'top-btn-badge--err'"
            aria-hidden="true"
          >
            {{ validationData.ready ? '就绪' : validationData.issues.filter(i => i.severity === 'error').length + ' 错误' }}
          </span>
        </button>

        <button
          class="top-btn top-btn--save"
          :disabled="saveSubmitting || loadState !== 'idle'"
          :aria-busy="saveSubmitting"
          @click="handleSave"
        >
          <span v-if="saveSubmitting" class="spinner" aria-hidden="true"/>
          {{ saveSubmitting ? '保存中…' : '保存草稿' }}
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
      <button class="banner-dismiss" aria-label="关闭提示" @click="saveBanner = ''">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
      </button>
    </div>

    <div v-if="saveSuccess" class="pipeline-banner pipeline-banner--success" role="status" aria-live="polite">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
        <path d="M20 6 9 17l-5-5"/>
      </svg>
      <span>流水线草稿已保存</span>
    </div>

    <!-- ─── Tab strip ──────────────────────────────────────────────────────── -->
    <nav class="tab-strip" aria-label="流水线配置标签页" role="tablist">
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
            <button class="banner-retry" @click="loadPipeline">↻ 重试</button>
          </div>

          <!-- Loading skeleton -->
          <div v-else-if="loadState === 'loading'" class="canvas-skeleton" aria-busy="true" aria-label="加载中">
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
            @update="handleCanvasUpdate"
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

  </div>
</template>

<style scoped>
/* ─── Root: full-height flex column ──────────────────────────────────────── */
.pipeline-root {
  display: flex;
  flex-direction: column;
  height: 100%;
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

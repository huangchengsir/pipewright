<script setup lang="ts">
/**
 * ProjectPipeline — 4-tab pipeline configuration editor.
 * Tabs: 流水线编排 / 变量与缓存 / 触发设置 / 环境与凭据
 * URL state: ?tab=canvas|vars|triggers|envs  (shareable)
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getPipeline, savePipeline, type PipelineDTO, type PipelineStage } from '../api/pipeline'
import { HttpError } from '../api/http'
import PipelineCanvas from '../components/pipeline/PipelineCanvas.vue'
import TriggersPanel from '../components/TriggersPanel.vue'

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

watch(projectId, loadPipeline)
onMounted(loadPipeline)

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
    case 'invalid_stage':   return '阶段名不能为空或 kind 不在允许值内,请检查后重试。'
    case 'invalid_job':     return '任务名称或类型不能为空,请补充后重试。'
    case 'duplicate_id':    return '阶段或任务 ID 重复,请删除重复项后重试。'
    default:                return err.apiError?.message ?? `保存失败(${err.status})`
  }
}

async function handleSave(): Promise<void> {
  saveSubmitting.value = true
  saveBanner.value     = ''
  saveSuccess.value    = false

  try {
    const dto = await savePipeline(projectId.value, { stages: editStages.value })
    applyPipeline(dto)
    showSaveSuccess()
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

// ─── AI button handler ────────────────────────────────────────────────────────

function handleAI(): void {
  // Story 2-5: AI generation
  saveBanner.value = ''
  saveSuccess.value = false
  // Use a non-error banner style
  saveBanner.value = 'AI 生成流水线将在 Story 2-5 提供,敬请期待。'
}

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
      class="pipeline-banner pipeline-banner--info"
      :class="{ 'pipeline-banner--error': !saveBanner.includes('2-5') }"
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

    <!-- ─── Tab panels ─────────────────────────────────────────────────────── -->

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
        @update="handleCanvasUpdate"
      />
    </div>

    <!-- 变量与缓存 (placeholder) -->
    <div
      v-show="activeTab === 'vars'"
      id="tabpanel-vars"
      class="tab-panel"
      role="tabpanel"
      aria-labelledby="tab-vars"
    >
      <div class="placeholder-card">
        <div class="placeholder-icon" aria-hidden="true">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M12 2 2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/>
          </svg>
        </div>
        <p class="placeholder-title">变量与缓存</p>
        <p class="placeholder-desc">环境变量、构建缓存策略及 Secret 引用管理将在 Story 2-4 落地。</p>
        <span class="placeholder-badge">将在 Story 2-4 提供</span>
      </div>
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

    <!-- 环境与凭据 (placeholder) -->
    <div
      v-show="activeTab === 'envs'"
      id="tabpanel-envs"
      class="tab-panel"
      role="tabpanel"
      aria-labelledby="tab-envs"
    >
      <div class="placeholder-card">
        <div class="placeholder-icon" aria-hidden="true">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
            <rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
        </div>
        <p class="placeholder-title">环境与凭据</p>
        <p class="placeholder-desc">目标环境绑定、凭据引用及 Secret 安全存储将在 Story 2-4 落地。</p>
        <span class="placeholder-badge">将在 Story 2-4 提供</span>
      </div>
    </div>

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
.tab-panel {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* Canvas panel: own flex layout for sidebar drawer */
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

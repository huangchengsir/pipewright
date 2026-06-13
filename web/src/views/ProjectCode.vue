<script setup lang="ts">
/**
 * ProjectCode — Story 7-4: 只读代码浏览(FR-4)。
 *
 * 双栏:左 CodeTree(目录树,懒展开)+ 右 CodeViewer(Monaco 只读高亮)。
 * ref:默认项目默认分支(从项目列表读 defaultBranch;后端空 ref 时回填规范化值)。
 * URL state:?path= 持久化当前选中文件(可分享/刷新保留)。
 *
 * degraded 友好态:克隆失败 → tree 空 + degraded=true → 整页显「源码暂不可读」,
 * 绝不白屏。纯只读,无编辑/提交。
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { listProjects, type Project } from '../api/projects'
import { HttpError } from '../api/http'
import type { SourceEntry } from '../api/source'
import CodeTree from '../components/code/CodeTree.vue'
import CodeViewer from '../components/code/CodeViewer.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const projectId = computed(() => String(route.params.id))

// ─── project meta(取默认分支 + 名称) ───────────────────────────────────────
const project = ref<Project | null>(null)
const projectLoading = ref(true)
const projectError = ref('')

const projectName = computed(() => project.value?.name ?? projectId.value)
// ref:项目默认分支;无则留空(后端按默认分支克隆并回填)。
const treeRef = computed(() => project.value?.defaultBranch ?? '')

async function loadProject(): Promise<void> {
  projectLoading.value = true
  projectError.value = ''
  try {
    const all = await listProjects()
    project.value = all.find((p) => p.id === projectId.value) ?? null
    if (!project.value) projectError.value = t('projectCode.projectNotFound')
  } catch (err) {
    projectError.value =
      err instanceof HttpError
        ? err.status === 0
          ? t('projectCode.connectFailed')
          : err.apiError?.message ?? t('projectCode.loadFailedStatus', { status: err.status })
        : t('projectCode.loadFailed')
  } finally {
    projectLoading.value = false
  }
}

onMounted(loadProject)

// ─── selected file(URL ?path=) ──────────────────────────────────────────────
const selectedPath = computed<string | null>(() => {
  const p = route.query.path
  return typeof p === 'string' && p.length > 0 ? p : null
})

function selectFile(entry: SourceEntry): void {
  void router.replace({ query: { ...route.query, path: entry.path } })
}

// ─── root tree degraded state(整页友好态) ──────────────────────────────────
const rootDegraded = ref(false)
const rootDegradedReason = ref('')

function onRootLoaded(payload: { degraded: boolean; degradedReason: string; empty: boolean }): void {
  // degraded 显式标记,或非 degraded 但空树(无任何文件)亦视作「暂不可读/空仓库」由
  // CodeTree 内部空态接管;此处仅接管 degraded=true 的整页提示。
  rootDegraded.value = payload.degraded
  rootDegradedReason.value = payload.degradedReason
}

function onRootError(): void {
  // 根树网络/服务错误由 CodeTree 内部就地显错并提供重试;此处不另起整页态。
  rootDegraded.value = false
}

// 切项目 → 复位 degraded。
watch(projectId, () => {
  rootDegraded.value = false
  rootDegradedReason.value = ''
})

function retryProject(): void {
  void loadProject()
}
</script>

<template>
  <div class="code-page">
    <!-- header -->
    <header class="code-top">
      <nav class="breadcrumb" :aria-label="t('projectCode.breadcrumbAria')">
        <router-link to="/projects" class="crumb-link">{{ t('projectCode.breadcrumbProjects') }}</router-link>
        <span class="crumb-sep" aria-hidden="true">/</span>
        <span class="crumb-cur">{{ projectName }}</span>
      </nav>
      <div class="code-top-row">
        <h1 class="code-title">{{ t('projectCode.title') }}</h1>
        <span v-if="treeRef" class="code-ref mono" :title="`ref: ${treeRef}`">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <line x1="6" y1="3" x2="6" y2="15" />
            <circle cx="18" cy="6" r="3" />
            <circle cx="6" cy="18" r="3" />
            <path d="M18 9a9 9 0 0 1-9 9" />
          </svg>
          {{ treeRef }}
        </span>
        <span class="code-readonly-tag">{{ t('projectCode.readonly') }}</span>
      </div>
    </header>

    <!-- project load error -->
    <div v-if="projectError" class="code-page-error" role="alert">
      <strong>{{ projectError }}</strong>
      <button type="button" class="code-page-retry" @click="retryProject">↻ {{ t('projectCode.retry') }}</button>
    </div>

    <!-- project loading skeleton -->
    <div v-else-if="projectLoading" class="code-page-loading">
      <span class="code-page-spin" aria-hidden="true" />
      <span class="mono">{{ t('projectCode.loadingProject') }}</span>
    </div>

    <!-- degraded: 源码暂不可读(整页友好态,不白屏) -->
    <div v-else-if="rootDegraded" class="code-degraded" role="status">
      <div class="code-degraded-icon" aria-hidden="true">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
          <path d="M12 9v4M12 17h.01" />
        </svg>
      </div>
      <strong class="code-degraded-title">{{ t('projectCode.degradedTitle') }}</strong>
      <p class="code-degraded-sub">
        {{ rootDegradedReason || t('projectCode.degradedDefault') }}
      </p>
    </div>

    <!-- two-pane browser -->
    <div v-else class="code-split">
      <aside class="code-split-tree">
        <CodeTree
          :project-id="projectId"
          :tree-ref="treeRef"
          :selected-path="selectedPath"
          @select="selectFile"
          @root-loaded="onRootLoaded"
          @root-error="onRootError"
        />
      </aside>
      <main class="code-split-view">
        <CodeViewer
          :project-id="projectId"
          :tree-ref="treeRef"
          :path="selectedPath"
        />
      </main>
    </div>
  </div>
</template>

<style scoped>
.code-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  gap: 14px;
}

/* ─── header ──────────────────────────────────────────────────────────────── */
.code-top {
  flex-shrink: 0;
}

.breadcrumb {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 0.78rem;
  margin-bottom: 6px;
}

.crumb-link {
  color: var(--color-faint);
  text-decoration: none;
  transition: color var(--duration-fast);
}

.crumb-link:hover {
  color: var(--color-primary);
}

.crumb-sep {
  color: var(--color-line-num);
}

.crumb-cur {
  color: var(--color-text);
  font-weight: 500;
}

.code-top-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.code-title {
  font-size: 1.32rem;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--color-text);
  margin: 0;
}

.code-ref {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.72rem;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-full);
  padding: 2px 10px;
}

.code-readonly-tag {
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--color-faint);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-sm);
  padding: 2px 9px;
}

/* ─── split layout ────────────────────────────────────────────────────────── */
.code-split {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(220px, 300px) 1fr;
  gap: 14px;
}

.code-split-tree {
  min-height: 0;
  min-width: 0;
}

.code-split-view {
  min-height: 0;
  min-width: 0;
}

/* ─── project load states ─────────────────────────────────────────────────── */
.code-page-error {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px 18px;
  border: 1px solid var(--color-red-soft);
  background: var(--color-red-soft);
  border-radius: var(--rounded-lg);
  color: var(--color-red);
  font-size: 0.85rem;
}

.code-page-retry,
.code-page-error strong {
  font-weight: 600;
}

.code-page-retry {
  margin-left: auto;
  font-family: var(--font-sans);
  font-size: 0.8rem;
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 5px 13px;
  cursor: pointer;
  transition: border-color var(--duration-fast);
}

.code-page-retry:hover {
  border-color: var(--color-faint);
}

.code-page-loading {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 40px;
  color: var(--color-faint);
  font-size: 0.82rem;
  justify-content: center;
}

.code-page-spin {
  width: 18px;
  height: 18px;
  border-radius: var(--rounded-full);
  border: 2.4px solid var(--color-border-strong);
  border-top-color: var(--color-primary);
  animation: code-page-spin 0.7s linear infinite;
}

@keyframes code-page-spin {
  to { transform: rotate(360deg); }
}

/* ─── degraded full-page ──────────────────────────────────────────────────── */
.code-degraded {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 40px 24px;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-lg);
  background: var(--color-inset);
}

.code-degraded-icon {
  width: 64px;
  height: 64px;
  border-radius: 18px;
  display: grid;
  place-items: center;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  margin-bottom: 16px;
}

.code-degraded-title {
  font-size: 1.05rem;
  font-weight: 650;
  color: var(--color-text);
}

.code-degraded-sub {
  font-size: 0.84rem;
  color: var(--color-faint);
  max-width: 46ch;
  line-height: 1.6;
  margin-top: 8px;
}

/* ─── responsive ──────────────────────────────────────────────────────────── */
@media (max-width: 760px) {
  .code-split {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(180px, 36vh) 1fr;
  }
}

@media (prefers-reduced-motion: reduce) {
  .code-page-spin { animation-duration: 1.6s; }
  .crumb-link,
  .code-page-retry { transition: none; }
}
</style>

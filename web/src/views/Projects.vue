<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import {
  listProjects,
  createProject,
  updateProject,
  deleteProject,
  testClone,
  type Project,
  type RunStatus,
  type CreateProjectInput,
  type UpdateProjectInput,
} from '../api/projects'
import { listCredentials, type Credential } from '../api/credentials'
import { triggerManual, type RunDetail } from '../api/runs'
import { HttpError } from '../api/http'

// ─── router ───────────────────────────────────────────────────────────────────

const router = useRouter()

function goToPipeline(projectId: string): void {
  void router.push({ name: 'project-pipeline', params: { id: projectId } })
}

// Story 7-4: read-only code browsing (FR-4)
function goToCode(projectId: string): void {
  void router.push({ name: 'project-code', params: { id: projectId } })
}

// ─── load state ──────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const projects = ref<Project[]>([])

// ─── search + filter ─────────────────────────────────────────────────────────

const searchQuery = ref('')
const statusFilter = ref<RunStatus | 'all'>('all')

const STATUS_OPTIONS: Array<{ value: RunStatus | 'all'; label: string }> = [
  { value: 'all',   label: '全部状态' },
  { value: '成功',   label: '成功' },
  { value: '失败',   label: '失败' },
  { value: '进行中', label: '进行中' },
  { value: '部分失败', label: '部分失败' },
  { value: '已回滚', label: '已回滚' },
  { value: '排队中', label: '排队中' },
]

const filteredProjects = computed(() => {
  let list = projects.value
  const q = searchQuery.value.trim().toLowerCase()
  if (q) {
    list = list.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.repoUrl.toLowerCase().includes(q) ||
        p.defaultBranch.toLowerCase().includes(q),
    )
  }
  if (statusFilter.value !== 'all') {
    list = list.filter((p) => p.lastRunStatus === statusFilter.value)
  }
  return list
})

// ─── credentials for dropdown ─────────────────────────────────────────────────

const credentials = ref<Credential[]>([])
const credentialsLoading = ref(false)

const gitCredentials = computed(() =>
  credentials.value.filter((c) => c.type === 'git_token'),
)

async function loadCredentials(): Promise<void> {
  credentialsLoading.value = true
  try {
    credentials.value = await listCredentials()
  } catch {
    // non-fatal; user will see empty dropdown with helper text
  } finally {
    credentialsLoading.value = false
  }
}

// ─── data loading ──────────────────────────────────────────────────────────────

async function loadProjects(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    projects.value = await listProjects()
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = '无法连接到服务器,请检查后端是否运行后重试'
      } else {
        loadError.value = err.apiError?.message ?? `加载项目失败(${err.status})`
      }
    } else {
      loadError.value = '加载项目失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

onMounted(async () => {
  await Promise.all([loadProjects(), loadCredentials()])
})

// ─── new project modal ────────────────────────────────────────────────────────

const createModalOpen = ref(false)

const createForm = ref({
  name: '',
  repoUrl: '',
  credentialId: '',
  defaultBranch: '',
})

const createErrors = ref({
  name: '',
  repoUrl: '',
  credentialId: '',
})

const createBanner = ref('')
const createSubmitting = ref(false)

// test-clone sub-state
type TestState = 'idle' | 'testing' | 'ok' | 'error'
const testState = ref<TestState>('idle')
const testError = ref('')
const testDetectedBranch = ref('')

function openCreateModal(): void {
  createForm.value = { name: '', repoUrl: '', credentialId: '', defaultBranch: '' }
  clearCreateErrors()
  createBanner.value = ''
  testState.value = 'idle'
  testError.value = ''
  testDetectedBranch.value = ''
  createModalOpen.value = true
}

function closeCreateModal(): void {
  if (createSubmitting.value) return
  createModalOpen.value = false
}

function clearCreateErrors(): void {
  createErrors.value = { name: '', repoUrl: '', credentialId: '' }
}

function validateCreateForm(): boolean {
  clearCreateErrors()
  let ok = true
  if (!createForm.value.name.trim()) {
    createErrors.value.name = '请输入项目名称'
    ok = false
  }
  if (!createForm.value.repoUrl.trim()) {
    createErrors.value.repoUrl = '请输入仓库地址'
    ok = false
  } else if (
    !createForm.value.repoUrl.trim().startsWith('http') &&
    !createForm.value.repoUrl.trim().startsWith('git@')
  ) {
    createErrors.value.repoUrl = '仓库地址格式不正确,请以 https:// 或 git@ 开头'
    ok = false
  }
  if (!createForm.value.credentialId) {
    createErrors.value.credentialId = '请选择仓库凭据'
    ok = false
  }
  return ok
}

async function handleTestClone(): Promise<void> {
  // Validate url + credential only
  let ok = true
  if (!createForm.value.repoUrl.trim()) {
    createErrors.value.repoUrl = '请先输入仓库地址'
    ok = false
  }
  if (!createForm.value.credentialId) {
    createErrors.value.credentialId = '请先选择仓库凭据'
    ok = false
  }
  if (!ok) return

  testState.value = 'testing'
  testError.value = ''
  testDetectedBranch.value = ''

  try {
    const result = await testClone({
      repoUrl: createForm.value.repoUrl.trim(),
      credentialId: createForm.value.credentialId,
    })
    testState.value = 'ok'
    testDetectedBranch.value = result.defaultBranch
    // Auto-fill default branch if user hasn't typed one
    if (!createForm.value.defaultBranch) {
      createForm.value.defaultBranch = result.defaultBranch
    }
  } catch (err) {
    testState.value = 'error'
    if (err instanceof HttpError) {
      const code = err.apiError?.code
      if (code === 'credential_error') {
        testError.value = '凭据错误:请检查 Gitee 访问令牌是否有效,前往凭据保险库更新。'
      } else if (code === 'repo_unreachable') {
        testError.value = '仓库不可达:请确认仓库地址正确,且仓库存在且可访问。'
      } else if (code === 'vault_unconfigured') {
        testError.value = '保险库未配置 master key,无法读取凭据。'
      } else if (err.status === 0) {
        testError.value = '无法连接到服务器,请检查后端是否运行后重试。'
      } else {
        testError.value = err.apiError?.message ?? `连接测试失败(${err.status})`
      }
    } else {
      testError.value = '连接测试失败,请稍后重试。'
    }
  }
}

async function handleCreateSubmit(): Promise<void> {
  if (!validateCreateForm()) return
  createSubmitting.value = true
  createBanner.value = ''

  const input: CreateProjectInput = {
    name: createForm.value.name.trim(),
    repoUrl: createForm.value.repoUrl.trim(),
    credentialId: createForm.value.credentialId,
  }
  if (createForm.value.defaultBranch.trim()) {
    input.defaultBranch = createForm.value.defaultBranch.trim()
  }

  try {
    const created = await createProject(input)
    projects.value = [created, ...projects.value]
    createModalOpen.value = false
  } catch (err) {
    if (err instanceof HttpError) {
      const code = err.apiError?.code
      if (code === 'credential_error') {
        createErrors.value.credentialId = '凭据错误:请检查访问令牌是否有效'
        createBanner.value = '凭据验证失败,请更换凭据或前往凭据保险库更新。'
      } else if (code === 'repo_unreachable') {
        createErrors.value.repoUrl = '仓库不可达:请确认地址正确且可访问'
        createBanner.value = '仓库地址不可达,创建失败。'
      } else if (code === 'vault_unconfigured') {
        createBanner.value = '保险库未配置 master key,无法保存项目。'
      } else if (err.status === 0) {
        createBanner.value = '无法连接到服务器,请检查后端是否运行后重试。'
      } else {
        createBanner.value = err.apiError?.message ?? `创建失败(${err.status})`
      }
    } else {
      createBanner.value = '创建失败,请稍后重试。'
    }
  } finally {
    createSubmitting.value = false
  }
}

// ─── rename modal ──────────────────────────────────────────────────────────────

const renameModalOpen = ref(false)
const renamingProject = ref<Project | null>(null)
const renameValue = ref('')
const renameError = ref('')
const renameBanner = ref('')
const renameSubmitting = ref(false)

function openRenameModal(p: Project): void {
  renamingProject.value = p
  renameValue.value = p.name
  renameError.value = ''
  renameBanner.value = ''
  renameModalOpen.value = true
}

function closeRenameModal(): void {
  if (renameSubmitting.value) return
  renameModalOpen.value = false
  renamingProject.value = null
}

async function handleRenameSubmit(): Promise<void> {
  if (!renameValue.value.trim()) {
    renameError.value = '项目名称不能为空'
    return
  }
  if (!renamingProject.value) return
  renameSubmitting.value = true
  renameBanner.value = ''

  const input: UpdateProjectInput = { name: renameValue.value.trim() }

  try {
    const updated = await updateProject(renamingProject.value.id, input)
    projects.value = projects.value.map((p) => (p.id === updated.id ? updated : p))
    renameModalOpen.value = false
    renamingProject.value = null
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        renameBanner.value = '无法连接到服务器,请稍后重试。'
      } else {
        renameBanner.value = err.apiError?.message ?? `重命名失败(${err.status})`
      }
    } else {
      renameBanner.value = '重命名失败,请稍后重试。'
    }
  } finally {
    renameSubmitting.value = false
  }
}

// ─── delete confirm modal ─────────────────────────────────────────────────────

const deleteModalOpen = ref(false)
const deletingProject = ref<Project | null>(null)
const deleteSubmitting = ref(false)
const deleteBanner = ref('')

function openDeleteModal(p: Project): void {
  deletingProject.value = p
  deleteBanner.value = ''
  deleteModalOpen.value = true
}

function closeDeleteModal(): void {
  if (deleteSubmitting.value) return
  deleteModalOpen.value = false
  deletingProject.value = null
}

async function confirmDelete(): Promise<void> {
  if (!deletingProject.value) return
  deleteSubmitting.value = true
  deleteBanner.value = ''
  const id = deletingProject.value.id

  try {
    await deleteProject(id)
    projects.value = projects.value.filter((p) => p.id !== id)
    deleteModalOpen.value = false
    deletingProject.value = null
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        deleteBanner.value = '无法连接到服务器,请稍后重试。'
      } else {
        deleteBanner.value = err.apiError?.message ?? `删除失败(${err.status})`
      }
    } else {
      deleteBanner.value = '删除失败,请稍后重试。'
    }
  } finally {
    deleteSubmitting.value = false
  }
}

// ─── manual trigger modal ─────────────────────────────────────────────────────

const triggerModalOpen   = ref(false)
const triggerProject     = ref<Project | null>(null)
const triggerForm        = ref({ branch: '', commit: '' })
const triggerBranchError = ref('')
const triggerBanner      = ref('')
const triggerSubmitting  = ref(false)

function openTriggerModal(p: Project): void {
  triggerProject.value     = p
  triggerForm.value        = { branch: p.defaultBranch || '', commit: '' }
  triggerBranchError.value = ''
  triggerBanner.value      = ''
  triggerSubmitting.value  = false
  triggerModalOpen.value   = true
}

function closeTriggerModal(): void {
  if (triggerSubmitting.value) return
  triggerModalOpen.value = false
  triggerProject.value   = null
}

async function handleTriggerSubmit(): Promise<void> {
  triggerBranchError.value = ''
  triggerBanner.value      = ''

  const branch = triggerForm.value.branch.trim()
  if (!branch) {
    triggerBranchError.value = '请输入目标分支'
    return
  }

  if (!triggerProject.value) return
  triggerSubmitting.value = true

  try {
    const input: { branch: string; commit?: string } = { branch }
    const commit = triggerForm.value.commit.trim()
    if (commit) input.commit = commit

    const run: RunDetail = await triggerManual(triggerProject.value.id, input)
    triggerModalOpen.value = false
    triggerProject.value   = null
    void router.push(`/runs/${run.id}`)
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        triggerBanner.value = '无法连接到服务器,请检查后端是否运行后重试。'
      } else if (err.status === 404) {
        triggerBanner.value = '项目不存在,请刷新后重试。'
      } else {
        triggerBanner.value = err.apiError?.message ?? `触发失败(${err.status})`
      }
    } else {
      triggerBanner.value = '触发失败,请稍后重试。'
    }
  } finally {
    triggerSubmitting.value = false
  }
}

// ─── helpers ──────────────────────────────────────────────────────────────────

function relativeTime(isoStr: string): string {
  const diff = Date.now() - new Date(isoStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return '刚刚'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  const d = Math.floor(h / 24)
  return `${d} 天前`
}

function credentialLabel(c: Credential): string {
  return `${c.name} · ${c.maskedValue}`
}

// Status pill config — fixed six-word vocabulary, no substitutes
type StatusConfig = { dot: string; bg: string; border: string; text: string; pulse: boolean }

const STATUS_CONFIG: Record<RunStatus, StatusConfig> = {
  '成功':   { dot: 'var(--color-green)',  bg: 'var(--color-green-soft)',  border: 'transparent',            text: 'var(--color-green)',  pulse: false },
  '失败':   { dot: 'var(--color-red)',    bg: 'var(--color-red-soft)',    border: 'var(--color-red-line)',   text: 'var(--color-red)',    pulse: false },
  '进行中': { dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)',  border: 'transparent',            text: 'var(--color-amber)',  pulse: true  },
  '部分失败': { dot: 'var(--color-red)',  bg: 'var(--color-red-soft)',    border: 'var(--color-red-line)',   text: 'var(--color-red)',    pulse: false },
  '已回滚': { dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)',  border: 'var(--color-amber-line)', text: 'var(--color-amber)',  pulse: false },
  '排队中': { dot: 'var(--color-faint)',  bg: 'var(--color-card-2)',      border: 'var(--color-border-strong)', text: 'var(--color-dim)', pulse: false },
}
</script>

<template>
  <div class="projects-root">
    <!-- ─── Page header ─────────────────────────────────────────────────── -->
    <header class="page-header">
      <div class="page-header-text">
        <h1 class="page-title">项目</h1>
        <p class="page-sub">纳管的 Gitee 仓库,每个项目对应一套流水线配置与部署目标</p>
      </div>
      <button
        class="btn-primary"
        :disabled="loadState === 'loading'"
        @click="openCreateModal"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
          <path d="M12 5v14M5 12h14"/>
        </svg>
        新建项目
      </button>
    </header>

    <!-- ─── Load error banner ────────────────────────────────────────────── -->
    <div
      v-if="loadState === 'error'"
      class="banner banner--error"
      role="alert"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadProjects">↻ 重试</button>
    </div>

    <!-- ─── Search + Filter toolbar ─────────────────────────────────────── -->
    <div
      v-if="loadState === 'idle' && projects.length > 0"
      class="toolbar"
      role="search"
      aria-label="项目搜索与筛选"
    >
      <!-- Search box -->
      <div class="search-wrap">
        <svg class="search-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7"/><path d="m21 21-4.35-4.35"/>
        </svg>
        <input
          v-model="searchQuery"
          type="search"
          class="search-input"
          placeholder="搜索项目名、仓库地址、分支…"
          aria-label="搜索项目"
        />
      </div>

      <!-- Status filter -->
      <div class="filter-tabs" role="group" aria-label="状态筛选">
        <button
          v-for="opt in STATUS_OPTIONS"
          :key="opt.value"
          type="button"
          class="filter-tab"
          :class="{ 'filter-tab--active': statusFilter === opt.value }"
          @click="statusFilter = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>
    </div>

    <!-- ─── Loading skeleton ─────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="project-grid" aria-busy="true" aria-label="加载中">
        <div
          v-for="i in 6"
          :key="i"
          class="skel-card"
          aria-hidden="true"
        >
          <div class="skel skel--name" />
          <div class="skel skel--url" />
          <div class="skel skel--tag" />
          <div class="skel skel--meta" />
        </div>
      </div>
    </template>

    <!-- ─── Empty state ──────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle' && projects.length === 0">
      <div class="empty-state" role="status">
        <div class="empty-icon" aria-hidden="true">
          <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M14.5 9.5 21 3M21 3h-5M21 3v5"/>
            <path d="M10 14a5 5 0 1 1-7 4.6"/>
          </svg>
        </div>
        <p class="empty-label">还没有项目</p>
        <p class="empty-hint">接入第一个 Gitee 仓库,后续可配置流水线并部署到目标服务器。</p>
        <button class="btn-primary" @click="openCreateModal">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          新建项目
        </button>
      </div>
    </template>

    <!-- ─── Empty search result ──────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle' && filteredProjects.length === 0">
      <div class="empty-state" role="status">
        <div class="empty-icon" aria-hidden="true">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
            <circle cx="11" cy="11" r="7"/><path d="m21 21-4.35-4.35"/>
          </svg>
        </div>
        <p class="empty-label">没有匹配的项目</p>
        <p class="empty-hint">调整搜索词或状态筛选条件后重试。</p>
        <button
          class="btn-secondary"
          @click="searchQuery = ''; statusFilter = 'all'"
        >清除筛选</button>
      </div>
    </template>

    <!-- ─── Project grid ─────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle'">
      <p class="result-count" aria-live="polite">
        {{ filteredProjects.length }} 个项目
        <template v-if="searchQuery || statusFilter !== 'all'">
          (共 {{ projects.length }} 个)
        </template>
      </p>

      <ul class="project-grid" role="list">
        <li
          v-for="project in filteredProjects"
          :key="project.id"
          class="project-card"
        >
          <!-- Card header: name + status badge -->
          <div class="card-header">
            <div class="project-icon" aria-hidden="true">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                <path d="M14.5 9.5 21 3M21 3h-5M21 3v5"/>
                <path d="M10 14a5 5 0 1 1-7 4.6"/>
              </svg>
            </div>
            <h2 class="project-name" :title="project.name">{{ project.name }}</h2>

            <!-- Status badge: only if we have a run status -->
            <div
              v-if="project.lastRunStatus"
              class="status-pill"
              :style="{
                background: STATUS_CONFIG[project.lastRunStatus].bg,
                border: `1px solid ${STATUS_CONFIG[project.lastRunStatus].border}`,
                color: STATUS_CONFIG[project.lastRunStatus].text,
              }"
              :aria-label="`运行状态:${project.lastRunStatus}`"
            >
              <span
                class="status-dot"
                :class="{ 'status-dot--pulse': STATUS_CONFIG[project.lastRunStatus].pulse }"
                :style="{ background: STATUS_CONFIG[project.lastRunStatus].dot }"
                aria-hidden="true"
              />
              {{ project.lastRunStatus }}
            </div>
          </div>

          <!-- Repo + branch (equal-width columns) -->
          <div class="card-repo">
            <div class="repo-url-row">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"/>
              </svg>
              <a
                class="repo-url mono"
                :href="project.repoUrl"
                target="_blank"
                rel="noopener noreferrer"
                :title="project.repoUrl"
              >{{ project.repoUrl.replace(/^https?:\/\//, '') }}</a>
            </div>
            <div class="branch-row">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M6 3v12"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>
              </svg>
              <span class="branch-name mono">{{ project.defaultBranch || '—' }}</span>
            </div>
          </div>

          <!-- Divider -->
          <div class="card-divider" aria-hidden="true" />

          <!-- Last run: empty placeholder or status -->
          <div class="card-meta-row">
            <span class="meta-label">上次运行</span>
            <span
              v-if="!project.lastRunStatus"
              class="meta-empty"
            >尚无运行</span>
            <span
              v-else
              class="meta-value"
              :style="{ color: STATUS_CONFIG[project.lastRunStatus].text }"
            >{{ project.lastRunStatus }}</span>
          </div>

          <!-- Target servers: empty placeholder or list -->
          <div class="card-meta-row">
            <span class="meta-label">目标服务器</span>
            <span
              v-if="!project.targetServers || project.targetServers.length === 0"
              class="meta-empty"
            >未绑定</span>
            <span v-else class="meta-value">
              {{ project.targetServers.join(', ') }}
            </span>
          </div>

          <!-- Credential reference: display name + masked, never plaintext -->
          <div class="card-meta-row">
            <span class="meta-label">仓库凭据</span>
            <span class="meta-value meta-value--mono" :title="'凭据引用(非明文)'">
              {{ project.credentialName || '—' }}
            </span>
          </div>

          <!-- Card footer: updatedAt + actions -->
          <div class="card-footer">
            <span class="card-time" :title="project.updatedAt">
              更新于 {{ relativeTime(project.updatedAt) }}
            </span>

            <div class="card-actions">
              <!-- Manual trigger / Run -->
              <button
                class="action-btn action-btn--run"
                :title="`手动触发运行 · ${project.name}`"
                :aria-label="`手动触发项目 ${project.name} 的流水线运行`"
                @click.stop="openTriggerModal(project)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <polygon points="5 3 19 12 5 21 5 3" fill="currentColor" stroke="none"/>
                </svg>
              </button>

              <!-- Rename -->
              <button
                class="action-btn"
                :title="`重命名 ${project.name}`"
                :aria-label="`重命名项目 ${project.name}`"
                @click="openRenameModal(project)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>

              <!-- Code browse (Story 7-4: read-only source viewer, FR-4) -->
              <button
                class="action-btn"
                :title="`代码浏览 · ${project.name}`"
                :aria-label="`浏览项目 ${project.name} 的代码`"
                @click="goToCode(project.id)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <polyline points="16 18 22 12 16 6"/>
                  <polyline points="8 6 2 12 8 18"/>
                </svg>
              </button>

              <!-- Configure → triggers page (Story 2.3; will be extended to 4-tab editor in 2-2) -->
              <button
                class="action-btn"
                :title="`流水线配置 · ${project.name}`"
                :aria-label="`配置项目 ${project.name} 的流水线`"
                @click="goToPipeline(project.id)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                  <circle cx="12" cy="12" r="3"/>
                  <path d="M19.07 4.93a10 10 0 1 1-14.14 0"/>
                  <path d="M12 2v4M12 18v4M4.93 4.93 7.76 7.76M16.24 16.24l2.83 2.83"/>
                </svg>
              </button>

              <!-- Delete -->
              <button
                class="action-btn action-btn--danger"
                :title="`删除 ${project.name}`"
                :aria-label="`删除项目 ${project.name}`"
                @click="openDeleteModal(project)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                  <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                  <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
                </svg>
              </button>
            </div>
          </div>
        </li>
      </ul>
    </template>
  </div>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Manual trigger modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="triggerModalOpen && triggerProject"
      class="modal-scrim"
      role="dialog"
      :aria-label="`手动触发 · ${triggerProject.name}`"
      aria-modal="true"
      @keydown.esc="closeTriggerModal"
      @click.self="closeTriggerModal"
    >
      <div class="modal modal--sm">
        <!-- Header -->
        <div class="modal-head">
          <div class="modal-icon modal-icon--run" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <polygon points="5 3 19 12 5 21 5 3" fill="currentColor" stroke="none"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">手动触发运行</h3>
            <p class="modal-sub">{{ triggerProject.name }} · 指定分支并立即创建一次流水线运行</p>
          </div>
          <button
            class="modal-close"
            aria-label="关闭对话框"
            :disabled="triggerSubmitting"
            @click="closeTriggerModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <!-- Error banner -->
        <div
          v-if="triggerBanner"
          class="banner banner--error modal-banner"
          role="alert"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ triggerBanner }}
        </div>

        <form
          class="modal-form"
          novalidate
          @submit.prevent="handleTriggerSubmit"
        >
          <!-- Branch -->
          <div class="field">
            <label class="field-label" for="trigger-branch">
              分支
              <span class="field-hint-inline">（必填）</span>
            </label>
            <input
              id="trigger-branch"
              v-model="triggerForm.branch"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': triggerBranchError }"
              type="text"
              placeholder="main"
              autocomplete="off"
              :disabled="triggerSubmitting"
              :aria-invalid="triggerBranchError ? 'true' : undefined"
              :aria-describedby="triggerBranchError ? 'trigger-branch-err' : undefined"
              @input="triggerBranchError = ''"
            />
            <span
              v-if="triggerBranchError"
              id="trigger-branch-err"
              class="field-error"
              role="alert"
            >{{ triggerBranchError }}</span>
          </div>

          <!-- Commit (optional) -->
          <div class="field">
            <label class="field-label" for="trigger-commit">
              Commit
              <span class="field-hint-inline">（可选,留空使用分支 HEAD）</span>
            </label>
            <input
              id="trigger-commit"
              v-model="triggerForm.commit"
              class="field-input field-input--mono"
              type="text"
              placeholder="例:a3f1c2d"
              autocomplete="off"
              :disabled="triggerSubmitting"
            />
          </div>

          <!-- Footer -->
          <div class="modal-footer">
            <button
              type="button"
              class="btn-secondary"
              :disabled="triggerSubmitting"
              @click="closeTriggerModal"
            >取消</button>
            <button
              type="submit"
              class="btn-run"
              :disabled="triggerSubmitting"
              :aria-busy="triggerSubmitting"
            >
              <span v-if="triggerSubmitting" class="spinner" aria-hidden="true" />
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
              </svg>
              {{ triggerSubmitting ? '触发中…' : '立即运行' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- ═══════════════════════════════════════════════════════════════════════
       New project modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="createModalOpen"
      class="modal-scrim"
      role="dialog"
      aria-label="新建项目"
      aria-modal="true"
      @keydown.esc="closeCreateModal"
      @click.self="closeCreateModal"
    >
      <div class="modal">
        <!-- Header -->
        <div class="modal-head">
          <div class="modal-icon" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M14.5 9.5 21 3M21 3h-5M21 3v5"/>
              <path d="M10 14a5 5 0 1 1-7 4.6"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">新建项目</h3>
            <p class="modal-sub">接入 Gitee 仓库并绑定仓库凭据</p>
          </div>
          <button
            class="modal-close"
            aria-label="关闭对话框"
            :disabled="createSubmitting"
            @click="closeCreateModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <!-- Error banner -->
        <div
          v-if="createBanner"
          class="banner banner--error modal-banner"
          role="alert"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ createBanner }}
        </div>

        <form
          class="modal-form"
          novalidate
          @submit.prevent="handleCreateSubmit"
        >
          <!-- Project name -->
          <div class="field">
            <label class="field-label" for="proj-name">项目名称</label>
            <input
              id="proj-name"
              v-model="createForm.name"
              class="field-input"
              :class="{ 'field-input--error': createErrors.name }"
              type="text"
              placeholder="例:acme-web"
              autocomplete="off"
              :disabled="createSubmitting"
              :aria-invalid="createErrors.name ? 'true' : undefined"
              :aria-describedby="createErrors.name ? 'proj-name-err' : undefined"
              @input="createErrors.name = ''"
            />
            <span v-if="createErrors.name" id="proj-name-err" class="field-error" role="alert">{{ createErrors.name }}</span>
          </div>

          <!-- Repo URL -->
          <div class="field">
            <label class="field-label" for="proj-repo">仓库地址</label>
            <input
              id="proj-repo"
              v-model="createForm.repoUrl"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': createErrors.repoUrl }"
              type="url"
              placeholder="https://gitee.com/your-org/repo.git"
              autocomplete="off"
              :disabled="createSubmitting"
              :aria-invalid="createErrors.repoUrl ? 'true' : undefined"
              :aria-describedby="createErrors.repoUrl ? 'proj-repo-err' : undefined"
              @input="createErrors.repoUrl = ''; testState = 'idle'"
            />
            <span v-if="createErrors.repoUrl" id="proj-repo-err" class="field-error" role="alert">{{ createErrors.repoUrl }}</span>
          </div>

          <!-- Credential dropdown — git_token only, masked display -->
          <div class="field">
            <label class="field-label" for="proj-cred">
              仓库凭据
              <span class="field-hint-inline">（仅显示 Git 令牌类型，不含明文）</span>
            </label>
            <div class="select-wrap">
              <select
                id="proj-cred"
                v-model="createForm.credentialId"
                class="field-select"
                :class="{ 'field-input--error': createErrors.credentialId }"
                :disabled="createSubmitting || credentialsLoading"
                :aria-invalid="createErrors.credentialId ? 'true' : undefined"
                :aria-describedby="createErrors.credentialId ? 'proj-cred-err' : undefined"
                @change="createErrors.credentialId = ''; testState = 'idle'"
              >
                <option value="" disabled>
                  {{ credentialsLoading ? '加载凭据中…' : '选择 Git 令牌凭据' }}
                </option>
                <option
                  v-for="cred in gitCredentials"
                  :key="cred.id"
                  :value="cred.id"
                >{{ credentialLabel(cred) }}</option>
              </select>
              <svg class="select-arrow" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M6 9l6 6 6-6"/>
              </svg>
            </div>
            <span v-if="createErrors.credentialId" id="proj-cred-err" class="field-error" role="alert">{{ createErrors.credentialId }}</span>
            <span
              v-if="!credentialsLoading && gitCredentials.length === 0"
              class="field-hint"
            >
              尚无 Git 令牌凭据,请先前往
              <a href="/settings/vault" class="link">凭据保险库</a>
              添加。
            </span>
          </div>

          <!-- Default branch (optional) -->
          <div class="field">
            <label class="field-label" for="proj-branch">
              默认分支
              <span class="field-hint-inline">（可选,留空由测试连接自动探测）</span>
            </label>
            <input
              id="proj-branch"
              v-model="createForm.defaultBranch"
              class="field-input field-input--mono"
              type="text"
              placeholder="main"
              autocomplete="off"
              :disabled="createSubmitting"
            />
          </div>

          <!-- Test clone — left-bottom, separated from primary actions -->
          <div class="test-clone-row">
            <button
              type="button"
              class="btn-ghost"
              :disabled="createSubmitting || testState === 'testing'"
              :aria-busy="testState === 'testing'"
              @click="handleTestClone"
            >
              <span v-if="testState === 'testing'" class="spinner spinner--dim" aria-hidden="true" />
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M5 12.55a11 11 0 0 1 14.08 0"/>
                <path d="M1.42 9a16 16 0 0 1 21.16 0"/>
                <path d="M8.53 16.11a6 6 0 0 1 6.95 0"/>
                <circle cx="12" cy="20" r="1" fill="currentColor"/>
              </svg>
              {{ testState === 'testing' ? '测试中…' : '测试连接' }}
            </button>

            <!-- Test result inline -->
            <div
              v-if="testState === 'ok'"
              class="test-result test-result--ok"
              role="status"
              aria-live="polite"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M20 6 9 17l-5-5"/>
              </svg>
              连接成功
              <span v-if="testDetectedBranch" class="test-branch mono">· 默认分支 {{ testDetectedBranch }}</span>
            </div>

            <div
              v-else-if="testState === 'error'"
              class="test-result test-result--error"
              role="alert"
              aria-live="assertive"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
              </svg>
              {{ testError }}
            </div>
          </div>

          <!-- Modal footer -->
          <div class="modal-footer">
            <button
              type="button"
              class="btn-secondary"
              :disabled="createSubmitting"
              @click="closeCreateModal"
            >取消</button>
            <button
              type="submit"
              class="btn-primary"
              :disabled="createSubmitting"
              :aria-busy="createSubmitting"
            >
              <span v-if="createSubmitting" class="spinner" aria-hidden="true" />
              {{ createSubmitting ? '创建中…' : '创建项目' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Rename modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="renameModalOpen && renamingProject"
      class="modal-scrim"
      role="dialog"
      aria-label="重命名项目"
      aria-modal="true"
      @keydown.esc="closeRenameModal"
      @click.self="closeRenameModal"
    >
      <div class="modal modal--sm">
        <div class="modal-head">
          <div class="modal-icon" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
              <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">重命名项目</h3>
            <p class="modal-sub">修改项目的显示名称</p>
          </div>
          <button
            class="modal-close"
            aria-label="关闭对话框"
            :disabled="renameSubmitting"
            @click="closeRenameModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <div
          v-if="renameBanner"
          class="banner banner--error modal-banner"
          role="alert"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ renameBanner }}
        </div>

        <form
          class="modal-form"
          novalidate
          @submit.prevent="handleRenameSubmit"
        >
          <div class="field">
            <label class="field-label" for="rename-input">项目名称</label>
            <input
              id="rename-input"
              v-model="renameValue"
              class="field-input"
              :class="{ 'field-input--error': renameError }"
              type="text"
              autocomplete="off"
              :disabled="renameSubmitting"
              :aria-invalid="renameError ? 'true' : undefined"
              :aria-describedby="renameError ? 'rename-err' : undefined"
              @input="renameError = ''"
            />
            <span v-if="renameError" id="rename-err" class="field-error" role="alert">{{ renameError }}</span>
          </div>

          <div class="modal-footer">
            <button
              type="button"
              class="btn-secondary"
              :disabled="renameSubmitting"
              @click="closeRenameModal"
            >取消</button>
            <button
              type="submit"
              class="btn-primary"
              :disabled="renameSubmitting"
              :aria-busy="renameSubmitting"
            >
              <span v-if="renameSubmitting" class="spinner" aria-hidden="true" />
              {{ renameSubmitting ? '保存中…' : '保存' }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Delete confirm modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="deleteModalOpen && deletingProject"
      class="modal-scrim"
      role="dialog"
      aria-label="确认删除项目"
      aria-modal="true"
      @keydown.esc="closeDeleteModal"
      @click.self="closeDeleteModal"
    >
      <div class="modal modal--sm">
        <div class="modal-head">
          <div class="modal-icon modal-icon--danger" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
              <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">删除项目</h3>
            <p class="modal-sub">此操作不可撤销</p>
          </div>
          <button
            class="modal-close"
            aria-label="关闭对话框"
            :disabled="deleteSubmitting"
            @click="closeDeleteModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <div class="modal-body">
          <p class="delete-confirm-text">
            确定要永久删除项目
            <strong class="delete-name">{{ deletingProject.name }}</strong>
            吗?其流水线配置、运行历史及凭据引用关系将一并清理。
          </p>

          <div
            v-if="deleteBanner"
            class="banner banner--error modal-banner-body"
            role="alert"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
            </svg>
            {{ deleteBanner }}
          </div>
        </div>

        <div class="modal-footer modal-footer--body">
          <button
            type="button"
            class="btn-secondary"
            :disabled="deleteSubmitting"
            @click="closeDeleteModal"
          >取消</button>
          <button
            type="button"
            class="btn-danger"
            :disabled="deleteSubmitting"
            :aria-busy="deleteSubmitting"
            @click="confirmDelete"
          >
            <span v-if="deleteSubmitting" class="spinner spinner--red" aria-hidden="true" />
            {{ deleteSubmitting ? '删除中…' : '确认删除' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* ─── root layout ──────────────────────────────────────────────────────────── */
.projects-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── page header ──────────────────────────────────────────────────────────── */
.page-header {
  display: flex;
  align-items: flex-start;
  gap: 16px;
}

.page-header-text {
  flex: 1;
}

.page-title {
  font-size: 1.5rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
  line-height: 1.2;
}

.page-sub {
  font-size: 0.82rem;
  color: var(--color-faint);
  margin-top: 5px;
  line-height: 1.5;
}

/* ─── toolbar (search + filter) ────────────────────────────────────────────── */
.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.search-wrap {
  position: relative;
  flex: 1;
  min-width: 200px;
  max-width: 380px;
}

.search-icon {
  position: absolute;
  left: 12px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-faint);
  pointer-events: none;
}

.search-input {
  width: 100%;
  height: 36px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 12px 0 34px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.search-input::placeholder {
  color: var(--color-faint);
}

.search-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

/* Clear the native "x" button on search inputs */
.search-input::-webkit-search-cancel-button {
  -webkit-appearance: none;
}

.filter-tabs {
  display: flex;
  gap: 4px;
  background: var(--color-inset);
  border-radius: var(--rounded);
  padding: 3px;
}

.filter-tab {
  height: 30px;
  padding: 0 11px;
  border: none;
  background: transparent;
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.78rem;
  font-weight: 500;
  border-radius: var(--rounded-md);
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--duration-fast), background-color var(--duration-fast), box-shadow var(--duration-fast);
}

.filter-tab:hover {
  color: var(--color-text);
  background: oklch(100% 0 0 / 0.04);
}

.filter-tab--active {
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: var(--shadow);
}

.filter-tab:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

/* ─── result count ─────────────────────────────────────────────────────────── */
.result-count {
  font-size: 0.78rem;
  color: var(--color-faint);
  margin-top: -4px;
}

/* ─── project grid ──────────────────────────────────────────────────────────── */
.project-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: var(--card-gap);
  list-style: none;
}

/* ─── project card ──────────────────────────────────────────────────────────── */
.project-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  padding: 18px 20px 16px;
  display: flex;
  flex-direction: column;
  gap: 0;
  animation: card-in 0.4s var(--ease-out-expo) both;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast), transform var(--duration-fast);
}

.project-card:hover {
  border-color: var(--color-border-strong);
  transform: translateY(-2px);
  box-shadow: var(--shadow), 0 0 0 1px var(--color-border-strong);
}

@keyframes card-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .project-card {
    animation: none;
  }
  .project-card:hover {
    transform: none;
  }
}

/* card header: icon + name + status badge */
.card-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 14px;
}

.project-icon {
  width: 30px;
  height: 30px;
  border-radius: var(--rounded);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.project-name {
  flex: 1;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: -0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.3;
}

/* status pill */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: var(--rounded-md);
  font-size: var(--text-micro);
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.status-dot--pulse {
  animation: dot-pulse 1.1s ease-in-out infinite;
}

@keyframes dot-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50%       { opacity: 0.5; transform: scale(0.8); }
}

@media (prefers-reduced-motion: reduce) {
  .status-dot--pulse {
    animation: none;
  }
}

/* repo + branch */
.card-repo {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 14px;
}

.repo-url-row,
.branch-row {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--color-faint);
}

.repo-url {
  font-size: 0.74rem;
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-decoration: none;
  flex: 1;
  min-width: 0;
}

.repo-url:hover {
  color: var(--color-primary);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.repo-url:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: 2px;
}

.branch-name {
  font-size: 0.72rem;
  color: var(--color-dim);
}

.mono {
  font-family: var(--font-mono);
}

/* divider */
.card-divider {
  height: 1px;
  background: var(--color-border);
  margin-bottom: 12px;
}

/* meta rows */
.card-meta-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 7px;
  min-height: 20px;
}

.card-meta-row:last-of-type {
  margin-bottom: 14px;
}

.meta-label {
  font-size: 0.71rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-faint);
  flex-shrink: 0;
}

.meta-empty {
  font-size: 0.78rem;
  color: var(--color-faint);
  font-style: italic;
}

.meta-value {
  font-size: 0.78rem;
  color: var(--color-dim);
  text-align: right;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 60%;
}

.meta-value--mono {
  font-family: var(--font-mono);
  font-size: 0.72rem;
  letter-spacing: 0.02em;
  user-select: none;
}

/* card footer */
.card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: auto;
  padding-top: 10px;
  border-top: 1px solid var(--color-border);
}

.card-time {
  font-size: 0.72rem;
  color: var(--color-faint);
}

.card-actions {
  display: flex;
  gap: 5px;
}

.action-btn {
  width: 28px;
  height: 26px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background-color var(--duration-fast);
}

.action-btn:hover {
  color: var(--color-text);
  border-color: var(--color-faint);
}

.action-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.action-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.action-btn--danger:hover {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}

.action-btn--run:hover {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

/* ─── skeleton ──────────────────────────────────────────────────────────────── */
.skel-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 200px;
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

.skel--name { height: 18px; width: 55%; }
.skel--url  { height: 12px; width: 80%; }
.skel--tag  { height: 12px; width: 40%; }
.skel--meta { height: 12px; width: 65%; }

/* ─── empty state ───────────────────────────────────────────────────────────── */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 72px 32px;
  text-align: center;
  background: var(--color-card);
  border: 1.5px dashed var(--color-border-strong);
  border-radius: var(--rounded-card);
}

.empty-icon {
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

.empty-label {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
}

.empty-hint {
  font-size: 0.82rem;
  color: var(--color-faint);
  max-width: 44ch;
  line-height: 1.6;
  margin-bottom: 6px;
}

/* ─── error banner ──────────────────────────────────────────────────────────── */
.banner {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  font-size: 0.83rem;
  line-height: 1.5;
}

.banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}

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

.banner-retry:hover {
  opacity: 0.8;
}

/* ─── buttons ───────────────────────────────────────────────────────────────── */
.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 15px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast), box-shadow var(--duration-fast);
  white-space: nowrap;
  flex-shrink: 0;
}

.btn-primary:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}

.btn-primary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}

.btn-primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none;
  box-shadow: none;
}

.btn-secondary {
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 0 15px;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 500;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.btn-secondary:hover:not(:disabled) {
  border-color: var(--color-faint);
}

.btn-secondary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.btn-secondary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.btn-ghost {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 12px;
  background: transparent;
  color: var(--color-dim);
  border: 1px solid var(--color-border-strong);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}

.btn-ghost:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-faint);
  background: var(--color-inset);
}

.btn-ghost:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.btn-ghost:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.btn-danger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 15px;
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid var(--color-red-line);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: background-color var(--duration-fast), transform var(--duration-fast);
}

.btn-danger:hover:not(:disabled) {
  background: oklch(62% 0.18 22 / 0.25);
  transform: translateY(-1px);
}

.btn-danger:focus-visible {
  outline: 2px solid var(--color-red);
  outline-offset: 2px;
}

.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

.btn-run {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 15px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast), box-shadow var(--duration-fast);
  white-space: nowrap;
}

.btn-run:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}

.btn-run:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
}

.btn-run:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none;
  box-shadow: none;
}

/* ─── modal ─────────────────────────────────────────────────────────────────── */
.modal-scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.62);
  display: grid;
  place-items: center;
  z-index: 100;
  padding: 24px;
  animation: scrim-in var(--duration-fast) ease both;
}

@keyframes scrim-in {
  from { opacity: 0; }
  to   { opacity: 1; }
}

.modal {
  width: 100%;
  max-width: 520px;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-xl);
  box-shadow: var(--shadow-modal);
  overflow: hidden;
  animation: modal-in 0.35s var(--ease-out-expo) both;
}

.modal--sm {
  max-width: 420px;
}

@keyframes modal-in {
  from { opacity: 0; transform: translateY(14px) scale(0.98); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .modal-scrim { animation: none; }
  .modal       { animation: none; }
}

.modal-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 20px 20px 16px;
  border-bottom: 1px solid var(--color-border);
}

.modal-icon {
  width: 36px;
  height: 36px;
  border-radius: var(--rounded-lg);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.modal-icon--danger {
  background: var(--color-red-soft);
  color: var(--color-red);
}

.modal-icon--run {
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.modal-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
  margin-top: 2px;
  letter-spacing: -0.01em;
}

.modal-sub {
  font-size: 0.78rem;
  color: var(--color-faint);
  margin-top: 3px;
  line-height: 1.4;
}

.modal-close {
  margin-left: auto;
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: var(--rounded-md);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.modal-close:hover {
  color: var(--color-text);
  background: var(--color-inset);
}

.modal-close:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.modal-close:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.modal-banner {
  margin: 16px 20px 0;
  border-radius: var(--rounded);
}

.modal-form {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.modal-body {
  padding: 20px;
}

.modal-banner-body {
  margin-top: 14px;
  border-radius: var(--rounded);
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}

.modal-footer--body {
  padding: 0 20px 20px;
  padding-top: 0;
}

/* ─── form fields ────────────────────────────────────────────────────────────── */
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field-label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-dim);
}

.field-hint-inline {
  font-weight: 400;
  color: var(--color-faint);
}

.field-input {
  width: 100%;
  height: 38px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 12px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.86rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.field-input::placeholder {
  color: var(--color-faint);
}

.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.field-input--error {
  border-color: var(--color-red);
}

.field-input--error:focus {
  border-color: var(--color-red);
  box-shadow: 0 0 0 3px var(--color-red-soft);
}

.field-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

/* Select / dropdown */
.select-wrap {
  position: relative;
}

.field-select {
  width: 100%;
  height: 38px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 36px 0 12px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.86rem;
  appearance: none;
  cursor: pointer;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.field-select:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}

.field-select.field-input--error {
  border-color: var(--color-red);
}

.field-select.field-input--error:focus {
  box-shadow: 0 0 0 3px var(--color-red-soft);
}

.field-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Style select options for dark background */
.field-select option {
  background: var(--color-card-2);
  color: var(--color-text);
}

.select-arrow {
  position: absolute;
  right: 11px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-faint);
  pointer-events: none;
}

.field-error {
  font-size: 0.76rem;
  color: var(--color-red);
  line-height: 1.4;
}

.field-hint {
  font-size: 0.74rem;
  color: var(--color-faint);
  line-height: 1.4;
}

.link {
  color: var(--color-primary);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.link:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: 2px;
}

/* ─── test clone row ─────────────────────────────────────────────────────────── */
.test-clone-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: -4px;
}

.test-result {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  font-size: 0.8rem;
  line-height: 1.4;
  flex: 1;
  padding-top: 6px;
}

.test-result--ok {
  color: var(--color-green);
}

.test-result--error {
  color: var(--color-red);
}

.test-branch {
  color: var(--color-dim);
  font-size: 0.76rem;
}

/* ─── delete confirm ─────────────────────────────────────────────────────────── */
.delete-confirm-text {
  font-size: 0.86rem;
  color: var(--color-dim);
  line-height: 1.6;
}

.delete-name {
  color: var(--color-text);
  font-weight: 600;
}

/* ─── spinner ────────────────────────────────────────────────────────────────── */
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

.spinner--dim {
  border-color: oklch(72% 0.008 270 / 0.3);
  border-top-color: var(--color-dim);
}

.spinner--red {
  border-color: oklch(69% 0.17 22 / 0.3);
  border-top-color: var(--color-red);
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spinner { animation: none; border-top-color: currentColor; }
}
</style>

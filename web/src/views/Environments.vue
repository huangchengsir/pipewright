<!--
  Environments.vue — 环境一等公民(对标 GitLab environments)。

  把「环境」做成可观测的一等对象:
    · 按环境聚合部署历史(哪个 run、何时、什么产物、成功/失败、目标机、谁触发)。
    · 每环境一张卡:醒目的「当前活跃版本」头部 + 时间线轨道(最近 N 次部署)。
    · 一键回滚:回滚到「上一次成功部署」(当前活跃版本之前最近一次全成功),复用既有部署链路重发。
    · URL 即状态:projectId 落 query,可分享、可前进后退(web-patterns「URL as state」)。

  数据为既有运行数据上的**只读聚合**(无新表):部署 = 实际向某环境执行过部署(有 deploy_targets)
  的 run;活跃版本 = 最近一次全机成功;回滚目标 = 活跃之前最近一次全机成功。
-->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  listEnvironmentDeployments,
  rollbackEnvironment,
  type EnvironmentTimeline,
} from '../api/environments'
import { canRollback, previousSuccess, shortCommit, toBadgeStatus } from '../api/environments.helpers'
import { listProjects, type Project } from '../api/projects'
import { HttpError } from '../api/http'
import { useConfirm } from '../composables/useConfirm'
import { useToast } from '../composables/useToast'
import AppButton from '../components/ui/AppButton.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import StatusBadge from '../components/ui/StatusBadge.vue'

type LoadState = 'idle' | 'loading' | 'error'

const route = useRoute()
const router = useRouter()
const confirm = useConfirm()
const toast = useToast()

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const timelines = ref<EnvironmentTimeline[]>([])
const projects = ref<Project[]>([])
/** 正在回滚中的环境名(禁用按钮 + loading)。 */
const rollingBack = ref<string>('')

// ─── URL as state ───────────────────────────────────────────────────────────

const projectId = computed<string>(() => {
  const p = route.query.projectId
  return typeof p === 'string' ? p : ''
})

function setProject(id: string): void {
  const next: Record<string, string> = { ...(route.query as Record<string, string>) }
  if (id) next.projectId = id
  else delete next.projectId
  void router.replace({ query: next })
}

function onProjectChange(e: Event): void {
  setProject((e.target as HTMLSelectElement).value)
}

// ─── derived display ──────────────────────────────────────────────────────────

function formatWhen(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

// ─── load ─────────────────────────────────────────────────────────────────────

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    timelines.value = await listEnvironmentDeployments(projectId.value)
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value =
        err.status === 0
          ? '无法连接到服务器,请检查后端是否运行后重试'
          : (err.apiError?.message ?? `加载环境部署历史失败(${err.status})`)
    } else {
      loadError.value = '加载环境部署历史失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

async function loadProjects(): Promise<void> {
  try {
    projects.value = await listProjects()
    // 未选项目且有项目 → 默认选首个(环境历史强依赖某个项目)。
    if (!projectId.value && projects.value.length > 0) {
      setProject(projects.value[0].id)
    }
  } catch {
    projects.value = []
  }
}

// ─── rollback ───────────────────────────────────────────────────────────────

async function onRollback(tl: EnvironmentTimeline): Promise<void> {
  const prev = previousSuccess(tl)
  if (!prev) return
  const ok = await confirm.open({
    title: `回滚环境「${tl.environment}」`,
    body: `将把该环境回滚到上一次成功部署(运行 ${shortCommit(prev.commit)} · ${formatWhen(prev.deployedAt)}),即把那次的产物重新部署到原目标机。此操作会触发一次真实部署。`,
    confirmLabel: '确认回滚',
    variant: 'danger',
  })
  if (!ok) return

  rollingBack.value = tl.environment
  try {
    const res = await rollbackEnvironment(projectId.value, tl.environment)
    const failed = res.targets.filter((t) => t.status === 'failed' || t.status === 'rolled_back').length
    if (failed === 0) {
      toast.success(`环境「${tl.environment}」已回滚`, { detail: `重发产物到 ${res.targets.length} 台目标机` })
    } else {
      toast.error(`环境「${tl.environment}」回滚部分失败`, { detail: `${failed}/${res.targets.length} 台目标机失败` })
    }
    await load()
  } catch (err) {
    const msg =
      err instanceof HttpError ? (err.apiError?.message ?? `回滚失败(${err.status})`) : '回滚失败,请稍后重试'
    toast.error(`环境「${tl.environment}」回滚失败`, { detail: msg })
  } finally {
    rollingBack.value = ''
  }
}

// query 变化 → 重新拉取(URL 即状态的单一数据流)。
watch(projectId, () => void load())

onMounted(() => {
  void loadProjects()
  if (projectId.value) void load()
})
</script>

<template>
  <div class="env-view">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">环境</h1>
        <p class="view-sub">
          按环境聚合的部署历史与当前活跃版本 · 一键回滚到上一次成功部署
        </p>
      </div>
      <AppButton variant="default" :loading="loadState === 'loading'" @click="load">刷新</AppButton>
    </header>

    <!-- Project filter -->
    <div class="env-controls">
      <label class="env-controls__field">
        <span class="env-controls__label">项目</span>
        <select class="select" :value="projectId" @change="onProjectChange" aria-label="按项目筛选">
          <option value="" disabled>请选择项目</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>
    </div>

    <!-- No project selected -->
    <EmptyState
      v-if="!projectId"
      title="请选择一个项目"
      description="环境部署历史按项目聚合,先在上方选择项目。"
    />

    <!-- Error -->
    <ErrorState
      v-else-if="loadState === 'error'"
      title="加载环境部署历史失败"
      :description="loadError"
      @retry="load"
    />

    <!-- Loading skeleton -->
    <div v-else-if="loadState === 'loading' && timelines.length === 0" class="env-grid" aria-busy="true">
      <div v-for="n in 2" :key="n" class="skeleton-card">
        <SkeletonBlock :height="16" width="30%" />
        <SkeletonBlock :height="40" width="70%" />
        <SkeletonBlock :height="14" width="90%" />
        <SkeletonBlock :height="14" width="60%" />
      </div>
    </div>

    <!-- Empty -->
    <EmptyState
      v-else-if="timelines.length === 0"
      title="该项目暂无环境部署历史"
      description="向某环境执行过部署后(webhook 分支映射解析出环境名并完成部署),这里会按环境聚合展示时间线。"
    />

    <!-- Content -->
    <div v-else class="env-grid">
      <article v-for="tl in timelines" :key="tl.environment" class="env-card">
        <!-- Active version hero -->
        <div class="env-card__head">
          <div class="env-card__name-row">
            <h2 class="env-card__name">{{ tl.environment }}</h2>
            <span v-if="tl.active" class="env-card__live" title="当前活跃版本">● 活跃</span>
            <span v-else class="env-card__stale" title="尚无全成功部署">无活跃版本</span>
          </div>

          <div v-if="tl.active" class="env-card__active">
            <div class="env-card__active-meta">
              <code class="env-card__commit">{{ shortCommit(tl.active.commit) }}</code>
              <span class="env-card__branch">{{ tl.active.branch || '—' }}</span>
              <StatusBadge :status="toBadgeStatus(tl.active.status)" />
            </div>
            <div class="env-card__active-sub">
              <span>{{ formatWhen(tl.active.deployedAt) }}</span>
              <span class="env-card__dot" aria-hidden="true">·</span>
              <span>{{ tl.active.triggeredBy || '—' }}</span>
              <span class="env-card__dot" aria-hidden="true">·</span>
              <span>{{ tl.active.targets.length }} 台目标机</span>
            </div>
          </div>

          <AppButton
            variant="default"
            :disabled="!canRollback(tl) || rollingBack === tl.environment"
            :loading="rollingBack === tl.environment"
            :title="canRollback(tl) ? '回滚到上一次成功部署' : '无可回滚的上一次成功部署'"
            @click="onRollback(tl)"
          >
            回滚
          </AppButton>
        </div>

        <!-- Deployment timeline rail -->
        <ol class="env-timeline" :aria-label="`${tl.environment} 部署历史`">
          <li
            v-for="dep in tl.deployments"
            :key="dep.runId"
            class="env-timeline__item"
            :class="{
              'env-timeline__item--active': dep.active,
              'env-timeline__item--failed': dep.status === 'failed',
              'env-timeline__item--partial': dep.status === 'partial_failed',
            }"
          >
            <span class="env-timeline__node" aria-hidden="true" />
            <div class="env-timeline__body">
              <div class="env-timeline__line1">
                <code class="env-timeline__commit">{{ shortCommit(dep.commit) }}</code>
                <StatusBadge :status="toBadgeStatus(dep.status)" />
                <span v-if="dep.active" class="env-timeline__tag">活跃</span>
              </div>
              <div class="env-timeline__line2">
                <span>{{ formatWhen(dep.deployedAt) }}</span>
                <span class="env-card__dot" aria-hidden="true">·</span>
                <span>{{ dep.triggeredBy || '—' }}</span>
                <span class="env-card__dot" aria-hidden="true">·</span>
                <span class="env-timeline__servers">{{ dep.targets.map((t) => t.serverName).join(', ') || '—' }}</span>
              </div>
              <div v-if="dep.artifacts.length > 0" class="env-timeline__artifacts">
                <span v-for="a in dep.artifacts" :key="a.id" class="env-timeline__artifact">
                  {{ a.type }}:{{ a.name }}
                </span>
              </div>
            </div>
          </li>
        </ol>
      </article>
    </div>
  </div>
</template>

<style scoped>
.env-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.view-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.view-title {
  font-size: var(--text-display);
  font-weight: 700;
  letter-spacing: -0.02em;
  margin: 0;
}
.view-sub {
  margin: var(--space-1) 0 0;
  color: var(--color-dim);
  font-size: var(--text-body);
}

.env-controls {
  display: flex;
  gap: var(--space-4);
  align-items: flex-end;
}
.env-controls__field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.env-controls__label {
  font-size: var(--text-label);
  color: var(--color-dim);
  font-weight: 600;
}
.select {
  appearance: none;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  color: var(--color-text);
  padding: 8px 32px 8px 12px;
  font-size: var(--text-body);
  min-width: 220px;
}

.env-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: var(--space-4);
}

.env-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

/* Active version hero */
.env-card__head {
  padding: var(--space-4);
  background: linear-gradient(180deg, var(--color-card-2), var(--color-card));
  border-bottom: 1px solid var(--color-border);
  display: grid;
  grid-template-columns: 1fr auto;
  grid-template-rows: auto auto;
  gap: var(--space-3);
  align-items: center;
}
.env-card__name-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  grid-column: 1 / 2;
}
.env-card__name {
  margin: 0;
  font-size: var(--text-kpi);
  font-weight: 700;
  letter-spacing: -0.01em;
}
.env-card__live {
  font-size: var(--text-micro);
  font-weight: 700;
  color: var(--color-green);
  letter-spacing: 0.02em;
}
.env-card__stale {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.env-card__active {
  grid-column: 1 / 2;
  grid-row: 2 / 3;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.env-card__active-meta {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}
.env-card__commit {
  font-family: var(--font-mono, monospace);
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
}
.env-card__branch {
  font-size: var(--text-label);
  color: var(--color-dim);
}
.env-card__active-sub {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  font-size: var(--text-label);
  color: var(--color-dim);
}
.env-card__dot {
  color: var(--color-faint);
}
/* Rollback button spans both rows on the right. */
.env-card__head > :deep(button) {
  grid-column: 2 / 3;
  grid-row: 1 / 3;
}

/* Timeline rail */
.env-timeline {
  list-style: none;
  margin: 0;
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  position: relative;
}
.env-timeline::before {
  content: '';
  position: absolute;
  left: calc(var(--space-4) + 5px);
  top: var(--space-4);
  bottom: var(--space-4);
  width: 2px;
  background: var(--color-border);
}
.env-timeline__item {
  position: relative;
  display: flex;
  gap: var(--space-3);
  padding-left: var(--space-4);
}
.env-timeline__node {
  position: absolute;
  left: 0;
  top: 4px;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--color-card);
  border: 2px solid var(--color-border-strong);
  z-index: 1;
}
.env-timeline__item--active .env-timeline__node {
  background: var(--color-green);
  border-color: var(--color-green);
  box-shadow: 0 0 0 4px color-mix(in oklch, var(--color-green) 22%, transparent);
}
.env-timeline__item--failed .env-timeline__node {
  border-color: var(--color-red);
}
.env-timeline__item--partial .env-timeline__node {
  border-color: var(--color-primary);
}
.env-timeline__body {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.env-timeline__line1 {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}
.env-timeline__commit {
  font-family: var(--font-mono, monospace);
  font-size: var(--text-label);
  font-weight: 600;
}
.env-timeline__tag {
  font-size: var(--text-micro);
  font-weight: 700;
  color: var(--color-green);
}
.env-timeline__line2 {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  font-size: var(--text-micro);
  color: var(--color-dim);
}
.env-timeline__servers {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 220px;
}
.env-timeline__artifacts {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 2px;
}
.env-timeline__artifact {
  font-size: var(--text-micro);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-sm);
  padding: 1px 6px;
  color: var(--color-dim);
}

.skeleton-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
</style>

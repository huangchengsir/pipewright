<script setup lang="ts">
/*
  Containers.vue — 容器/镜像管理总览。顶部聚合 KPI + 状态筛选,下面每台服务器一张
  ServerCard(卡片内部按 容器/镜像 分 tab,只针对这一台)。日志走抽屉、终端跳全屏页、
  新增容器走弹窗。每 12s 自动刷新聚合(stale-while-revalidate)。
*/
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { getAllContainers, type ServerContainers, type ContainerInfo } from '../api/containers'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import { stateBucket, type StateBucket } from '../lib/containerState'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import ServerCard from '../components/ops/ServerCard.vue'
import ContainerLogsDrawer from '../components/ops/ContainerLogsDrawer.vue'
import CreateContainerModal from '../components/ops/CreateContainerModal.vue'
import AiDiagnosisModal from '../components/ops/AiDiagnosisModal.vue'
import ContainerAiPanel from '../components/ops/ContainerAiPanel.vue'

type LoadState = 'idle' | 'loading' | 'error'
type StateFilter = 'all' | StateBucket

const router = useRouter()

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const groups = ref<ServerContainers[]>([])
const serverById = ref<Map<string, Server>>(new Map())
const refreshing = ref(false)

const POLL_MS = 12_000
let timer: ReturnType<typeof setInterval> | null = null

// ─── 聚合统计 ─────────────────────────────────────────────────────────────────
const totalContainers = computed(() => groups.value.reduce((n, g) => n + g.total, 0))
const runningContainers = computed(() => groups.value.reduce((n, g) => n + g.running, 0))
const stoppedContainers = computed(() => totalContainers.value - runningContainers.value)
const coveredServers = computed(() => groups.value.filter((g) => g.reachable && g.runtime).length)

function serverName(id: string): string {
  return serverById.value.get(id)?.name ?? id
}
function serverHost(id: string): string {
  const s = serverById.value.get(id)
  return s ? `${s.user}@${s.host}:${s.port}` : ''
}

// ─── 状态筛选 ─────────────────────────────────────────────────────────────────
const stateFilter = ref<StateFilter>('all')

const bucketCounts = computed(() => {
  let running = 0
  let paused = 0
  let stopped = 0
  for (const g of groups.value) {
    for (const c of g.containers) {
      const b = stateBucket(c.state)
      if (b === 'running') running++
      else if (b === 'paused') paused++
      else stopped++
    }
  }
  return { all: running + paused + stopped, running, paused, stopped }
})

interface FilterOption {
  key: StateFilter
  label: string
}
const FILTER_OPTIONS: FilterOption[] = [
  { key: 'all', label: '全部' },
  { key: 'running', label: '运行中' },
  { key: 'stopped', label: '已停止' },
  { key: 'paused', label: '已暂停' },
]
function filterCount(key: StateFilter): number {
  return bucketCounts.value[key]
}

// ─── 新增容器 ─────────────────────────────────────────────────────────────────
const showCreate = ref(false)
const creatableServers = computed(() =>
  groups.value
    .filter((g) => g.reachable && g.runtime)
    .map((g) => ({ id: g.serverId, name: serverName(g.serverId) })),
)
function onContainerCreated(): void {
  void load()
}

// ─── 日志抽屉 / 终端 ──────────────────────────────────────────────────────────
const logsTarget = ref<{ serverId: string; name: string; id: string } | null>(null)
function openLogs(serverId: string, c: ContainerInfo): void {
  logsTarget.value = { serverId, name: c.names, id: c.id.length > 12 ? c.id.slice(0, 12) : c.id }
}
function openTerminal(serverId: string, c: ContainerInfo): void {
  const url = router.resolve({
    name: 'server-terminal',
    params: { id: serverId },
    query: { container: c.names, cid: c.id.length > 12 ? c.id.slice(0, 12) : c.id },
  }).href
  window.open(url, '_blank', 'noopener')
}

// AI 助手抽屉。
const showAi = ref(false)
const aiContext = { os: 'linux', shell: '/bin/sh', container: '(docker 主机)' }

// AI 诊断弹窗。
const diagnoseTarget = ref<{ serverId: string; name: string } | null>(null)
function openDiagnose(serverId: string, c: ContainerInfo): void {
  diagnoseTarget.value = { serverId, name: c.names }
}

// ─── 加载 ─────────────────────────────────────────────────────────────────────
async function load(): Promise<void> {
  const firstLoad = groups.value.length === 0
  loadState.value = 'loading'
  loadError.value = ''
  if (!firstLoad) refreshing.value = true
  try {
    const [servers, items] = await Promise.all([listServers(), getAllContainers()])
    const m = new Map<string, Server>()
    for (const s of servers) m.set(s.id, s)
    serverById.value = m
    groups.value = items
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value =
        err.status === 0
          ? '无法连接到服务器,请检查后端是否运行后重试'
          : (err.apiError?.message ?? `加载容器列表失败(${err.status})`)
    } else {
      loadError.value = '加载容器列表失败,请稍后重试'
    }
    loadState.value = 'error'
  } finally {
    refreshing.value = false
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(), POLL_MS)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="containers-view">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">容器</h1>
        <p class="view-sub">
          跨所有已登记服务器,按主机管理容器与镜像
          <span v-if="totalContainers > 0" class="view-sub__count">
            · 共 {{ totalContainers }} 个容器 · {{ runningContainers }} 运行中
          </span>
          <span class="view-sub__count">· 每 12 秒自动刷新</span>
        </p>
      </div>
      <div class="header-actions">
        <AppButton variant="ai" @click="showAi = true">✦ AI 助手</AppButton>
        <AppButton variant="primary" :disabled="creatableServers.length === 0" @click="showCreate = true">
          + 新增容器
        </AppButton>
        <AppButton variant="default" :loading="loadState === 'loading' && groups.length === 0" @click="load">
          刷新
        </AppButton>
      </div>
    </header>

    <!-- 首屏骨架 -->
    <div v-if="loadState === 'loading' && groups.length === 0" class="skeletons" aria-busy="true" aria-label="正在加载容器列表">
      <div v-for="n in 2" :key="n" class="skeleton-panel">
        <SkeletonBlock :height="20" width="40%" />
        <SkeletonBlock :height="48" width="100%" />
        <SkeletonBlock :height="48" width="100%" />
      </div>
    </div>

    <ErrorState
      v-else-if="loadState === 'error' && groups.length === 0"
      title="加载容器列表失败"
      :description="loadError"
      @retry="load"
    />

    <EmptyState
      v-else-if="groups.length === 0"
      title="尚无已登记服务器"
      description="先在「设置 › 服务器」登记目标服务器,这里就会汇总它们上面的容器与镜像。"
    />

    <template v-else>
      <!-- 聚合 KPI 条 -->
      <section class="kpi-strip" aria-label="容器聚合统计">
        <div class="kpi kpi--total">
          <span class="kpi__num">{{ totalContainers }}</span>
          <span class="kpi__label">容器总数</span>
        </div>
        <div class="kpi kpi--running">
          <span class="kpi__num">{{ runningContainers }}</span>
          <span class="kpi__label">运行中</span>
        </div>
        <div class="kpi kpi--stopped">
          <span class="kpi__num">{{ stoppedContainers }}</span>
          <span class="kpi__label">已停止</span>
        </div>
        <div class="kpi kpi--hosts">
          <span class="kpi__num">{{ coveredServers }}<span class="kpi__den">/{{ groups.length }}</span></span>
          <span class="kpi__label">有容器的服务器</span>
        </div>
      </section>

      <!-- 状态筛选段(作用于各卡片的容器 tab) -->
      <div class="filter-bar" role="group" aria-label="按状态筛选容器">
        <button
          v-for="f in FILTER_OPTIONS"
          :key="f.key"
          class="filter-chip"
          :class="{ 'filter-chip--active': stateFilter === f.key }"
          :aria-pressed="stateFilter === f.key"
          @click="stateFilter = f.key"
        >
          {{ f.label }}
          <span class="filter-chip__count">{{ filterCount(f.key) }}</span>
        </button>
      </div>

      <!-- 逐台卡片(卡片内自带 容器/镜像 tab) -->
      <section class="cards" :aria-busy="refreshing || undefined">
        <ServerCard
          v-for="g in groups"
          :key="g.serverId"
          :group="g"
          :name="serverName(g.serverId)"
          :host="serverHost(g.serverId)"
          :state-filter="stateFilter"
          @changed="load"
          @logs="(c) => openLogs(g.serverId, c)"
          @terminal="(c) => openTerminal(g.serverId, c)"
          @diagnose="(c) => openDiagnose(g.serverId, c)"
        />
      </section>
    </template>

    <!-- 新增容器弹窗 -->
    <CreateContainerModal
      v-if="showCreate"
      :servers="creatableServers"
      @close="showCreate = false"
      @created="onContainerCreated"
    />

    <!-- 容器日志抽屉 -->
    <ContainerLogsDrawer
      v-if="logsTarget"
      :server-id="logsTarget.serverId"
      :container-name="logsTarget.name"
      :container-id="logsTarget.id"
      @close="logsTarget = null"
    />

    <!-- AI 诊断弹窗 -->
    <AiDiagnosisModal
      v-if="diagnoseTarget"
      :server-id="diagnoseTarget.serverId"
      :container-name="diagnoseTarget.name"
      @close="diagnoseTarget = null"
    />

    <!-- AI 助手抽屉 -->
    <ContainerAiPanel v-if="showAi" :context="aiContext" @close="showAi = false" />
  </div>
</template>

<style scoped>
.containers-view {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

.view-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.view-header__text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.view-title {
  margin: 0;
  font-size: var(--text-h1);
  font-weight: 700;
  color: var(--color-text);
}
.view-sub {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
}
.view-sub__count {
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}

/* 状态筛选段 */
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 6px 13px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 999px;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-out-expo), border-color var(--duration-fast) var(--ease-out-expo), background var(--duration-fast) var(--ease-out-expo);
}
.filter-chip:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}
.filter-chip--active {
  color: #fff;
  background: var(--color-primary);
  border-color: var(--color-primary);
}
.filter-chip__count {
  font-size: var(--text-micro);
  font-variant-numeric: tabular-nums;
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--color-inset);
  color: var(--color-faint);
}
.filter-chip--active .filter-chip__count {
  background: oklch(100% 0 0 / 0.22);
  color: #fff;
}

/* KPI 条 */
.kpi-strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
}
.kpi {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 16px 18px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  overflow: hidden;
}
.kpi::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--color-faint);
}
.kpi--running::before { background: var(--color-green); }
.kpi--stopped::before { background: var(--color-faint); }
.kpi--total::before { background: var(--color-primary); }
.kpi--hosts::before { background: var(--color-cyan); }
.kpi__num {
  font-size: var(--text-kpi);
  font-weight: 700;
  line-height: 1;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.kpi__den {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-faint);
}
.kpi__label {
  font-size: var(--text-caps);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

/* 卡片列表 */
.cards {
  display: flex;
  flex-direction: column;
  gap: 16px;
  transition: opacity var(--duration-fast) var(--ease-out-expo);
}
.cards[aria-busy='true'] {
  opacity: 0.7;
}

.skeletons {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.skeleton-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-card);
}
</style>

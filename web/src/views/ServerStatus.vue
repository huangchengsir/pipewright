<!--
  ServerStatus.vue — Story 6-1: 多机状态总览(FR-15,服务器层资源指标)

  在一个面板看所有已登记服务器的 CPU 负载 / 内存 / 磁盘使用,免逐台 SSH。
    · 登记服务器列表来自 4-1;指标经 6-1 批量端点并行采集(每台独立)。
    · 某台不可达 → 该卡灰显 + 人读错误,不连累其它台。
    · 某指标缺失(跨平台 best-effort,如 macOS 无 free)→ 该行「不可用」。
    · 「刷新」重取(指标实时、不落库)。

  这是一个**新增**的总览入口,不动 4-1 SettingsServers CRUD、6-2 日志入口。
-->
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { getAllServerMetrics, listServers, type ServerMetrics, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import ServerMetricsCard from '../components/ops/ServerMetricsCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const metrics = ref<ServerMetrics[]>([])
/** serverId → 展示名(来自登记列表,用于卡片标题)。 */
const nameById = ref<Map<string, string>>(new Map())

/** 首次加载尚无数据时显示骨架;刷新时保留旧数据(stale-while-revalidate)。 */
const refreshing = ref(false)

const reachableCount = computed(() => metrics.value.filter((m) => m.reachable).length)
const totalCount = computed(() => metrics.value.length)

function displayName(m: ServerMetrics): string {
  return nameById.value.get(m.serverId) ?? m.serverId
}

async function load(): Promise<void> {
  const firstLoad = metrics.value.length === 0
  loadState.value = 'loading'
  loadError.value = ''
  if (!firstLoad) refreshing.value = true
  try {
    // 并行:登记列表(取展示名)+ 批量指标。互不阻塞。
    const [servers, items] = await Promise.all([
      loadServerNames(),
      getAllServerMetrics(),
    ])
    nameById.value = servers
    metrics.value = items
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value =
        err.status === 0
          ? '无法连接到服务器,请检查后端是否运行后重试'
          : (err.apiError?.message ?? `加载服务器状态失败(${err.status})`)
    } else {
      loadError.value = '加载服务器状态失败,请稍后重试'
    }
    loadState.value = 'error'
  } finally {
    refreshing.value = false
  }
}

async function loadServerNames(): Promise<Map<string, string>> {
  const servers: Server[] = await listServers()
  const m = new Map<string, string>()
  for (const s of servers) m.set(s.id, s.name)
  return m
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="server-status">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">服务器状态</h1>
        <p class="view-sub">
          所有已登记服务器的 CPU 负载、内存与磁盘使用,经 SSH 实时采集
          <span v-if="totalCount > 0" class="view-sub__count">
            · {{ reachableCount }}/{{ totalCount }} 可达
          </span>
        </p>
      </div>
      <AppButton variant="default" :loading="loadState === 'loading'" @click="load">
        刷新
      </AppButton>
    </header>

    <!-- Initial loading skeletons -->
    <div
      v-if="loadState === 'loading' && metrics.length === 0"
      class="metrics-grid"
      aria-busy="true"
      aria-label="正在加载服务器状态"
    >
      <div v-for="n in 3" :key="n" class="skeleton-card">
        <SkeletonBlock :height="20" width="50%" />
        <SkeletonBlock :height="14" width="80%" />
        <SkeletonBlock :height="14" width="80%" />
      </div>
    </div>

    <!-- Load error (whole-page; metrics endpoint unreachable / 5xx) -->
    <ErrorState
      v-else-if="loadState === 'error'"
      title="加载服务器状态失败"
      :description="loadError"
      @retry="load"
    />

    <!-- Empty: no registered servers -->
    <EmptyState
      v-else-if="metrics.length === 0"
      title="尚无已登记服务器"
      description="先在「设置 › 服务器」登记目标服务器,这里就会显示它们的资源指标。"
    />

    <!-- Metrics grid (per-host cards) -->
    <div v-else class="metrics-grid" :aria-busy="refreshing || undefined">
      <ServerMetricsCard
        v-for="m in metrics"
        :key="m.serverId"
        :name="displayName(m)"
        :metrics="m"
      />
    </div>
  </div>
</template>

<style scoped>
.server-status {
  display: flex;
  flex-direction: column;
  gap: 24px;
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
.view-title {
  margin: 0;
  font-size: var(--text-h1, 1.5rem);
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

.metrics-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
  transition: opacity var(--duration-fast) var(--ease-out-expo);
}
.metrics-grid[aria-busy='true'] {
  opacity: 0.65;
}

.skeleton-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  border: 1px solid var(--color-line);
  border-radius: var(--rounded-lg);
  background: var(--color-surface);
}
</style>

<!--
  DoraDashboard.vue — FR-8-15: DORA 四指标仪表盘。

  对既有运行数据做**只读聚合**的交付效能视图:
    · 部署频率 / 变更前置时长 / 变更失败率 / 故障恢复时长(MTTR)。
    · 每指标一张卡(值 + DORA 绩效分档 Elite/High/Medium/Low + 趋势 sparkline)。
    · 顶部汇总条:窗口内部署总数 / 成功 / 失败 + 项目筛选 + 时间窗口切换 + 刷新。
    · URL 即状态:projectId / window 落 query,可分享、可前进后退(web-patterns「URL as state」)。

  口径(与后端 internal/dora 一致):部署=终态运行;成功部署=success;失败=failed/partial/rolled_back;
  前置时长=finished−commit(commit 不可得回退 created 代理);MTTR=失败→恢复成功配对中位数。
  这些是 CI 运行数据上的**近似**口径,卡片 caption 已显式说明样本。
-->
<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  getDoraMetrics,
  formatDuration,
  formatFrequency,
  formatPercent,
  type DoraMetrics,
} from '../api/metrics'
import { listProjects, type Project } from '../api/projects'
import { HttpError } from '../api/http'
import DoraMetricCard from '../components/metrics/DoraMetricCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

type LoadState = 'idle' | 'loading' | 'error'

const route = useRoute()
const router = useRouter()

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const data = ref<DoraMetrics | null>(null)
const projects = ref<Project[]>([])

/** 窗口选项(label 即发给后端的 window 值)。 */
const windowOptions = [
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
  { value: '90d', label: '近 90 天' },
] as const

// ─── URL as state ───────────────────────────────────────────────────────────

const projectId = computed<string>(() => {
  const p = route.query.projectId
  return typeof p === 'string' ? p : ''
})
const windowSel = computed<string>(() => {
  const w = route.query.window
  return typeof w === 'string' && windowOptions.some((o) => o.value === w) ? w : '30d'
})

function setQuery(patch: Record<string, string | undefined>): void {
  const next: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) {
    if (typeof v === 'string' && v !== '') next[k] = v
  }
  void router.replace({ query: next })
}

function onProjectChange(e: Event): void {
  setQuery({ projectId: (e.target as HTMLSelectElement).value || undefined })
}
function onWindowChange(value: string): void {
  setQuery({ window: value })
}

// ─── derived display ──────────────────────────────────────────────────────────

const generatedAtLabel = computed(() => {
  if (!data.value?.generatedAt) return ''
  const d = new Date(data.value.generatedAt)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
})

const deployTrend = computed(() => (data.value?.trend ?? []).map((t) => t.deployments))
const successTrend = computed(() => (data.value?.trend ?? []).map((t) => t.successes))
const cfrTrend = computed(() => (data.value?.trend ?? []).map((t) => t.changeFailureRate))

const windowDaysLabel = computed(() => data.value?.windowDays ?? 30)

// ─── load ─────────────────────────────────────────────────────────────────────

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const res = await getDoraMetrics({ projectId: projectId.value, window: windowSel.value })
    data.value = res
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value =
        err.status === 0
          ? '无法连接到服务器,请检查后端是否运行后重试'
          : (err.apiError?.message ?? `加载 DORA 指标失败(${err.status})`)
    } else {
      loadError.value = '加载 DORA 指标失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

async function loadProjects(): Promise<void> {
  try {
    projects.value = await listProjects()
  } catch {
    // 项目下拉失败不阻断主指标:留空(只能看全部项目)。
    projects.value = []
  }
}

// query 变化 → 重新拉取(URL 即状态的单一数据流)。
watch([projectId, windowSel], () => void load())

onMounted(() => {
  void loadProjects()
  void load()
})
</script>

<template>
  <div class="dora-view">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">DORA 指标</h1>
        <p class="view-sub">
          基于既有运行数据聚合的交付效能视图 · 部署频率 / 前置时长 / 变更失败率 / 故障恢复
          <span v-if="generatedAtLabel" class="view-sub__count">· 数据截至 {{ generatedAtLabel }}</span>
        </p>
      </div>
      <AppButton variant="default" :loading="loadState === 'loading'" @click="load">刷新</AppButton>
    </header>

    <!-- Controls: project filter + window segmented -->
    <div class="dora-controls">
      <label class="dora-controls__field">
        <span class="dora-controls__label">项目</span>
        <select class="select" :value="projectId" @change="onProjectChange" aria-label="按项目筛选">
          <option value="">全部项目</option>
          <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </label>

      <div class="dora-segmented" role="group" aria-label="时间窗口">
        <button
          v-for="opt in windowOptions"
          :key="opt.value"
          type="button"
          class="dora-segmented__btn"
          :class="{ 'dora-segmented__btn--active': windowSel === opt.value }"
          :aria-pressed="windowSel === opt.value"
          @click="onWindowChange(opt.value)"
        >
          {{ opt.label }}
        </button>
      </div>
    </div>

    <!-- Error -->
    <ErrorState
      v-if="loadState === 'error'"
      title="加载 DORA 指标失败"
      :description="loadError"
      @retry="load"
    />

    <!-- Loading skeleton (first load) -->
    <div v-else-if="loadState === 'loading' && !data" class="dora-grid" aria-busy="true">
      <div v-for="n in 4" :key="n" class="skeleton-card">
        <SkeletonBlock :height="14" width="40%" />
        <SkeletonBlock :height="32" width="60%" />
        <SkeletonBlock :height="14" width="80%" />
      </div>
    </div>

    <!-- Content -->
    <template v-else-if="data">
      <!-- Summary strip -->
      <div class="dora-summary">
        <div class="dora-summary__stat">
          <span class="dora-summary__num">{{ data.totalDeployments }}</span>
          <span class="dora-summary__lbl">{{ windowDaysLabel }} 天内部署</span>
        </div>
        <div class="dora-summary__divider" aria-hidden="true" />
        <div class="dora-summary__stat">
          <span class="dora-summary__num dora-summary__num--ok">{{ data.successfulDeployments }}</span>
          <span class="dora-summary__lbl">成功</span>
        </div>
        <div class="dora-summary__stat">
          <span class="dora-summary__num dora-summary__num--bad">{{ data.failedDeployments }}</span>
          <span class="dora-summary__lbl">失败</span>
        </div>
      </div>

      <!-- 4 metric cards (bento-ish 2×2; wide screens 4-up) -->
      <div class="dora-grid">
        <DoraMetricCard
          title="部署频率"
          subtitle="Deployment Frequency"
          :display="formatFrequency(data.metrics.deploymentFrequency.value)"
          :caption="`${windowDaysLabel} 天内 ${data.successfulDeployments} 次成功部署`"
          :band="data.metrics.deploymentFrequency.band"
          :sample-count="data.metrics.deploymentFrequency.sampleCount"
          :trend="successTrend"
        />
        <DoraMetricCard
          title="变更前置时长"
          subtitle="Lead Time for Changes"
          :display="formatDuration(data.metrics.leadTime.value)"
          :caption="
            data.metrics.leadTime.sampleCount > 0
              ? `${data.metrics.leadTime.sampleCount} 次成功部署的中位提交→投产时长`
              : '尚无成功部署可统计前置时长'
          "
          :band="data.metrics.leadTime.band"
          :sample-count="data.metrics.leadTime.sampleCount"
          :trend="deployTrend"
        />
        <DoraMetricCard
          title="变更失败率"
          subtitle="Change Failure Rate"
          :display="formatPercent(data.metrics.changeFailureRate.value)"
          :caption="`${data.failedDeployments} / ${data.totalDeployments} 次部署失败`"
          :band="data.metrics.changeFailureRate.band"
          :sample-count="data.metrics.changeFailureRate.sampleCount"
          :trend="cfrTrend"
        />
        <DoraMetricCard
          title="故障恢复时长"
          subtitle="Time to Restore (MTTR)"
          :display="formatDuration(data.metrics.mttr.value)"
          :caption="
            data.metrics.mttr.sampleCount > 0
              ? `${data.metrics.mttr.sampleCount} 段「失败→恢复」的中位时长`
              : '窗口内无「失败后恢复」配对'
          "
          :band="data.metrics.mttr.band"
          :sample-count="data.metrics.mttr.sampleCount"
          :trend="cfrTrend"
        />
      </div>

      <p class="dora-note">
        口径说明:一次「部署」= 一条进入终态的运行;前置时长在缺少提交时间时以入队时刻近似。
        DORA 指标基于 CI 运行数据为<strong>近似</strong>参考,不作 SLA 依据。
      </p>
    </template>
  </div>
</template>

<style scoped>
.dora-view {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
}

.view-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.view-header__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.view-title {
  margin: 0;
  font-size: var(--text-display);
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
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

/* Controls */
.dora-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-4);
}
.dora-controls__field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.dora-controls__label {
  font-size: var(--text-micro);
  text-transform: uppercase;
  letter-spacing: 0.07em;
  color: var(--color-faint);
}
.select {
  appearance: none;
  padding: 7px 30px 7px 11px;
  font-size: var(--text-body);
  color: var(--color-text);
  background: var(--color-card);
  background-image: linear-gradient(45deg, transparent 50%, var(--color-dim) 50%),
    linear-gradient(135deg, var(--color-dim) 50%, transparent 50%);
  background-position:
    calc(100% - 16px) center,
    calc(100% - 11px) center;
  background-size: 5px 5px, 5px 5px;
  background-repeat: no-repeat;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  min-width: 180px;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-out-expo);
}
.select:hover {
  border-color: var(--color-primary);
}
.select:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

/* Segmented window switch */
.dora-segmented {
  display: inline-flex;
  padding: 3px;
  gap: 2px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
}
.dora-segmented__btn {
  padding: 5px 13px;
  font-size: var(--text-label);
  color: var(--color-dim);
  background: transparent;
  border: none;
  border-radius: var(--rounded-sm);
  cursor: pointer;
  transition:
    background var(--duration-fast) var(--ease-out-expo),
    color var(--duration-fast) var(--ease-out-expo);
}
.dora-segmented__btn:hover {
  color: var(--color-text);
}
.dora-segmented__btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}
.dora-segmented__btn--active {
  color: var(--color-text);
  background: var(--color-card);
  box-shadow: var(--shadow);
  font-weight: 600;
}

/* Summary strip */
.dora-summary {
  display: flex;
  align-items: center;
  gap: var(--space-6);
  padding: var(--space-4) var(--space-5);
  background: linear-gradient(135deg, var(--color-card-2), var(--color-card));
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
}
.dora-summary__stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.dora-summary__num {
  font-size: var(--text-kpi);
  font-weight: 700;
  line-height: 1;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.dora-summary__num--ok {
  color: var(--color-green);
}
.dora-summary__num--bad {
  color: var(--color-red);
}
.dora-summary__lbl {
  font-size: var(--text-micro);
  color: var(--color-dim);
}
.dora-summary__divider {
  width: 1px;
  align-self: stretch;
  background: var(--color-border);
}

/* Cards grid: 2×2 on medium, 4-up on wide */
.dora-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-4);
}
@media (min-width: 1180px) {
  .dora-grid {
    grid-template-columns: repeat(4, 1fr);
  }
}
@media (max-width: 640px) {
  .dora-grid {
    grid-template-columns: 1fr;
  }
}

.skeleton-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-5);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  background: var(--color-card);
}

.dora-note {
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
  line-height: 1.5;
}
.dora-note strong {
  color: var(--color-dim);
  font-weight: 600;
}
</style>

<script setup lang="ts">
/**
 * Dashboard — 全局概览首页(container)。
 *
 * 并行(Promise.allSettled,单点失败不拖垮整页)拉取真实数据:运行 / 项目 / 服务器 + 指标 /
 * DORA / 异常告警 / AI 诊断 / 各项目环境部署,派生 KPI 并以 bento 网格组织各 presentational widget。
 * 无数据各 widget 自行降级到空状态(自托管首启即可用,不白屏)。
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { listRuns, type RunListItem } from '../api/runs'
import { listProjects, type Project } from '../api/projects'
import { listServers, getAllServerMetrics, type Server, type ServerMetrics } from '../api/servers'
import { listEnvironmentDeployments } from '../api/environments'
import { getDoraMetrics, type DoraMetrics, formatFrequency, formatDuration, formatPercent } from '../api/metrics'
import { listAnomalyAlerts, type AnomalyAlert } from '../api/anomaly'
import { getDiagnosisStats, type DiagnosisStats } from '../api/settings'

import DashRecentRuns from '../components/dashboard/DashRecentRuns.vue'
import DashServers from '../components/dashboard/DashServers.vue'
import DashEnvironments, { type DashEnvEntry } from '../components/dashboard/DashEnvironments.vue'
import DashAlerts from '../components/dashboard/DashAlerts.vue'
import DoraMetricCard from '../components/metrics/DoraMetricCard.vue'
import '../components/dashboard/dashboard.css'

const router = useRouter()
const { t } = useI18n()

const loading = ref(true)
const refreshing = ref(false)

const runs = ref<RunListItem[]>([])
const runsTotal = ref(0)
const runningTotal = ref(0)
const projects = ref<Project[]>([])
const servers = ref<Server[]>([])
const serverMetrics = ref<ServerMetrics[]>([])
const dora = ref<DoraMetrics | null>(null)
const alerts = ref<AnomalyAlert[]>([])
const diagnosis = ref<DiagnosisStats | null>(null)
const envEntries = ref<DashEnvEntry[]>([])

/** allSettled 取值助手:fulfilled 返回值,否则 fallback。 */
function val<T>(r: PromiseSettledResult<T>, fallback: T): T {
  return r.status === 'fulfilled' ? r.value : fallback
}

async function load(isRefresh = false): Promise<void> {
  if (isRefresh) refreshing.value = true
  else loading.value = true

  const [recentR, runningR, projectsR, serversR, metricsR, doraR, alertsR, diagR] = await Promise.allSettled([
    listRuns({ pageSize: 12 }),
    listRuns({ status: 'running', pageSize: 1 }),
    listProjects(),
    listServers(),
    getAllServerMetrics(),
    getDoraMetrics({}),
    listAnomalyAlerts({ limit: 6 }),
    getDiagnosisStats(),
  ])

  const recent = val(recentR, { items: [], page: 1, total: 0 })
  runs.value = recent.items
  runsTotal.value = recent.total
  runningTotal.value = val(runningR, { items: [], page: 1, total: 0 }).total
  projects.value = val(projectsR, [])
  servers.value = val(serversR, [])
  serverMetrics.value = val(metricsR, [])
  dora.value = doraR.status === 'fulfilled' ? doraR.value : null
  alerts.value = val(alertsR, [])
  diagnosis.value = diagR.status === 'fulfilled' ? diagR.value : null

  // 环境:逐项目拉部署时间线并聚合(自托管项目数少,并行可控)。
  const envResults = await Promise.allSettled(
    projects.value.map((p) =>
      listEnvironmentDeployments(p.id).then((timelines) =>
        timelines.map<DashEnvEntry>((t) => ({
          projectId: p.id,
          projectName: p.name,
          environment: t.environment,
          active: t.active,
        })),
      ),
    ),
  )
  const flat = envResults.flatMap((r) => (r.status === 'fulfilled' ? r.value : []))
  // 已部署的(有 active)按部署时间倒序在前,未部署的在后。
  flat.sort((a, b) => {
    const ta = a.active ? new Date(a.active.deployedAt).getTime() : 0
    const tb = b.active ? new Date(b.active.deployedAt).getTime() : 0
    return tb - ta
  })
  envEntries.value = flat

  loading.value = false
  refreshing.value = false
}

// ─── KPI 派生 ───────────────────────────────────────────────
const successRate = computed<number | null>(() => {
  const terminal = runs.value.filter((r) =>
    ['success', 'failed', 'partial_failed', 'rolled_back'].includes(r.status),
  )
  if (!terminal.length) return null
  const ok = terminal.filter((r) => r.status === 'success').length
  return Math.round((ok / terminal.length) * 100)
})
const serversOnline = computed(() => serverMetrics.value.filter((m) => m.reachable).length)

const kpis = computed(() => [
  { label: t('dashboard.kpiProjects'), value: String(projects.value.length), to: '/projects', accent: 'primary' },
  { label: t('dashboard.kpiRuns'), value: String(runsTotal.value), to: '/runs', accent: 'neutral' },
  {
    label: t('dashboard.kpiSuccess'),
    value: successRate.value === null ? '—' : `${successRate.value}%`,
    suffix: successRate.value === null ? '' : t('dashboard.kpiSuccessSuffix'),
    to: '/runs',
    accent: successRate.value === null ? 'neutral' : successRate.value >= 90 ? 'ok' : successRate.value >= 70 ? 'warn' : 'err',
  },
  { label: t('dashboard.kpiRunning'), value: String(runningTotal.value), to: '/runs', accent: runningTotal.value > 0 ? 'run' : 'neutral' },
  {
    label: t('dashboard.kpiServers'),
    value: servers.value.length ? `${serversOnline.value}/${servers.value.length}` : '0',
    to: '/server-status',
    accent: servers.value.length && serversOnline.value < servers.value.length ? 'warn' : 'ok',
  },
])

// ─── DORA 4 卡(复用 DoraMetricCard)─────────────────────────
const doraTrend = computed(() => dora.value?.trend ?? [])
const deployTrend = computed(() => doraTrend.value.map((p) => p.deployments))
const cfrTrend = computed(() => doraTrend.value.map((p) => p.changeFailureRate))

onMounted(load)
</script>

<template>
  <div class="dashboard">
    <header class="dash-header">
      <div>
        <h1 class="dash-title">{{ t('dashboard.title') }}</h1>
        <p class="dash-sub">{{ t('dashboard.subtitle') }}</p>
      </div>
      <div class="dash-actions">
        <button class="dash-ghost" type="button" :disabled="refreshing" @click="load(true)">
          <svg class="dash-refresh-icon" :class="{ spin: refreshing }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M1 4v6h6M23 20v-6h-6" /><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
          </svg>
          {{ t('common.refresh') }}
        </button>
        <button class="dash-primary" type="button" @click="router.push('/projects')">{{ t('dashboard.newProject') }}</button>
      </div>
    </header>

    <!-- KPI 行 -->
    <section class="kpi-row" :aria-label="t('dashboard.kpiAria')">
      <button v-for="k in kpis" :key="k.label" class="kpi" :class="`kpi--${k.accent}`" type="button" @click="router.push(k.to)">
        <span class="kpi-label">{{ k.label }}</span>
        <strong class="kpi-value">{{ k.value }}</strong>
        <span v-if="k.suffix" class="kpi-suffix">{{ k.suffix }}</span>
      </button>
    </section>

    <!-- bento 网格 -->
    <div class="bento">
      <div class="bento-runs"><DashRecentRuns :runs="runs" :loading="loading" /></div>
      <div class="bento-env"><DashEnvironments :entries="envEntries" :loading="loading" /></div>
      <div class="bento-srv"><DashServers :servers="servers" :metrics="serverMetrics" :loading="loading" /></div>
      <div class="bento-alerts"><DashAlerts :alerts="alerts" :diagnosis="diagnosis" :loading="loading" /></div>

      <section class="card bento-dora" aria-labelledby="dash-dora-h">
        <header class="card-head">
          <h2 id="dash-dora-h" class="card-title">{{ t('dashboard.doraTitle') }} <span class="dora-window">{{ dora?.window ?? '30d' }}</span></h2>
          <button class="card-link" type="button" @click="router.push('/metrics/dora')">{{ t('common.detailArrow') }}</button>
        </header>
        <div v-if="loading" class="dora-skeleton"><span v-for="i in 4" :key="i" /></div>
        <div v-else-if="dora" class="dora-grid">
          <DoraMetricCard
            :title="t('dashboard.doraDeployFreq')" :subtitle="t('dashboard.subDeployFreq')"
            :display="dora.metrics.deploymentFrequency.sampleCount ? formatFrequency(dora.deploymentFrequency) : '—'"
            :caption="t('dashboard.capDeployments', { days: dora.windowDays, count: dora.totalDeployments })"
            :band="dora.metrics.deploymentFrequency.band" :sampleCount="dora.metrics.deploymentFrequency.sampleCount" :trend="deployTrend"
          />
          <DoraMetricCard
            :title="t('dashboard.doraLeadTime')" :subtitle="t('dashboard.subLeadTime')"
            :display="dora.metrics.leadTime.sampleCount ? formatDuration(dora.leadTimeSeconds) : '—'"
            :caption="t('dashboard.capLeadTime')"
            :band="dora.metrics.leadTime.band" :sampleCount="dora.metrics.leadTime.sampleCount" :trend="deployTrend"
          />
          <DoraMetricCard
            :title="t('dashboard.doraCfr')" :subtitle="t('dashboard.subCfr')"
            :display="dora.metrics.changeFailureRate.sampleCount ? formatPercent(dora.changeFailureRate) : '—'"
            :caption="t('dashboard.capCfr', { failed: dora.failedDeployments, total: dora.totalDeployments })"
            :band="dora.metrics.changeFailureRate.band" :sampleCount="dora.metrics.changeFailureRate.sampleCount" :trend="cfrTrend"
          />
          <DoraMetricCard
            :title="t('dashboard.doraMttr')" :subtitle="t('dashboard.subMttr')"
            :display="dora.metrics.mttr.sampleCount ? formatDuration(dora.mttrSeconds) : '—'"
            :caption="t('dashboard.capMttr')"
            :band="dora.metrics.mttr.band" :sampleCount="dora.metrics.mttr.sampleCount" :trend="cfrTrend"
          />
        </div>
        <p v-else class="card-empty">{{ t('dashboard.doraEmpty') }}</p>
      </section>
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
.dash-header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
  padding-bottom: var(--space-2);
}
.dash-title {
  font-size: var(--text-display);
  font-weight: 800;
  letter-spacing: -0.02em;
  color: var(--color-text);
}
.dash-sub {
  font-size: var(--text-body);
  color: var(--color-faint);
  margin-top: 2px;
}
.dash-actions {
  display: flex;
  gap: 10px;
  flex: none;
}
.dash-ghost,
.dash-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  font-size: var(--text-label);
  font-weight: 600;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast), transform var(--duration-fast);
}
.dash-ghost {
  color: var(--color-text-soft);
  background: var(--color-card);
  border: 1px solid var(--color-border);
}
.dash-ghost:hover:not(:disabled) {
  background: var(--color-surface-hover);
}
.dash-ghost:disabled {
  opacity: 0.6;
  cursor: progress;
}
.dash-primary {
  color: #fff;
  background: var(--color-primary);
  border: none;
}
.dash-primary:hover {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}
.dash-refresh-icon.spin {
  animation: dash-spin 0.8s linear infinite;
}
@keyframes dash-spin {
  to {
    transform: rotate(360deg);
  }
}

/* KPI 行 */
.kpi-row {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 12px;
}
.kpi {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px 18px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  cursor: pointer;
  text-align: left;
  overflow: hidden;
  transition: transform var(--duration-fast), box-shadow var(--duration-fast), border-color var(--duration-fast);
}
.kpi::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--accent, var(--color-border-strong));
}
.kpi:hover {
  transform: translateY(-2px);
  box-shadow: 0 10px 24px -16px rgba(0, 0, 0, 0.3);
  border-color: var(--color-border-strong);
}
.kpi--primary {
  --accent: var(--color-primary);
}
.kpi--ok {
  --accent: var(--color-green);
}
.kpi--warn {
  --accent: var(--color-amber);
}
.kpi--err {
  --accent: var(--color-red);
}
.kpi--run {
  --accent: var(--color-cyan);
}
.kpi--neutral {
  --accent: var(--color-border-strong);
}
.kpi-label {
  font-size: var(--text-micro);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-faint);
}
.kpi-value {
  font-size: var(--text-kpi);
  font-weight: 800;
  line-height: 1;
  letter-spacing: -0.02em;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.kpi-suffix {
  font-size: var(--text-micro);
  color: var(--color-faint);
}

/* bento 网格 */
.bento {
  display: grid;
  grid-template-columns: 1.5fr 1fr;
  gap: 16px;
}
.bento-runs {
  grid-column: 1;
}
.bento-env {
  grid-column: 2;
}
.bento-srv {
  grid-column: 1;
}
.bento-alerts {
  grid-column: 2;
}
.bento-dora {
  grid-column: 1 / -1;
}
.dora-window {
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-faint);
  font-family: var(--font-mono);
  margin-left: 4px;
}
.dora-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}
.dora-skeleton {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
}
.dora-skeleton span {
  height: 150px;
  border-radius: var(--radius-md);
  background: linear-gradient(90deg, var(--color-inset), var(--color-surface-hover), var(--color-inset));
  background-size: 200% 100%;
  animation: dash-shimmer 1.3s ease-in-out infinite;
}
@keyframes dash-shimmer {
  to {
    background-position: -200% 0;
  }
}

@media (max-width: 1080px) {
  .kpi-row {
    grid-template-columns: repeat(3, 1fr);
  }
  .bento {
    grid-template-columns: 1fr;
  }
  .bento-runs,
  .bento-env,
  .bento-srv,
  .bento-alerts,
  .bento-dora {
    grid-column: 1;
  }
  .dora-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
@media (max-width: 560px) {
  .kpi-row {
    grid-template-columns: repeat(2, 1fr);
  }
  .dora-grid {
    grid-template-columns: 1fr;
  }
}
</style>

<script setup lang="ts">
/**
 * Dashboard — 全局概览首页(container)。
 *
 * 并行(Promise.allSettled,单点失败不拖垮整页)拉取真实数据:运行 / 项目 / 服务器 + 指标 /
 * DORA / 异常告警 / AI 诊断 / 各项目环境部署,派生 KPI 并以 bento 网格组织各 presentational widget。
 * 无数据各 widget 自行降级到空状态(自托管首启即可用,不白屏)。
 */
import { ref, computed, onMounted, onUnmounted, reactive } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";

import { listRuns, type RunListItem } from "../api/runs";
import { listProjects, type Project } from "../api/projects";
import {
  listServers,
  getAllServerMetrics,
  type Server,
  type ServerMetrics,
} from "../api/servers";
import { listEnvironmentDeployments } from "../api/environments";
import {
  getDoraMetrics,
  type DoraMetrics,
  formatFrequency,
  formatDuration,
  formatPercent,
} from "../api/metrics";
import { listAnomalyAlerts, type AnomalyAlert } from "../api/anomaly";
import { getDiagnosisStats, type DiagnosisStats } from "../api/settings";

import DashDataState from "../components/dashboard/DashDataState.vue";
import { HttpError } from "../api/http";
import DashRecentRuns from "../components/dashboard/DashRecentRuns.vue";
import DashServers from "../components/dashboard/DashServers.vue";
import DashEnvironments, {
  type DashEnvEntry,
} from "../components/dashboard/DashEnvironments.vue";
import DashAlerts from "../components/dashboard/DashAlerts.vue";
import DoraMetricCard from "../components/metrics/DoraMetricCard.vue";
import "../components/dashboard/dashboard.css";

const router = useRouter();
const { t } = useI18n();

type ResourceKey =
  | "recent"
  | "running"
  | "projects"
  | "servers"
  | "metrics"
  | "dora"
  | "alerts"
  | "diagnosis"
  | "environments";
interface ResourceState {
  pending: boolean;
  updatedAt: number;
  error: boolean;
  forbidden: boolean;
  generation: number;
}
const keys: ResourceKey[] = [
  "recent",
  "running",
  "projects",
  "servers",
  "metrics",
  "dora",
  "alerts",
  "diagnosis",
  "environments",
];
const resources = reactive(
  Object.fromEntries(
    keys.map((k) => [
      k,
      {
        pending: true,
        updatedAt: 0,
        error: false,
        forbidden: false,
        generation: 0,
      },
    ]),
  ) as Record<ResourceKey, ResourceState>,
);
let disposed = false;
const refreshing = computed(() => keys.some((k) => resources[k].pending));
function state(
  keys: ResourceKey[],
  empty = false,
): "loading" | "ready" | "empty" | "error" | "stale" {
  const states = keys.map((k) => resources[k]);
  if (states.some((s) => s.pending && !s.updatedAt)) return "loading";
  if (states.some((s) => s.error))
    return states.every((s) => s.updatedAt) ? "stale" : "error";
  return empty ? "empty" : "ready";
}
function updated(keys: ResourceKey[]): number {
  return Math.min(...keys.map((k) => resources[k].updatedAt));
}
function pending(keys: ResourceKey[]): boolean {
  return keys.some((k) => resources[k].pending);
}
function forbidden(keys: ResourceKey[]): boolean {
  return keys.some((k) => resources[k].forbidden);
}
function known(key: ResourceKey): boolean {
  return resources[key].updatedAt > 0;
}

const runs = ref<RunListItem[]>([]);
const runsTotal = ref(0);
const runningTotal = ref(0);
const projects = ref<Project[]>([]);
const servers = ref<Server[]>([]);
const serverMetrics = ref<ServerMetrics[]>([]);
const dora = ref<DoraMetrics | null>(null);
const alerts = ref<AnomalyAlert[]>([]);
const diagnosis = ref<DiagnosisStats | null>(null);
const envEntries = ref<DashEnvEntry[]>([]);

async function fetchEnvironments(): Promise<void> {
  // Fetch the dependency on every local retry. A failed project or environment
  // response cannot turn an unknown deployment into an empty/healthy result.
  const allProjects = await listProjects();
  const groups = await Promise.all(
    allProjects.map((p) =>
      listEnvironmentDeployments(p.id).then((timelines) =>
        timelines.map<DashEnvEntry>((t) => ({
          projectId: p.id,
          projectName: p.name,
          environment: t.environment,
          active: t.active,
        })),
      ),
    ),
  );
  const flat = groups.flat();
  flat.sort(
    (a, b) =>
      (b.active ? new Date(b.active.deployedAt).getTime() : 0) -
      (a.active ? new Date(a.active.deployedAt).getTime() : 0),
  );
  envEntries.value = flat;
}
const loaders: Record<ResourceKey, () => Promise<void>> = {
  recent: async () => {
    const v = await listRuns({ pageSize: 12 });
    runs.value = v.items;
    runsTotal.value = v.total;
  },
  running: async () => {
    runningTotal.value = (
      await listRuns({ status: "running", pageSize: 1 })
    ).total;
  },
  projects: async () => {
    projects.value = await listProjects();
  },
  servers: async () => {
    servers.value = await listServers();
  },
  metrics: async () => {
    serverMetrics.value = await getAllServerMetrics();
  },
  dora: async () => {
    dora.value = await getDoraMetrics({});
  },
  alerts: async () => {
    alerts.value = await listAnomalyAlerts({ limit: 6 });
  },
  diagnosis: async () => {
    diagnosis.value = await getDiagnosisStats();
  },
  environments: fetchEnvironments,
};
async function retry(targets: ResourceKey[]): Promise<void> {
  await Promise.all(
    targets.map(async (key) => {
      const st = resources[key];
      if (st.pending && st.generation > 0) return;
      const generation = ++st.generation;
      st.pending = true;
      try {
        await loaders[key]();
        if (disposed || st.generation !== generation) return;
        st.updatedAt = Date.now();
        st.error = false;
        st.forbidden = false;
      } catch (err) {
        if (disposed || st.generation !== generation) return;
        st.error = true;
        st.forbidden = err instanceof HttpError && err.status === 403;
      } finally {
        if (!disposed && st.generation === generation) st.pending = false;
      }
    }),
  );
}
async function load(): Promise<void> {
  await retry(keys);
}
onUnmounted(() => {
  disposed = true;
});

// ─── KPI 派生 ───────────────────────────────────────────────
const originalTerminalRuns = computed(() =>
  runs.value.filter(
    (r) =>
      r.executionKind !== "recovery" &&
      ["success", "failed", "partial_failed", "rolled_back"].includes(r.status),
  ),
);
const successRate = computed<number | null>(() => {
  const terminal = originalTerminalRuns.value;
  if (!terminal.length) return null;
  const ok = terminal.filter((r) => r.status === "success").length;
  return Math.round((ok / terminal.length) * 100);
});
const serversUnknown = computed(
  () =>
    servers.value.filter(
      (s) => !serverMetrics.value.some((m) => m.serverId === s.id),
    ).length,
);
const serversOnline = computed(
  () =>
    servers.value.filter((s) =>
      serverMetrics.value.some((m) => m.serverId === s.id && m.reachable),
    ).length,
);

const kpiSources: ResourceKey[][] = [
  ["projects"],
  ["recent"],
  ["recent"],
  ["running"],
  ["servers", "metrics"],
];
const kpis = computed(() => [
  {
    label: t("dashboard.kpiProjects"),
    value: known("projects") ? String(projects.value.length) : "—",
    to: "/projects",
    accent: "primary",
  },
  {
    label: t("dashboard.kpiRuns"),
    value: known("recent") ? String(runsTotal.value) : "—",
    to: "/runs",
    accent: "neutral",
  },
  {
    label: t("dashboard.kpiSuccess"),
    value: successRate.value === null ? "—" : `${successRate.value}%`,
    suffix:
      successRate.value === null
        ? ""
        : t("runRetry.originalSamples", {
            count: originalTerminalRuns.value.length,
          }),
    to: "/runs",
    accent:
      successRate.value === null
        ? "neutral"
        : successRate.value >= 90
          ? "ok"
          : successRate.value >= 70
            ? "warn"
            : "err",
  },
  {
    label: t("dashboard.kpiRunning"),
    value: known("running") ? String(runningTotal.value) : "—",
    to: "/runs",
    accent: runningTotal.value > 0 ? "run" : "neutral",
  },
  {
    label: t("dashboard.kpiServers"),
    value:
      !known("servers") || !known("metrics")
        ? "—"
        : serversUnknown.value
          ? `—/${servers.value.length}`
          : servers.value.length
            ? `${serversOnline.value}/${servers.value.length}`
            : "0",
    to: "/server-status",
    suffix: serversUnknown.value
      ? t("runExperience.unknownCount", { n: serversUnknown.value })
      : "",
    accent:
      serversUnknown.value || !known("metrics")
        ? "neutral"
        : servers.value.length && serversOnline.value < servers.value.length
          ? "warn"
          : "ok",
  },
]);

// ─── DORA 4 卡(复用 DoraMetricCard)─────────────────────────
const doraTrend = computed(() => dora.value?.trend ?? []);
const deployTrend = computed(() => doraTrend.value.map((p) => p.deployments));
const cfrTrend = computed(() =>
  doraTrend.value.map((p) => p.changeFailureRate),
);

onMounted(load);
</script>

<template>
  <div class="dashboard">
    <header class="dash-header">
      <div>
        <h1 class="dash-title">{{ t("dashboard.title") }}</h1>
        <p class="dash-sub">{{ t("dashboard.subtitle") }}</p>
      </div>
      <div class="dash-actions">
        <button
          class="dash-ghost"
          type="button"
          :disabled="refreshing"
          @click="load()"
        >
          <svg
            class="dash-refresh-icon"
            :class="{ spin: refreshing }"
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2.2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M1 4v6h6M23 20v-6h-6" />
            <path
              d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"
            />
          </svg>
          {{ t("common.refresh") }}
        </button>
        <button
          class="dash-primary"
          type="button"
          @click="router.push('/projects')"
        >
          {{ t("dashboard.newProject") }}
        </button>
      </div>
    </header>

    <!-- KPI 行 -->
    <section class="kpi-row" :aria-label="t('dashboard.kpiAria')">
      <button
        v-for="(k, index) in kpis"
        :key="k.label"
        class="kpi"
        :class="`kpi--${k.accent}`"
        type="button"
        @click="router.push(k.to)"
      >
        <span class="kpi-label">{{ k.label }}</span>
        <strong class="kpi-value">{{ k.value }}</strong>
        <span
          v-if="
            state(kpiSources[index]!) === 'error' ||
            state(kpiSources[index]!) === 'stale'
          "
          class="kpi-suffix"
          >{{ t(`runExperience.${state(kpiSources[index]!)}`) }}</span
        >
        <span v-if="k.suffix" class="kpi-suffix">{{ k.suffix }}</span>
      </button>
    </section>

    <div
      v-if="
        ['projects', 'running'].some((k) => resources[k as ResourceKey].error)
      "
      class="dash-kpi-errors"
    >
      <DashDataState
        v-for="key in ['projects', 'running'] as ResourceKey[]"
        :key="key"
        v-show="resources[key].error"
        :state="state([key])"
        :updated-at="updated([key])"
        :pending="pending([key])"
        :forbidden="forbidden([key])"
        :label="
          key === 'projects'
            ? t('dashboard.kpiProjects')
            : t('dashboard.kpiRunning')
        "
        @retry="retry([key])"
      />
    </div>
    <!-- bento 网格 -->
    <div class="bento">
      <DashDataState
        class="bento-runs"
        :state="state(['recent'], !runs.length)"
        :updated-at="updated(['recent'])"
        :pending="pending(['recent'])"
        :forbidden="forbidden(['recent'])"
        :label="t('dashboard.kpiRuns')"
        @retry="retry(['recent'])"
        ><DashRecentRuns :runs="runs" :loading="false"
      /></DashDataState>
      <DashDataState
        class="bento-env"
        :state="state(['environments'], !envEntries.length)"
        :updated-at="updated(['environments'])"
        :pending="pending(['environments'])"
        :forbidden="forbidden(['environments'])"
        :label="t('dash.environments')"
        @retry="retry(['environments'])"
        ><DashEnvironments :entries="envEntries" :loading="false"
      /></DashDataState>
      <DashDataState
        class="bento-srv"
        :state="state(['servers', 'metrics'], !servers.length)"
        :updated-at="updated(['servers', 'metrics'])"
        :pending="pending(['servers', 'metrics'])"
        :forbidden="forbidden(['servers', 'metrics'])"
        :label="t('dash.servers')"
        @retry="retry(['servers', 'metrics'])"
        ><DashServers
          :servers="servers"
          :metrics="serverMetrics"
          :loading="false"
      /></DashDataState>
      <div class="bento-alerts">
        <DashDataState
          :state="state(['alerts'], !alerts.length)"
          :updated-at="updated(['alerts'])"
          :pending="pending(['alerts'])"
          :forbidden="forbidden(['alerts'])"
          :label="t('dash.resourceAnomalies')"
          @retry="retry(['alerts'])"
        />
        <DashDataState
          :state="state(['diagnosis'], !diagnosis?.totalFeedback)"
          :updated-at="updated(['diagnosis'])"
          :pending="pending(['diagnosis'])"
          :forbidden="forbidden(['diagnosis'])"
          :label="t('dash.aiDiagLoop')"
          @retry="retry(['diagnosis'])"
        />
        <DashAlerts
          :alerts="alerts"
          :diagnosis="diagnosis"
          :loading="false"
          :alerts-unavailable="!known('alerts')"
          :diagnosis-unavailable="!known('diagnosis')"
        />
      </div>

      <DashDataState
        class="bento-dora"
        :state="state(['dora'], !dora)"
        :updated-at="updated(['dora'])"
        :pending="pending(['dora'])"
        :forbidden="forbidden(['dora'])"
        :label="t('dashboard.doraTitle')"
        @retry="retry(['dora'])"
      >
        <section class="card" aria-labelledby="dash-dora-h">
          <header class="card-head">
            <h2 id="dash-dora-h" class="card-title">
              {{ t("dashboard.doraTitle") }}
              <span class="dora-window">{{ dora?.window ?? "30d" }}</span>
            </h2>
            <button
              class="card-link"
              type="button"
              @click="router.push('/metrics/dora')"
            >
              {{ t("common.detailArrow") }}
            </button>
          </header>
          <div v-if="pending(['dora']) && !known('dora')" class="dora-skeleton">
            <span v-for="i in 4" :key="i" />
          </div>
          <div v-else-if="dora" class="dora-grid">
            <DoraMetricCard
              :title="t('dashboard.doraDeployFreq')"
              :subtitle="t('dashboard.subDeployFreq')"
              :display="
                dora.metrics.deploymentFrequency.sampleCount
                  ? formatFrequency(dora.deploymentFrequency)
                  : '—'
              "
              :caption="
                t('dashboard.capDeployments', {
                  days: dora.windowDays,
                  count: dora.totalDeployments,
                })
              "
              :band="dora.metrics.deploymentFrequency.band"
              :sampleCount="dora.metrics.deploymentFrequency.sampleCount"
              :trend="deployTrend"
            />
            <DoraMetricCard
              :title="t('dashboard.doraLeadTime')"
              :subtitle="t('dashboard.subLeadTime')"
              :display="
                dora.metrics.leadTime.sampleCount
                  ? formatDuration(dora.leadTimeSeconds)
                  : '—'
              "
              :caption="t('dashboard.capLeadTime')"
              :band="dora.metrics.leadTime.band"
              :sampleCount="dora.metrics.leadTime.sampleCount"
              :trend="deployTrend"
            />
            <DoraMetricCard
              :title="t('dashboard.doraCfr')"
              :subtitle="t('dashboard.subCfr')"
              :display="
                dora.metrics.changeFailureRate.sampleCount
                  ? formatPercent(dora.changeFailureRate)
                  : '—'
              "
              :caption="
                t('dashboard.capCfr', {
                  failed: dora.failedDeployments,
                  total: dora.totalDeployments,
                })
              "
              :band="dora.metrics.changeFailureRate.band"
              :sampleCount="dora.metrics.changeFailureRate.sampleCount"
              :trend="cfrTrend"
            />
            <DoraMetricCard
              :title="t('dashboard.doraMttr')"
              :subtitle="t('dashboard.subMttr')"
              :display="
                dora.metrics.mttr.sampleCount
                  ? formatDuration(dora.mttrSeconds)
                  : '—'
              "
              :caption="t('dashboard.capMttr')"
              :band="dora.metrics.mttr.band"
              :sampleCount="dora.metrics.mttr.sampleCount"
              :trend="cfrTrend"
            />
          </div>
          <p v-else class="card-empty">{{ t("dashboard.doraEmpty") }}</p>
        </section>
      </DashDataState>
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

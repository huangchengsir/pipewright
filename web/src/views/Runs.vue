<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter, useRoute, onBeforeRouteLeave } from "vue-router";
import {
  saveRunListPosition,
  restoreRunListPosition,
  clearRunListPosition,
} from "../lib/runListContext";
import {
  listRuns,
  triggerManual,
  type RunListItem,
  type RunStatus,
  type RunDetail,
  type ListRunsParams,
  type TriggerManualInput,
} from "../api/runs";
import RunParamsEditor from "../components/RunParamsEditor.vue";
import TypedRunParams from "../components/TypedRunParams.vue";
import {
  getParameters,
  validateParamValues,
  type ParamDef,
} from "../api/parameters";
import { listProjects, type Project } from "../api/projects";
import { HttpError } from "../api/http";

const { t } = useI18n();

// ─── router ───────────────────────────────────────────────────────────────────

const router = useRouter();
const route = useRoute();

function runHref(id: string): string {
  return router.resolve({
    name: "run-detail",
    params: { id },
    query: { returnTo: route.fullPath },
  }).href;
}
function openRun(event: MouseEvent, id: string): void {
  if (
    event.button !== 0 ||
    event.ctrlKey ||
    event.metaKey ||
    event.shiftKey ||
    event.altKey
  )
    return;
  event.preventDefault();
  goToDetail(id);
}
function goToDetail(id: string): void {
  saveRunListPosition(route.fullPath, id);
  void router.push({
    name: "run-detail",
    params: { id },
    query: { returnTo: route.fullPath },
  });
}

// ─── load state ──────────────────────────────────────────────────────────────

type LoadState = "idle" | "loading" | "error";

const loadState = ref<LoadState>("idle");
const loadError = ref("");
const runs = ref<RunListItem[]>([]);
const totalRuns = ref(0);

// ─── filters ─────────────────────────────────────────────────────────────────

const statusFilter = ref<RunStatus | "all">("all");

const STATUS_FILTER_OPTIONS: Array<RunStatus | "all"> = [
  "all",
  "running",
  "success",
  "failed",
  "partial_failed",
  "rolled_back",
  "queued",
  "waiting_approval",
];

// 筛选标签:'all' 用本页 key;其余复用全局 runStatus.* 状态文案。
function filterLabel(value: RunStatus | "all"): string {
  return value === "all" ? t("runs.filterAll") : t(`runStatus.${value}`);
}

// ─── data loading ─────────────────────────────────────────────────────────────

const projectFilter = ref("");
const queryFilter = ref("");
const periodFilter = ref("all");
const afterFilter = ref("");
const currentPage = ref(1);
const PAGE_SIZE = 20;
const totalPages = computed(() =>
  Math.max(1, Math.ceil(totalRuns.value / PAGE_SIZE)),
);
const hasFilters = computed(
  () =>
    statusFilter.value !== "all" ||
    projectFilter.value !== "" ||
    queryFilter.value !== "" ||
    afterFilter.value !== "",
);
let appliedQuery: Record<string, string> = {};
let requestGeneration = 0;
let listAbort: AbortController | null = null;
let disposed = false;
function readQuery(): void {
  const q = route.query;
  statusFilter.value = STATUS_FILTER_OPTIONS.includes(q.status as RunStatus)
    ? (q.status as RunStatus)
    : "all";
  projectFilter.value = typeof q.projectId === "string" ? q.projectId : "";
  queryFilter.value = typeof q.q === "string" ? q.q.slice(0, 200) : "";
  periodFilter.value = ["1", "7", "30"].includes(String(q.period))
    ? String(q.period)
    : "all";
  afterFilter.value =
    typeof q.after === "string" && Number.isFinite(Date.parse(q.after))
      ? new Date(q.after).toISOString()
      : "";
  if (!afterFilter.value) periodFilter.value = "all";
  if (afterFilter.value && periodFilter.value === "all")
    periodFilter.value = "custom";
  currentPage.value = /^\d+$/.test(String(q.page))
    ? Math.min(100000, Math.max(1, Number(q.page)))
    : 1;
  appliedQuery = queryFromInputs();
}
function queryFromInputs(): Record<string, string> {
  const query: Record<string, string> = {};
  if (statusFilter.value !== "all") query.status = statusFilter.value;
  if (projectFilter.value) query.projectId = projectFilter.value;
  if (queryFilter.value.trim()) query.q = queryFilter.value.trim();
  if (afterFilter.value) {
    query.after = afterFilter.value;
    query.period = periodFilter.value;
  }
  return query;
}
function navigatePage(page: number, replace = false, submit = false): void {
  const query = submit ? queryFromInputs() : { ...appliedQuery };
  if (page > 1) query.page = String(page);
  const target = router.resolve({ name: "runs", query }).fullPath;
  clearRunListPosition(target);
  if (target === route.fullPath) {
    if (submit) readQuery();
    void loadRuns();
    return;
  }
  void (replace
    ? router.replace({ name: "runs", query })
    : router.push({ name: "runs", query }));
}
function onFilterChange(): void {
  navigatePage(1, false, true);
}
function onPeriodChange(): void {
  afterFilter.value =
    periodFilter.value === "all"
      ? ""
      : new Date(
          Date.now() - Number(periodFilter.value) * 86400000,
        ).toISOString();
  onFilterChange();
}
function clearFilters(): void {
  statusFilter.value = "all";
  projectFilter.value = "";
  queryFilter.value = "";
  periodFilter.value = "all";
  afterFilter.value = "";
  navigatePage(1, false, true);
}
async function loadRuns(): Promise<void> {
  const gen = ++requestGeneration;
  listAbort?.abort();
  listAbort = new AbortController();
  const path = route.fullPath;
  loadState.value = "loading";
  loadError.value = "";
  try {
    const params: ListRunsParams = {
      page: currentPage.value,
      pageSize: PAGE_SIZE,
    };
    if (appliedQuery.status) params.status = appliedQuery.status as RunStatus;
    if (appliedQuery.projectId) params.projectId = appliedQuery.projectId;
    if (appliedQuery.q) params.q = appliedQuery.q;
    if (appliedQuery.after) params.after = appliedQuery.after;
    const res = await listRuns(params, { signal: listAbort.signal });
    if (gen !== requestGeneration || disposed) return;
    runs.value = res.items;
    totalRuns.value = res.total;
    loadState.value = "idle";
    if (currentPage.value > totalPages.value) {
      navigatePage(totalPages.value, true);
      return;
    }
    await restoreRunListPosition(
      path,
      () => !disposed && route.fullPath === path,
    );
  } catch (err) {
    if (gen !== requestGeneration || disposed) return;
    loadError.value =
      err instanceof HttpError
        ? err.status === 0
          ? t("runs.errNoServer")
          : (err.apiError?.message ??
            t("runs.errLoadFailedCode", { n: err.status }))
        : t("runs.errLoadFailedRetry");
    loadState.value = "error";
  }
}
watch(
  () => route.fullPath,
  () => {
    readQuery();
    void loadRuns();
  },
  { immediate: true },
);
onBeforeRouteLeave(() => {
  const focused = document.activeElement as HTMLElement | null;
  if (focused?.dataset.runId)
    saveRunListPosition(route.fullPath, focused.dataset.runId);
});
onUnmounted(() => {
  disposed = true;
  requestGeneration++;
  listAbort?.abort();
});
onMounted(() => {
  void loadProjectsForTrigger();
});

// ─── manual trigger modal ─────────────────────────────────────────────────────

const triggerModalOpen    = ref(false)
const projects            = ref<Project[]>([])
const projectsLoading     = ref(false)
const projectsLoadError = ref(false)
const triggerForm         = ref({ projectId: '', branch: '', commit: '' })
const triggerParams       = ref<Record<string, string>>({})
const triggerDefs         = ref<ParamDef[]>([])
const triggerBranchError  = ref('')
const triggerProjectError = ref('')
const triggerBanner       = ref('')
const triggerSubmitting   = ref(false)

const selectedProject = computed<Project | undefined>(() =>
  projects.value.find((p) => p.id === triggerForm.value.projectId),
)

async function loadProjectsForTrigger(): Promise<void> {
  if (projects.value.length > 0) return
  projectsLoading.value = true
  projectsLoadError.value = false
  try {
    projects.value = await listProjects()
  } catch {
    projectsLoadError.value = true
  } finally {
    projectsLoading.value = false
  }
}

function openTriggerModal(): void {
  triggerForm.value        = { projectId: '', branch: '', commit: '' }
  triggerParams.value       = {}
  triggerDefs.value         = []
  triggerBranchError.value  = ''
  triggerProjectError.value = ''
  triggerBanner.value       = ''
  triggerSubmitting.value   = false
  triggerModalOpen.value    = true
  void loadProjectsForTrigger()
}

function closeTriggerModal(): void {
  if (triggerSubmitting.value) return
  triggerModalOpen.value = false
}

function onProjectSelect(): void {
  triggerProjectError.value = ''
  // Auto-fill branch from project's defaultBranch when user picks a project
  const proj = selectedProject.value
  if (proj && !triggerForm.value.branch) {
    triggerForm.value.branch = proj.defaultBranch || ''
  }
  // 切项目 → 重载类型化参数定义(P0);清空已填值。失败静默回退自由 KV。
  triggerParams.value = {}
  triggerDefs.value = []
  const pid = triggerForm.value.projectId
  if (pid) {
    void getParameters(pid)
      .then((defs) => {
        if (triggerForm.value.projectId === pid) triggerDefs.value = defs
      })
      .catch(() => {})
  }
}

async function handleTriggerSubmit(): Promise<void> {
  triggerBranchError.value  = ''
  triggerProjectError.value = ''
  triggerBanner.value       = ''

  let ok = true
  if (!triggerForm.value.projectId) {
    triggerProjectError.value = t('runs.errSelectProject')
    ok = false
  }
  const branch = triggerForm.value.branch.trim()
  if (!branch) {
    triggerBranchError.value = t('runs.errEnterBranch')
    ok = false
  }
  if (!ok) return

  if (triggerDefs.value.length) {
    const verr = validateParamValues(triggerDefs.value, triggerParams.value)
    if (verr) {
      triggerBanner.value = verr
      return
    }
  }

  triggerSubmitting.value = true
  try {
    const input: TriggerManualInput = { branch }
    const commit = triggerForm.value.commit.trim()
    if (commit) input.commit = commit
    if (Object.keys(triggerParams.value).length) input.params = triggerParams.value

    const run: RunDetail = await triggerManual(triggerForm.value.projectId, input)
    triggerModalOpen.value = false
    goToDetail(run.id)
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        triggerBanner.value = t('runs.errNoServerDot')
      } else if (err.status === 404) {
        triggerBanner.value = t('runs.errProjectNotFound')
      } else {
        triggerBanner.value = err.apiError?.message ?? t('runs.errTriggerFailedCode', { n: err.status })
      }
    } else {
      triggerBanner.value = t('runs.errTriggerFailedRetry')
    }
  } finally {
    triggerSubmitting.value = false
  }
}

// ─── group runs by date ───────────────────────────────────────────────────────

interface RunGroup {
  label: string
  items: RunListItem[]
}

const groupedRuns = computed<RunGroup[]>(() => {
  const groups: Map<string, RunListItem[]> = new Map()
  for (const run of runs.value) {
    const d = new Date(run.createdAt)
    const label = formatDateGroup(d)
    if (!groups.has(label)) groups.set(label, [])
    groups.get(label)!.push(run)
  }
  return Array.from(groups.entries()).map(([label, items]) => ({ label, items }))
})

function formatDateGroup(d: Date): string {
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(today.getDate() - 1)

  const sameDay = (a: Date, b: Date): boolean =>
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()

  if (sameDay(d, today)) return t('runs.today')
  if (sameDay(d, yesterday)) return t('runs.yesterday')

  return d.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

// ─── Status pill config (fixed six-word set) ──────────────────────────────────

interface StatusConfig {
  dot: string
  bg: string
  border: string
  text: string
  pulse: boolean
}

// 文案统一走全局 runStatus.*;此处只配语义色调与脉冲。
const STATUS_CONFIG: Record<RunStatus, StatusConfig> = {
  queued:        { dot: 'var(--color-faint)',  bg: 'var(--color-card-2)',     border: 'var(--color-border-strong)', text: 'var(--color-dim)',   pulse: false },
  running:       { dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)', border: 'transparent',               text: 'var(--color-amber)', pulse: true  },
  waiting_approval:{ dot: 'var(--color-amber)', bg: 'var(--color-amber-soft)', border: 'var(--color-amber-line)',   text: 'var(--color-amber)', pulse: true  },
  success:       { dot: 'var(--color-green)',  bg: 'var(--color-green-soft)', border: 'transparent',               text: 'var(--color-green)', pulse: false },
  failed:        { dot: 'var(--color-red)',    bg: 'var(--color-red-soft)',   border: 'var(--color-red-line)',     text: 'var(--color-red)',   pulse: false },
  partial_failed:{ dot: 'var(--color-red)',    bg: 'var(--color-red-soft)',   border: 'var(--color-red-line)',     text: 'var(--color-red)',   pulse: false },
  rolled_back:   { dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)', border: 'var(--color-amber-line)',   text: 'var(--color-amber)', pulse: false },
}

// ─── helpers ──────────────────────────────────────────────────────────────────

function shortId(id: string): string {
  return id.slice(0, 8)
}

function shortCommit(commit: string): string {
  return commit.length > 7 ? commit.slice(0, 7) : commit
}

function formatDuration(ms: number | null): string {
  if (ms === null) return '—'
  if (ms < 1000) return `${ms}ms`
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function triggerTypeLabel(type: 'webhook' | 'manual'): string {
  return type === 'webhook' ? 'Webhook' : t('runs.triggerManual')
}

function isFailedStatus(status: RunStatus): boolean {
  return status === 'failed' || status === 'partial_failed'
}
</script>

<template>
  <div class="runs-root">
    <!-- ─── Page header ─────────────────────────────────────────────────── -->
    <header class="page-header">
      <div class="page-header-text">
        <h1 class="page-title">{{ t('runs.title') }}</h1>
        <p class="page-sub">{{ t('runs.subtitle') }}</p>
      </div>
      <button
        class="btn-run"
        :disabled="loadState === 'loading'"
        @click="openTriggerModal"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
        </svg>
        {{ t('runs.manualTrigger') }}
      </button>
    </header>

    <!-- ─── Error banner ────────────────────────────────────────────────── -->
    <div
      v-if="loadState === 'error'"
      class="banner banner--error"
      role="alert"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadRuns">↻ {{ t('runs.retry') }}</button>
    </div>

    <form class="run-query" @submit.prevent="onFilterChange">
      <label
        ><span>{{ t("runs.colProject") }}</span
        ><select v-model="projectFilter" @change="onFilterChange">
          <option value="">{{ t("runExperience.allProjects") }}</option>
          <option
            v-if="
              projectFilter && !projects.some((p) => p.id === projectFilter)
            "
            :value="projectFilter"
          >
            {{ projectFilter }}
          </option>
          <option v-for="p in projects" :key="p.id" :value="p.id">
            {{ p.name }}
          </option>
        </select></label
      >
      <label
        ><span>{{ t("runExperience.period") }}</span
        ><select v-model="periodFilter" @change="onPeriodChange">
          <option value="all">{{ t("runExperience.allTime") }}</option>
          <option v-if="periodFilter === 'custom'" value="custom">
            {{
              t("runExperience.sinceDate", { date: afterFilter.slice(0, 10) })
            }}
          </option>
          <option v-for="n in [1, 7, 30]" :key="n" :value="String(n)">
            {{ t("runExperience.lastDays", { n }) }}
          </option>
        </select></label
      >
      <label class="run-query-search"
        ><span>{{ t("runExperience.search") }}</span
        ><input
          v-model="queryFilter"
          type="search"
          maxlength="200"
          :placeholder="t('runExperience.queryHint')"
      /></label>
      <button class="btn-secondary" type="submit">
        {{ t("runExperience.search") }}
      </button>
      <button
        v-if="hasFilters"
        class="banner-retry"
        type="button"
        @click="clearFilters"
      >
        {{ t("runs.clearFilter") }}
      </button>
    </form>
    <p v-if="projectsLoadError" class="query-warning" role="status">
      {{ t("runExperience.projectOptionsError") }}
      <button
        type="button"
        class="banner-retry"
        :disabled="projectsLoading"
        @click="loadProjectsForTrigger"
      >
        {{ t("runs.retry") }}
      </button>
    </p>
    <!-- ─── Filter bar ───────────────────────────────────────────────────── -->
    <div class="filter-bar" role="group" :aria-label="t('runs.filterAriaLabel')">
      <button
        v-for="opt in STATUS_FILTER_OPTIONS"
        :key="opt"
        type="button"
        class="filter-tab"
        :class="{ 'filter-tab--active': statusFilter === opt }"
        :aria-pressed="statusFilter === opt"
        @click="statusFilter = opt; onFilterChange()"
      >{{ filterLabel(opt) }}</button>
    </div>

    <!-- ─── Loading skeleton ─────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="table-wrap" aria-busy="true" :aria-label="t('runs.loading')">
        <div class="skel-group-label skel" />
        <div
          v-for="i in 5"
          :key="i"
          class="skel-row"
          aria-hidden="true"
        >
          <div class="skel skel--pill" />
          <div class="skel skel--name" />
          <div class="skel skel--branch" />
          <div class="skel skel--trigger" />
          <div class="skel skel--dur" />
          <div class="skel skel--time" />
        </div>
        <div class="skel-group-label skel" style="margin-top: 20px;" />
        <div
          v-for="i in 3"
          :key="`b${i}`"
          class="skel-row"
          aria-hidden="true"
        >
          <div class="skel skel--pill" />
          <div class="skel skel--name" />
          <div class="skel skel--branch" />
          <div class="skel skel--trigger" />
          <div class="skel skel--dur" />
          <div class="skel skel--time" />
        </div>
      </div>
    </template>

    <!-- ─── Empty state ──────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle' && runs.length === 0">
      <div class="empty-state" role="status">
        <div class="empty-icon" aria-hidden="true">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
            <circle cx="12" cy="12" r="3"/>
            <path d="M12 3v3m0 12v3M3 12h3m12 0h3M5.64 5.64l2.12 2.12m8.48 8.48 2.12 2.12M5.64 18.36l2.12-2.12m8.48-8.48 2.12-2.12"/>
          </svg>
        </div>
        <p class="empty-label">{{ t('runs.empty') }}</p>
        <p class="empty-hint">
          <template v-if="hasFilters">{{ t('runs.emptyFiltered') }}</template>
          <template v-else>{{ t('runs.emptyHint') }}</template>
        </p>
        <button
          v-if="hasFilters"
          class="btn-secondary"
          @click="clearFilters"
        >{{ t('runs.clearFilter') }}</button>
      </div>
    </template>

    <!-- ─── Runs table (grouped by date) ────────────────────────────────── -->
    <template v-else-if="loadState === 'idle'">
      <p class="result-count" aria-live="polite">
        {{ t('runs.resultCount', { n: totalRuns }) }}
      </p>

      <div class="table-wrap">
        <!-- Table header -->
        <div class="table-head" role="row" :aria-label="t('runs.tableHeaderAria')">
          <span class="th">{{ t('runs.colStatus') }}</span>
          <span class="th">{{ t('runs.colProject') }}</span>
          <span class="th">{{ t('runs.colBranchCommit') }}</span>
          <span class="th">{{ t('runs.colTrigger') }}</span>
          <span class="th">{{ t('runs.colDuration') }}</span>
          <span class="th">{{ t('runs.colTime') }}</span>
          <span class="th th--action" />
        </div>

        <!-- Date groups -->
        <template v-for="group in groupedRuns" :key="group.label">
          <!-- Group header -->
          <div class="group-label" role="rowgroup">
            <span class="group-label-text">{{ group.label }}</span>
            <span class="group-label-count">{{ t('runs.groupCount', { n: group.items.length }) }}</span>
          </div>

          <!-- Run rows -->
          <a
            v-for="run in group.items"
            :href="runHref(run.id)"
            :key="run.id"
            class="run-row"
            :data-run-id="run.id"
            :class="{
              'run-row--failed': isFailedStatus(run.status),
              'run-row--running': run.status === 'running',
            }"

            :aria-label="`${run.projectName} · ${t('runs.rowAria', { id: shortId(run.id), status: t(`runStatus.${run.status}`) })}`"
            @click="openRun($event, run.id)"
          >
            <!-- Status pill -->
            <div class="col-status">
              <span
                class="status-pill"
                :style="{
                  background: STATUS_CONFIG[run.status].bg,
                  border: `1px solid ${STATUS_CONFIG[run.status].border}`,
                  color: STATUS_CONFIG[run.status].text,
                }"
                :aria-label="t('runs.statusAria', { status: t(`runStatus.${run.status}`) })"
              >
                <span
                  class="status-dot"
                  :class="{ 'status-dot--pulse': STATUS_CONFIG[run.status].pulse }"
                  :style="{ background: STATUS_CONFIG[run.status].dot }"
                  aria-hidden="true"
                />
                {{ t(`runStatus.${run.status}`) }}
              </span>
            </div>

            <!-- Project name + run ID -->
            <div class="col-project">
              <span class="project-name">{{ run.projectName }} <small v-if="run.executionKind === 'recovery'">{{ t('runRetry.attempt', { n: run.attemptNumber ?? 1 }) }}</small></span>
              <span class="run-id mono">#{{ shortId(run.id) }}</span>
            </div>

            <!-- Branch + commit -->
            <div class="col-branch">
              <span class="branch-tag mono">{{ run.trigger.branch }}</span>
              <span class="commit-hash mono">{{ shortCommit(run.trigger.commit) }}</span>
            </div>

            <!-- Trigger type + actor -->
            <div class="col-trigger">
              <span class="trigger-badge">{{ triggerTypeLabel(run.trigger.type) }}</span>
              <span class="trigger-actor">{{ run.trigger.actor }}</span>
            </div>

            <!-- Duration -->
            <div class="col-dur">
              <span class="dur-value mono">{{ formatDuration(run.durationMs) }}</span>
            </div>

            <!-- Time -->
            <div class="col-time">
              <span class="time-value">{{ formatTime(run.createdAt) }}</span>
            </div>

            <!-- Failed row: diagnosis shortcut -->
            <div class="col-action">
              <span
                v-if="isFailedStatus(run.status)"
                class="diag-link"
                aria-hidden="true"
              >{{ t('runs.viewDiagnosis') }} →</span>
            </div>

            <!-- Running row: mini pulse bar -->
            <div
              v-if="run.status === 'running'"
              class="run-row-progress"
              aria-hidden="true"
            />
          </a>
        </template>
      </div>

      <nav class="run-pagination" :aria-label="t('runExperience.pagination')">
        <span>{{
          t("runExperience.pageOf", { page: currentPage, total: totalPages })
        }}</span>
        <button
          class="btn-secondary"
          type="button"
          :disabled="currentPage <= 1"
          @click="navigatePage(currentPage - 1)"
        >
          {{ t("runExperience.previous") }}
        </button>
        <button
          class="btn-secondary"
          type="button"
          :disabled="currentPage >= totalPages"
          @click="navigatePage(currentPage + 1)"
        >
          {{ t("runExperience.next") }}
        </button>
      </nav>
    </template>
  </div>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Manual trigger modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="triggerModalOpen"
      class="modal-scrim"
      role="dialog"
      :aria-label="t('runs.modalAria')"
      aria-modal="true"
      @keydown.esc="closeTriggerModal"
      @click.self="closeTriggerModal"
    >
      <div class="modal modal--sm">
        <!-- Header -->
        <div class="modal-head">
          <div class="modal-icon" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">{{ t('runs.modalTitle') }}</h3>
            <p class="modal-sub">{{ t('runs.modalSub') }}</p>
          </div>
          <button
            class="modal-close"
            :aria-label="t('runs.closeDialog')"
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
          <!-- Project dropdown -->
          <div class="field">
            <label class="field-label" for="trigger-project">{{ t('runs.fieldProject') }}</label>
            <div class="select-wrap">
              <select
                id="trigger-project"
                v-model="triggerForm.projectId"
                class="field-select"
                :class="{ 'field-input--error': triggerProjectError }"
                :disabled="triggerSubmitting || projectsLoading"
                :aria-invalid="triggerProjectError ? 'true' : undefined"
                :aria-describedby="triggerProjectError ? 'trigger-proj-err' : undefined"
                @change="onProjectSelect"
              >
                <option value="" disabled>
                  {{ projectsLoading ? t('runs.loadingProjects') : t('runs.selectProject') }}
                </option>
                <option
                  v-for="proj in projects"
                  :key="proj.id"
                  :value="proj.id"
                >{{ proj.name }}</option>
              </select>
              <svg class="select-arrow" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M6 9l6 6 6-6"/>
              </svg>
            </div>
            <span
              v-if="triggerProjectError"
              id="trigger-proj-err"
              class="field-error"
              role="alert"
            >{{ triggerProjectError }}</span>
          </div>

          <!-- Branch -->
          <div class="field">
            <label class="field-label" for="trigger-branch">
              {{ t('runs.fieldBranch') }}
              <span class="field-hint-inline">{{ t('runs.fieldBranchHint') }}</span>
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
              {{ t('runs.fieldCommit') }}
              <span class="field-hint-inline">{{ t('runs.fieldCommitHint') }}</span>
            </label>
            <input
              id="trigger-commit"
              v-model="triggerForm.commit"
              class="field-input field-input--mono"
              type="text"
              :placeholder="t('runs.commitPlaceholder')"
              autocomplete="off"
              :disabled="triggerSubmitting"
            />
          </div>

          <!-- Parameters · 有类型化定义(P0)→ 类型化控件;无 → 自由 KV(Story 8-11) -->
          <div class="field">
            <label class="field-label">
              {{ t('runs.fieldParams') }}
              <span class="field-hint-inline">{{ triggerDefs.length ? t('runs.fieldParamsHintTyped') : t('runs.fieldParamsHintFree') }}</span>
            </label>
            <TypedRunParams
              v-if="triggerDefs.length"
              :key="`typed-${triggerForm.projectId}`"
              :defs="triggerDefs"
              v-model="triggerParams"
            />
            <RunParamsEditor v-else v-model="triggerParams" :disabled="triggerSubmitting" />
          </div>

          <!-- Footer -->
          <div class="modal-footer">
            <button
              type="button"
              class="btn-secondary"
              :disabled="triggerSubmitting"
              @click="closeTriggerModal"
            >{{ t('runs.cancel') }}</button>
            <button
              type="submit"
              class="btn-run"
              :disabled="triggerSubmitting"
              :aria-busy="triggerSubmitting"
            >
              <span v-if="triggerSubmitting" class="spinner" aria-hidden="true" />
              <svg v-else width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
              </svg>
              {{ triggerSubmitting ? t('runs.triggering') : t('runs.runNow') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* ─── root ───────────────────────────────────────────────────────────────── */
.runs-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── page header ────────────────────────────────────────────────────────── */
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

/* ─── filter bar ─────────────────────────────────────────────────────────── */
.filter-bar {
  display: flex;
  gap: 4px;
  background: var(--color-inset);
  border-radius: var(--rounded);
  padding: 3px;
  width: fit-content;
  flex-wrap: wrap;
}

.filter-tab {
  height: 30px;
  padding: 0 12px;
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

/* ─── result count ────────────────────────────────────────────────────────── */
.result-count {
  font-size: 0.78rem;
  color: var(--color-faint);
  margin-top: -4px;
}

/* ─── table ──────────────────────────────────────────────────────────────── */
.table-wrap {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  overflow: hidden;
}

.table-head {
  display: grid;
  grid-template-columns: 110px 1fr 160px 120px 80px 90px 100px;
  padding: 0 18px;
  height: 38px;
  align-items: center;
  background: var(--color-card-2);
  border-bottom: 1px solid var(--color-border);
  gap: 12px;
}

.th {
  font-size: 0.71rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.th--action {
  text-align: right;
}

/* ─── group label ────────────────────────────────────────────────────────── */
.group-label {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 18px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
}

.group-label-text {
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.03em;
  color: var(--color-dim);
}

.group-label-count {
  font-size: 0.72rem;
  color: var(--color-faint);
}

/* ─── run row ────────────────────────────────────────────────────────────── */
.run-row {
  position: relative;
  display: grid;
  grid-template-columns: 110px 1fr 160px 120px 80px 90px 100px;
  padding: 0 18px;
  min-height: 52px;
  align-items: center;
  gap: 12px;
  border-bottom: 1px solid var(--color-border);
  cursor: pointer;
  transition: background-color var(--duration-fast);
  outline: none;
}

.run-row:last-child {
  border-bottom: none;
}

.run-row:hover {
  background: var(--color-inset);
}

.run-row:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

/* Failed row: left-side red gradient */
.run-row--failed {
  background: linear-gradient(
    90deg,
    var(--color-red-soft) 0%,
    transparent 48%
  );
}

.run-row--failed:hover {
  background: linear-gradient(
    90deg,
    oklch(62% 0.18 22 / 0.2) 0%,
    var(--color-inset) 55%
  );
}

/* Running row: progress shimmer at bottom */
.run-row-progress {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: linear-gradient(
    90deg,
    transparent 0%,
    var(--color-amber) 40%,
    var(--color-primary) 60%,
    transparent 100%
  );
  background-size: 200% 100%;
  animation: flow 2s linear infinite;
}

@keyframes flow {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .run-row-progress { animation: none; background: var(--color-amber); }
}

/* ─── status pill ────────────────────────────────────────────────────────── */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: var(--rounded-md);
  font-size: var(--text-micro);
  font-weight: 600;
  white-space: nowrap;
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
  .status-dot--pulse { animation: none; }
}

/* ─── columns ────────────────────────────────────────────────────────────── */
.col-status {
  display: flex;
  align-items: center;
}

.col-project {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.project-name {
  font-size: 0.86rem;
  font-weight: 500;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.run-id {
  font-size: 0.72rem;
  color: var(--color-faint);
}

.col-branch {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.branch-tag {
  font-size: 0.78rem;
  color: var(--color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.commit-hash {
  font-size: 0.72rem;
  color: var(--color-faint);
}

.col-trigger {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.trigger-badge {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-dim);
}

.trigger-actor {
  font-size: 0.72rem;
  color: var(--color-faint);
}

.col-dur {
  display: flex;
  align-items: center;
}

.dur-value {
  font-size: 0.8rem;
  color: var(--color-dim);
}

.col-time {
  display: flex;
  align-items: center;
}

.time-value {
  font-size: 0.78rem;
  color: var(--color-faint);
}

.col-action {
  display: flex;
  align-items: center;
  justify-content: flex-end;
}

.diag-link {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-red);
  white-space: nowrap;
  opacity: 0.7;
  transition: opacity var(--duration-fast);
}

.run-row:hover .diag-link {
  opacity: 1;
}

.mono {
  font-family: var(--font-mono);
}

/* ─── pagination placeholder ─────────────────────────────────────────────── */
.pagination-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  border-top: 1px solid var(--color-border);
}

.pagination-hint {
  font-size: 0.75rem;
  color: var(--color-faint);
}

/* ─── skeleton ───────────────────────────────────────────────────────────── */
.skel-group-label {
  height: 32px;
  border-radius: 0;
  width: 100%;
}

.skel-row {
  display: grid;
  grid-template-columns: 110px 1fr 160px 120px 80px 90px;
  gap: 12px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  align-items: center;
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

.skel--pill    { height: 22px; width: 80px; }
.skel--name    { height: 14px; width: 70%; }
.skel--branch  { height: 12px; width: 90px; }
.skel--trigger { height: 12px; width: 70px; }
.skel--dur     { height: 12px; width: 50px; }
.skel--time    { height: 12px; width: 70px; }

/* ─── error banner ────────────────────────────────────────────────────────── */
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

.banner-retry:hover { opacity: 0.8; }

/* ─── empty state ────────────────────────────────────────────────────────── */
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

/* ─── buttons ─────────────────────────────────────────────────────────────── */
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

.btn-secondary:hover {
  border-color: var(--color-faint);
}

.btn-secondary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ─── run button ─────────────────────────────────────────────────────────── */
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
  flex-shrink: 0;
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

/* ─── modal ──────────────────────────────────────────────────────────────── */
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

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}

/* ─── form fields ─────────────────────────────────────────────────────────── */
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

.field-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

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

/* ─── spinner ─────────────────────────────────────────────────────────────── */
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

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spinner { animation: none; border-top-color: currentColor; }
}
.run-row {
  text-decoration: none;
  color: inherit;
}
.query-warning {
  font-size: 0.75rem;
  color: var(--color-dim);
  margin: 0;
}
.run-query {
  display: flex;
  align-items: end;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 14px;
}
.run-query label {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 0.75rem;
  color: var(--color-text-soft);
  min-width: 0;
}
.run-query input,
.run-query select {
  height: 36px;
  min-width: 0;
  width: 100%;
  padding: 0 10px;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--radius-md);
  background: var(--color-card);
  color: var(--color-text);
  font: inherit;
}
.run-query label:not(.run-query-search) {
  flex: 0 1 180px;
}
.run-query-search {
  flex: 1 1 240px;
}
.run-pagination {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 16px;
  font-size: 0.8rem;
  color: var(--color-text-soft);
}
.table-head,
.run-row {
  grid-template-columns: 108px minmax(140px, 1fr) minmax(
      115px,
      160px
    ) 110px 70px 85px 90px;
}
@media (max-width: 1100px) {
  .table-head,
  .run-row {
    grid-template-columns: 108px minmax(120px, 1fr) minmax(
        100px,
        140px
      ) 80px 70px;
  }
  .col-trigger,
  .col-action,
  .table-head .th:nth-child(4),
  .table-head .th:nth-child(7) {
    display: none;
  }
}
@media (max-width: 760px) {
  .table-head {
    display: none;
  }
  .run-row {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 8px 12px;
    padding: 12px;
  }
  .col-project {
    grid-column: 1;
    grid-row: 1;
  }
  .col-status {
    grid-column: 2;
    grid-row: 1;
  }
  .col-branch {
    grid-column: 1;
    grid-row: 2;
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .col-time {
    grid-column: 2;
    grid-row: 2;
  }
  .col-dur {
    display: none;
  }
  .run-query label:not(.run-query-search) {
    flex: 1 1 120px;
  }
  .run-query-search {
    flex-basis: 100%;
  }
  .run-query input,
  .run-query select {
    height: 44px;
    font-size: 16px;
  }
  .run-pagination button {
    min-height: 44px;
  }
}
</style>

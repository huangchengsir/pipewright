<!--
  AnomalyDetection.vue — Story 6-5: 可配置异常检测与告警(FR-23)

  管理员配阈值规则(metric ∈ cpu/memory/disk;operator gt/lt;threshold;作用域全局/指定服务器),
  「立即检测」对服务器指标(经 6-1 SSH 采集)逐规则求值,命中产告警并列表展示(按严重度着色)。
    · 不可达 / 指标不可得的服务器跳过(不误报),由后端保证。
    · 这是**新增**入口,不动 6-1 状态页 / 6-2 日志 / 6-3 操作入口。
-->
<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  listAnomalyRules,
  createAnomalyRule,
  updateAnomalyRule,
  deleteAnomalyRule,
  checkAnomaly,
  listAnomalyAlerts,
  getAnomalyConfig,
  getMetricsHistory,
  type AnomalyRule,
  type AnomalyAlert,
  type AnomalyMetric,
  type AnomalyOperator,
  type MetricPoint,
} from '../api/anomaly'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import AppButton from '../components/ui/AppButton.vue'
import FormField from '../components/ui/FormField.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'
import MetricTrendChart from '../components/metrics/MetricTrendChart.vue'

type LoadState = 'idle' | 'loading' | 'error'

const { t } = useI18n()

const loadState = ref<LoadState>('idle')
const loadError = ref('')

const rules = ref<AnomalyRule[]>([])
const alerts = ref<AnomalyAlert[]>([])
const servers = ref<Server[]>([])

// ─── new-rule form ────────────────────────────────────────────────────────────

const form = ref<{
  metric: AnomalyMetric
  operator: AnomalyOperator
  threshold: number
  serverId: string // '' = global
}>({
  metric: 'cpu',
  operator: 'gt',
  threshold: 90,
  serverId: '',
})

const formError = ref('')
const creating = ref(false)

// ─── check / actions state ────────────────────────────────────────────────────

const checking = ref(false)
const checkBanner = ref('')
const deletingId = ref<string | null>(null)

// ─── detection cadence (visible auto-check rhythm) ──────────────────────────────
const intervalSeconds = ref(0) // 0 = periodic detection off
const countdown = ref(0)
let refreshTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null

const cadenceText = computed(() =>
  intervalSeconds.value > 0
    ? t('anomaly.autoEvery', { n: intervalSeconds.value })
    : t('anomaly.autoOff'),
)

// ─── inline rule editing ────────────────────────────────────────────────────────
const editingRuleId = ref<string | null>(null)
const editForm = ref<{ metric: AnomalyMetric; operator: AnomalyOperator; threshold: number; serverId: string }>({
  metric: 'cpu',
  operator: 'gt',
  threshold: 90,
  serverId: '',
})
const savingEdit = ref(false)

// ─── metric trend chart ─────────────────────────────────────────────────────────
const trendServerId = ref('')
const trendHours = ref(6)
const trendPoints = ref<MetricPoint[]>([])
const trendLoading = ref(false)

const METRIC_KEYS: Record<AnomalyMetric, string> = {
  cpu: 'anomaly.metricCpu',
  memory: 'anomaly.metricMemory',
  disk: 'anomaly.metricDisk',
}

function metricLabel(m: AnomalyMetric): string {
  const key = METRIC_KEYS[m]
  return key ? t(key) : m
}
function operatorSymbol(op: AnomalyOperator): string {
  return op === 'gt' ? '>' : '<'
}
function serverName(id: string | null): string {
  if (!id) return t('anomaly.scopeAllServers')
  const s = servers.value.find((x) => x.id === id)
  return s ? s.name : t('anomaly.serverDeleted')
}

/**
 * Severity bucket for alert coloring — derived from how far value overshoots
 * the threshold (gt) / undershoots (lt). Data-viz as part of the design system.
 */
function severity(a: AnomalyAlert): 'critical' | 'warning' | 'info' {
  const delta = a.operator === 'gt' ? a.value - a.threshold : a.threshold - a.value
  if (delta >= 15) return 'critical'
  if (delta >= 5) return 'warning'
  return 'info'
}

const hasServers = computed(() => servers.value.length > 0)

// ─── data loading ──────────────────────────────────────────────────────────────

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const [r, s, a] = await Promise.all([
      listAnomalyRules(),
      listServers(),
      listAnomalyAlerts({ limit: 50 }),
    ])
    rules.value = r
    servers.value = s
    alerts.value = a
    loadState.value = 'idle'
  } catch (err) {
    loadError.value = humanError(err, t('anomaly.errLoad'))
    loadState.value = 'error'
  }
}

function humanError(err: unknown, fallback: string): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return t('anomaly.errConnect')
    return err.apiError?.message ?? t('anomaly.errStatus', { fallback, status: err.status })
  }
  return t('anomaly.errRetry', { fallback })
}

// ─── create rule ───────────────────────────────────────────────────────────────

async function submitRule(): Promise<void> {
  formError.value = ''
  if (!Number.isFinite(form.value.threshold)) {
    formError.value = t('anomaly.errThresholdNumber')
    return
  }
  creating.value = true
  try {
    const created = await createAnomalyRule({
      metric: form.value.metric,
      operator: form.value.operator,
      threshold: form.value.threshold,
      serverId: form.value.serverId || null,
    })
    // Prepend (list is created-desc).
    rules.value = [created, ...rules.value]
    // Reset threshold/scope but keep metric/operator for fast multi-add.
    form.value.serverId = ''
  } catch (err) {
    formError.value = humanError(err, t('anomaly.errCreateRule'))
  } finally {
    creating.value = false
  }
}

// ─── delete rule ───────────────────────────────────────────────────────────────

async function removeRule(rule: AnomalyRule): Promise<void> {
  deletingId.value = rule.id
  try {
    await deleteAnomalyRule(rule.id)
    rules.value = rules.value.filter((r) => r.id !== rule.id)
  } catch (err) {
    checkBanner.value = humanError(err, t('anomaly.errDeleteRule'))
  } finally {
    deletingId.value = null
  }
}

// ─── run detection now ─────────────────────────────────────────────────────────

async function runCheck(): Promise<void> {
  checking.value = true
  checkBanner.value = ''
  try {
    const fresh = await checkAnomaly()
    // Refresh the alert list (newest first) and surface this run's hit count.
    alerts.value = await listAnomalyAlerts({ limit: 50 })
    checkBanner.value =
      fresh.length === 0
        ? t('anomaly.checkDoneNone')
        : t('anomaly.checkDoneNew', { n: fresh.length })
  } catch (err) {
    checkBanner.value = humanError(err, t('anomaly.errCheck'))
  } finally {
    checking.value = false
  }
}

function formatAt(at: string): string {
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString()
}

// ─── inline rule edit ───────────────────────────────────────────────────────────

function startEdit(rule: AnomalyRule): void {
  editingRuleId.value = rule.id
  editForm.value = {
    metric: rule.metric,
    operator: rule.operator,
    threshold: rule.threshold,
    serverId: rule.serverId ?? '',
  }
}

function cancelEdit(): void {
  editingRuleId.value = null
}

async function saveEdit(rule: AnomalyRule): Promise<void> {
  if (!Number.isFinite(editForm.value.threshold)) {
    checkBanner.value = t('anomaly.errThresholdNumber')
    return
  }
  savingEdit.value = true
  try {
    const updated = await updateAnomalyRule(rule.id, {
      metric: editForm.value.metric,
      operator: editForm.value.operator,
      threshold: editForm.value.threshold,
      serverId: editForm.value.serverId || null,
    })
    rules.value = rules.value.map((r) => (r.id === updated.id ? updated : r))
    editingRuleId.value = null
  } catch (err) {
    checkBanner.value = humanError(err, t('anomaly.errUpdateRule'))
  } finally {
    savingEdit.value = false
  }
}

// ─── metric trend ─────────────────────────────────────────────────────────────

async function loadTrend(): Promise<void> {
  if (!trendServerId.value) {
    trendPoints.value = []
    return
  }
  trendLoading.value = true
  try {
    trendPoints.value = await getMetricsHistory(trendServerId.value, trendHours.value)
  } catch {
    trendPoints.value = []
  } finally {
    trendLoading.value = false
  }
}

watch([trendServerId, trendHours], loadTrend)

// ─── auto-refresh on the detection cadence ──────────────────────────────────────

async function autoRefresh(): Promise<void> {
  try {
    alerts.value = await listAnomalyAlerts({ limit: 50 })
  } catch {
    /* best-effort background refresh; ignore transient errors */
  }
  await loadTrend()
  countdown.value = intervalSeconds.value
}

function startTimers(): void {
  if (intervalSeconds.value <= 0) return
  countdown.value = intervalSeconds.value
  refreshTimer = setInterval(autoRefresh, intervalSeconds.value * 1000)
  countdownTimer = setInterval(() => {
    countdown.value = countdown.value > 0 ? countdown.value - 1 : intervalSeconds.value
  }, 1000)
}

onMounted(async () => {
  try {
    const cfg = await getAnomalyConfig()
    intervalSeconds.value = cfg.intervalSeconds
  } catch {
    /* config best-effort; cadence badge just won't show a number */
  }
  await load()
  // default trend to the first server, then load its series + start cadence timers.
  if (!trendServerId.value && servers.value.length > 0) {
    trendServerId.value = servers.value[0].id
  } else {
    await loadTrend()
  }
  startTimers()
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  if (countdownTimer) clearInterval(countdownTimer)
})
</script>

<template>
  <div class="anomaly">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">{{ t('anomaly.title') }}</h1>
        <p class="view-sub">
          {{ t('anomaly.subtitle') }}
        </p>
      </div>
      <div class="view-header__actions">
        <span class="cadence" :class="{ 'cadence--off': intervalSeconds <= 0 }" role="status">
          <span class="cadence__dot" aria-hidden="true" />
          {{ cadenceText }}
          <span v-if="intervalSeconds > 0" class="cadence__next">· {{ t('anomaly.nextRefresh', { n: countdown }) }}</span>
        </span>
        <AppButton variant="primary" :loading="checking" @click="runCheck">
          {{ t('anomaly.checkNow') }}
        </AppButton>
      </div>
    </header>

    <ErrorState
      v-if="loadState === 'error'"
      :title="t('anomaly.errLoadTitle')"
      :description="loadError"
      @retry="load"
    />

    <template v-else>
      <p v-if="checkBanner" class="check-banner" role="status">{{ checkBanner }}</p>

      <!-- ── Rule configuration ──────────────────────────────────────────── -->
      <section class="panel" aria-labelledby="rules-heading">
        <h2 id="rules-heading" class="panel-title">{{ t('anomaly.rulesHeading') }}</h2>

        <form class="rule-form" @submit.prevent="submitRule">
          <FormField :label="t('anomaly.fieldMetric')" field-id="rule-metric" class="rule-form__field">
            <select id="rule-metric" v-model="form.metric" class="select">
              <option value="cpu">{{ t('anomaly.metricCpu') }}</option>
              <option value="memory">{{ t('anomaly.metricMemory') }}</option>
              <option value="disk">{{ t('anomaly.metricDisk') }}</option>
            </select>
          </FormField>

          <FormField :label="t('anomaly.fieldOperator')" field-id="rule-operator" class="rule-form__field">
            <select id="rule-operator" v-model="form.operator" class="select">
              <option value="gt">{{ t('anomaly.operatorGt') }}</option>
              <option value="lt">{{ t('anomaly.operatorLt') }}</option>
            </select>
          </FormField>

          <FormField :label="t('anomaly.fieldThreshold')" field-id="rule-threshold" class="rule-form__field">
            <input
              id="rule-threshold"
              v-model.number="form.threshold"
              type="number"
              step="0.1"
              class="input"
            />
          </FormField>

          <FormField :label="t('anomaly.fieldScope')" field-id="rule-server" class="rule-form__field rule-form__field--wide">
            <select id="rule-server" v-model="form.serverId" class="select">
              <option value="">{{ t('anomaly.scopeAllGlobal') }}</option>
              <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </FormField>

          <div class="rule-form__submit">
            <AppButton type="submit" variant="default" :loading="creating">
              {{ t('anomaly.addRule') }}
            </AppButton>
          </div>
        </form>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <p v-if="!hasServers" class="hint">
          {{ t('anomaly.noServersHint') }}
        </p>

        <!-- Rule list -->
        <div v-if="loadState === 'loading' && rules.length === 0" class="rule-skeletons" aria-busy="true">
          <SkeletonBlock v-for="n in 2" :key="n" :height="44" width="100%" />
        </div>
        <EmptyState
          v-else-if="rules.length === 0"
          :title="t('anomaly.rulesEmptyTitle')"
          :description="t('anomaly.rulesEmptyDesc')"
        />
        <ul v-else class="rule-list">
          <li v-for="rule in rules" :key="rule.id" class="rule-item">
            <!-- view mode -->
            <template v-if="editingRuleId !== rule.id">
              <div class="rule-item__main">
                <span class="rule-item__expr">
                  <strong>{{ metricLabel(rule.metric) }}</strong>
                  {{ operatorSymbol(rule.operator) }} {{ rule.threshold }}%
                </span>
                <span class="rule-item__scope">{{ serverName(rule.serverId) }}</span>
                <span v-if="!rule.enabled" class="rule-item__disabled">{{ t('anomaly.disabled') }}</span>
              </div>
              <div class="rule-item__actions">
                <AppButton variant="ghost" @click="startEdit(rule)">{{ t('anomaly.edit') }}</AppButton>
                <AppButton variant="ghost" :loading="deletingId === rule.id" @click="removeRule(rule)">
                  {{ t('anomaly.delete') }}
                </AppButton>
              </div>
            </template>

            <!-- edit mode (inline) -->
            <form v-else class="rule-edit" @submit.prevent="saveEdit(rule)">
              <select v-model="editForm.metric" class="select" :aria-label="t('anomaly.fieldMetric')">
                <option value="cpu">{{ t('anomaly.metricCpu') }}</option>
                <option value="memory">{{ t('anomaly.metricMemory') }}</option>
                <option value="disk">{{ t('anomaly.metricDisk') }}</option>
              </select>
              <select v-model="editForm.operator" class="select" :aria-label="t('anomaly.fieldOperator')">
                <option value="gt">{{ t('anomaly.operatorGt') }}</option>
                <option value="lt">{{ t('anomaly.operatorLt') }}</option>
              </select>
              <input
                v-model.number="editForm.threshold"
                type="number"
                step="0.1"
                class="input rule-edit__threshold"
                :aria-label="t('anomaly.fieldThreshold')"
              />
              <select v-model="editForm.serverId" class="select" :aria-label="t('anomaly.fieldScope')">
                <option value="">{{ t('anomaly.scopeAllGlobal') }}</option>
                <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
              </select>
              <div class="rule-edit__actions">
                <AppButton type="submit" variant="primary" :loading="savingEdit">{{ t('anomaly.save') }}</AppButton>
                <AppButton type="button" variant="ghost" @click="cancelEdit">{{ t('anomaly.cancel') }}</AppButton>
              </div>
            </form>
          </li>
        </ul>
      </section>

      <!-- ── Metric trends ───────────────────────────────────────────────── -->
      <section class="panel" aria-labelledby="trend-heading">
        <div class="trend-head">
          <h2 id="trend-heading" class="panel-title">{{ t('anomaly.trendHeading') }}</h2>
          <div class="trend-controls">
            <select v-model="trendServerId" class="select trend-controls__server" :aria-label="t('anomaly.trendServer')">
              <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
            <div class="trend-range" role="group" :aria-label="t('anomaly.trendRange')">
              <button
                v-for="r in [{ h: 1, k: 'range1h' }, { h: 6, k: 'range6h' }, { h: 24, k: 'range24h' }]"
                :key="r.h"
                type="button"
                class="trend-range__btn"
                :class="{ 'trend-range__btn--active': trendHours === r.h }"
                @click="trendHours = r.h"
              >{{ t(`anomaly.${r.k}`) }}</button>
            </div>
          </div>
        </div>
        <MetricTrendChart :points="trendPoints" />
      </section>

      <!-- ── Alerts ──────────────────────────────────────────────────────── -->
      <section class="panel" aria-labelledby="alerts-heading">
        <h2 id="alerts-heading" class="panel-title">{{ t('anomaly.alertsHeading') }}</h2>

        <div v-if="loadState === 'loading' && alerts.length === 0" class="rule-skeletons" aria-busy="true">
          <SkeletonBlock v-for="n in 3" :key="n" :height="52" width="100%" />
        </div>
        <EmptyState
          v-else-if="alerts.length === 0"
          :title="t('anomaly.alertsEmptyTitle')"
          :description="t('anomaly.alertsEmptyDesc')"
        />
        <ul v-else class="alert-list">
          <li
            v-for="a in alerts"
            :key="a.id"
            class="alert-item"
            :class="`alert-item--${severity(a)}`"
          >
            <span class="alert-item__dot" aria-hidden="true"></span>
            <div class="alert-item__body">
              <p class="alert-item__msg">{{ a.message }}</p>
              <p class="alert-item__meta">
                {{ a.serverName }} · {{ metricLabel(a.metric) }}
                {{ operatorSymbol(a.operator) }} {{ a.threshold }}%
                · {{ t('anomaly.actual', { n: a.value.toFixed(1) }) }}
                · {{ formatAt(a.at) }}
              </p>
            </div>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.anomaly {
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

.check-banner {
  margin: 0;
  padding: 10px 14px;
  border-radius: var(--rounded-md, 8px);
  background: var(--color-surface-raised, var(--color-surface));
  border: 1px solid var(--color-line);
  font-size: var(--text-label);
  color: var(--color-text);
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 20px;
  border: 1px solid var(--color-line);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-surface);
}
.panel-title {
  margin: 0;
  font-size: var(--text-h2, 1.15rem);
  font-weight: 650;
  color: var(--color-text);
}

.rule-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 14px;
  align-items: end;
}
.rule-form__field--wide {
  grid-column: span 2;
}
.rule-form__submit {
  display: flex;
  align-items: flex-end;
}

.select,
.input {
  width: 100%;
  box-sizing: border-box;
  padding: 8px 10px;
  font-size: var(--text-body, 0.9rem);
  color: var(--color-text);
  background: var(--color-bg, var(--color-surface));
  border: 1px solid var(--color-line);
  border-radius: var(--rounded-md, 8px);
}
.select:focus-visible,
.input:focus-visible {
  outline: 2px solid var(--color-accent, #4f7cff);
  outline-offset: 1px;
}

.form-error {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-danger, #d64545);
}
.hint {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-faint, var(--color-dim));
}

.rule-skeletons {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.rule-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.rule-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--color-line);
  border-radius: var(--rounded-md, 8px);
  background: var(--color-bg, var(--color-surface));
}
.rule-item__main {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.rule-item__expr {
  font-size: var(--text-body, 0.9rem);
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.rule-item__scope {
  font-size: var(--text-label);
  color: var(--color-dim);
}
.rule-item__disabled {
  font-size: var(--text-caption, 0.75rem);
  color: var(--color-faint, var(--color-dim));
  border: 1px solid var(--color-line);
  border-radius: 999px;
  padding: 1px 8px;
}

.alert-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.alert-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--color-line);
  border-left-width: 3px;
  border-radius: var(--rounded-md, 8px);
  background: var(--color-bg, var(--color-surface));
}
.alert-item__dot {
  flex: none;
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 999px;
  background: var(--color-dim);
}
.alert-item--critical {
  border-left-color: var(--color-danger, #d64545);
}
.alert-item--critical .alert-item__dot {
  background: var(--color-danger, #d64545);
}
.alert-item--warning {
  border-left-color: var(--color-warning, #e0a32e);
}
.alert-item--warning .alert-item__dot {
  background: var(--color-warning, #e0a32e);
}
.alert-item--info {
  border-left-color: var(--color-accent, #4f7cff);
}
.alert-item--info .alert-item__dot {
  background: var(--color-accent, #4f7cff);
}
.alert-item__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.alert-item__msg {
  margin: 0;
  font-size: var(--text-body, 0.9rem);
  font-weight: 550;
  color: var(--color-text);
}
.alert-item__meta {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
  font-variant-numeric: tabular-nums;
}

/* ── cadence badge ─────────────────────────────────────────────────────── */
.view-header__actions {
  display: flex;
  align-items: center;
  gap: 14px;
}
.cadence {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-label);
  color: var(--color-dim);
  white-space: nowrap;
}
.cadence__dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: var(--color-success, #16a34a);
  box-shadow: 0 0 0 3px color-mix(in oklch, var(--color-success, #16a34a) 22%, transparent);
  animation: cadence-pulse 2s ease-in-out infinite;
}
.cadence--off .cadence__dot {
  background: var(--color-faint, var(--color-dim));
  box-shadow: none;
  animation: none;
}
.cadence__next {
  color: var(--color-faint, var(--color-dim));
  font-variant-numeric: tabular-nums;
}
@keyframes cadence-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.45; }
}

/* ── inline rule edit ──────────────────────────────────────────────────── */
.rule-item__actions {
  display: flex;
  gap: 8px;
  flex: none;
}
.rule-edit {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  width: 100%;
}
.rule-edit .select {
  width: auto;
  min-width: 120px;
}
.rule-edit__threshold {
  width: 96px;
}
.rule-edit__actions {
  display: flex;
  gap: 8px;
  margin-left: auto;
}

/* ── trend section ─────────────────────────────────────────────────────── */
.trend-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}
.trend-controls {
  display: flex;
  align-items: center;
  gap: 12px;
}
.trend-controls__server {
  width: auto;
  min-width: 160px;
}
.trend-range {
  display: inline-flex;
  border: 1px solid var(--color-line, var(--color-border));
  border-radius: var(--rounded-md, 8px);
  overflow: hidden;
}
.trend-range__btn {
  padding: 6px 12px;
  font-size: var(--text-label);
  color: var(--color-dim);
  background: var(--color-surface);
  border: none;
  border-left: 1px solid var(--color-line, var(--color-border));
  cursor: pointer;
}
.trend-range__btn:first-child {
  border-left: none;
}
.trend-range__btn--active {
  background: var(--color-accent, #4f7cff);
  color: #fff;
}
</style>

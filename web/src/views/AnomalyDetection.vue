<!--
  AnomalyDetection.vue — Story 6-5: 可配置异常检测与告警(FR-23)

  管理员配阈值规则(metric ∈ cpu/memory/disk;operator gt/lt;threshold;作用域全局/指定服务器),
  「立即检测」对服务器指标(经 6-1 SSH 采集)逐规则求值,命中产告警并列表展示(按严重度着色)。
    · 不可达 / 指标不可得的服务器跳过(不误报),由后端保证。
    · 这是**新增**入口,不动 6-1 状态页 / 6-2 日志 / 6-3 操作入口。
-->
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  listAnomalyRules,
  createAnomalyRule,
  deleteAnomalyRule,
  checkAnomaly,
  listAnomalyAlerts,
  type AnomalyRule,
  type AnomalyAlert,
  type AnomalyMetric,
  type AnomalyOperator,
} from '../api/anomaly'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import AppButton from '../components/ui/AppButton.vue'
import FormField from '../components/ui/FormField.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

type LoadState = 'idle' | 'loading' | 'error'

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

const METRIC_LABELS: Record<AnomalyMetric, string> = {
  cpu: 'CPU 使用率',
  memory: '内存使用率',
  disk: '磁盘使用率',
}
const OPERATOR_LABELS: Record<AnomalyOperator, string> = {
  gt: '大于',
  lt: '小于',
}

function metricLabel(m: AnomalyMetric): string {
  return METRIC_LABELS[m] ?? m
}
function operatorSymbol(op: AnomalyOperator): string {
  return op === 'gt' ? '>' : '<'
}
function serverName(id: string | null): string {
  if (!id) return '全部服务器'
  const s = servers.value.find((x) => x.id === id)
  return s ? s.name : '(服务器已删除)'
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
    loadError.value = humanError(err, '加载异常检测配置失败')
    loadState.value = 'error'
  }
}

function humanError(err: unknown, fallback: string): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return '无法连接到服务器,请检查后端是否运行后重试'
    return err.apiError?.message ?? `${fallback}(${err.status})`
  }
  return `${fallback},请稍后重试`
}

// ─── create rule ───────────────────────────────────────────────────────────────

async function submitRule(): Promise<void> {
  formError.value = ''
  if (!Number.isFinite(form.value.threshold)) {
    formError.value = '阈值必须是数字'
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
    formError.value = humanError(err, '创建规则失败')
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
    checkBanner.value = humanError(err, '删除规则失败')
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
        ? '检测完成:本次未命中任何规则'
        : `检测完成:本次新增 ${fresh.length} 条告警`
  } catch (err) {
    checkBanner.value = humanError(err, '执行检测失败')
  } finally {
    checking.value = false
  }
}

function formatAt(at: string): string {
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? at : d.toLocaleString()
}

onMounted(load)
</script>

<template>
  <div class="anomaly">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">异常检测</h1>
        <p class="view-sub">
          配置阈值规则,对服务器 CPU / 内存 / 磁盘指标做检测,命中即产告警
        </p>
      </div>
      <AppButton variant="primary" :loading="checking" @click="runCheck">
        立即检测
      </AppButton>
    </header>

    <ErrorState
      v-if="loadState === 'error'"
      title="加载失败"
      :description="loadError"
      @retry="load"
    />

    <template v-else>
      <p v-if="checkBanner" class="check-banner" role="status">{{ checkBanner }}</p>

      <!-- ── Rule configuration ──────────────────────────────────────────── -->
      <section class="panel" aria-labelledby="rules-heading">
        <h2 id="rules-heading" class="panel-title">检测规则</h2>

        <form class="rule-form" @submit.prevent="submitRule">
          <FormField label="指标" field-id="rule-metric" class="rule-form__field">
            <select id="rule-metric" v-model="form.metric" class="select">
              <option value="cpu">CPU 使用率</option>
              <option value="memory">内存使用率</option>
              <option value="disk">磁盘使用率</option>
            </select>
          </FormField>

          <FormField label="条件" field-id="rule-operator" class="rule-form__field">
            <select id="rule-operator" v-model="form.operator" class="select">
              <option value="gt">大于</option>
              <option value="lt">小于</option>
            </select>
          </FormField>

          <FormField label="阈值 (%)" field-id="rule-threshold" class="rule-form__field">
            <input
              id="rule-threshold"
              v-model.number="form.threshold"
              type="number"
              step="0.1"
              class="input"
            />
          </FormField>

          <FormField label="作用范围" field-id="rule-server" class="rule-form__field rule-form__field--wide">
            <select id="rule-server" v-model="form.serverId" class="select">
              <option value="">全部服务器(全局)</option>
              <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
            </select>
          </FormField>

          <div class="rule-form__submit">
            <AppButton type="submit" variant="default" :loading="creating">
              添加规则
            </AppButton>
          </div>
        </form>
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
        <p v-if="!hasServers" class="hint">
          尚无已登记服务器。规则仍可保存,但需先在「设置 › 服务器」登记目标后检测才有数据。
        </p>

        <!-- Rule list -->
        <div v-if="loadState === 'loading' && rules.length === 0" class="rule-skeletons" aria-busy="true">
          <SkeletonBlock v-for="n in 2" :key="n" :height="44" width="100%" />
        </div>
        <EmptyState
          v-else-if="rules.length === 0"
          title="尚无检测规则"
          description="添加一条规则(如 CPU 大于 90%)即可开始检测。"
        />
        <ul v-else class="rule-list">
          <li v-for="rule in rules" :key="rule.id" class="rule-item">
            <div class="rule-item__main">
              <span class="rule-item__expr">
                <strong>{{ metricLabel(rule.metric) }}</strong>
                {{ operatorSymbol(rule.operator) }} {{ rule.threshold }}%
              </span>
              <span class="rule-item__scope">{{ serverName(rule.serverId) }}</span>
              <span v-if="!rule.enabled" class="rule-item__disabled">已停用</span>
            </div>
            <AppButton
              variant="ghost"
              :loading="deletingId === rule.id"
              @click="removeRule(rule)"
            >
              删除
            </AppButton>
          </li>
        </ul>
      </section>

      <!-- ── Alerts ──────────────────────────────────────────────────────── -->
      <section class="panel" aria-labelledby="alerts-heading">
        <h2 id="alerts-heading" class="panel-title">最近告警</h2>

        <div v-if="loadState === 'loading' && alerts.length === 0" class="rule-skeletons" aria-busy="true">
          <SkeletonBlock v-for="n in 3" :key="n" :height="52" width="100%" />
        </div>
        <EmptyState
          v-else-if="alerts.length === 0"
          title="暂无告警"
          description="运行「立即检测」后,命中规则的告警会显示在这里。"
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
                · 实际 {{ a.value.toFixed(1) }}%
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
</style>

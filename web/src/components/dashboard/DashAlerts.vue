<script setup lang="ts">
/**
 * DashAlerts — 概览页「异常告警 + AI 诊断」(presentational)。
 * 上:最近资源异常告警(磁盘/内存/CPU 越阈)。下:AI 失败诊断闭环准确率。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { AnomalyAlert } from '../../api/anomaly'
import type { DiagnosisStats } from '../../api/settings'

const props = defineProps<{
  alerts: AnomalyAlert[]
  diagnosis: DiagnosisStats | null
  loading: boolean
}>()

const router = useRouter()
const { t } = useI18n()

const recent = computed(() => props.alerts.slice(0, 4))

const accuracyPct = computed(() => {
  const a = props.diagnosis?.accuracy
  return a === null || a === undefined ? null : Math.round(a * 100)
})

function fmtAgo(rfc: string): string {
  const ts = new Date(rfc).getTime()
  if (Number.isNaN(ts)) return ''
  const m = Math.floor(Math.max(0, Date.now() - ts) / 60000)
  if (m < 60) return t('timeShort.min', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('timeShort.hour', { n: h })
  return t('timeShort.day', { n: Math.floor(h / 24) })
}
</script>

<template>
  <section class="card alerts" aria-labelledby="dash-alerts-h">
    <header class="card-head">
      <h2 id="dash-alerts-h" class="card-title">{{ t('dash.alertsTitle') }}</h2>
      <button class="card-link" type="button" @click="router.push('/anomaly')">{{ t('dash.anomalyLink') }}</button>
    </header>

    <div v-if="loading" class="al-skeleton" aria-busy="true">
      <span v-for="i in 3" :key="i" class="sk-row" />
    </div>

    <template v-else>
      <!-- 异常告警 -->
      <div class="al-block">
        <div class="al-sub">{{ t('dash.resourceAnomalies') }}</div>
        <p v-if="!recent.length" class="al-none">{{ t('dash.noAnomalies') }}</p>
        <ul v-else class="al-list" role="list">
          <li v-for="a in recent" :key="a.id" class="al-row">
            <span class="al-icon" :class="a.metric" aria-hidden="true" />
            <span class="al-msg">{{ a.message }}</span>
            <span class="al-srv">{{ a.serverName }}</span>
            <span class="al-time">{{ fmtAgo(a.at) }}</span>
          </li>
        </ul>
      </div>

      <!-- AI 诊断准确率 -->
      <div class="al-block diag">
        <div class="al-sub">{{ t('dash.aiDiagLoop') }}</div>
        <div v-if="(diagnosis?.totalFeedback ?? 0) === 0" class="al-none">{{ t('dash.noDiagFeedback') }}</div>
        <div v-else class="diag-row">
          <div class="diag-acc">
            <span class="diag-acc-val">{{ accuracyPct ?? '—' }}<span class="diag-acc-unit">%</span></span>
            <span class="diag-acc-label">{{ t('dash.accuracy') }}</span>
          </div>
          <div class="diag-counts">
            <span class="diag-up">👍 {{ diagnosis?.thumbsUp ?? 0 }}</span>
            <span class="diag-down">👎 {{ diagnosis?.thumbsDown ?? 0 }}</span>
            <span class="diag-total">{{ t('dash.feedbackTotal', { n: diagnosis?.totalFeedback ?? 0 }) }}</span>
          </div>
          <button class="card-link" type="button" @click="router.push('/settings/diagnosis-stats')">{{ t('common.detailArrow') }}</button>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.al-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.al-block.diag {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px dashed var(--color-border);
}
.al-sub {
  font-size: var(--text-micro);
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-faint);
}
.al-none {
  font-size: var(--text-caption);
  color: var(--color-text-soft);
  margin: 0;
}
.al-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.al-row {
  display: grid;
  grid-template-columns: auto 1fr auto auto;
  align-items: center;
  gap: 9px;
  padding: 6px 2px;
}
.al-icon {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  background: var(--color-amber);
}
.al-icon.disk {
  background: var(--color-red);
}
.al-icon.memory {
  background: var(--color-amber);
}
.al-icon.cpu {
  background: var(--color-cyan);
}
.al-msg {
  font-size: var(--text-caption);
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.al-srv {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
}
.al-time {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.diag-row {
  display: flex;
  align-items: center;
  gap: 16px;
}
.diag-acc {
  display: flex;
  flex-direction: column;
  align-items: center;
  line-height: 1;
}
.diag-acc-val {
  font-size: var(--text-kpi);
  font-weight: 800;
  color: var(--color-success);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
}
.diag-acc-unit {
  font-size: var(--text-body);
  margin-left: 1px;
}
.diag-acc-label {
  font-size: var(--text-micro);
  color: var(--color-faint);
  margin-top: 3px;
}
.diag-counts {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--text-caption);
  color: var(--color-text-soft);
  flex: 1;
}
.diag-total {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.al-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.sk-row {
  height: 30px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--color-inset), var(--color-surface-hover), var(--color-inset));
  background-size: 200% 100%;
  animation: dash-shimmer 1.3s ease-in-out infinite;
}
@keyframes dash-shimmer {
  to {
    background-position: -200% 0;
  }
}
</style>

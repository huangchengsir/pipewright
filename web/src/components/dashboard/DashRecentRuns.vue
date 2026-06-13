<script setup lang="ts">
/**
 * DashRecentRuns — 概览页「最近运行」feed(presentational)。
 * 收最近若干条 RunListItem,渲染状态徽章 + 项目 + 触发源 + 耗时 + 相对时间。
 * 整卡点击进运行列表;单条点击进该 run 详情。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { RunListItem, RunStatus } from '../../api/runs'

const props = defineProps<{
  runs: RunListItem[]
  loading: boolean
}>()

const router = useRouter()
const { t } = useI18n()

// RunStatus → 语义色调(成功/失败/进行/等待/回滚);文案走 i18n `runStatus.*`。
const STATUS_TONE: Record<RunStatus, string> = {
  queued: 'idle',
  running: 'run',
  waiting_approval: 'wait',
  success: 'ok',
  failed: 'err',
  partial_failed: 'warn',
  rolled_back: 'roll',
}

const items = computed(() => props.runs.slice(0, 10))

function fmtDuration(ms: number | null): string {
  if (ms === null || ms < 0) return '—'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${s % 60 ? ` ${s % 60}s` : ''}`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

function fmtAgo(rfc: string): string {
  const ts = new Date(rfc).getTime()
  if (Number.isNaN(ts)) return ''
  const diff = Math.max(0, Date.now() - ts)
  const m = Math.floor(diff / 60000)
  if (m < 1) return t('time.justNow')
  if (m < 60) return t('time.minAgo', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('time.hourAgo', { n: h })
  return t('time.dayAgo', { n: Math.floor(h / 24) })
}
</script>

<template>
  <section class="card runs" aria-labelledby="dash-runs-h">
    <header class="card-head">
      <h2 id="dash-runs-h" class="card-title">{{ t('dash.recentRuns') }}</h2>
      <button class="card-link" type="button" @click="router.push('/runs')">{{ t('common.allArrow') }}</button>
    </header>

    <div v-if="loading" class="runs-skeleton" aria-busy="true">
      <span v-for="i in 5" :key="i" class="sk-row" />
    </div>

    <p v-else-if="!items.length" class="card-empty">{{ t('dash.recentRunsEmpty') }}</p>

    <ul v-else class="runs-list" role="list">
      <li v-for="r in items" :key="r.id">
        <button class="run-row" type="button" @click="router.push(`/runs/${r.id}`)">
          <span class="run-badge" :class="`t-${STATUS_TONE[r.status]}`">
            <span class="run-dot" aria-hidden="true" />
            {{ t(`runStatus.${r.status}`) }}
          </span>
          <span class="run-project">{{ r.projectName }}</span>
          <span class="run-trigger">{{ r.trigger?.branch ?? '' }}</span>
          <span class="run-dur">{{ fmtDuration(r.durationMs) }}</span>
          <span class="run-ago">{{ fmtAgo(r.createdAt) }}</span>
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.runs {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.runs-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.run-row {
  width: 100%;
  display: grid;
  grid-template-columns: 96px 1fr auto auto auto;
  align-items: center;
  gap: 12px;
  padding: 10px 8px;
  background: none;
  border: none;
  border-radius: var(--radius-sm);
  cursor: pointer;
  text-align: left;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast);
}
.run-row:hover {
  background: var(--color-surface-hover);
}
.runs-list li:last-child .run-row {
  border-bottom: none;
}
.run-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
  justify-self: start;
}
.run-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.t-ok {
  color: var(--color-success);
  background: var(--color-green-soft);
}
.t-err {
  color: var(--color-danger);
  background: var(--color-danger-soft);
}
.t-run {
  color: var(--color-primary);
  background: var(--color-primary-soft);
}
.t-run .run-dot {
  animation: dash-pulse 1.4s ease-out infinite;
}
.t-wait {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.t-warn {
  color: var(--color-amber);
  background: var(--color-amber-soft);
}
.t-roll {
  color: var(--color-dim);
  background: var(--color-inset);
}
.t-idle {
  color: var(--color-faint);
  background: var(--color-inset);
}
.run-project {
  font-size: var(--text-caption);
  font-weight: 600;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run-trigger {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-family: var(--font-mono);
}
.run-dur {
  font-size: var(--text-micro);
  color: var(--color-text-soft);
  font-variant-numeric: tabular-nums;
}
.run-ago {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
}
.runs-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 0;
}
.sk-row {
  height: 38px;
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
@keyframes dash-pulse {
  0% {
    box-shadow: 0 0 0 0 currentColor;
    opacity: 1;
  }
  100% {
    box-shadow: 0 0 0 5px transparent;
    opacity: 0.7;
  }
}
@media (max-width: 720px) {
  .run-row {
    grid-template-columns: 84px 1fr auto;
  }
  .run-trigger,
  .run-dur {
    display: none;
  }
}
</style>

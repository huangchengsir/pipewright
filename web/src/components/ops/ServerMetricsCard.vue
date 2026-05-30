<!--
  ServerMetricsCard.vue — Story 6-1: 多机状态总览(FR-15,服务器层资源指标)

  单台已登记服务器的指标卡:
    · 名称 + reachable 徽标(可达 / 不可达)
    · CPU:1 分钟负载 + 核数(负载相对核数着色:>1×核数 偏红、>0.7× 偏黄)
    · 内存 used/total 进度条 + 人读字节
    · 磁盘 used/total 进度条 + 人读字节
    · 某指标 null → 该行标「不可用」(跨平台 best-effort,如 macOS 无 free → memory 不可用)
    · 不可达 → 整卡灰显 + 人读错误(绝不含凭据明文)

  复用 1-6 ui:ProgressBar(进度条)。徽标用本组件内联(reachable 语义,非 6-state run 状态)。
-->
<script setup lang="ts">
import { computed } from 'vue'
import ProgressBar, { type ProgressVariant } from '../ui/ProgressBar.vue'
import type { ServerMetrics } from '../../api/servers'

const props = defineProps<{
  /** 展示用服务器名(列表 join 而来)。 */
  name: string
  /** 该台指标(reachable:false 时各指标为 null)。 */
  metrics: ServerMetrics
}>()

// ─── derived display ───────────────────────────────────────────────────────────

/** 用量百分比(0–100);分母为 0 或缺失 → null(不渲染进度)。 */
function pct(used: number, total: number): number | null {
  if (!total || total <= 0) return null
  return Math.min(100, Math.max(0, (used / total) * 100))
}

/** 进度条着色:>90% 红、>75% 黄、否则默认。 */
function usageVariant(percent: number | null): ProgressVariant {
  if (percent === null) return 'default'
  if (percent >= 90) return 'error'
  if (percent >= 75) return 'warn'
  return 'default'
}

/** 人读字节(二进制单位,1 位小数)。 */
function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`
}

const memPercent = computed(() =>
  props.metrics.memory ? pct(props.metrics.memory.usedBytes, props.metrics.memory.totalBytes) : null,
)
const diskPercent = computed(() =>
  props.metrics.disk ? pct(props.metrics.disk.usedBytes, props.metrics.disk.totalBytes) : null,
)

/** CPU 负载相对核数的健康着色(无核数则不着色)。 */
const loadVariant = computed<ProgressVariant>(() => {
  const cpu = props.metrics.cpu
  if (!cpu || cpu.loadavg1 === null) return 'default'
  const cores = cpu.cores ?? 0
  if (cores <= 0) return 'default'
  const ratio = cpu.loadavg1 / cores
  if (ratio >= 1) return 'error'
  if (ratio >= 0.7) return 'warn'
  return 'default'
})

const loadText = computed(() => {
  const cpu = props.metrics.cpu
  if (!cpu || cpu.loadavg1 === null) return '不可用'
  const coresText = cpu.cores !== null ? ` / ${cpu.cores} 核` : ''
  return `${cpu.loadavg1.toFixed(2)}${coresText}`
})
</script>

<template>
  <article
    class="metrics-card"
    :class="{ 'metrics-card--unreachable': !metrics.reachable }"
    :aria-label="`服务器 ${name} 指标`"
  >
    <header class="metrics-card__head">
      <h3 class="metrics-card__name" :title="name">{{ name }}</h3>
      <span
        class="reach-badge"
        :class="metrics.reachable ? 'reach-badge--ok' : 'reach-badge--down'"
        role="status"
      >
        <span class="reach-badge__dot" aria-hidden="true" />
        {{ metrics.reachable ? '可达' : '不可达' }}
      </span>
    </header>

    <!-- Unreachable: human error, no metrics rows -->
    <p v-if="!metrics.reachable" class="metrics-card__error" role="alert">
      {{ metrics.error || '无法采集指标' }}
    </p>

    <!-- Reachable: metric rows (each independently degradable) -->
    <dl v-else class="metrics-card__body">
      <!-- CPU -->
      <div class="metric-row">
        <dt class="metric-row__label">CPU 负载</dt>
        <dd class="metric-row__value">
          <span
            class="metric-num"
            :class="{
              'metric-num--warn': loadVariant === 'warn',
              'metric-num--error': loadVariant === 'error',
              'metric-num--na': loadText === '不可用',
            }"
            >{{ loadText }}</span
          >
        </dd>
      </div>

      <!-- Memory -->
      <div class="metric-row">
        <dt class="metric-row__label">内存</dt>
        <dd class="metric-row__value">
          <template v-if="metrics.memory && memPercent !== null">
            <ProgressBar
              :value="memPercent"
              :variant="usageVariant(memPercent)"
              :label="`内存使用 ${memPercent.toFixed(0)}%`"
            />
            <span class="metric-sub">
              {{ humanBytes(metrics.memory.usedBytes) }} / {{ humanBytes(metrics.memory.totalBytes) }}
              <span class="metric-pct">({{ memPercent.toFixed(0) }}%)</span>
            </span>
          </template>
          <span v-else class="metric-num metric-num--na">不可用</span>
        </dd>
      </div>

      <!-- Disk -->
      <div class="metric-row">
        <dt class="metric-row__label">磁盘 {{ metrics.disk?.path ?? '/' }}</dt>
        <dd class="metric-row__value">
          <template v-if="metrics.disk && diskPercent !== null">
            <ProgressBar
              :value="diskPercent"
              :variant="usageVariant(diskPercent)"
              :label="`磁盘使用 ${diskPercent.toFixed(0)}%`"
            />
            <span class="metric-sub">
              {{ humanBytes(metrics.disk.usedBytes) }} / {{ humanBytes(metrics.disk.totalBytes) }}
              <span class="metric-pct">({{ diskPercent.toFixed(0) }}%)</span>
            </span>
          </template>
          <span v-else class="metric-num metric-num--na">不可用</span>
        </dd>
      </div>
    </dl>

    <footer v-if="metrics.reachable" class="metrics-card__foot">
      采集于 {{ new Date(metrics.collectedAt).toLocaleTimeString() }}
    </footer>
  </article>
</template>

<style scoped>
.metrics-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px 18px 14px;
  border: 1px solid var(--color-line);
  border-radius: var(--rounded-lg);
  background: var(--color-surface);
  transition: border-color var(--duration-fast) var(--ease-out-expo);
}
.metrics-card:hover {
  border-color: var(--color-line-strong, var(--color-line));
}
.metrics-card--unreachable {
  opacity: 0.62;
  background: var(--color-inset);
}

.metrics-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.metrics-card__name {
  margin: 0;
  font-size: var(--text-body);
  font-weight: 650;
  color: var(--color-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ——— reachability badge (reachable semantics, distinct from run StatusBadge) ——— */
.reach-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
  font-size: var(--text-label);
  font-weight: 600;
  line-height: 1;
  padding: 3px 9px 3px 8px;
  border-radius: var(--rounded-md);
}
.reach-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}
.reach-badge--ok {
  color: var(--color-green);
  background: var(--color-green-soft);
}
.reach-badge--ok .reach-badge__dot {
  background: var(--color-green);
}
.reach-badge--down {
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
}
.reach-badge--down .reach-badge__dot {
  background: var(--color-red);
}

.metrics-card__error {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-red);
  line-height: 1.5;
}

.metrics-card__body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin: 0;
}
.metric-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.metric-row__label {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
}
.metric-row__value {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.metric-num {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  font-size: var(--text-body);
  color: var(--color-text);
}
.metric-num--warn {
  color: var(--color-amber);
}
.metric-num--error {
  color: var(--color-red);
}
.metric-num--na {
  color: var(--color-faint);
  font-weight: 500;
  font-style: italic;
}
.metric-sub {
  font-size: var(--text-label);
  color: var(--color-dim);
  font-variant-numeric: tabular-nums;
}
.metric-pct {
  color: var(--color-faint);
}

.metrics-card__foot {
  font-size: var(--text-label);
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}
</style>

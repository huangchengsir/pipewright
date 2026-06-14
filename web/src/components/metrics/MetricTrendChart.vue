<!--
  MetricTrendChart.vue — 服务器指标时序折线(纯 SVG,无第三方图表库)。

  把某台服务器的 CPU / 内存 / 磁盘使用率序列画成三条折线,y 轴固定 0–100%,x 轴为时间。
  · null 点(该时刻指标不可得)断线,不强连成误导走势。
  · compositor-friendly:静态 SVG path,无逐帧动画。
  · 数据不足(每条 < 2 个有效点)→ 占位提示,不报错。
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MetricPoint } from '../../api/anomaly'

const props = withDefaults(
  defineProps<{
    points: MetricPoint[]
    width?: number
    height?: number
  }>(),
  { width: 760, height: 260 },
)

const { t } = useI18n()

const padL = 34 // y 轴标签留白
const padR = 12
const padT = 12
const padB = 22 // x 轴时间标签留白

const SERIES = [
  { key: 'cpu' as const, color: 'var(--color-accent, #4f7cff)', labelKey: 'anomaly.metricCpu' },
  { key: 'memory' as const, color: 'var(--color-success, #16a34a)', labelKey: 'anomaly.metricMemory' },
  { key: 'disk' as const, color: 'var(--color-warning, #e0a32e)', labelKey: 'anomaly.metricDisk' },
]

const innerW = computed(() => props.width - padL - padR)
const innerH = computed(() => props.height - padT - padB)

/** 时间范围(ms);用首末点的时间戳。 */
const timeRange = computed(() => {
  const ts = props.points.map((p) => new Date(p.at).getTime()).filter((n) => !Number.isNaN(n))
  if (ts.length === 0) return null
  const min = Math.min(...ts)
  const max = Math.max(...ts)
  return { min, max, span: max - min || 1 }
})

function xFor(at: string): number {
  const r = timeRange.value
  if (!r) return padL
  const ms = new Date(at).getTime()
  return padL + ((ms - r.min) / r.span) * innerW.value
}
function yFor(v: number): number {
  // 0–100% 固定刻度;翻转使大值在上。
  const clamped = Math.max(0, Math.min(100, v))
  return padT + innerH.value - (clamped / 100) * innerH.value
}

/** 每条 series 拆成「连续非空段」的 path 列表(null 断线)。 */
function segmentsFor(key: 'cpu' | 'memory' | 'disk'): string[] {
  const segs: string[] = []
  let cur: string[] = []
  for (const p of props.points) {
    const v = p[key]
    if (v == null || Number.isNaN(new Date(p.at).getTime())) {
      if (cur.length >= 2) segs.push(cur.join(' '))
      cur = []
      continue
    }
    cur.push(`${cur.length === 0 ? 'M' : 'L'}${xFor(p.at).toFixed(1)},${yFor(v).toFixed(1)}`)
  }
  if (cur.length >= 2) segs.push(cur.join(' '))
  return segs
}

const lines = computed(() =>
  SERIES.map((s) => ({
    ...s,
    label: t(s.labelKey),
    paths: segmentsFor(s.key),
    last: lastValue(s.key),
  })),
)

function lastValue(key: 'cpu' | 'memory' | 'disk'): number | null {
  for (let i = props.points.length - 1; i >= 0; i--) {
    const v = props.points[i][key]
    if (v != null) return v
  }
  return null
}

/** y 轴刻度线(含 85/90 告警参考虚线由模板单独画)。 */
const yTicks = [0, 25, 50, 75, 100]

/** x 轴时间标签(首 / 中 / 末)。 */
const xLabels = computed(() => {
  const r = timeRange.value
  if (!r) return []
  const fmt = (ms: number) => {
    const d = new Date(ms)
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  }
  return [
    { x: padL, text: fmt(r.min), anchor: 'start' },
    { x: padL + innerW.value / 2, text: fmt(r.min + r.span / 2), anchor: 'middle' },
    { x: padL + innerW.value, text: fmt(r.max), anchor: 'end' },
  ]
})

const hasData = computed(() => lines.value.some((l) => l.paths.length > 0))
</script>

<template>
  <div class="trend">
    <div v-if="!hasData" class="trend__empty">{{ t('anomaly.trendEmpty') }}</div>
    <template v-else>
      <svg
        class="trend__svg"
        :viewBox="`0 0 ${width} ${height}`"
        :width="width"
        :height="height"
        role="img"
        :aria-label="t('anomaly.trendAria')"
        preserveAspectRatio="none"
      >
        <!-- y gridlines + labels -->
        <g class="trend__grid">
          <template v-for="tk in yTicks" :key="tk">
            <line :x1="padL" :y1="yFor(tk)" :x2="width - padR" :y2="yFor(tk)" />
            <text :x="padL - 6" :y="yFor(tk) + 3" text-anchor="end" class="trend__axis">{{ tk }}</text>
          </template>
        </g>
        <!-- 告警参考线 85 / 90 -->
        <line class="trend__ref" :x1="padL" :y1="yFor(85)" :x2="width - padR" :y2="yFor(85)" />
        <line class="trend__ref" :x1="padL" :y1="yFor(90)" :x2="width - padR" :y2="yFor(90)" />

        <!-- x time labels -->
        <text
          v-for="(xl, i) in xLabels"
          :key="i"
          :x="xl.x"
          :y="height - 6"
          :text-anchor="xl.anchor"
          class="trend__axis"
        >{{ xl.text }}</text>

        <!-- series lines (null breaks into segments) -->
        <template v-for="l in lines" :key="l.key">
          <path
            v-for="(d, i) in l.paths"
            :key="i"
            :d="d"
            :stroke="l.color"
            fill="none"
            class="trend__line"
          />
        </template>
      </svg>

      <!-- legend -->
      <ul class="trend__legend">
        <li v-for="l in lines" :key="l.key" class="trend__legend-item">
          <span class="trend__swatch" :style="{ background: l.color }" aria-hidden="true" />
          <span class="trend__legend-label">{{ l.label }}</span>
          <span class="trend__legend-val">{{ l.last != null ? l.last.toFixed(1) + '%' : '—' }}</span>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.trend {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.trend__empty {
  padding: 28px 0;
  text-align: center;
  font-size: var(--text-label);
  color: var(--color-faint, var(--color-dim));
}
.trend__svg {
  display: block;
  width: 100%;
  height: auto;
}
.trend__grid line {
  stroke: var(--color-line, var(--color-border));
  stroke-width: 1;
}
.trend__axis {
  fill: var(--color-faint, var(--color-dim));
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}
.trend__ref {
  stroke: var(--color-danger, #d64545);
  stroke-width: 1;
  stroke-dasharray: 2 4;
  opacity: 0.5;
}
.trend__line {
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}
.trend__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  list-style: none;
  margin: 0;
  padding: 0;
}
.trend__legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--text-label);
  color: var(--color-dim);
}
.trend__swatch {
  width: 10px;
  height: 10px;
  border-radius: 2px;
  flex: none;
}
.trend__legend-val {
  font-variant-numeric: tabular-nums;
  color: var(--color-text);
  font-weight: 600;
}
</style>

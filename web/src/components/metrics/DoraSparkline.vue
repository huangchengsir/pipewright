<!--
  DoraSparkline.vue — 趋势迷你折线(纯 SVG,无第三方图表库)。

  把一组数值画成紧凑折线 + 末点强调,供 DORA 卡片底部展示节奏走向。
  · 纯展示、无交互(图表作为设计系统的一部分,非事后堆叠)。
  · compositor-friendly:仅静态 SVG path,无逐帧布局动画。
  · 无数据(<2 点)→ 渲染一条基线占位,不报错。
-->
<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 等间距数值序列(如每桶部署数 / 失败率)。 */
    values: number[]
    /** 折线描边色(CSS 颜色或 var())。 */
    stroke?: string
    /** 区域填充色(渐变顶色;底部透明)。 */
    fill?: string
    width?: number
    height?: number
  }>(),
  {
    stroke: 'var(--color-primary)',
    fill: 'var(--color-primary-soft)',
    width: 220,
    height: 44,
  },
)

const pad = 3

/** 归一化后的折线点(x 等间距,y 翻转使大值在上)。 */
const points = computed(() => {
  const vs = props.values
  if (vs.length < 2) return []
  const max = Math.max(...vs)
  const min = Math.min(...vs)
  const span = max - min || 1
  const innerW = props.width - pad * 2
  const innerH = props.height - pad * 2
  return vs.map((v, i) => {
    const x = pad + (i / (vs.length - 1)) * innerW
    const y = pad + innerH - ((v - min) / span) * innerH
    return { x, y }
  })
})

const linePath = computed(() => {
  if (points.value.length === 0) return ''
  return points.value.map((p, i) => `${i === 0 ? 'M' : 'L'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')
})

const areaPath = computed(() => {
  if (points.value.length === 0) return ''
  const first = points.value[0]
  const last = points.value[points.value.length - 1]
  const baseY = props.height - pad
  return `${linePath.value} L${last.x.toFixed(1)},${baseY} L${first.x.toFixed(1)},${baseY} Z`
})

const lastPoint = computed(() =>
  points.value.length > 0 ? points.value[points.value.length - 1] : null,
)

const gradId = `spark-grad-${Math.random().toString(36).slice(2, 8)}`
</script>

<template>
  <svg
    class="sparkline"
    :viewBox="`0 0 ${width} ${height}`"
    :width="width"
    :height="height"
    role="img"
    aria-label="趋势走向"
    preserveAspectRatio="none"
  >
    <defs>
      <linearGradient :id="gradId" x1="0" y1="0" x2="0" y2="1">
        <stop offset="0%" :stop-color="fill" />
        <stop offset="100%" stop-color="transparent" />
      </linearGradient>
    </defs>

    <!-- baseline when too few points -->
    <line
      v-if="points.length < 2"
      :x1="pad"
      :y1="height / 2"
      :x2="width - pad"
      :y2="height / 2"
      class="sparkline__baseline"
    />

    <template v-else>
      <path :d="areaPath" :fill="`url(#${gradId})`" stroke="none" />
      <path :d="linePath" :stroke="stroke" fill="none" class="sparkline__line" />
      <circle
        v-if="lastPoint"
        :cx="lastPoint.x"
        :cy="lastPoint.y"
        r="2.6"
        :fill="stroke"
        class="sparkline__dot"
      />
    </template>
  </svg>
</template>

<style scoped>
.sparkline {
  display: block;
  overflow: visible;
}
.sparkline__line {
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}
.sparkline__baseline {
  stroke: var(--color-border-strong);
  stroke-width: 1;
  stroke-dasharray: 3 3;
}
.sparkline__dot {
  filter: drop-shadow(0 0 3px var(--color-primary-soft));
}
</style>

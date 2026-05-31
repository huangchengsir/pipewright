<!--
  DoraMetricCard.vue — 单个 DORA 指标卡(部署频率 / 前置时长 / 变更失败率 / MTTR)。

  设计意图(避开「dashboard-by-numbers」):
    · 强层级:大号主值(tabular-nums)+ 弱化的单位/口径行 + 顶部细绩效色条(语义色,非装饰)。
    · 绩效分档徽标(精英/高效/中等/待改进)按 band 着色 —— 颜色承载语义。
    · 底部 sparkline 把「趋势」纳入设计系统,而非事后堆图。
    · 无数据(sampleCount 0)→ 主值显「—」+ 「暂无数据」徽标,口径行解释为何为空。
    · hover 抬升 + 边框增强,聚焦态可见(键盘可达,卡片是 article 语义)。
-->
<script setup lang="ts">
import { computed } from 'vue'
import { type DoraBand, bandLabel } from '../../api/metrics'
import DoraSparkline from './DoraSparkline.vue'

const props = defineProps<{
  /** 指标标题(中文,如「部署频率」)。 */
  title: string
  /** 英文副标(如 "Deployment Frequency",编排排版的次要层级)。 */
  subtitle: string
  /** 主值显示串(已格式化;无数据时传 "—")。 */
  display: string
  /** 口径/解释行(如「30 天内 12 次成功部署」)。 */
  caption: string
  band: DoraBand
  /** 该指标样本数;0 ⇒ 无数据态。 */
  sampleCount: number
  /** sparkline 数值序列(可空)。 */
  trend: number[]
}>()

const hasData = computed(() => props.sampleCount > 0)

/** band → 语义色变量(用于色条 / 徽标 / sparkline)。 */
const bandColor = computed(() => {
  switch (props.band) {
    case 'elite':
      return 'var(--color-green)'
    case 'high':
      return 'var(--color-cyan)'
    case 'medium':
      return 'var(--color-amber)'
    case 'low':
      return 'var(--color-red)'
    default:
      return 'var(--color-faint)'
  }
})

const bandSoft = computed(() => {
  switch (props.band) {
    case 'elite':
      return 'var(--color-green-soft)'
    case 'high':
      return 'var(--color-cyan-soft)'
    case 'medium':
      return 'var(--color-amber-soft)'
    case 'low':
      return 'var(--color-red-soft)'
    default:
      return 'transparent'
  }
})

const label = computed(() => bandLabel(props.band))
</script>

<template>
  <article
    class="dora-card"
    :class="{ 'dora-card--empty': !hasData }"
    :style="{ '--band-color': bandColor, '--band-soft': bandSoft }"
    tabindex="0"
  >
    <!-- semantic performance bar (top) -->
    <span class="dora-card__bar" aria-hidden="true" />

    <header class="dora-card__head">
      <div class="dora-card__titles">
        <h2 class="dora-card__title">{{ title }}</h2>
        <p class="dora-card__subtitle">{{ subtitle }}</p>
      </div>
      <span class="dora-card__band" :data-band="band">{{ label }}</span>
    </header>

    <div class="dora-card__value-row">
      <span class="dora-card__value">{{ hasData ? display : '—' }}</span>
    </div>

    <p class="dora-card__caption">{{ caption }}</p>

    <div class="dora-card__spark" v-if="hasData && trend.length >= 2">
      <DoraSparkline :values="trend" :stroke="bandColor" :fill="bandSoft" :width="240" :height="40" />
    </div>
    <div class="dora-card__spark dora-card__spark--placeholder" v-else aria-hidden="true" />
  </article>
</template>

<style scoped>
.dora-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-5) var(--space-5) var(--space-4);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  transition:
    transform var(--duration-fast) var(--ease-out-expo),
    border-color var(--duration-fast) var(--ease-out-expo),
    box-shadow var(--duration-fast) var(--ease-out-expo);
}
.dora-card:hover {
  transform: translateY(-2px);
  border-color: var(--color-border-strong);
}
.dora-card:focus-visible {
  outline: 2px solid var(--band-color);
  outline-offset: 2px;
}
.dora-card--empty {
  box-shadow: none;
}

/* Top semantic bar — colour carries the band meaning. */
.dora-card__bar {
  position: absolute;
  inset: 0 0 auto 0;
  height: 3px;
  background: linear-gradient(90deg, var(--band-color), transparent 85%);
}

.dora-card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}
.dora-card__titles {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.dora-card__title {
  margin: 0;
  font-size: var(--text-section);
  font-weight: 650;
  color: var(--color-text);
  letter-spacing: 0.01em;
}
.dora-card__subtitle {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--text-micro);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--color-faint);
}

.dora-card__band {
  flex-shrink: 0;
  padding: 2px 9px;
  font-size: var(--text-caps);
  font-weight: 600;
  border-radius: var(--rounded-full);
  color: var(--band-color);
  background: var(--band-soft);
  border: 1px solid color-mix(in oklch, var(--band-color) 35%, transparent);
  white-space: nowrap;
}
.dora-card--empty .dora-card__band {
  color: var(--color-faint);
  border-color: var(--color-border);
}

.dora-card__value-row {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
}
.dora-card__value {
  font-size: var(--text-kpi);
  font-weight: 700;
  line-height: 1;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.01em;
}
.dora-card--empty .dora-card__value {
  color: var(--color-faint);
}

.dora-card__caption {
  margin: 0;
  font-size: var(--text-micro);
  color: var(--color-dim);
  min-height: 1.1em;
}

.dora-card__spark {
  margin-top: auto;
  padding-top: var(--space-2);
}
.dora-card__spark--placeholder {
  height: 40px;
}
</style>

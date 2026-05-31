<!--
  RunStepList.vue — 步骤详情列表(从 RunDetail 抽出复用):每步状态图标 + 名称 + 时长。
  运行中态与终态(成功/失败)共用,使「步骤」在构建完成后仍可见(不再只在进行中展示)。
  纯展示组件:仅接收 steps prop,不自取数据。
-->
<script setup lang="ts">
import type { RunStep } from '../../api/runs'

defineProps<{
  steps: RunStep[]
  /** 当前选中的步骤序号(用于高亮 + 过滤日志);null = 全部。 */
  selected?: number | null
}>()

const emit = defineEmits<{ select: [ordinal: number | null] }>()

function formatDuration(ms: number | null): string {
  if (ms === null) return '—'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}
</script>

<template>
  <div class="step-list">
    <h3 class="subsection-title">步骤详情<span class="step-hint">点击查看单步日志</span></h3>
    <ul class="steps" role="list">
      <li>
        <button
          type="button"
          class="step-row step-row--all"
          :class="{ 'step-row--selected': selected == null }"
          @click="emit('select', null)"
        >
          <div class="step-icon"><span class="all-dot" aria-hidden="true" /></div>
          <div class="step-info"><span class="step-name">全部日志</span></div>
        </button>
      </li>
      <li
        v-for="(step, idx) in steps"
        :key="step.id"
      >
        <button
          type="button"
          class="step-row"
          :class="[`step-row--${step.status}`, { 'step-row--selected': selected === idx }]"
          @click="emit('select', idx)"
        >
        <div class="step-icon" :aria-label="step.status">
          <svg v-if="step.status === 'success'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          <span v-else-if="step.status === 'running'" class="spinner spinner--amber" aria-hidden="true" />
          <svg v-else-if="step.status === 'failed'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="m18 6-12 12M6 6l12 12"/>
          </svg>
          <svg v-else-if="step.status === 'skipped'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M13 17l5-5-5-5M6 17l5-5-5-5"/>
          </svg>
          <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="3"/>
          </svg>
        </div>
        <div class="step-info">
          <span class="step-name">{{ step.name }}</span>
          <span v-if="step.durationMs !== null" class="step-dur mono">{{ formatDuration(step.durationMs) }}</span>
        </div>
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
/* 左栏粘顶:终端很长时步骤选择器跟随视口,不在左侧留一大片死白。 */
.step-list {
  position: sticky;
  top: 16px;
  align-self: start;
}

.subsection-title {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-dim);
  margin-bottom: 12px;
  letter-spacing: 0.01em;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.step-hint {
  font-size: 0.66rem;
  font-weight: 400;
  color: var(--color-faint);
}

.steps {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
}

.steps li { list-style: none; }

/* step-row 现在是按钮:点击切换「单步日志」过滤 */
.step-row {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 11px;
  border: 1px solid transparent;
  border-radius: var(--rounded-md);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
  transition: background-color var(--duration-fast), border-color var(--duration-fast);
}

.step-row:hover {
  background: var(--color-inset);
}

.step-row--selected {
  background: var(--color-primary-soft);
  border-color: var(--color-primary);
}

.step-row:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.step-row--running {
  background: var(--color-amber-soft);
}

.all-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  background: var(--color-dim);
  display: inline-block;
}

.step-icon {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
}

.step-row--success .step-icon { color: var(--color-green); }
.step-row--failed .step-icon { color: var(--color-red); }
.step-row--running .step-icon { color: var(--color-amber); }
.step-row--skipped .step-icon,
.step-row--pending .step-icon { color: var(--color-faint); }

.step-info {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.step-name {
  font-size: 0.84rem;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.step-row--pending .step-name,
.step-row--skipped .step-name { color: var(--color-faint); }

.step-dur {
  font-size: 0.74rem;
  color: var(--color-faint);
  flex-shrink: 0;
}

.mono { font-family: var(--font-mono); }
</style>

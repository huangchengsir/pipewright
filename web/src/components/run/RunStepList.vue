<!--
  RunStepList.vue — 步骤详情列表(从 RunDetail 抽出复用):每步状态图标 + 名称 + 时长。
  运行中态与终态(成功/失败)共用,使「步骤」在构建完成后仍可见(不再只在进行中展示)。
  纯展示组件:仅接收 steps prop,不自取数据。
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { RunStep, StepStatus } from '../../api/runs'

const props = defineProps<{
  steps: RunStep[]
  /** 当前选中的步骤序号(用于高亮 + 过滤日志);null = 全部。 */
  selected?: number | null
}>()

const emit = defineEmits<{ select: [ordinal: number | null] }>()

interface NodeRow {
  step: RunStep
  ordinal: number // 全局 step 序号(= 日志 stepOrdinal,用于过滤)
}
interface StageGroup {
  stage: string
  nodes: NodeRow[]
  status: StepStatus
}

/** 任一失败→failed;任一运行→running;全跳过→skipped;全成功→success;否则 pending。 */
function aggregate(nodes: NodeRow[]): StepStatus {
  const ss = nodes.map((n) => n.step.status)
  if (ss.some((s) => s === 'failed')) return 'failed'
  if (ss.some((s) => s === 'running')) return 'running'
  if (ss.length > 0 && ss.every((s) => s === 'success')) return 'success'
  if (ss.length > 0 && ss.every((s) => s === 'skipped')) return 'skipped'
  if (ss.some((s) => s === 'success')) return 'success'
  return 'pending'
}

/** 是否启用节点级分组(任一 step 带 stage)。 */
const grouped = computed(() => props.steps.some((s) => !!s.stage))

/** 按阶段聚合(step 已按 ordinal 升序,同阶段连续)。 */
const groups = computed<StageGroup[]>(() => {
  const out: StageGroup[] = []
  props.steps.forEach((step, ordinal) => {
    const stage = step.stage || ''
    let g = out[out.length - 1]
    if (!g || g.stage !== stage) {
      g = { stage, nodes: [], status: 'pending' }
      out.push(g)
    }
    g.nodes.push({ step, ordinal })
  })
  for (const g of out) g.status = aggregate(g.nodes)
  return out
})

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

      <template v-for="g in groups" :key="g.stage || '_'">
        <!-- 阶段分组标题(节点级):阶段名 + 聚合状态点;无阶段名(旧数据)则不显示标题 -->
        <li v-if="grouped && g.stage" class="stage-head" :class="`stage-head--${g.status}`">
          <span class="stage-dot" :aria-label="g.status" />
          <span class="stage-head-name">{{ g.stage }}</span>
          <span class="stage-head-count">{{ g.nodes.length }} 节点</span>
        </li>

        <li v-for="n in g.nodes" :key="n.step.id">
          <button
            type="button"
            class="step-row"
            :class="[`step-row--${n.step.status}`, { 'step-row--selected': selected === n.ordinal, 'step-row--nested': grouped && g.stage }]"
            @click="emit('select', n.ordinal)"
          >
            <div class="step-icon" :aria-label="n.step.status">
              <svg v-if="n.step.status === 'success'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="M20 6 9 17l-5-5"/>
              </svg>
              <span v-else-if="n.step.status === 'running'" class="spinner spinner--amber" aria-hidden="true" />
              <svg v-else-if="n.step.status === 'failed'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                <path d="m18 6-12 12M6 6l12 12"/>
              </svg>
              <svg v-else-if="n.step.status === 'skipped'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M13 17l5-5-5-5M6 17l5-5-5-5"/>
              </svg>
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="12" cy="12" r="3"/>
              </svg>
            </div>
            <div class="step-info">
              <span class="step-name">{{ n.step.name }}</span>
              <span v-if="n.step.durationMs !== null" class="step-dur mono">{{ formatDuration(n.step.durationMs) }}</span>
            </div>
          </button>
        </li>
      </template>
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

/* 阶段分组标题(节点级):小标签 + 聚合状态点;节点行在其下缩进。 */
.stage-head {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 11px 4px;
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--color-dim);
}
.stage-head-name { text-transform: none; }
.stage-head-count {
  margin-left: auto;
  font-size: 0.64rem;
  font-weight: 400;
  color: var(--color-faint);
}
.stage-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-faint);
  flex-shrink: 0;
}
.stage-head--success .stage-dot { background: var(--color-green); }
.stage-head--failed .stage-dot { background: var(--color-red); }
.stage-head--running .stage-dot { background: var(--color-amber); }
.stage-head--skipped .stage-dot { background: var(--color-faint); }
/* 节点行在阶段标题下缩进,呈现「阶段 → 节点」两级。 */
.step-row--nested { margin-left: 12px; }

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

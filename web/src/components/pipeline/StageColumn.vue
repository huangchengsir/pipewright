<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import type { PipelineStage, PipelineJob } from '../../api/pipeline'
import JobCard from './JobCard.vue'
import { eligibleNeeds, toggleNeed } from './stageDeps'
import { hasAnyJobNeeds, layoutJobs, eligibleJobNeeds } from './jobDeps'
import {
  hasWhen,
  whenSummary,
  hasMatrix,
  matrixSummary,
  hasPost,
  postSummary,
  hasServices,
  servicesSummary,
} from './stageSettings'

const props = defineProps<{
  stage: PipelineStage
  stageIndex: number
  selectedJobId: string | null
  /** All stages — used to compute cycle-safe dependency options. */
  allStages: PipelineStage[]
  /** True when this stage's settings drawer is open (highlights the gear button). */
  settingsActive?: boolean
}>()

const emit = defineEmits<{
  (e: 'select-job', jobId: string): void
  (e: 'delete-job', jobId: string): void
  /** Add a job to this stage; optional `needs` seeds its intra-stage dependencies. */
  (e: 'add-job', needs?: string[]): void
  (e: 'delete-stage'): void
  (e: 'reorder-job', payload: { from: number; to: number }): void
  (e: 'update-needs', needs: string[]): void
  (e: 'update-allow-failure', value: boolean): void
  /** Update one job's intra-stage dependencies (横串竖并). */
  (e: 'update-job-needs', payload: { jobId: string; needs: string[] }): void
  /** Open this stage's settings in the shared right-side drawer (条件/审批门/矩阵/服务/后置). */
  (e: 'open-settings'): void
}>()

function stageLabel(index: number): string {
  if (props.stage.kind === 'source') return '源'
  return String(index)
}

// ─── Stage dependencies (needs · DAG) ─────────────────────────────────────────

const depsOpen = ref(false)

const currentNeeds = computed<string[]>(() => props.stage.needs ?? [])

/** Stages that can be an upstream dependency without creating a cycle. */
const depChoices = computed(() => eligibleNeeds(props.allStages, props.stage.id))

/** Display labels for the current needs (name of each upstream stage). */
const needLabels = computed(() =>
  currentNeeds.value
    .map((id) => props.allStages.find((s) => s.id === id)?.name ?? id)
    .filter(Boolean),
)

function toggleDep(needId: string): void {
  emit('update-needs', toggleNeed(currentNeeds.value, needId))
}

// ─── Intra-stage job DAG (横串竖并) ───────────────────────────────────────────
// 阶段内任一 job 声明 needs → 渲染二维 DAG(横=串行连线、纵=并行并排);否则纵向列表(原样)。

const useDag = computed(() => hasAnyJobNeeds(props.stage.jobs))
const layout = computed(() => layoutJobs(props.stage.jobs))

/** jobId → 其上游 job 名(卡片上展示 chip)。 */
const upstreamNamesByJob = computed<Record<string, string[]>>(() => {
  const byId = new Map(props.stage.jobs.map((j) => [j.id, j.name]))
  const out: Record<string, string[]> = {}
  for (const j of props.stage.jobs) {
    if (j.needs && j.needs.length) out[j.id] = j.needs.map((n) => byId.get(n) ?? n)
  }
  return out
})

/** 加节点的锚点:优先当前选中(且属本阶段)的 job,否则取声明序最后一个。 */
const anchorJob = computed<PipelineJob | null>(() => {
  const sel = props.stage.jobs.find((j) => j.id === props.selectedJobId)
  if (sel) return sel
  return props.stage.jobs.length ? props.stage.jobs[props.stage.jobs.length - 1] : null
})

function addPlain(): void {
  emit('add-job', [])
}
/** 串行节点:依赖锚点 job(出现在其右侧,串行其后)。 */
function addSerial(): void {
  emit('add-job', anchorJob.value ? [anchorJob.value.id] : [])
}
/** 并行节点:与锚点 job 同上游(出现在同一 rank 的并行车道)。 */
function addParallel(): void {
  emit('add-job', anchorJob.value ? [...(anchorJob.value.needs ?? [])] : [])
}

// ─── Per-job dependency editor (popover) ──────────────────────────────────────

const jobDepsForId = ref<string | null>(null)
const jobDepsJob = computed<PipelineJob | null>(
  () => props.stage.jobs.find((j) => j.id === jobDepsForId.value) ?? null,
)
const jobDepChoices = computed<PipelineJob[]>(() =>
  jobDepsJob.value ? eligibleJobNeeds(props.stage.jobs, jobDepsJob.value.id) : [],
)

function openJobDeps(jobId: string): void {
  jobDepsForId.value = jobDepsForId.value === jobId ? null : jobId
}
function toggleJobDep(needId: string): void {
  const job = jobDepsJob.value
  if (!job) return
  emit('update-job-needs', { jobId: job.id, needs: toggleNeed(job.needs ?? [], needId) })
}

// ─── Intra-stage edge overlay (SVG connectors, 横向串行) ───────────────────────

const dagRef = ref<HTMLElement | null>(null)
const dagOverlay = ref({ w: 0, h: 0 })
const dagPaths = ref<string[]>([])

function measureEdges(): void {
  const root = dagRef.value
  if (!root) {
    dagPaths.value = []
    return
  }
  const nodes = new Map<string, HTMLElement>()
  root.querySelectorAll<HTMLElement>('.job-node').forEach((el) => {
    const id = el.dataset.jobId
    if (id) nodes.set(id, el)
  })
  dagOverlay.value = { w: root.scrollWidth, h: root.scrollHeight }
  const paths: string[] = []
  for (const j of props.stage.jobs) {
    for (const need of j.needs ?? []) {
      const a = nodes.get(need)
      const b = nodes.get(j.id)
      if (!a || !b) continue
      const x1 = a.offsetLeft + a.offsetWidth
      const y1 = a.offsetTop + a.offsetHeight / 2
      const x2 = b.offsetLeft
      const y2 = b.offsetTop + b.offsetHeight / 2
      const dx = Math.max(18, Math.abs(x2 - x1) * 0.5)
      paths.push(`M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`)
    }
  }
  dagPaths.value = paths
}

let ro: ResizeObserver | null = null
function remeasure(): void {
  void nextTick(measureEdges)
}
onMounted(() => {
  ro = new ResizeObserver(() => measureEdges())
  if (dagRef.value) ro.observe(dagRef.value)
  remeasure()
})
onBeforeUnmount(() => ro?.disconnect())
watch(dagRef, (el) => {
  ro?.disconnect()
  if (el && ro) ro.observe(el)
  remeasure()
})
watch(() => props.stage.jobs, remeasure, { deep: true })

// ─── Stage rule chips (when 条件 / gate 审批门 / matrix / services / post) ──────
// 编辑全部下沉到右侧 StageDrawer(复用 JobDrawer 卡片骨架);此处只渲染汇总 chip。

/** Any stage rule set → highlight the gear button + show the dot. */
const hasStageRules = computed(
  () =>
    hasWhen(props.stage.when) ||
    props.stage.gate === true ||
    hasMatrix(props.stage.matrix) ||
    hasPost(props.stage.post) ||
    hasServices(props.stage.services),
)

const whenChip = computed(() => whenSummary(props.stage.when))
const postChip = computed(() => postSummary(props.stage.post))
const servicesChip = computed(() => servicesSummary(props.stage.services))
const matrixChip = computed(() => matrixSummary(props.stage.matrix))

// ─── Drag-to-reorder (within this stage; flat-list mode only) ──────────────────

const dragFrom = ref<number | null>(null)
const dragOver = ref<number | null>(null)

function onDragStart(i: number): void {
  dragFrom.value = i
}

function onDragEnter(i: number): void {
  if (dragFrom.value !== null) dragOver.value = i
}

function onDrop(i: number): void {
  const from = dragFrom.value
  if (from !== null && from !== i) emit('reorder-job', { from, to: i })
  resetDrag()
}

function resetDrag(): void {
  dragFrom.value = null
  dragOver.value = null
}
</script>

<template>
  <div class="stage-col" :class="{ 'stage-col--dag': useDag }">
    <!-- Stage header -->
    <div class="stage-header">
      <span class="stage-index" aria-hidden="true">{{ stageLabel(stageIndex) }}</span>
      <span class="stage-name-text">{{ stage.name }}</span>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-deps-btn"
        :class="{ 'stage-deps-btn--active': depsOpen || currentNeeds.length > 0 }"
        :aria-label="`编辑阶段 ${stage.name} 的依赖`"
        :aria-expanded="depsOpen"
        title="编辑依赖(DAG)"
        @click="depsOpen = !depsOpen"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="6" cy="6" r="2.4"/><circle cx="6" cy="18" r="2.4"/><circle cx="18" cy="12" r="2.4"/>
          <path d="M8 6.6c5 0 3 5.4 8 5.4M8 17.4c5 0 3-5.4 8-5.4"/>
        </svg>
        依赖<span v-if="currentNeeds.length" class="stage-deps-count">{{ currentNeeds.length }}</span>
      </button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-deps-btn"
        :class="{ 'stage-deps-btn--active': settingsActive || hasStageRules }"
        :aria-label="`编辑阶段 ${stage.name} 的设置(条件/审批门/矩阵/服务/后置)`"
        :aria-expanded="settingsActive"
        title="阶段设置:条件 / 审批门 / 矩阵 / 服务 / 后置"
        @click="emit('open-settings')"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
        条件<span v-if="hasStageRules" class="stage-deps-count stage-deps-count--dot" aria-hidden="true"></span>
      </button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-add-job"
        :aria-label="`在阶段 ${stage.name} 中添加任务`"
        @click="addPlain"
      >+ 任务</button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-del"
        :aria-label="`删除阶段 ${stage.name}`"
        title="删除此阶段"
        @click="emit('delete-stage')"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
          <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
        </svg>
      </button>
    </div>

    <!-- Dependency chips (declared needs) -->
    <div v-if="currentNeeds.length" class="stage-needs-chips" aria-label="上游依赖">
      <span class="stage-needs-arrow" aria-hidden="true">⤳</span>
      <span v-for="label in needLabels" :key="label" class="stage-need-chip">{{ label }}</span>
    </div>

    <!-- Condition / gate chips (when · 8-5 / gate · 8-4) -->
    <div v-if="hasStageRules" class="stage-rule-chips" aria-label="阶段条件与审批门">
      <span v-if="whenChip" class="stage-rule-chip stage-rule-chip--when" title="仅满足条件时执行">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M20 6L9 17l-5-5"/></svg>
        {{ whenChip }}
      </span>
      <span v-if="stage.gate" class="stage-rule-chip stage-rule-chip--gate" title="进入前需人工审批">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
        审批门
      </span>
      <span v-if="matrixChip" class="stage-rule-chip stage-rule-chip--matrix" title="矩阵展开:并行多个 cell">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
        {{ matrixChip }}
      </span>
      <span v-if="servicesChip" class="stage-rule-chip stage-rule-chip--svc" title="旁挂服务(同网按服务名互访)">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg>
        {{ servicesChip }}
      </span>
      <span v-if="postChip" class="stage-rule-chip stage-rule-chip--post" title="后置步骤(无论成败按条件跑)">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M21 12a9 9 0 1 1-6.2-8.5"/><path d="M21 3v6h-6"/></svg>
        {{ postChip }}
      </span>
    </div>

    <!-- Stage dependency editor popover -->
    <div v-if="depsOpen && stage.kind !== 'source'" class="deps-popover" role="group" aria-label="阶段依赖编辑">
      <div class="deps-popover-title">依赖的上游阶段</div>
      <p v-if="depChoices.length === 0" class="deps-empty">没有可选的上游阶段</p>
      <label v-for="opt in depChoices" :key="opt.id" class="deps-option">
        <input
          type="checkbox"
          :checked="currentNeeds.includes(opt.id)"
          @change="toggleDep(opt.id)"
        />
        <span>{{ opt.name }}</span>
      </label>
      <div class="deps-divider"></div>
      <label class="deps-option deps-option--toggle">
        <input
          type="checkbox"
          :checked="stage.allowFailure === true"
          @change="emit('update-allow-failure', ($event.target as HTMLInputElement).checked)"
        />
        <span>失败不阻断下游(allowFailure)</span>
      </label>
      <p class="deps-hint">留空 = 无显式依赖;全流水线无依赖时按从左到右线性执行</p>
    </div>

    <!-- Per-job dependency editor popover (横串竖并) -->
    <div v-if="jobDepsJob" class="deps-popover deps-popover--job" role="group" :aria-label="`任务 ${jobDepsJob.name} 的依赖`">
      <div class="deps-popover-title">「{{ jobDepsJob.name }}」依赖的上游任务</div>
      <p v-if="jobDepChoices.length === 0" class="deps-empty">本阶段没有其他可选任务</p>
      <label v-for="opt in jobDepChoices" :key="opt.id" class="deps-option">
        <input
          type="checkbox"
          :checked="(jobDepsJob.needs ?? []).includes(opt.id)"
          @change="toggleJobDep(opt.id)"
        />
        <span>{{ opt.name }}</span>
      </label>
      <p class="deps-hint">勾选 = 串行(本任务排在其后);不勾任何项 = 与其并行</p>
      <button class="deps-done" @click="jobDepsForId = null">完成</button>
    </div>

    <!-- Job cards — 2-D DAG (横串竖并) when this stage declares any job needs -->
    <div
      v-if="useDag"
      ref="dagRef"
      class="job-dag"
      :style="{ '--ranks': layout.ranks }"
      aria-label="阶段内任务依赖图"
    >
      <svg
        v-if="dagPaths.length"
        class="job-dag-overlay"
        :width="dagOverlay.w"
        :height="dagOverlay.h"
        :viewBox="`0 0 ${dagOverlay.w} ${dagOverlay.h}`"
        aria-hidden="true"
      >
        <path v-for="(d, i) in dagPaths" :key="i" class="job-edge" :d="d" />
        <path v-for="(d, i) in dagPaths" :key="`f${i}`" class="job-edge-flow" :d="d" />
      </svg>
      <div
        v-for="job in stage.jobs"
        :key="job.id"
        class="job-node"
        :data-job-id="job.id"
        :style="{
          gridColumn: (layout.positions.get(job.id)?.rank ?? 0) + 1,
          gridRow: (layout.positions.get(job.id)?.lane ?? 0) + 1,
        }"
      >
        <JobCard
          :job="job"
          :stage-kind="stage.kind"
          :selected="job.id === selectedJobId"
          :in-dag="true"
          :upstream-names="upstreamNamesByJob[job.id]"
          @select="emit('select-job', job.id)"
          @delete="emit('delete-job', job.id)"
          @edit-deps="openJobDeps(job.id)"
        />
      </div>
    </div>

    <!-- Job cards — flat vertical list (no intra-stage deps; original behavior) -->
    <template v-else>
      <JobCard
        v-for="(job, i) in stage.jobs"
        :key="job.id"
        :job="job"
        :stage-kind="stage.kind"
        :selected="job.id === selectedJobId"
        :dragging="dragFrom === i"
        :drag-over="dragOver === i && dragFrom !== i"
        @select="emit('select-job', job.id)"
        @delete="emit('delete-job', job.id)"
        @dragstart="onDragStart(i)"
        @dragend="resetDrag"
        @dragenter="onDragEnter(i)"
        @drop="onDrop(i)"
      />
    </template>

    <!-- Add-job triggers: plain / serial(→) / parallel(↓) (source stage is preset) -->
    <div v-if="stage.kind !== 'source'" class="add-job-row">
      <button
        class="add-job-btn add-job-btn--serial"
        :aria-label="`在阶段 ${stage.name} 加一个串行任务(依赖上一个)`"
        title="串行节点:排在锚点任务之后(横向连线)"
        @click="addSerial"
      >+ 串行节点</button>
      <button
        class="add-job-btn add-job-btn--parallel"
        :aria-label="`在阶段 ${stage.name} 加一个并行任务`"
        title="并行节点:与锚点任务并行(纵向并排)"
        @click="addParallel"
      >+ 并行节点</button>
    </div>
  </div>
</template>

<style scoped>
.stage-col {
  position: relative;
}

/* DAG mode: let the column grow horizontally to fit the rank grid. */
.stage-col--dag {
  width: auto;
}

.stage-name-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* DAG mode: the column is as wide as the rank grid below; if the title keeps `flex:1`
   it stretches across that whole width and dumps the action buttons to the far right
   with an ugly empty gap in the middle. Pin the header content together at the left
   (title takes only its own width; no auto-margin spacer) so it reads as one compact bar. */
.stage-col--dag .stage-header {
  width: fit-content;
}
.stage-col--dag .stage-name-text {
  flex: 0 1 auto;
  max-width: 220px;
}
.stage-col--dag .stage-add-job {
  margin-left: 0;
}

.stage-deps-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  padding: 0 7px;
  background: none;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-dim);
  font: inherit;
  font-size: 0.7rem;
  font-weight: 500;
  cursor: pointer;
  transition: border-color var(--duration-fast), color var(--duration-fast), background-color var(--duration-fast);
}
.stage-deps-btn:hover { border-color: var(--color-primary); color: var(--color-primary); }
.stage-deps-btn--active { border-color: var(--color-primary); color: var(--color-primary); background: var(--color-primary-soft); }
.stage-deps-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }

.stage-deps-count {
  display: inline-grid;
  place-items: center;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 7px;
  background: var(--color-primary);
  color: #fff;
  font-size: 0.6rem;
  font-weight: 700;
}

.stage-needs-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  margin: 2px 0 8px;
}
.stage-needs-arrow { color: var(--color-faint); font-size: 0.8rem; }
.stage-need-chip {
  font-size: 0.66rem;
  font-weight: 600;
  padding: 1px 7px;
  border-radius: 8px;
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}

.deps-popover {
  position: absolute;
  top: 40px;
  right: 6px;
  z-index: 40;
  width: 210px;
  padding: 11px 12px;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22);
}
.deps-popover--job { z-index: 41; }
.deps-popover-title {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.02em;
  color: var(--color-faint);
  margin-bottom: 8px;
}
.deps-empty { font-size: 0.74rem; color: var(--color-faint); font-style: italic; margin: 0; }
.deps-option {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 3px 0;
  font-size: 0.78rem;
  color: var(--color-text);
  cursor: pointer;
}
.deps-option input { width: 14px; height: 14px; accent-color: var(--color-primary); cursor: pointer; }
.deps-option--toggle { color: var(--color-dim); }
.deps-divider { height: 1px; background: var(--color-border); margin: 8px 0; }
.deps-hint { margin: 7px 0 0; font-size: 0.68rem; color: var(--color-faint); line-height: 1.4; }
.deps-done {
  margin-top: 9px;
  width: 100%;
  height: 26px;
  border: none;
  border-radius: var(--rounded-md);
  background: var(--color-primary);
  color: #fff;
  font: inherit;
  font-size: 0.74rem;
  font-weight: 600;
  cursor: pointer;
}
.deps-done:hover { filter: brightness(1.06); }

/* ─── Intra-stage job DAG grid (横=rank/串行, 纵=lane/并行)──────────────────── */
.job-dag {
  position: relative;
  display: grid;
  grid-template-columns: repeat(var(--ranks, 1), 200px);
  grid-auto-rows: min-content;
  column-gap: 54px;
  row-gap: 12px;
  align-items: start;
  width: max-content;
  padding-bottom: 4px;
}
.job-node {
  width: 200px;
  position: relative;
  z-index: 1;
}
/* grid handles vertical rhythm; drop the list margin inside the DAG */
.job-dag :deep(.job-card) { margin-bottom: 0; }

.job-dag-overlay {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  overflow: visible;
}
.job-edge {
  fill: none;
  stroke: var(--color-border-strong);
  stroke-width: 2;
}
.job-edge-flow {
  fill: none;
  stroke: var(--color-primary);
  stroke-width: 2;
  stroke-dasharray: 5 12;
  opacity: 0.75;
  animation: job-dag-flow 1.4s linear infinite;
}
@keyframes job-dag-flow {
  to { stroke-dashoffset: -34; }
}
@media (prefers-reduced-motion: reduce) {
  .job-edge-flow { animation: none; }
}

/* ─── Add-job row (serial / parallel) ─────────────────────────────────────── */
.add-job-row {
  display: flex;
  gap: 8px;
  margin-top: 4px;
}
.add-job-row .add-job-btn { width: auto; flex: 1; }
.add-job-btn--serial { /* horizontal serial accent */ }
.add-job-btn--parallel { /* vertical parallel accent */ }

/* ─── Condition / gate chips ──────────────────────────────────────────────── */
.stage-deps-count--dot {
  min-width: 7px;
  width: 7px;
  height: 7px;
  padding: 0;
  border-radius: 50%;
}
.stage-rule-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: -4px 0 8px;
}
.stage-rule-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.66rem;
  font-weight: 600;
  padding: 1px 7px 1px 6px;
  border-radius: 8px;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.stage-rule-chip svg { flex: none; }
.stage-rule-chip--when { background: var(--color-primary-soft); color: var(--color-primary); }
.stage-rule-chip--gate { background: var(--color-amber-soft, rgba(217, 119, 6, 0.14)); color: var(--color-amber, #b45309); }
.stage-rule-chip--matrix { background: var(--color-violet-soft, rgba(124, 58, 237, 0.13)); color: var(--color-violet, #6d28d9); }
.stage-rule-chip--svc { background: var(--color-cyan-soft, rgba(8, 145, 178, 0.13)); color: var(--color-cyan, #0e7490); }
.stage-rule-chip--post { background: var(--color-amber-soft, rgba(217, 119, 6, 0.14)); color: var(--color-amber, #b45309); }

</style>

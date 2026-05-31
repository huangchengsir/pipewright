<script setup lang="ts">
import { ref, computed } from 'vue'
import type { PipelineStage } from '../../api/pipeline'
import JobCard from './JobCard.vue'
import { eligibleNeeds, toggleNeed } from './stageDeps'

const props = defineProps<{
  stage: PipelineStage
  stageIndex: number
  selectedJobId: string | null
  /** All stages — used to compute cycle-safe dependency options. */
  allStages: PipelineStage[]
}>()

const emit = defineEmits<{
  (e: 'select-job', jobId: string): void
  (e: 'delete-job', jobId: string): void
  (e: 'add-job'): void
  (e: 'delete-stage'): void
  (e: 'reorder-job', payload: { from: number; to: number }): void
  (e: 'update-needs', needs: string[]): void
  (e: 'update-allow-failure', value: boolean): void
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

// ─── Drag-to-reorder (within this stage) ──────────────────────────────────────

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
  <div class="stage-col">
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
        class="stage-add-job"
        :aria-label="`在阶段 ${stage.name} 中添加任务`"
        @click="emit('add-job')"
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

    <!-- Dependency editor popover -->
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

    <!-- Job cards -->
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

    <!-- Add job trigger (source stage has no add-job since it's preset) -->
    <button
      v-if="stage.kind !== 'source'"
      class="add-job-btn"
      :aria-label="`在阶段 ${stage.name} 末尾添加任务`"
      @click="emit('add-job')"
    >+ 添加任务</button>
  </div>
</template>

<style scoped>
.stage-col {
  position: relative;
}

.stage-name-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
</style>

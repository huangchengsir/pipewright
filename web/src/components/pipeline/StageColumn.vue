<script setup lang="ts">
import { ref } from 'vue'
import type { PipelineStage } from '../../api/pipeline'
import JobCard from './JobCard.vue'

const props = defineProps<{
  stage: PipelineStage
  stageIndex: number
  selectedJobId: string | null
}>()

const emit = defineEmits<{
  (e: 'select-job', jobId: string): void
  (e: 'delete-job', jobId: string): void
  (e: 'add-job'): void
  (e: 'delete-stage'): void
  (e: 'reorder-job', payload: { from: number; to: number }): void
}>()

function stageLabel(index: number): string {
  if (props.stage.kind === 'source') return '源'
  return String(index)
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
      <span>{{ stage.name }}</span>
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

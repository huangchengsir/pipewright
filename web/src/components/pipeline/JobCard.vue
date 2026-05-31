<script setup lang="ts">
import type { PipelineJob, StageKind } from '../../api/pipeline'
import { jobTypeLabel } from './jobConfigSchema'
import JobTypeIcon from './JobTypeIcon.vue'

defineProps<{
  job: PipelineJob
  stageKind: StageKind
  selected: boolean
  dragging?: boolean
  dragOver?: boolean
}>()

const emit = defineEmits<{
  (e: 'select'): void
  (e: 'delete'): void
  (e: 'dragstart'): void
  (e: 'dragend'): void
  (e: 'dragenter'): void
  (e: 'drop'): void
}>()

function handleDelete(e: MouseEvent): void {
  e.stopPropagation()
  emit('delete')
}
</script>

<template>
  <div
    class="job-card"
    :class="{
      'job-card--selected': selected,
      'job-card--dragging': dragging,
      'job-card--dragover': dragOver,
    }"
    role="button"
    tabindex="0"
    draggable="true"
    :aria-pressed="selected"
    :aria-label="`任务: ${job.name}(${jobTypeLabel(job.type)})`"
    @click="emit('select')"
    @keydown.enter="emit('select')"
    @keydown.space.prevent="emit('select')"
    @dragstart="emit('dragstart')"
    @dragend="emit('dragend')"
    @dragenter.prevent="emit('dragenter')"
    @dragover.prevent
    @drop.prevent="emit('drop')"
  >
    <div class="job-card-top">
      <JobTypeIcon :type="job.type" :size="24" />
      <span class="job-name">{{ job.name }}</span>
      <button
        class="job-card-del"
        :aria-label="`删除任务 ${job.name}`"
        title="删除此任务"
        @click="handleDelete"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>
    <div class="job-card-type">{{ jobTypeLabel(job.type) }}</div>
    <div v-if="job.summary" class="job-summary">{{ job.summary }}</div>
  </div>
</template>

<style scoped>
.job-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.job-card-type {
  margin-top: 4px;
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.job-card--dragging {
  opacity: 0.45;
}

.job-card--dragover {
  border-color: var(--color-primary);
  box-shadow: 0 -2px 0 0 var(--color-primary);
}
</style>

<script setup lang="ts">
import type { PipelineJob, StageKind } from '../../api/pipeline'

const props = defineProps<{
  job: PipelineJob
  stageKind: StageKind
  selected: boolean
}>()

const emit = defineEmits<{
  (e: 'select'): void
  (e: 'delete'): void
}>()

function iconClass(): string {
  switch (props.stageKind) {
    case 'source': return 'job-icon--source'
    case 'build':  return 'job-icon--build'
    case 'deploy': return 'job-icon--deploy'
    case 'notify': return 'job-icon--notify'
    default:       return 'job-icon--custom'
  }
}

function iconGlyph(): string {
  switch (props.stageKind) {
    case 'source': return '⎇'
    case 'build':  return '▢'
    case 'deploy': return '⬆'
    case 'notify': return '✉'
    default:       return '◈'
  }
}

function handleDelete(e: MouseEvent): void {
  e.stopPropagation()
  emit('delete')
}
</script>

<template>
  <div
    class="job-card"
    :class="{ 'job-card--selected': selected }"
    role="button"
    tabindex="0"
    :aria-pressed="selected"
    :aria-label="`任务: ${job.name}`"
    @click="emit('select')"
    @keydown.enter="emit('select')"
    @keydown.space.prevent="emit('select')"
  >
    <div class="job-card-top">
      <span class="job-icon" :class="iconClass()" aria-hidden="true">{{ iconGlyph() }}</span>
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
</style>

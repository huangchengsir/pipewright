<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PipelineJob, StageKind } from '../../api/pipeline'
import { jobTypeLabel } from './jobConfigSchema'
import JobTypeIcon from './JobTypeIcon.vue'

defineProps<{
  job: PipelineJob
  stageKind: StageKind
  selected: boolean
  dragging?: boolean
  dragOver?: boolean
  /** Names of upstream jobs this job depends on (intra-stage DAG); shown as chips. */
  upstreamNames?: string[]
  /** True when the stage renders the intra-stage DAG (enables the per-job deps button). */
  inDag?: boolean
}>()

const emit = defineEmits<{
  (e: 'select'): void
  (e: 'delete'): void
  (e: 'edit-deps'): void
  (e: 'dragstart'): void
  (e: 'dragend'): void
  (e: 'dragenter'): void
  (e: 'drop'): void
}>()

const { t } = useI18n()

function handleDelete(e: MouseEvent): void {
  e.stopPropagation()
  emit('delete')
}

function handleEditDeps(e: MouseEvent): void {
  e.stopPropagation()
  emit('edit-deps')
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
    :aria-label="t('pipelineCanvas.jobAria', { name: job.name, type: jobTypeLabel(job.type) })"
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
        v-if="inDag"
        class="job-card-del job-card-deps"
        :aria-label="t('pipelineCanvas.editJobDepsAria', { name: job.name })"
        :title="t('pipelineCanvas.editJobDepsTitle')"
        @click="handleEditDeps"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="6" cy="6" r="2.4"/><circle cx="6" cy="18" r="2.4"/><circle cx="18" cy="12" r="2.4"/>
          <path d="M8 6.6c5 0 3 5.4 8 5.4M8 17.4c5 0 3-5.4 8-5.4"/>
        </svg>
      </button>
      <button
        class="job-card-del"
        :aria-label="t('pipelineCanvas.deleteJobAria', { name: job.name })"
        :title="t('pipelineCanvas.deleteJobTitle')"
        @click="handleDelete"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>
    <div class="job-card-type">{{ jobTypeLabel(job.type) }}</div>
    <div v-if="job.summary" class="job-summary">{{ job.summary }}</div>
    <div v-if="upstreamNames && upstreamNames.length" class="job-up-chips" :aria-label="t('pipelineCanvas.upstreamJobsAria')">
      <span class="job-up-arrow" aria-hidden="true">⟵</span>
      <span v-for="n in upstreamNames" :key="n" class="job-up-chip">{{ n }}</span>
    </div>
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

/* deps button sits left of delete; reuse job-card-del sizing/hover */
.job-card-deps { color: var(--color-faint); }
.job-card-deps:hover { color: var(--color-primary); }

.job-up-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  margin-top: 7px;
}
.job-up-arrow { color: var(--color-faint); font-size: 0.72rem; }
.job-up-chip {
  font-size: 0.64rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 7px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

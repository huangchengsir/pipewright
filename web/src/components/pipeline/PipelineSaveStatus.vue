<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { AlertTriangle, CircleCheck, CircleDot, Loader } from '@vicons/tabler'
import type { PipelineSaveState } from '../../composables/usePipelineDraftSave'

const props = defineProps<{
  state: PipelineSaveState
  pendingScopes: string[]
  completedScopes: string[]
  failedScopes: string[]
  unavailableScopes: string[]
  error?: string
}>()
const { t } = useI18n()
const hasError = computed(() => ['error', 'partial', 'unavailable'].includes(props.state))
const working = computed(() => props.state === 'saving' || props.state === 'loading')
const statusKey = computed(() => props.state === 'dirty' && props.completedScopes.length
  ? 'projectPipeline.saveStatus.newerEdits'
  : `projectPipeline.saveStatus.${props.state}`)
const icon = computed(() => working.value ? Loader
  : hasError.value ? AlertTriangle
  : props.state === 'saved' ? CircleCheck : CircleDot)
</script>

<template>
  <div
    class="pipeline-save-status"
    :class="[
      `pipeline-save-status--${state}`,
      { 'pipeline-banner--success': state === 'saved', 'pipeline-banner--error': hasError },
    ]"
    :data-save-state="state"
    role="status"
    aria-live="polite"
    aria-atomic="true"
    :aria-busy="working"
  >
    <component :is="icon" class="save-status-icon" :class="{ 'save-status-icon--working': working }" aria-hidden="true" />
    <div class="save-status-content">
      <strong class="save-status-title">{{ t(statusKey) }}</strong>
      <span v-if="state !== 'saved' && state !== 'saving' && completedScopes.length" class="save-status-completed">
        {{ t('projectPipeline.saveStatus.completedScopes', { scopes: completedScopes.join(', ') }) }}
      </span>
      <span v-if="pendingScopes.length" class="save-status-pending">
        {{ t('projectPipeline.saveStatus.pendingScopes', { scopes: pendingScopes.join(', ') }) }}
      </span>
      <span v-if="failedScopes.length && hasError" class="save-status-failed">
        {{ t('projectPipeline.saveStatus.failedScopes', { scopes: failedScopes.join(', ') }) }}
      </span>
      <span v-if="unavailableScopes.length && state !== 'loading'" class="save-status-unavailable">
        {{ t('projectPipeline.saveStatus.unavailableScopes', { scopes: unavailableScopes.join(', ') }) }}
      </span>
      <span v-if="error && hasError" class="save-status-error">{{ error }}</span>
    </div>
  </div>
</template>

<style scoped>
.pipeline-save-status {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  flex: none;
  padding: 9px 20px;
  min-height: 38px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
  color: var(--color-dim);
  font-size: 12px;
  line-height: 1.5;
}
.save-status-icon { width: 16px; height: 16px; flex: none; margin-top: 1px; }
.save-status-content { display: flex; flex-wrap: wrap; gap: 3px 14px; min-width: 0; overflow-wrap: anywhere; }
.save-status-title { font-weight: 600; }
.save-status-completed { color: var(--color-green); }
.save-status-pending { color: var(--color-amber); }
.save-status-error { flex-basis: 100%; }
.pipeline-save-status.pipeline-save-status--saved { color: var(--color-green); }
.pipeline-save-status.pipeline-save-status--dirty, .pipeline-save-status.pipeline-save-status--partial {
  color: var(--color-amber);
  background: var(--color-inset);
  border-color: var(--color-border);
}
.pipeline-save-status.pipeline-save-status--error, .pipeline-save-status.pipeline-save-status--unavailable, .save-status-failed { color: var(--color-red); }
.pipeline-save-status--saving, .pipeline-save-status--loading { color: var(--color-primary); }
.save-status-icon--working { animation: save-status-spin 1s linear infinite; }
@keyframes save-status-spin { to { transform: rotate(360deg); } }
@media (max-width: 600px) {
  .pipeline-save-status { padding-inline: 12px; }
  .save-status-title { flex-basis: 100%; }
}
@media (prefers-reduced-motion: reduce) { .save-status-icon--working { animation: none; } }
</style>

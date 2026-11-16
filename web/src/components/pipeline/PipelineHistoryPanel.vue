<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import { getPipelineHistoryVersion, listPipelineHistory, type PipelineHistoryChange, type PipelineHistoryDetail, type PipelineHistoryVersion } from '../../api/pipeline'
import { useAuthorizedRequestScope } from '../../composables/useAuthorizedRequestScope'

const props = defineProps<{
  projectId: string
  versionId?: string
  canRestore: boolean
  restoreDraft: (id: string, sequence: number) => Promise<string | undefined>
}>()
const emit = defineEmits<{ select: [id: string] }>()
const { t, te, locale } = useI18n()

const versions = ref<PipelineHistoryVersion[]>([])
const selectedId = ref('')
const detail = ref<PipelineHistoryDetail | null>(null)
const nextBefore = ref<number>()
const loading = ref(true)
const listError = ref(false)
const detailLoading = ref(false)
const detailError = ref(false)
const restoring = ref(false)
const restoreError = ref('')
let request = 0

const scope = useAuthorizedRequestScope({ action: 'pipeline.read', resourceId: props.projectId }, () => {
  request++
  versions.value = []
  detail.value = null
  restoreError.value = ''
})

const records = computed(() => {
  if (detail.value && !versions.value.some(version => version.id === detail.value?.id)) {
    return [detail.value, ...versions.value]
  }
  return versions.value
})

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.valueOf()) ? '—' : new Intl.DateTimeFormat(locale.value, {
    dateStyle: 'medium', timeStyle: 'short',
  }).format(date)
}

function fieldLabel(field: string): string {
  const key = 'pipelineHistory.fields.' + field
  return te(key) ? t(key) : t('pipelineHistory.field')
}

function displayValue(value: string | undefined, field: string): string {
  if (value === undefined) return '—'
  if ((field === 'gate' || field === 'allowFailure') && (value === 'true' || value === 'false')) {
    return t('pipelineHistory.values.' + value)
  }
  return value
}

function hiddenFields(change: PipelineHistoryChange): string[] {
  const fields = (change.fields ?? []).filter(field => !change.details?.some(value => value.field === field))
  return [...new Set(fields.map(fieldLabel))]
}

async function selectVersion(id: string, notify = false, retry = false): Promise<void> {
  if (!scope.active() || (notify && restoring.value) || (!retry && id === selectedId.value)) return
  const pending = ++request
  selectedId.value = id
  detail.value = null
  detailError.value = false
  restoreError.value = ''
  detailLoading.value = true
  if (notify) emit('select', id)

  try {
    const response = await scope.run(() => getPipelineHistoryVersion(props.projectId, id, scope.requestOptions))
    if (scope.active() && pending === request) detail.value = response
  } catch {
    if (scope.active() && pending === request) detailError.value = true
  } finally {
    if (pending === request) detailLoading.value = false
  }
}

async function loadPage(append = false): Promise<void> {
  if (!scope.active()) return
  loading.value = true
  listError.value = false
  try {
    const page = await scope.run(() => listPipelineHistory(props.projectId, append ? nextBefore.value : undefined, scope.requestOptions))
    if (!scope.active()) return
    versions.value = append ? [...versions.value, ...page.versions] : page.versions
    nextBefore.value = page.nextBefore
    const first = props.versionId || versions.value[0]?.id
    if (first && !selectedId.value) void selectVersion(first)
  } catch {
    if (scope.active()) listError.value = true
  } finally {
    loading.value = false
  }
}

async function createDraft(): Promise<void> {
  const version = detail.value
  if (!scope.active() || !version?.draftAvailable || version.current || !props.canRestore || restoring.value) return
  restoring.value = true
  restoreError.value = ''
  try {
    const error = await props.restoreDraft(version.id, version.sequence)
    if (scope.active() && error) restoreError.value = error
  } finally {
    restoring.value = false
  }
}

watch(() => props.versionId, id => {
  const version = id || versions.value[0]?.id
  if (version) void selectVersion(version)
}, { immediate: true })
void loadPage()
</script>
<template>
  <div v-if="scope.active()" class="history-panel">
    <aside class="history-list" aria-labelledby="history-list-title">
      <h2 id="history-list-title">{{ t('pipelineHistory.records') }}</h2>
      <p v-if="loading && !records.length" role="status" class="history-muted">{{ t('projectPipeline.loading') }}</p>
      <div v-if="listError" role="alert">
        <p class="history-muted">{{ t('pipelineHistory.loadError') }}</p>
        <AppButton variant="ghost" @click="loadPage()">{{ t('projectPipeline.retry') }}</AppButton>
      </div>
      <p v-else-if="!loading && !records.length" class="history-muted">{{ t('pipelineHistory.empty') }}</p>
      <ul v-if="records.length" class="history-items">
        <li v-for="version in records" :key="version.id">
          <button type="button" class="history-choice" :aria-pressed="version.id === selectedId" :disabled="restoring"
            @click="selectVersion(version.id, true)">
            <span class="history-choice-title">
              <strong>{{ t('pipelineHistory.version', { n: version.sequence }) }}</strong>
              <span v-if="version.current" class="history-current">{{ t('pipelineHistory.current') }}</span>
            </span>
            <span class="history-muted">{{ version.actor || t('pipelineHistory.unknownActor') }} · {{ formatTime(version.createdAt) }}</span>
          </button>
        </li>
      </ul>
      <AppButton v-if="nextBefore !== undefined" variant="ghost" :disabled="loading || restoring" @click="loadPage(true)">
        {{ t('pipelineHistory.more') }}
      </AppButton>
    </aside>

    <section class="history-detail" :aria-busy="detailLoading">
      <p v-if="detailLoading" role="status" class="history-message">{{ t('projectPipeline.loading') }}</p>
      <div v-else-if="detailError" class="history-message" role="alert">
        <p>{{ t('pipelineHistory.versionError') }}</p>
        <AppButton variant="ghost" @click="selectVersion(selectedId, false, true)">{{ t('projectPipeline.retry') }}</AppButton>
      </div>
      <template v-else-if="detail">
        <div class="history-main">
          <div class="history-title">
            <h2>{{ t('pipelineHistory.version', { n: detail.sequence }) }}</h2>
            <span v-if="detail.previousSequence" class="history-base">{{ t('pipelineHistory.compare', { n: detail.previousSequence }) }}</span>
          </div>
          <p class="history-meta">
            <strong>{{ detail.actor || t('pipelineHistory.unknownActor') }}</strong>
            <span>{{ formatTime(detail.createdAt) }}</span>
            <span>{{ t('pipelineHistory.sources.' + detail.source) }}</span>
          </p>
          <div class="history-title">
            <h3>{{ t('pipelineHistory.changes') }}</h3>
            <span class="history-muted">{{ t('pipelineHistory.changeCount', { n: detail.changeCount }) }}</span>
          </div>
          <p v-if="!detail.changes?.length" class="history-muted">{{ t('pipelineHistory.baseline') }}</p>
          <ul v-else class="history-changes">
            <li v-for="change in detail.changes" :key="change.kind + ':' + change.id + ':' + change.action">
              <div class="change-heading">
                <strong>{{ change.name || change.id }}</strong>
                <span>{{ t('pipelineHistory.kinds.' + change.kind) }} · {{ t('pipelineHistory.actions.' + change.action) }}</span>
              </div>
              <div v-for="value in change.details" :key="value.field" class="field-change">
                <div class="field-name">{{ fieldLabel(value.field) }}</div>
                <div class="value-pair">
                  <div class="value-before"><small>{{ t('pipelineHistory.before') }}</small><code>{{ displayValue(value.before, value.field) }}</code></div>
                  <div class="value-after"><small>{{ t('pipelineHistory.after') }}</small><code>{{ displayValue(value.after, value.field) }}</code></div>
                </div>
              </div>
              <p v-for="field in hiddenFields(change)" :key="field" class="hidden-field">
                {{ t('pipelineHistory.valueHidden', { field }) }}
              </p>
            </li>
          </ul>
        </div>
        <footer v-if="props.canRestore" class="history-footer">
          <p v-if="restoreError" class="history-restore-error" role="alert">{{ restoreError }}</p>
          <p v-else class="history-muted">
            {{ t(detail.current ? 'pipelineHistory.currentNote' : detail.draftAvailable ? 'pipelineHistory.draftNote' : 'pipelineHistory.draftUnavailable') }}
          </p>
          <AppButton variant="primary" :disabled="detail.current || !detail.draftAvailable || restoring" @click="createDraft">
            {{ t('pipelineHistory.createDraft') }}
          </AppButton>
        </footer>
      </template>
    </section>
  </div>
</template>
<style scoped>
.history-panel { display: grid; grid-template-columns: minmax(0, 1fr); border: 1px solid var(--color-border); border-radius: 14px; background: var(--color-surface); overflow: hidden; }
.history-list { min-width: 0; background: var(--color-bg); padding: 14px; border-bottom: 1px solid var(--color-border); }
.history-list h2, .history-title h3 { font-size: .92rem; }
.history-list h2 { margin: 0 0 9px; }
.history-items, .history-changes { list-style: none; margin: 0; padding: 0; }
.history-items { display: grid; gap: 5px; }
.history-choice { position: relative; display: grid; grid-template-columns: minmax(0, 1fr); gap: 2px; min-height: 52px; width: 100%; padding: 6px 10px 6px 12px; border: 1px solid transparent; border-radius: 8px; background: transparent; text-align: start; color: var(--color-text); cursor: pointer; }
.history-choice:hover { border-color: var(--color-border); }
.history-choice:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.history-choice[aria-pressed="true"] { background: color-mix(in srgb, var(--color-primary) 12%, transparent); border-color: color-mix(in srgb, var(--color-primary) 35%, transparent); }
.history-choice[aria-pressed="true"]::before { content: ""; position: absolute; inset: 8px auto 8px 0; width: 3px; background: var(--color-primary); border-radius: 0 4px 4px 0; }
.history-choice-title, .history-title { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
.history-current, .history-base { display: inline-block; border: 1px solid var(--color-border); border-radius: 5px; font-size: .72rem; padding: 2px 6px; }
.history-current { color: var(--color-primary); }
.history-muted, .history-base { color: var(--color-text-soft); font-size: .8rem; overflow-wrap: anywhere; }
.history-detail { display: flex; flex-direction: column; min-width: 0; }
.history-main, .history-message { padding: 17px; min-width: 0; }
.history-title h2 { margin: 0; font-size: 1.13rem; }
.history-title h3 { margin: 0; }
.history-meta { margin: 7px 0 16px; display: flex; flex-wrap: wrap; gap: 5px 11px; color: var(--color-text-soft); font-size: .83rem; }
.history-meta strong { color: var(--color-text); }
.history-changes { border: 1px solid var(--color-border); border-radius: 9px; margin-top: 8px; }
.history-changes > li { padding: 11px; overflow-wrap: anywhere; }
.history-changes > li + li { border-top: 1px solid var(--color-border); }
.change-heading { display: flex; flex-wrap: wrap; gap: 4px 9px; align-items: baseline; font-size: .88rem; }
.history-meta strong, .history-choice-title strong { overflow-wrap: anywhere; }
.change-heading span, .field-name { color: var(--color-text-soft); font-size: .76rem; }
.field-change { margin-top: 6px; }
.value-pair { display: grid; gap: 6px; grid-template-columns: repeat(2, minmax(0, 1fr)); margin-top: 3px; }
.value-pair > div, .hidden-field { padding: 6px 8px; border-radius: 5px; }
.value-pair small, .value-pair code { display: block; overflow-wrap: anywhere; }
.value-pair small { color: var(--color-text-soft); font-size: .72rem; }
.value-before { color: var(--color-danger); background: color-mix(in srgb, var(--color-danger) 10%, transparent); }
.value-after { color: var(--color-success); background: color-mix(in srgb, var(--color-success) 10%, transparent); }
.hidden-field { background: var(--color-bg); color: var(--color-text-soft); margin: 5px 0 0; font-size: .8rem; }
.history-footer { margin-top: auto; padding: 12px 17px; border-top: 1px solid var(--color-border); display: flex; gap: 8px 14px; align-items: center; justify-content: space-between; flex-wrap: wrap; }
.history-footer p { margin: 0; overflow-wrap: anywhere; }
.history-footer :deep(button) { min-width: 0; max-width: 100%; white-space: normal; }
.history-restore-error { color: var(--color-danger); font-size: .82rem; }
@media (min-width: 720px) {
  .history-panel { grid-template-columns: clamp(195px, 23%, 260px) minmax(0, 1fr); }
  .history-list { border-bottom: 0; border-right: 1px solid var(--color-border); }
  .history-main { padding: 21px; }
  .history-footer { padding-inline: 21px; }
}
</style>

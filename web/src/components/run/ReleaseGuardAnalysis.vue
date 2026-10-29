<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import { guardDate, guardNumber } from '../../utils/guardEvidencePresentation'
import { HttpError } from '../../api/http'
import {
  listGuardAnalyses,
  previewGuardAnalysis,
  revokeGuardAnalysis,
  sendGuardAnalysis,
  type GuardAnalysis,
  type GuardReport,
} from '../../api/releaseGuard'

const props = defineProps<{ report: GuardReport }>()
const emit = defineEmits<{ locateEvidence: [id: string] }>()
const { t, locale } = useI18n()
const includeRuntime = ref(false)
const includeLogs = ref(false)
const confirmed = ref(false)
const items = ref<GuardAnalysis[]>([])
const selectedId = ref('')
const busy = ref(false)
const revoking = ref(false)
const loading = ref(false)
const error = ref('')
const missingProvider = ref(false)
const now = ref(Date.now())
let alive = true
let generation = 0
let readGeneration = 0
let readController: AbortController | undefined
let actionController: AbortController | undefined
let revokeController: AbortController | undefined
const clock = setInterval(() => {
  now.value = Date.now()
}, 1000)
const selected = computed(() =>
  items.value.find((item) => item.id === selectedId.value),
)
const unresolved = computed(() =>
  items.value.some(
    (item) => item.status === 'unknown' || item.status === 'sending',
  ),
)
const snapshotTargets = computed(() => {
  try {
    const payload = JSON.parse(selected.value?.payload ?? '{}') as {
      items?: Array<{ target?: unknown }>
    }
    const ids = [
      ...new Set(
        (payload.items ?? [])
          .map((item) => item.target)
          .filter((id): id is string => typeof id === 'string'),
      ),
    ]
    return (
      ids
        .map(
          (id) =>
            props.report.targets.find((target) => target.id === id)
              ?.serverName ?? t('releaseGuardPresentation.unknownTarget'),
        )
        .join(', ') || t('releaseGuard.notProvided')
    )
  } catch {
    return t('releaseGuard.notProvided')
  }
})
const snapshotWindowEnd = computed(() =>
  selected.value &&
  props.report.finishedAt &&
  Date.parse(props.report.finishedAt) <= Date.parse(selected.value.createdAt)
    ? props.report.finishedAt
    : (selected.value?.createdAt ?? ''),
)
const canPreview = computed(
  () =>
    !busy.value &&
    !loading.value &&
    !unresolved.value &&
    !props.report.evidenceExpired &&
    (includeRuntime.value || includeLogs.value),
)
const canSend = computed(() => {
  const item = selected.value
  return (
    !!item &&
    item.status === 'preview' &&
    !item.stale &&
    !item.revoked &&
    !unresolved.value &&
    !busy.value &&
    !loading.value &&
    confirmed.value &&
    item.input.includeRuntime === includeRuntime.value &&
    item.input.includeLogs === includeLogs.value &&
    !!item.payloadHash &&
    !!item.provider.configHash &&
    !!item.provider.destination &&
    item.planHash === props.report.plan.hash &&
    (!props.report.evidenceSetHash ||
      item.evidenceSetHash === props.report.evidenceSetHash) &&
    Date.parse(item.expiresAt) > now.value &&
    !props.report.evidenceExpired
  )
})
function when(value: string): string {
  return guardDate(value, locale.value, t)
}
function state(value: string): string {
  return t(
    `releaseGuardAnalysis.states.${['preview', 'sending', 'completed', 'failed', 'unknown', 'revoked'].includes(value) ? value : 'unknown'}`,
  )
}
function reason(value: string): string {
  if (value === 'model_not_dispatched')
    return t('releaseGuardAnalysis.notDispatched')
  if (value === 'analysis_schema_or_citation_invalid')
    return t('releaseGuardAnalysis.invalidResult')
  if (
    value === 'dispatch_outcome_unknown_no_automatic_retry' ||
    value === 'unknown'
  )
    return t('releaseGuardAnalysis.unknown')
  return t('releaseGuardPresentation.unconfirmed')
}
function save(item: GuardAnalysis): void {
  if (item.guardId !== props.report.id)
    throw new Error('Mismatched analysis identity')
  if (items.value.find((existing) => existing.id === item.id)?.revoked)
    item = { ...item, revoked: true }
  items.value = [
    item,
    ...items.value.filter((existing) => existing.id !== item.id),
  ]
  selectedId.value = item.id
}
async function revoke(): Promise<void> {
  const item = selected.value
  if (!item || item.revoked || revoking.value) return
  const ticket = generation
  const request = new AbortController()
  revokeController = request
  revoking.value = true
  confirmed.value = false
  try {
    const response = await revokeGuardAnalysis(
      props.report.projectId,
      props.report.id,
      item.id,
      { signal: request.signal },
    )
    if (alive && ticket === generation && !request.signal.aborted)
      save(response)
  } catch (cause) {
    if (alive && ticket === generation && !request.signal.aborted)
      error.value = 'releaseGuard.requestFailed'
  } finally {
    if (alive && ticket === generation) revoking.value = false
  }
}
async function load(): Promise<void> {
  if (busy.value) return
  const ticket = generation
  const readTicket = ++readGeneration
  readController?.abort()
  const request = new AbortController()
  readController = request
  loading.value = true
  try {
    const response = await listGuardAnalyses(
      props.report.projectId,
      props.report.id,
      { signal: request.signal },
    )
    if (
      !alive ||
      generation !== ticket ||
      readTicket !== readGeneration ||
      request.signal.aborted
    )
      return
    items.value = response.items.filter(
      (item) => item.guardId === props.report.id,
    )
    if (!items.value.some((item) => item.id === selectedId.value))
      selectedId.value = items.value[0]?.id ?? ''
  } catch (cause) {
    if (
      alive &&
      ticket === generation &&
      readTicket === readGeneration &&
      !request.signal.aborted
    )
      error.value = 'releaseGuard.requestFailed'
  } finally {
    if (alive && ticket === generation && readTicket === readGeneration)
      loading.value = false
  }
}
async function perform(action: 'preview' | 'send'): Promise<void> {
  if (
    busy.value ||
    (action === 'preview' && !canPreview.value) ||
    (action === 'send' && !canSend.value)
  )
    return
  const snapshot = selected.value
  if (action !== 'preview' && !snapshot) return
  const ticket = generation
  const request = new AbortController()
  actionController = request
  readController?.abort()
  readGeneration++
  loading.value = false
  busy.value = true
  error.value = ''
  missingProvider.value = false
  confirmed.value = false
  if (action === 'send' && snapshot) save({ ...snapshot, status: 'sending' })
  try {
    const projectId = props.report.projectId
    const id = props.report.id
    const result =
      action === 'preview'
        ? await previewGuardAnalysis(
            projectId,
            id,
            {
              includeRuntime: includeRuntime.value,
              includeLogs: includeLogs.value,
            },
            { signal: request.signal },
          )
        : await sendGuardAnalysis(
            projectId,
            id,
            snapshot!.id,
            {
              payloadHash: snapshot!.payloadHash,
              providerConfigHash: snapshot!.provider.configHash,
              confirmed: true,
            },
            { signal: request.signal },
          )
    if (!alive || ticket !== generation || request.signal.aborted) return
    save(result)
  } catch (cause) {
    if (!alive || ticket !== generation || request.signal.aborted) return
    if (
      action === 'send' &&
      (!(cause instanceof HttpError) ||
        cause.status === 0 ||
        cause.status >= 500)
    ) {
      if (snapshot) save({ ...snapshot, status: 'unknown', reason: 'unknown' })
      error.value = 'releaseGuardAnalysis.unknown'
    } else if (cause instanceof HttpError && cause.status === 409) {
      if (snapshot && action === 'send') save({ ...snapshot, stale: true })
      error.value = 'releaseGuardAnalysis.changed'
    } else if (cause instanceof HttpError && cause.status === 503) {
      missingProvider.value = true
      error.value = 'releaseGuardAnalysis.noProvider'
    } else error.value = 'releaseGuard.requestFailed'
  } finally {
    if (alive && ticket === generation) busy.value = false
  }
}
watch(selectedId, () => {
  confirmed.value = false
})
watch([includeRuntime, includeLogs], () => {
  confirmed.value = false
})
watch(
  [() => props.report.id, () => props.report.projectId],
  () => {
    generation++
    readGeneration++
    readController?.abort()
    actionController?.abort()
    revokeController?.abort()
    includeRuntime.value = false
    includeLogs.value = false
    confirmed.value = false
    items.value = []
    selectedId.value = ''
    busy.value = false
    revoking.value = false
    loading.value = false
    error.value = ''
    missingProvider.value = false
    void load()
  },
  { immediate: true },
)
watch(
  () => props.report.eventsCursor,
  () => {
    confirmed.value = false
    void load()
  },
)
watch(
  () => props.report.evidenceExpired,
  (expired) => {
    if (!expired) return
    generation++
    readGeneration++
    readController?.abort()
    actionController?.abort()
    revokeController?.abort()
    items.value = []
    selectedId.value = ''
    confirmed.value = false
    busy.value = false
    loading.value = false
    revoking.value = false
  },
)
onBeforeUnmount(() => {
  alive = false
  generation++
  readGeneration++
  readController?.abort()
  actionController?.abort()
  revokeController?.abort()
  clearInterval(clock)
})
</script>

<template>
  <section class="guard-analysis" :aria-label="t('releaseGuardAnalysis.title')">
    <header>
      <div>
        <h3>{{ t('releaseGuardAnalysis.title') }}</h3>
        <p>{{ t('releaseGuardPresentation.snapshotScope') }}</p>
      </div>
      <AppButton :disabled="busy" :loading="loading" @click="load">{{
        t('common.refresh')
      }}</AppButton>
    </header>
    <p class="guard-analysis-muted">
      {{ t('releaseGuardAnalysis.defaultOff') }}
    </p>
    <div class="guard-analysis-scope">
      <label
        ><input
          v-model="includeRuntime"
          type="checkbox"
          :disabled="busy || unresolved"
        />{{ t('releaseGuardAnalysis.runtime') }}</label
      ><label
        ><input
          v-model="includeLogs"
          type="checkbox"
          :disabled="busy || unresolved"
        />{{ t('releaseGuardAnalysis.logs') }}</label
      >
    </div>
    <p>{{ t('releaseGuardAnalysis.scopeHint') }}</p>
    <AppButton
      :disabled="!canPreview"
      :loading="busy && !selected"
      @click="perform('preview')"
      >{{ t('releaseGuardAnalysis.preview') }}</AppButton
    >
    <p v-if="unresolved" class="guard-analysis-notice">
      {{ t('releaseGuardAnalysis.unknown') }}
    </p>
    <p v-if="error" class="guard-analysis-error" role="alert">{{ t(error) }}</p>
    <RouterLink v-if="missingProvider" :to="{ name: 'settings-ai' }">{{
      t('releaseGuardAnalysis.openSettings')
    }}</RouterLink>
    <label v-if="items.length > 1" class="guard-analysis-select"
      >{{ t('releaseGuardAnalysis.history')
      }}<select v-model="selectedId" :disabled="busy">
        <option v-for="item in items" :key="item.id" :value="item.id">
          {{ when(item.createdAt) }} · {{ state(item.status) }}
        </option>
      </select></label
    >
    <article v-if="selected" class="guard-analysis-snapshot">
      <h4>
        {{ state(selected.status) }}
        <span v-if="selected.stale"
          >· {{ t('releaseGuardAnalysis.stale') }}</span
        ><span v-if="Date.parse(selected.expiresAt) <= now">
          · {{ t('releaseGuard.execution.expired') }}</span
        >
      </h4>
      <p v-if="selected.reason">{{ reason(selected.reason) }}</p>
      <dl>
        <dt>{{ t('releaseGuardAnalysis.provider') }}</dt>
        <dd>{{ selected.provider.name }} · {{ selected.provider.model }}</dd>
        <dt>{{ t('releaseGuardAnalysis.destination') }}</dt>
        <dd>
          <code>{{ selected.provider.destination }}</code>
        </dd>
        <dt>{{ t('releaseGuardAnalysis.scope') }}</dt>
        <dd>
          {{
            selected.input.includeRuntime
              ? t('releaseGuardAnalysis.runtime')
              : ''
          }}
          {{ selected.input.includeLogs ? t('releaseGuardAnalysis.logs') : '' }}
          · {{ snapshotTargets }}
        </dd>
        <dt>{{ t('releaseGuard.observationWindow') }}</dt>
        <dd>
          {{
            report.startedAt
              ? when(report.startedAt)
              : t('releaseGuard.notProvided')
          }}
          → {{ when(snapshotWindowEnd) }}
        </dd>
        <dt>{{ t('releaseGuardAnalysis.expires') }}</dt>
        <dd>{{ when(selected.expiresAt) }}</dd>
      </dl>
      <details>
        <summary>{{ t('releaseGuardAnalysis.fullPayload') }}</summary>
        <pre>{{ selected.payload }}</pre>
        <p class="guard-analysis-muted">
          {{ t('releaseGuardAnalysis.payloadHash') }}
          <code>{{ selected.payloadHash }}</code>
        </p>
      </details>
      <p class="guard-analysis-notice">
        {{ t('releaseGuardAnalysis.sensitive') }}
      </p>
      <p>{{ t('releaseGuardAnalysis.costUnknown') }}</p>
      <template v-if="selected.status === 'preview'"
        ><label class="guard-analysis-confirm"
          ><input
            v-model="confirmed"
            type="checkbox"
            :disabled="busy || selected.stale || selected.revoked || unresolved"
          />{{ t('releaseGuardAnalysis.confirm') }}</label
        ><AppButton
          variant="primary"
          :disabled="!canSend"
          :loading="busy"
          @click="perform('send')"
          >{{ t('releaseGuardAnalysis.sendOnce') }}</AppButton
        ></template
      >
      <AppButton
        v-if="
          !selected.revoked &&
          ['preview', 'sending', 'unknown'].includes(selected.status)
        "
        :disabled="revoking"
        :loading="revoking"
        @click="revoke"
        >{{ t('releaseGuardAnalysis.revoke') }}</AppButton
      >
      <p v-if="selected.revoked">{{ t('releaseGuardAnalysis.revoked') }}</p>
      <p class="guard-analysis-muted">
        {{ t('releaseGuardAnalysis.cannotRecall') }}
      </p>
      <section v-if="selected.result" class="guard-analysis-result">
        <h4>{{ t('releaseGuardAnalysis.candidates') }}</h4>
        <p>{{ selected.result.summary }}</p>
        <p class="guard-analysis-muted">
          {{ t('releaseGuard.candidateDisclaimer') }}
        </p>
        <article
          v-for="candidate in selected.result.candidates"
          :key="candidate.id"
        >
          <h4>{{ candidate.title }}</h4>
          <p>
            {{ t('releaseGuard.supportingEvidence') }}:
            <span v-if="!candidate.supportingEvidenceIds.length">{{
              t('releaseGuard.notProvided')
            }}</span
            ><a
              v-for="(id, index) in candidate.supportingEvidenceIds"
              :key="id"
              href="#guard-evidence-section"
              :aria-label="
                t('releaseGuardPresentation.citationLabel', {
                  candidate: candidate.title,
                  context: t('releaseGuard.supportingEvidence'),
                  n: guardNumber(index + 1, locale),
                })
              "
              @click.prevent="emit('locateEvidence', id)"
              >{{
                t('releaseGuardPresentation.evidenceReference', {
                  n: guardNumber(index + 1, locale),
                })
              }}
            </a>
          </p>
          <p>
            {{ t('releaseGuard.counterEvidence') }}:
            <span v-if="!candidate.counterEvidenceIds.length">{{
              t('releaseGuard.notProvided')
            }}</span
            ><a
              v-for="(id, index) in candidate.counterEvidenceIds"
              :key="id"
              href="#guard-evidence-section"
              :aria-label="
                t('releaseGuardPresentation.citationLabel', {
                  candidate: candidate.title,
                  context: t('releaseGuard.counterEvidence'),
                  n: guardNumber(index + 1, locale),
                })
              "
              @click.prevent="emit('locateEvidence', id)"
              >{{
                t('releaseGuardPresentation.evidenceReference', {
                  n: guardNumber(index + 1, locale),
                })
              }}
            </a>
          </p>
          <ul>
            <li v-for="item in candidate.missingEvidence" :key="item">
              {{ item }}
            </li>
          </ul>
          <p>
            {{ t('releaseGuard.nextCheck') }}: {{ candidate.nextReadOnlyCheck }}
          </p>
        </article>
      </section>
      <details>
        <summary>{{ t('releaseGuardPresentation.technicalDetails') }}</summary>
        <pre>{{ JSON.stringify(selected, null, 2) }}</pre>
      </details>
    </article>
  </section>
</template>

<style scoped>
.guard-analysis {
  display: grid;
  gap: 14px;
}
header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
}
h3,
h4,
p {
  margin: 0;
}
h3 {
  margin-bottom: 8px;
  font-size: 1rem;
}
h4 {
  font-size: 0.9rem;
}
p {
  font-size: 0.85rem;
  line-height: 1.7;
}
header p,
.guard-analysis-muted {
  color: var(--color-dim);
}
.guard-analysis-notice {
  padding: 12px;
  border-left: 2px solid var(--color-amber);
  background: var(--color-amber-soft);
}
.guard-analysis-error {
  color: var(--color-red);
}
.guard-analysis-scope {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 24px;
}
label {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  font-size: 0.875rem;
}
input {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  accent-color: var(--color-primary);
}
.guard-analysis-select {
  display: grid;
  gap: 8px;
}
select {
  padding: 8px;
  min-height: 44px;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  font-size: 1rem;
}
.guard-analysis :deep(.app-btn) {
  min-height: 44px;
  justify-self: start;
}
.guard-analysis-snapshot {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
}
dl {
  display: grid;
  gap: 6px;
  margin: 0;
  font-size: 0.8rem;
}
dt {
  color: var(--color-dim);
}
dd {
  margin: 0 0 6px;
  overflow-wrap: anywhere;
}
code {
  font: 0.75rem var(--font-mono);
  overflow-wrap: anywhere;
}
summary {
  cursor: pointer;
  min-height: 44px;
  display: flex;
  align-items: center;
  color: var(--color-primary);
  font-size: 0.875rem;
}
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: 0.75rem/1.7 var(--font-mono);
  max-height: 420px;
  overflow: auto;
  padding: 14px;
  background: var(--color-card-2);
}
.guard-analysis-result {
  display: grid;
  gap: 12px;
  border-top: 1px solid var(--color-border);
  padding-top: 14px;
}
.guard-analysis-result article {
  display: grid;
  gap: 8px;
  padding: 12px;
  background: var(--color-card-2);
}
ul {
  margin: 0;
  padding-left: 18px;
  font-size: 0.85rem;
}
a {
  color: var(--color-primary);
  display: inline-flex;
  min-height: 44px;
  align-items: center;
}
@media (min-width: 700px) {
  dl {
    grid-template-columns: 150px minmax(0, 1fr);
  }
  dd {
    margin: 0;
  }
}
@media (max-width: 639px) {
  header {
    flex-wrap: wrap;
  }
  header > div {
    min-width: 0;
  }
  .guard-analysis-scope {
    display: grid;
    gap: 8px;
  }
  label {
    min-height: 44px;
    align-items: flex-start;
    padding-block: 10px;
    line-height: 1.6;
  }
  label input {
    margin-top: 3px;
  }
  .guard-analysis-snapshot {
    padding: 12px;
  }
  .guard-analysis :deep(.app-btn) {
    max-width: 100%;
    word-break: keep-all;
  }
}
@media (min-width: 640px) {
  :is(h3, h4) {
    font-size: 14px;
  }
  :is(p, label, select, li) {
    font-size: 13px;
  }
  :is(summary, label, select, a) {
    min-height: 34px;
  }
  .guard-analysis :deep(.app-btn) {
    min-height: 34px;
    height: auto;
    padding-block: 6px;
    font-size: 13px;
    white-space: normal;
  }
}
@media (max-width: 639px) {
  :is(input:not([type='checkbox']):not([type='radio']), select, textarea) {
    font-size: 16px;
    min-height: 44px;
  }
}
</style>

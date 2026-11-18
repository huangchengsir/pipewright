<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy } from '@vicons/tabler'
import type { Call, Entry, Run, RunSummary } from '../../api/opsChat'
import AiOpsRunResults from './AiOpsRunResults.vue'
import AiOpsWaiting from './AiOpsWaiting.vue'
import AiOpsMarkdown from './AiOpsMarkdown.vue'
const props = withDefaults(
  defineProps<{
    canSubmit?: boolean
    canTerminal?: (id: string) => boolean
    entries: Entry[]
    calls: Record<string, Call>
    runs?: Run[]
    optimistic?: Entry[]
    waiting?: boolean
    disabled: boolean
    modelAvailable: boolean
    hasHistory: boolean
  }>(),
  { canSubmit: true, canTerminal: () => true, runs: () => [], optimistic: () => [], waiting: false },
)
const emit = defineEmits<{
  copy: [text: string]
  retry: [id: string]
  analyze: [ids: string[]]
  analyzeRun: [runId: string]
  terminal: [id: string]
  history: []
}>()
const { t } = useI18n()
function messageText(entry: Entry): string {
  const text = entry.text ?? ''
  if (entry.kind === 'assistant' && entry.status === 'failed') {
    const planning =
      /^\[(planning|continuation):(unknown|token_limit|timeout|rejected|invalid_response|unavailable|input_limit|invalid_plan)\]$/.exec(
        text,
      )
    if (planning) {
      const code = planning[2]!
      if (planning[1] === 'continuation') {
        const reason = ['unknown', 'unavailable', 'invalid_plan'].includes(code)
          ? 'opsChat.continuationError.' + code
          : 'opsChat.planningError.' + code
        return t(reason) + ' ' + t('opsChat.continuationError.preserved')
      }
      return t('opsChat.planningError.' + code)
    }
    if (text === 'Planning could not be completed.')
      return t('opsChat.planningError.unknown')
  }
  if (entry.kind !== 'analysis' || entry.status !== 'failed') return text
  const match =
    /^\[analysis:(unknown|token_limit|timeout|rejected|invalid_response|unavailable|input_limit)\]$/.exec(
      text,
    )
  if (match) return t('opsChat.analysisError.' + match[1])
  if (
    text ===
      'Analysis could not be completed; local tool results are preserved.' ||
    text === 'Analysis could not be completed.'
  ) {
    return t('opsChat.analysisError.unknown')
  }
  if (
    text ===
    'Analysis exceeds the input limit; no results were sent. Local facts are preserved.'
  ) {
    return t('opsChat.analysisError.input_limit')
  }
  return text
}
interface Row {
  entry: Entry
  calls?: Call[]
  summary?: RunSummary
  run?: Run
}
const visible = computed(() => {
  const seen = new Set<string>()
  const grouped = new Map<string, Call[]>()
  for (const call of Object.values(props.calls)) {
    const calls = grouped.get(call.runId) ?? []
    calls.push(call)
    grouped.set(call.runId, calls)
  }
  const summaries = new Map(
    props.entries
      .filter((e) => e.summary && e.runId)
      .map((e) => [e.runId!, e.summary!]),
  )
  const runs = new Map(props.runs.map((r) => [r.id, r]))
  return [...props.entries, ...props.optimistic].flatMap<Row>((entry) => {
    if (entry.callId && !entry.runId)
      entry = { ...entry, runId: props.calls[entry.callId]?.runId }
    if ((entry.callId || entry.kind === 'summary') && entry.runId) {
      if (seen.has(entry.runId)) return []
      seen.add(entry.runId)
      return [
        {
          entry,
          calls: grouped.get(entry.runId) ?? [],
          summary: summaries.get(entry.runId),
          run: runs.get(entry.runId),
        },
      ]
    }
    return entry.text
      ? [{ entry, calls: undefined, summary: undefined, run: undefined }]
      : []
  })
})
</script>
<template>
  <div class="messages">
    <button
      v-if="hasHistory"
      class="ops-btn"
      :disabled="disabled"
      @click="emit('history')"
    >
      {{ t('opsChat.older') }}
    </button>
    <p v-if="!visible.length && !waiting" class="ops-muted">
      {{ t('opsChat.empty') }}
    </p>
    <template v-for="row in visible" :key="row.entry.seq">
      <AiOpsRunResults
        v-if="row.calls"
        :run-id="row.entry.runId!"
        :calls="row.calls"
        :run="row.run"
        :summary="row.summary"
        :disabled="disabled"
        :model-available="modelAvailable"
        :can-submit="canSubmit"
        :can-terminal="canTerminal"
        @copy="emit('copy', $event)"
        @retry="emit('retry', $event)"
        @analyze="emit('analyze', $event)"
        @analyze-run="emit('analyzeRun', $event)"
        @terminal="emit('terminal', $event)"
      />
      <div
        v-else
        class="message"
        :class="{
          user: row.entry.kind === 'user',
        }"
        :data-entry-seq="row.entry.seq"
        :data-entry-status="row.entry.status"
        :data-request-id="row.entry.requestId"
      >
        <div v-if="row.entry.kind === 'assistant' || row.entry.kind === 'analysis'" class="message-heading">
          <span class="message-brand" aria-hidden="true">p&gt;</span>
          <strong>{{ t('opsChat.title') }}</strong>
          <span v-if="row.entry.model" class="ops-muted ops-wrap">{{ row.entry.model }}</span>
        </div>
        <AiOpsMarkdown
          v-if="
            (row.entry.kind === 'assistant' || row.entry.kind === 'analysis') &&
            row.entry.status !== 'failed'
          "
          :text="messageText(row.entry)"
          :target-names="row.entry.targetNames"
        />
        <p v-else class="ops-wrap">{{ messageText(row.entry) }}</p>
        <span
          v-if="
            row.entry.status === 'interrupted' || row.entry.status === 'failed'
          "
          class="ops-error"
          role="status"
          >{{ t('opsChat.status.' + row.entry.status) }}</span
        >
        <span
          v-if="row.entry.requestId && row.entry.seq < 0"
          class="ops-muted"
          role="status"
          >{{ t('opsChat.submitting') }}</span
        >
        <button
          v-if="row.entry.kind === 'assistant' || row.entry.kind === 'analysis'"
          class="ops-btn ops-icon message-copy"
          :title="t('opsChat.copy')"
          :aria-label="t('opsChat.copy')"
          @click="emit('copy', messageText(row.entry))"
        >
          <Copy />
        </button>
      </div>
    </template>
    <AiOpsWaiting v-if="waiting" />
  </div>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.messages {
  width: min(100%, 590px);
  margin-inline: auto;
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}
.message {
  padding: 8px 0;
  min-width: 0;
}
.message-heading {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.message-brand {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 6px;
  background: var(--color-primary);
  color: white;
  font-size: 11px;
  font-weight: 700;
}
.message-copy {
  margin-top: 6px;
  width: 28px;
  height: 28px;
  min-height: 28px;
  border-color: transparent;
  background: transparent;
  color: var(--color-dim);
}
.message-copy svg {
  width: 16px;
  height: 16px;
}
.message-copy:hover:not(:disabled) {
  color: var(--color-text);
  background: var(--color-inset);
}
.message > [role='status'] {
  display: block;
  margin-top: 6px;
}
.message p {
  margin: 0;
  line-height: 1.65;
}
.message.user {
  align-self: flex-end;
  max-width: 88%;
  padding: 8px 12px;
  background: var(--color-primary-soft);
  border-radius: 12px 12px 3px 12px;
}
@media (max-width: 600px), (pointer: coarse) {
  .message { font-size: 15px; }
  .message-copy { width: 44px; height: 44px; min-height: 44px; }
}
</style>

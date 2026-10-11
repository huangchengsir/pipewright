<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Stars } from '@vicons/tabler'
import type { Call, Run, RunSummary } from '../../api/opsChat'
import AiOpsToolResult from './AiOpsToolResult.vue'
const props = defineProps<{
  runId: string
  calls: Call[]
  run?: Run
  summary?: RunSummary
  disabled: boolean
  modelAvailable: boolean
}>()
const emit = defineEmits<{
  copy: [text: string]
  retry: [id: string]
  analyze: [ids: string[]]
  analyzeRun: [runId: string]
  terminal: [id: string]
}>()
const { t } = useI18n()
const terminal = (status: string) =>
  ['succeeded', 'partial_failed', 'failed', 'interrupted', 'unknown'].includes(
    status,
  )
const groups = computed(() => {
  const groups = new Map<
    string,
    { id: string; name: string; calls: Call[]; done: number }
  >()
  for (const call of props.calls) {
    let group = groups.get(call.serverId)
    if (!group) {
      group = { id: call.serverId, name: call.serverName, calls: [], done: 0 }
      groups.set(call.serverId, group)
    }
    group.calls.push(call)
    if (terminal(call.status)) group.done++
  }
  return [...groups.values()].map((group) => {
    const facts = props.summary?.servers.find(
      (server) => server.serverId === group.id,
    )?.facts
    return {
      ...group,
      complete: !!facts,
      total: facts?.length ?? group.calls.length,
      done: facts ? facts.filter((f) => terminal(f.status)).length : group.done,
      ids: facts
        ? facts.map((f) => f.callId)
        : group.calls.map((c) => c.callId),
    }
  })
})
const completed = computed(() =>
  props.run
    ? terminal(props.run.status)
    : props.calls.every((c) => terminal(c.status)),
)
const summaryServers = computed(() => props.summary?.servers ?? [])
const bytes = (n: number) =>
  n >= 1073741824
    ? (n / 1073741824).toFixed(1) + ' GiB'
    : (n / 1048576).toFixed(1) + ' MiB'
</script>
<template>
  <section class="run-results" :data-run-id="runId">
    <header class="ops-row run-header">
      <strong class="ops-grow">{{ t('opsChat.runResults') }}</strong>
      <button
        class="ops-btn ops-icon"
        :disabled="disabled || !modelAvailable || !completed"
        :title="t('opsChat.analyzeRun')"
        :aria-label="t('opsChat.analyzeRun')"
        @click="emit('analyzeRun', runId)"
      >
        <Stars />
      </button>
    </header>
    <section
      v-for="group in groups"
      :key="group.id"
      class="server-group"
      :data-server-id="group.id"
    >
      <header class="ops-row group-header">
        <strong class="ops-grow ops-wrap">{{ group.name || group.id }}</strong>
        <span class="ops-muted"
          >{{ group.done }}/{{ group.total
          }}<template v-if="!group.complete">
            · {{ t('opsChat.loadedItems') }}</template
          ></span
        >
        <button
          class="ops-btn ops-icon"
          :disabled="disabled || !modelAvailable || group.done !== group.total"
          :title="
            t(group.complete ? 'opsChat.analyzeGroup' : 'opsChat.analyzeLoaded')
          "
          :aria-label="
            t(group.complete ? 'opsChat.analyzeGroup' : 'opsChat.analyzeLoaded')
          "
          @click="emit('analyze', group.ids)"
        >
          <Stars />
        </button>
      </header>
      <AiOpsToolResult
        v-for="call in group.calls"
        :key="call.callId"
        :call="call"
        :disabled="disabled"
        :model-available="modelAvailable"
        @copy="emit('copy', $event)"
        @retry="emit('retry', $event)"
        @analyze="emit('analyze', [$event])"
        @terminal="emit('terminal', $event)"
      />
    </section>
  </section>
  <section v-if="summary" class="local-summary" :data-summary-run="runId">
    <strong>{{ t('opsChat.localSummary') }}</strong>
    <section
      v-for="server in summaryServers"
      :key="server.serverId"
      class="summary-server"
    >
      <strong class="ops-wrap">{{
        server.serverName || server.serverId
      }}</strong>
      <template v-for="fact in server.facts" :key="fact.callId">
        <p class="ops-muted">
          {{ t('opsChat.tools.' + fact.toolId) }} ·
          {{ t('opsChat.status.' + fact.status)
          }}<template v-if="fact.truncated">
            · {{ t('opsChat.truncated') }}</template
          >
        </p>
        <p v-if="fact.error" class="ops-error ops-wrap">{{ fact.error }}</p>
        <template v-if="fact.resources">
          <p v-if="fact.resources.memory">
            {{ t('opsChat.memory') }}:
            {{ bytes(fact.resources.memory.usedBytes) }} /
            {{ bytes(fact.resources.memory.totalBytes) }} ·
            {{ t('opsChat.available') }}
            {{ bytes(fact.resources.memory.availableBytes) }}
          </p>
          <p v-if="fact.resources.load">
            {{ t('opsChat.load') }}: {{ fact.resources.load.one }} /
            {{ fact.resources.load.five }} / {{ fact.resources.load.fifteen }}
          </p>
          <p
            v-for="disk in fact.resources.disks ?? []"
            :key="disk.mount"
            class="ops-wrap"
          >
            {{ disk.mount }} · {{ disk.usedPercent }}% ·
            {{ bytes(disk.usedBytes) }} / {{ bytes(disk.totalBytes) }}
          </p>
          <p v-if="fact.resources.unavailable?.length" class="ops-muted">
            {{ t('opsChat.metricsMissing') }}:
            {{ fact.resources.unavailable.join(', ') }}
          </p>
        </template>
        <ul v-if="fact.occupants?.length" class="occupants">
          <li v-for="owner in fact.occupants" :key="owner.id" class="ops-wrap">
            {{ owner.name }} ({{ owner.id }}) · CPU
            {{ owner.cpuPercent }}%<template
              v-if="owner.memoryPercent !== undefined"
            >
              · {{ t('opsChat.memory') }} {{ owner.memoryPercent }}%</template
            ><template v-else-if="owner.memoryUsage">
              · {{ owner.memoryUsage }}</template
            >
          </li>
        </ul>
      </template>
      <p v-if="server.missing?.length" class="ops-muted">
        {{ t('opsChat.occupantsMissing') }}:
        {{
          server.missing.map((key) => t('opsChat.occupants.' + key)).join(', ')
        }}
      </p>
    </section>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.run-results {
  min-width: 0;
}
.run-header {
  border-bottom: 1px solid var(--color-border-strong);
  padding-bottom: 4px;
}
.server-group {
  min-width: 0;
}
.group-header {
  padding-block: 8px;
}
.local-summary {
  border-top: 1px solid var(--color-border);
  padding-top: 12px;
  min-width: 0;
}
.summary-server {
  padding-block: 8px;
  min-width: 0;
}
.summary-server p {
  margin: 4px 0;
  line-height: 1.5;
}
.occupants {
  margin: 4px 0;
  padding-left: 20px;
}
</style>

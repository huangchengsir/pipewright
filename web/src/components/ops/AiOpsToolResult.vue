<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, Refresh, Stars, Terminal2 } from '@vicons/tabler'
import type { Call } from '../../api/opsChat'
const props = defineProps<{
  call: Call
  disabled: boolean
  modelAvailable: boolean
}>()
const emit = defineEmits<{
  copy: [text: string]
  retry: [id: string]
  analyze: [id: string]
  terminal: [serverId: string]
}>()
const { t } = useI18n()
const retryable = computed(() =>
  ['failed', 'partial_failed'].includes(props.call.status),
)
const copyable = computed(
  () =>
    props.call.output ||
    (props.call.resources
      ? JSON.stringify(
          {
            serverId: props.call.serverId,
            serverName: props.call.serverName,
            status: props.call.status,
            collectedAt: props.call.collectedAt,
            resources: props.call.resources,
          },
          null,
          2,
        )
      : ''),
)
const bytes = (n: number): string =>
  n >= 1073741824
    ? (n / 1073741824).toFixed(1) + ' GiB'
    : (n / 1048576).toFixed(1) + ' MiB'
</script>
<template>
  <article class="result" :data-call-id="call.callId">
    <header class="ops-row">
      <strong class="ops-grow ops-wrap">{{
        call.serverName || call.serverId
      }}</strong>
      <span
        class="result-status"
        :class="{
          'ops-error': ['failed', 'partial_failed', 'unknown'].includes(
            call.status,
          ),
        }"
        >{{ t('opsChat.status.' + call.status) }}</span
      >
    </header>
    <p class="ops-muted ops-wrap">
      {{ t('opsChat.tools.' + call.toolId) }} · {{ call.serverId }}
    </p>
    <p v-if="call.object" class="ops-muted ops-wrap">
      {{
        call.args.action
          ? t('opsChat.actions.' + call.args.action) + ' · '
          : ''
      }}{{ call.object }}
    </p>
    <div v-if="call.resources" class="metrics">
      <p v-if="call.resources.load">
        {{ t('opsChat.load') }}: {{ call.resources.load.one }} /
        {{ call.resources.load.five }} / {{ call.resources.load.fifteen }} ·
        {{ t('opsChat.uptime') }}: {{ call.resources.load.uptimeSeconds }}s
      </p>
      <p v-if="call.resources.memory">
        {{ t('opsChat.memory') }}:
        {{ bytes(call.resources.memory.usedBytes) }} /
        {{ bytes(call.resources.memory.totalBytes) }} ·
        {{ t('opsChat.available') }}
        {{ bytes(call.resources.memory.availableBytes) }}
      </p>
      <p
        v-for="disk in call.resources.disks ?? []"
        :key="disk.mount"
        class="ops-wrap"
      >
        {{ disk.mount }} · {{ disk.usedPercent }}% ·
        {{ bytes(disk.usedBytes) }} / {{ bytes(disk.totalBytes) }}
      </p>
      <p v-if="call.resources.unavailable?.length" class="ops-muted">
        {{ t('opsChat.metricsMissing') }}:
        {{ call.resources.unavailable.join(', ') }}
      </p>
    </div>
    <pre v-if="call.output" class="ops-pre">{{ call.output }}</pre>
    <p v-if="call.error" class="ops-error ops-wrap">{{ call.error }}</p>
    <p
      v-if="call.status === 'unknown' || call.status === 'interrupted'"
      class="ops-error"
    >
      {{ t('opsChat.verifyFirst') }}
    </p>
    <footer class="ops-row">
      <span class="ops-muted ops-grow ops-wrap"
        >{{
          call.collectedAt
            ? new Date(call.collectedAt).toLocaleString()
            : t('opsChat.notCollected')
        }}<template v-if="call.exitCode !== undefined">
          · {{ t('opsChat.exit') }} {{ call.exitCode }}</template
        ><template v-if="call.truncated">
          · {{ t('opsChat.truncated') }}</template
        ></span
      >
      <button
        class="ops-btn ops-icon"
        :disabled="!copyable"
        :title="t('opsChat.copy')"
        :aria-label="t('opsChat.copy')"
        @click="emit('copy', copyable)"
      >
        <Copy />
      </button>
      <button
        v-if="retryable"
        class="ops-btn ops-icon"
        :disabled="disabled"
        :title="t('opsChat.retryFailed')"
        :aria-label="t('opsChat.retryFailed')"
        @click="emit('retry', call.callId)"
      >
        <Refresh />
      </button>
      <button
        class="ops-btn ops-icon"
        :disabled="disabled || !modelAvailable || !call.collectedAt"
        :title="t('opsChat.analyze')"
        :aria-label="t('opsChat.analyze')"
        @click="emit('analyze', call.callId)"
      >
        <Stars />
      </button>
      <button
        class="ops-btn ops-icon"
        :title="t('opsChat.terminal')"
        :aria-label="t('opsChat.terminal')"
        @click="emit('terminal', call.serverId)"
      >
        <Terminal2 />
      </button>
    </footer>
  </article>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.result {
  border: 1px solid var(--color-border-strong);
  border-radius: 8px;
  padding: 12px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.result p {
  margin: 0;
  line-height: 1.6;
}
.result-status {
  font-size: 12px;
}
.metrics {
  border-top: 1px solid var(--color-border);
  padding-top: 8px;
}
</style>

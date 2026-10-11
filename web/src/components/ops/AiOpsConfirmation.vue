<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, shallowRef, onBeforeUnmount } from 'vue'
import { isValidExecArgv, type BoundCall, type Call, type Confirmation } from '../../api/opsChat'
const props = defineProps<{
  confirmation: Confirmation
  calls: Record<string, Call>
  disabled: boolean
}>()
const emit = defineEmits<{
  confirm: [value: Confirmation]
  cancel: [runId: string]
  reissue: [runId: string]
}>()
const { t } = useI18n()
const now = shallowRef(Date.now())
const timer = setInterval(() => {
  now.value = Date.now()
}, 1000)
onBeforeUnmount(() => clearInterval(timer))
const fresh = computed(
  () =>
    props.confirmation.valid &&
    Date.parse(props.confirmation.expiresAt) > now.value,
)
function matchingCall(bound: BoundCall): Call | undefined {
  const call = props.calls[bound.callId]
  if (!call || call.callId !== bound.callId || call.runId !== props.confirmation.runId ||
    call.serverId !== bound.serverId || call.toolId !== bound.toolId ||
    !bound.object || call.object !== bound.object ||
    !bound.argsHash || call.argsHash !== bound.argsHash ||
    !bound.targetHash || call.targetHash !== bound.targetHash) return undefined
  if (bound.toolId === 'docker_exec') {
    return isValidExecArgv(call.args.argv) && call.args.action === undefined ? call : undefined
  }
  return ['docker_action', 'systemd_action'].includes(bound.toolId) && !!call.args.action ? call : undefined
}
const rows = computed(() => props.confirmation.calls.map((bound) => ({ bound, call: matchingCall(bound) })))
const loaded = computed(() => rows.value.length > 0 && rows.value.every((row) => !!row.call))
const hasExec = computed(() => props.confirmation.calls.some((bound) => bound.toolId === 'docker_exec'))
function confirm(): void {
  if (!props.disabled && fresh.value && loaded.value) emit('confirm', props.confirmation)
}
</script>
<template>
  <section class="confirmation">
    <strong>{{ t('opsChat.batch') }}</strong>
    <p class="ops-muted">{{ t('opsChat.batchImpact') }}</p>
    <p v-if="hasExec" class="ops-error" role="status">{{ t('opsChat.exec.risk') }}</p>
    <ul class="batch-list">
      <li v-for="{ bound, call } in rows" :key="bound.callId">
        <strong class="ops-wrap">{{
          call?.serverName || bound.serverId
        }}</strong>
        <p class="ops-muted ops-wrap">{{ bound.serverId }}</p>
        <p class="ops-wrap">
          {{ t('opsChat.tools.' + bound.toolId) }} ·
          {{
            !call ? t('opsChat.loadingResult') :
              bound.toolId === 'docker_exec' ? t('opsChat.exec.argv') :
                t('opsChat.actions.' + call.args.action)
          }}
        </p>
        <code class="ops-wrap">{{ bound.object }}</code>
        <pre v-if="call && bound.toolId === 'docker_exec'" class="exec-argv">{{ JSON.stringify(call.args.argv) }}</pre>
        <details class="ops-muted">
          <summary>{{ t('opsChat.binding') }}</summary>
          <p class="ops-wrap">
            {{ bound.serverId }}<br />{{ bound.callId }}<br />{{
              bound.targetHash
            }}<br />{{ bound.argsHash }}
          </p>
        </details>
      </li>
    </ul>
    <p class="ops-muted">
      {{ t('opsChat.expires') }}
      {{ new Date(confirmation.expiresAt).toLocaleString() }}
    </p>
    <div class="ops-row">
      <button
        v-if="fresh"
        class="ops-btn ops-primary"
        :disabled="disabled || !loaded"
        @click="confirm"
      >
        {{ t('opsChat.confirmBatch') }}
      </button>
      <button
        v-else
        class="ops-btn"
        :disabled="disabled"
        @click="emit('reissue', confirmation.runId)"
      >
        {{ t('opsChat.refreshConfirmation') }}
      </button>
      <button
        class="ops-btn"
        :disabled="disabled"
        @click="emit('cancel', confirmation.runId)"
      >
        {{ t('opsChat.cancelBatch') }}
      </button>
    </div>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.confirmation {
  border: 1px solid var(--color-amber);
  border-radius: 8px;
  margin: 12px 16px;
  padding: 12px;
}
.batch-list {
  padding-left: 20px;
}
.batch-list li {
  padding: 6px 0;
}
.confirmation p {
  margin: 6px 0;
}
.exec-argv {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-width: 0;
  margin: 8px 0;
  padding: 8px;
  font-family: var(--font-mono);
  color: var(--color-text);
  background: var(--color-inset);
}
</style>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, shallowRef, onBeforeUnmount } from 'vue'
import type { Call, Confirmation } from '../../api/opsChat'
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
const loaded = computed(
  () =>
    props.confirmation.calls.length > 0 &&
    props.confirmation.calls.every((bound) => {
      const call = props.calls[bound.callId]
      return (
        call &&
        call.serverId === bound.serverId &&
        call.object === bound.object &&
        call.argsHash === bound.argsHash &&
        call.targetHash === bound.targetHash &&
        !!call.args.action
      )
    }),
)
</script>
<template>
  <section class="confirmation">
    <strong>{{ t('opsChat.batch') }}</strong>
    <p class="ops-muted">{{ t('opsChat.batchImpact') }}</p>
    <ul class="batch-list">
      <li v-for="bound in confirmation.calls" :key="bound.callId">
        <strong class="ops-wrap">{{
          calls[bound.callId]?.serverName || bound.serverId
        }}</strong>
        <p class="ops-wrap">
          {{ t('opsChat.tools.' + bound.toolId) }} ·
          {{
            calls[bound.callId]?.args.action
              ? t('opsChat.actions.' + calls[bound.callId]!.args.action)
              : t('opsChat.loadingResult')
          }}
        </p>
        <code class="ops-wrap">{{ bound.object }}</code>
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
        @click="emit('confirm', confirmation)"
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
</style>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy } from '@vicons/tabler'
import type { Call, Entry } from '../../api/opsChat'
import AiOpsToolResult from './AiOpsToolResult.vue'
const props = defineProps<{
  entries: Entry[]
  calls: Record<string, Call>
  disabled: boolean
  modelAvailable: boolean
  hasHistory: boolean
}>()
const emit = defineEmits<{
  copy: [text: string]
  retry: [id: string]
  analyze: [id: string]
  terminal: [id: string]
  history: []
}>()
const { t } = useI18n()
// Each call has one result surface, even when several status events refer to it.
const visible = computed(() => {
  const seen = new Set<string>()
  return props.entries.filter((entry) => {
    if (entry.callId) {
      if (seen.has(entry.callId)) return false
      seen.add(entry.callId)
      return true
    }
    return !!entry.text
  })
})
</script>
<template>
  <div class="messages" aria-live="polite">
    <button
      v-if="hasHistory"
      class="ops-btn"
      :disabled="disabled"
      @click="emit('history')"
    >
      {{ t('opsChat.older') }}
    </button>
    <p v-if="!visible.length" class="ops-muted">{{ t('opsChat.empty') }}</p>
    <template v-for="entry in visible" :key="entry.seq">
      <AiOpsToolResult
        v-if="entry.callId && calls[entry.callId]"
        :call="calls[entry.callId]!"
        :disabled="disabled"
        :model-available="modelAvailable"
        @copy="emit('copy', $event)"
        @retry="emit('retry', $event)"
        @analyze="emit('analyze', $event)"
        @terminal="emit('terminal', $event)"
      />
      <p v-else-if="entry.callId" class="ops-muted">
        {{ t('opsChat.loadingResult') }}
      </p>
      <div v-else class="message" :class="{ user: entry.kind === 'user' }">
        <p class="ops-wrap">{{ entry.text }}</p>
        <button
          v-if="entry.kind === 'assistant' || entry.kind === 'manual_advice'"
          class="ops-btn"
          @click="emit('copy', entry.text ?? '')"
        >
          <Copy />{{ t('opsChat.copy') }}
        </button>
      </div>
    </template>
  </div>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.messages {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}
.message {
  padding: 10px 12px;
  border-left: 2px solid var(--color-border-strong);
}
.message p {
  margin: 0;
  line-height: 1.65;
}
.message.user {
  background: var(--color-primary-soft);
  border-left-color: var(--color-primary);
  border-radius: 4px;
}
</style>

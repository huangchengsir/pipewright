<script setup lang="ts">
import { shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Preview, Call } from '../../api/opsChat'
const props = defineProps<{
  preview: Preview
  calls: Record<string, Call>
  disabled: boolean
}>()
const emit = defineEmits<{ approve: []; cancel: [] }>()
const { t } = useI18n()
const consent = shallowRef(false)
watch(
  () => props.preview,
  () => {
    consent.value = false
  },
)
</script>
<template>
  <section class="consent">
    <strong>{{ t('opsChat.analysisPreview') }}</strong>
    <p class="ops-wrap">
      {{ preview.provider.provider }} · {{ preview.provider.model }}
    </p>
    <details>
      <summary>{{ t('opsChat.binding') }}</summary>
      <p class="ops-muted ops-wrap">
        {{ preview.provider.configHash }}<br />{{ preview.hash }}
      </p>
    </details>
    <div v-for="item in preview.items" :key="item.callId" class="preview-item">
      <p class="ops-wrap">
        {{ calls[item.callId]?.serverName || item.targetAlias }} ·
        {{ item.callId }} · {{ t('opsChat.tools.' + item.toolId) }} ·
        {{ t('opsChat.status.' + item.status) }}
      </p>
      <pre class="ops-pre"
        >{{ item.output }}{{ item.error ? '\n' + item.error : '' }}</pre
      >
    </div>
    <label class="consent-label"
      ><input v-model="consent" type="checkbox" :disabled="disabled" />{{
        t('opsChat.consent')
      }}</label
    >
    <div class="ops-row">
      <button
        class="ops-btn ops-primary"
        :disabled="disabled || !consent"
        @click="emit('approve')"
      >
        {{ t('opsChat.analyze') }}
      </button>
      <button class="ops-btn" :disabled="disabled" @click="emit('cancel')">
        {{ t('opsChat.cancel') }}
      </button>
    </div>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.consent {
  margin: 12px 16px;
  padding: 12px;
  border: 1px solid var(--color-border-strong);
  border-radius: 8px;
}
.consent-label {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 12px 0;
  line-height: 1.6;
}
.consent-label input {
  width: 18px;
  height: 18px;
  flex: none;
}
.preview-item {
  margin: 12px 0;
}
</style>

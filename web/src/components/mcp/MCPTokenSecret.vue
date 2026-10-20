<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { NModal } from 'naive-ui'
import { Copy, X } from '@vicons/tabler'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import AppTooltip from '../ui/AppTooltip.vue'
import { copyText } from '../../utils/clipboard'
import { useToast } from '../../composables/useToast'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const toast = useToast()
let alive = true
async function copy() {
  const token = props.token
  if (!alive || !token) return
  const ok = await copyText(token, () => alive && !!props.token && props.token === token)
  if (!alive || props.token !== token) return
  if (ok) toast.success(t('settingsMCP.copied'))
  else toast.error(t('settingsMCP.copyFailed'))
}
onBeforeUnmount(() => { alive = false })
</script>

<template>
  <NModal :show="!!token" :mask-closable="false" :close-on-esc="false" preset="card" :closable="false" :style="{ width: 'min(560px, calc(100vw - 32px))', borderRadius: '8px' }" :title="t('settingsMCP.secretTitle')">
    <template #header-extra><button class="mcp-secret-close" :aria-label="t('settingsMCP.close')" @click="emit('close')"><X aria-hidden="true" /></button></template>
    <p class="mcp-secret-warning">{{ t('settingsMCP.secretWarning') }}</p>
    <div class="mcp-secret-value"><code>{{ token }}</code><AppTooltip :content="t('settingsMCP.copy')"><button class="mcp-secret-copy" :aria-label="t('settingsMCP.copy')" @click="copy"><Copy aria-hidden="true" /></button></AppTooltip></div>
    <template #footer><div class="mcp-secret-footer"><AppButton variant="primary" @click="emit('close')">{{ t('settingsMCP.close') }}</AppButton></div></template>
  </NModal>
</template>

<style scoped>
.mcp-secret-warning { color: var(--color-dim); margin: 0 0 16px; line-height: 1.6; }
.mcp-secret-value { display: flex; align-items: center; gap: 10px; padding: 14px; border: 1px solid var(--color-border); border-radius: 6px; background: var(--color-inset); }
.mcp-secret-value code { min-width: 0; flex: 1; overflow-wrap: anywhere; font-family: var(--font-mono); font-size: 13px; }
.mcp-secret-close, .mcp-secret-copy { width: 30px; height: 30px; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; color: var(--color-dim); border: 1px solid var(--color-border); border-radius: 6px; background: transparent; cursor: pointer; }
.mcp-secret-close svg, .mcp-secret-copy svg { width: 16px; height: 16px; }
.mcp-secret-footer { display: flex; justify-content: flex-end; }
</style>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../ui/AppButton.vue'
import { fenceMCPConfigOperation, getMCPConfigOperation, type MCPConfigOperationResult } from '../../api/mcpMaintenance'

const { t } = useI18n()
const operationId = ref('')
const result = ref<MCPConfigOperationResult | null>(null)
const loading = ref(false)
const fencing = ref(false)
const actionLocked = ref(false)
const error = ref('')
const knownStates = new Set(['pending', 'applied', 'rejected', 'failed', 'unknown'])
const canFence = computed(() => !!result.value && ['pending', 'unknown'].includes(result.value.state) && !actionLocked.value && !loading.value && !fencing.value)
let generation = 0
let alive = true
let controller: AbortController | undefined
watch(operationId, () => { generation++; controller?.abort(); result.value = null; error.value = ''; actionLocked.value = false; loading.value = false })
async function lookup(): Promise<void> {
  const id = operationId.value.trim()
  if (!alive || fencing.value || !id || id.length > 128) return
  const ticket = ++generation
  controller?.abort(); controller = new AbortController()
  loading.value = true; result.value = null; error.value = ''
  try {
    const value = await getMCPConfigOperation(id, { signal: controller.signal })
    if (!alive || ticket !== generation) return
    if (value.operationId !== id) throw new Error('invalid_operation_binding')
    result.value = value
    actionLocked.value = false
  } catch { if (alive && ticket === generation) error.value = t('mcpConfig.operationLookupFailed') }
  finally { if (alive && ticket === generation) loading.value = false }
}
async function fence(): Promise<void> {
  const original = result.value
  if (!alive || !original || !canFence.value) return
  const ticket = generation
  fencing.value = true; error.value = ''
  try {
    const value = await fenceMCPConfigOperation(original.operationId, { signal: controller?.signal })
    if (!alive || ticket !== generation) return
    if (value.operationId !== original.operationId) throw new Error('invalid_operation_binding')
    result.value = value
  } catch { if (alive && ticket === generation) error.value = t('mcpConfig.operationFenceFailed') }
  finally { if (alive && ticket === generation) { fencing.value = false; actionLocked.value = true } }
}
onBeforeUnmount(() => { alive = false; generation++; controller?.abort() })
</script>

<template>
  <section class="operation-panel" aria-labelledby="operation-panel-title">
    <h2 id="operation-panel-title">{{ t('mcpConfig.operationTitle') }}</h2>
    <p>{{ t('mcpConfig.operationHint') }}</p>
    <form class="operation-lookup" @submit.prevent="lookup"><label for="mcp-operation-id">{{ t('mcpConfig.operationId') }}</label><input id="mcp-operation-id" v-model="operationId" maxlength="128" autocomplete="off" :disabled="fencing" /><AppButton type="submit" :loading="loading" :disabled="fencing || !operationId.trim()">{{ t('mcpConfig.operationLookup') }}</AppButton></form>
    <p v-if="error" role="alert" class="operation-error">{{ error }}</p>
    <template v-if="result">
      <dl><div><dt>{{ t('mcpConfig.project') }}</dt><dd><code>{{ result.projectId }}</code></dd></div><div><dt>{{ t('settingsMCP.status') }}</dt><dd role="status">{{ t(`mcpConfig.states.${knownStates.has(result.state) ? result.state : 'unknown'}`) }}</dd></div><div v-if="result.newRevision"><dt>{{ t('mcpConfig.newRevision') }}</dt><dd><code>{{ result.newRevision }}</code></dd></div></dl>
      <p v-if="result.state === 'applied'">{{ t('mcpConfig.operationApplied') }}</p>
      <p v-else-if="result.reason === 'interrupted_not_applied'">{{ t('mcpConfig.operationFenced') }}</p>
      <div v-if="['pending', 'unknown'].includes(result.state)"><p>{{ t('mcpConfig.operationFenceHint') }}</p><AppButton v-if="!actionLocked" :loading="fencing" :disabled="!canFence" @click="fence">{{ t('mcpConfig.operationFence') }}</AppButton></div>
    </template>
  </section>
</template>

<style scoped>
.operation-panel { padding: 24px 0; margin-top: 24px; border-top: 1px solid var(--color-border-strong); }
h2 { margin: 0 0 12px; font-size: 20px; }
p { line-height: 1.7; color: var(--color-dim); font-size: 14px; max-width: 75ch; }
.operation-lookup { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
label { width: 100%; font-size: 14px; }
input { width: 100%; min-width: 0; height: 44px; border: 1px solid var(--color-border-strong); background: var(--color-inset); color: var(--color-text); padding: 8px 12px; border-radius: var(--rounded); font: 16px var(--font-mono); }
input:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
dl { display: flex; flex-wrap: wrap; gap: 20px; margin: 20px 0; }
dt { font-size: 13px; color: var(--color-dim); margin-bottom: 6px; } dd { margin: 0; overflow-wrap: anywhere; }
code { font-family: var(--font-mono); font-size: 13px; }
.operation-error { color: var(--color-red); }
:deep(.app-btn) { min-height: 44px; }
@media(min-width:640px) { input { width: min(100%, 480px); } }
</style>

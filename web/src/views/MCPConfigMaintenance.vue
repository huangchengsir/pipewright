<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../components/ui/AppButton.vue'
import MCPConfigOperationPanel from '../components/mcp/MCPConfigOperationPanel.vue'
import { getMCPConfigMaintenance, recoverMCPConfigActivity, type MCPConfigActivity, type MCPConfigMaintenance } from '../api/mcpMaintenance'

const { t } = useI18n()
const data = ref<MCPConfigMaintenance | null>(null)
const loading = ref(false)
const recovering = ref('')
const error = ref('')
const refreshRequired = ref(false)
let generation = 0
let alive = true
let controller: AbortController | undefined
const phases = new Set(['queued', 'executing', 'finalizing', 'cleanup_unknown', 'released'])
function canRecover(item: MCPConfigActivity): boolean {
  return item.recoverable === true && !!item.evidenceId && !!item.digest && /^cfg-[0-9]+$/.test(item.projectRevision) && !refreshRequired.value
}
async function load(): Promise<void> {
  if (!alive || recovering.value) return
  const ticket = ++generation
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  data.value = null
  try {
    const result = await getMCPConfigMaintenance({ signal: controller.signal })
    if (!alive || ticket !== generation) return
    data.value = result
    refreshRequired.value = false
  } catch {
    if (alive && ticket === generation) error.value = t('mcpConfig.maintenanceLoadFailed')
  } finally { if (alive && ticket === generation) loading.value = false }
}
async function recover(item: MCPConfigActivity): Promise<void> {
  if (!alive || recovering.value || loading.value || !canRecover(item) || !data.value?.items.includes(item)) return
  const ticket = generation
  recovering.value = item.activityId
  error.value = ''
  let succeeded = false
  try {
    await recoverMCPConfigActivity({ activityId: item.activityId, expectedDigest: item.digest, evidenceId: item.evidenceId }, { signal: controller?.signal })
    succeeded = true
  } catch {
    if (alive && ticket === generation) {
      refreshRequired.value = true
      error.value = t('mcpConfig.maintenanceRecoverFailed')
    }
  } finally { if (alive && ticket === generation) recovering.value = '' }
  if (succeeded && alive && ticket === generation) await load()
}
onMounted(load)
onBeforeUnmount(() => { alive = false; generation++; controller?.abort() })
</script>

<template>
  <main class="maintenance" aria-labelledby="mcp-maintenance-title">
    <header><div><p class="maintenance-eyebrow">MCP</p><h1 id="mcp-maintenance-title">{{ t('mcpConfig.maintenanceTitle') }}</h1></div><AppButton :loading="loading" :disabled="!!recovering" @click="load">{{ t('common.refresh') }}</AppButton></header>
    <p class="maintenance-lede">{{ t('mcpConfig.maintenanceHint') }}</p>
    <MCPConfigOperationPanel />
    <p v-if="error" role="alert" class="maintenance-error">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('mcpConfig.loading') }}</p>
    <template v-if="data">
      <p class="maintenance-epoch">{{ t('mcpConfig.executorEpoch') }} <code>{{ data.executorEpoch }}</code></p>
      <p v-if="data.truncated" role="note" class="maintenance-warning">{{ t('mcpConfig.maintenanceTruncated') }}</p>
      <p v-if="!data.items.length" class="maintenance-empty">{{ t('mcpConfig.maintenanceEmpty') }}</p>
      <article v-for="item in data.items" :key="item.activityId" class="maintenance-item" :data-activity-id="item.activityId">
        <dl><div><dt>{{ t('mcpConfig.project') }}</dt><dd><code>{{ item.projectId }}</code></dd></div><div><dt>{{ t('mcpConfig.activity') }}</dt><dd><code>{{ item.activityId }}</code></dd></div><div><dt>{{ t('mcpConfig.run') }}</dt><dd><code>{{ item.runId || t('mcpConfig.notProvided') }}</code></dd></div><div><dt>{{ t('settingsMCP.status') }}</dt><dd>{{ t(`mcpConfig.activityPhases.${phases.has(item.phase) ? item.phase : 'unknown'}`) }}</dd></div><div><dt>{{ t('mcpConfig.ownerEpoch') }}</dt><dd><code>{{ item.ownerEpoch }}</code></dd></div><div><dt>{{ t('mcpConfig.baseRevision') }}</dt><dd><code>{{ item.projectRevision }}</code></dd></div></dl>
        <p v-if="canRecover(item)" class="maintenance-evidence">{{ t('mcpConfig.maintenanceEvidence') }}</p>
        <p v-else class="maintenance-warning">{{ t(item.reason === 'evidence_unavailable' ? 'mcpConfig.maintenanceUnavailable' : 'mcpConfig.maintenanceBlocked') }}</p>
        <AppButton v-if="canRecover(item)" :loading="recovering === item.activityId" :disabled="loading || !!recovering" @click="recover(item)">{{ t('mcpConfig.recoverActivity') }}</AppButton>
      </article>
    </template>
  </main>
</template>

<style scoped>
.maintenance { max-width: 1040px; margin: 0 auto; padding: 24px 16px 48px; min-width: 0; color: var(--color-text); }
.maintenance > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.maintenance-eyebrow { color: var(--color-primary); font: 600 12px var(--font-mono); margin: 0 0 6px; }
h1 { font-size: 26px; margin: 0; }
.maintenance-lede { color: var(--color-dim); line-height: 1.7; max-width: 72ch; margin: 16px 0 24px; }
.maintenance-epoch, .maintenance-empty { color: var(--color-dim); }
.maintenance-item { border-top: 1px solid var(--color-border-strong); padding: 20px 0; }
.maintenance-item dl { display: grid; gap: 16px; margin: 0; }
dt { color: var(--color-dim); font-size: 13px; margin-bottom: 6px; }
dd { margin: 0; overflow-wrap: anywhere; }
code { font: 12px var(--font-mono); }
.maintenance-warning, .maintenance-error, .maintenance-evidence { padding: 14px 16px; line-height: 1.7; font-size: 14px; border-left: 3px solid var(--color-amber); background: var(--color-amber-soft); }
.maintenance-error { color: var(--color-red); background: var(--color-red-soft); border-left-color: var(--color-red); }
.maintenance-evidence { background: var(--color-inset); border-left-color: var(--color-primary); }
.maintenance-item :deep(.app-btn), .maintenance > header :deep(.app-btn) { min-height: 44px; }
@media (min-width: 640px) { .maintenance { padding: 32px 28px 64px; } .maintenance-item dl { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1000px) { .maintenance-item dl { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
</style>

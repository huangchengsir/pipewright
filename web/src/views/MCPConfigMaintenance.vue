<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../components/ui/AppButton.vue'
import MCPConfigOperationPanel from '../components/mcp/MCPConfigOperationPanel.vue'
import '../styles/settings-surface.css'
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
  <main class="maintenance settings-page" aria-labelledby="mcp-maintenance-title">
    <header><div><p class="maintenance-eyebrow">MCP</p><h1 id="mcp-maintenance-title">{{ t('mcpConfig.maintenanceTitle') }}</h1></div><AppButton :loading="loading" :disabled="!!recovering" @click="load">{{ t('common.refresh') }}</AppButton></header>
    <p class="maintenance-lede">{{ t('mcpConfig.maintenanceSummary') }}</p><details class="maintenance-details"><summary>{{ t('mcpConfig.maintenanceDetails') }}</summary><p>{{ t('mcpConfig.maintenanceHint') }}</p></details>
    <MCPConfigOperationPanel />
    <p v-if="error" role="alert" class="maintenance-error">{{ error }}</p>
    <p v-if="loading" role="status">{{ t('mcpConfig.loading') }}</p>
    <template v-if="data">
      <details class="maintenance-details"><summary>{{ t('mcpConfig.technicalDetails') }}</summary><p class="maintenance-epoch">{{ t('mcpConfig.executorEpoch') }} <code>{{ data.executorEpoch }}</code></p></details>
      <p v-if="data.truncated" role="note" class="maintenance-warning">{{ t('mcpConfig.maintenanceTruncated') }}</p>
      <p v-if="!data.items.length" class="maintenance-empty">{{ t('mcpConfig.maintenanceEmpty') }}</p>
      <article v-for="item in data.items" :key="item.activityId" class="maintenance-item" :data-activity-id="item.activityId">
        <header class="maintenance-item-header"><h2>{{ item.projectId }}</h2><span class="maintenance-status">{{ t(`mcpConfig.activityPhases.${phases.has(item.phase) ? item.phase : 'unknown'}`) }}</span></header>
        <p class="maintenance-identity"><span>{{ t('mcpConfig.activity') }} · <code>{{ item.activityId }}</code></span><span>{{ t('mcpConfig.run') }} · <code>{{ item.runId || t('mcpConfig.notProvided') }}</code></span></p>
        <p :class="canRecover(item) ? 'maintenance-evidence' : 'maintenance-warning'">{{ t(canRecover(item) ? 'mcpConfig.maintenanceReady' : 'mcpConfig.maintenanceNeedsReview') }}</p>
        <details class="maintenance-details"><summary>{{ t('mcpConfig.maintenanceDetails') }}</summary>
        <dl><div><dt>{{ t('mcpConfig.project') }}</dt><dd><code>{{ item.projectId }}</code></dd></div><div><dt>{{ t('mcpConfig.activity') }}</dt><dd><code>{{ item.activityId }}</code></dd></div><div><dt>{{ t('mcpConfig.run') }}</dt><dd><code>{{ item.runId || t('mcpConfig.notProvided') }}</code></dd></div><div><dt>{{ t('settingsMCP.status') }}</dt><dd>{{ t(`mcpConfig.activityPhases.${phases.has(item.phase) ? item.phase : 'unknown'}`) }}</dd></div><div><dt>{{ t('mcpConfig.ownerEpoch') }}</dt><dd><code>{{ item.ownerEpoch }}</code></dd></div><div><dt>{{ t('mcpConfig.baseRevision') }}</dt><dd><code>{{ item.projectRevision }}</code></dd></div></dl>
        <p v-if="canRecover(item)" class="maintenance-evidence">{{ t('mcpConfig.maintenanceEvidence') }}</p>
        <p v-else class="maintenance-warning">{{ t(item.reason === 'evidence_unavailable' ? 'mcpConfig.maintenanceUnavailable' : 'mcpConfig.maintenanceBlocked') }}</p>
        </details>
        <AppButton v-if="canRecover(item)" :loading="recovering === item.activityId" :disabled="loading || !!recovering" @click="recover(item)">{{ t('mcpConfig.recoverActivity') }}</AppButton>
      </article>
    </template>
  </main>
</template>

<style scoped>
.maintenance-identity { display: flex; flex-wrap: wrap; gap: 6px 16px; margin: 8px 0; font-size: 12px; color: var(--color-dim); overflow-wrap: anywhere; }
.maintenance { max-width: 1120px; margin: 0 auto; padding: 0 0 32px; min-width: 0; color: var(--color-text); font-size: var(--settings-text-size, 13px); }
.maintenance > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.maintenance-eyebrow { color: var(--color-primary); font: 600 11px var(--font-mono); margin: 0 0 5px; }
h1 { font-size: 20px; line-height: 1.35; margin: 0; overflow-wrap: anywhere; }
.maintenance-lede { color: var(--color-dim); font-size: 13px; line-height: 1.6; max-width: 80ch; margin: 8px 0 12px; }
.maintenance-details { font-size: 12px; line-height: 1.65; color: var(--color-dim); margin: 10px 0; }
.maintenance-details > summary { cursor: pointer; min-height: 28px; width: fit-content; }
.maintenance-details > summary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.maintenance-details p { margin: 7px 0; }
.maintenance-epoch, .maintenance-empty { color: var(--color-dim); }
.maintenance-item { border: 1px solid var(--color-border); border-radius: 9px; background: var(--color-card); padding: 16px 18px; margin-top: 16px; }
.maintenance-item-header { display: flex; align-items: baseline; gap: 12px; justify-content: space-between; flex-wrap: wrap; }
.maintenance-item-header h2 { font-size: 14px; margin: 0; overflow-wrap: anywhere; }
.maintenance-status { color: var(--color-dim); background: var(--color-inset); padding: 3px 7px; border-radius: 4px; font-size: 11px; }
.maintenance-item dl { display: grid; gap: 12px; margin: 10px 0; padding: 12px; border: 1px solid var(--color-border); border-radius: 6px; background: var(--color-inset); }
dt { color: var(--color-dim); font-size: 11px; margin-bottom: 5px; }
dd { margin: 0; overflow-wrap: anywhere; }
code { font: 12px var(--font-mono); }
.maintenance-warning, .maintenance-error, .maintenance-evidence { padding: 9px 12px; line-height: 1.65; font-size: 12px; border: 1px solid var(--color-border); border-radius: 6px; }
.maintenance-warning { color: var(--color-amber); background: var(--color-amber-soft); }
.maintenance-error { color: var(--color-red); background: var(--color-red-soft); border-color: var(--color-red-soft); }
.maintenance-evidence { color: var(--color-dim); background: var(--color-inset); }
.maintenance-details .maintenance-warning, .maintenance-details .maintenance-evidence { padding: 0; border: 0; background: transparent; }
.maintenance-item :deep(.app-btn), .maintenance > header :deep(.app-btn) { height: var(--settings-action-height, 34px); min-height: var(--settings-action-height, 34px); }
@media (min-width: 640px) { .maintenance-item dl { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1000px) { .maintenance-item dl { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 639px) { .maintenance-details > summary { min-height: 44px; align-content: center; } .maintenance-item { padding: 16px; } .maintenance-warning, .maintenance-error, .maintenance-evidence { font-size: 13px; } }
</style>

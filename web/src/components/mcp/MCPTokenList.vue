<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Ban } from '@vicons/tabler'
import AppTooltip from '../ui/AppTooltip.vue'
import { mcpTokenState, type MCPToken } from '../../api/mcp'

const props = defineProps<{ tokens: MCPToken[]; projectNames: Record<string, string>; now: number; revoking: string }>()
const emit = defineEmits<{ revoke: [token: MCPToken] }>()
const { t, locale } = useI18n()
const rows = computed(() => props.tokens.map(token => ({ token, state: mcpTokenState(token, props.now), projects: token.projectIds.map(id => props.projectNames[id] ?? id).join(', ') })))
function date(value: string) { const d = new Date(value); return Number.isFinite(d.getTime()) ? d.toLocaleString(locale.value) : t('settingsMCP.expired') }
</script>

<template>
  <p v-if="!rows.length" class="mcp-empty">{{ t('settingsMCP.empty') }}</p>
  <div v-else class="mcp-table-wrap">
    <table class="mcp-table">
      <thead><tr><th>{{ t('settingsMCP.name') }}</th><th>{{ t('settingsMCP.projects') }}</th><th>{{ t('settingsMCP.authorization') }}</th><th>{{ t('settingsMCP.expires') }}</th><th>{{ t('settingsMCP.status') }}</th><th class="mcp-actions"><span class="mcp-sr">{{ t('settingsMCP.revoke') }}</span></th></tr></thead>
      <tbody><tr v-for="row in rows" :key="row.token.id" :data-token-id="row.token.id">
        <td><strong>{{ row.token.name }}</strong><small>{{ t('settingsMCP.created') }} {{ date(row.token.createdAt) }}</small></td>
        <td>{{ row.projects }}</td><td>
          {{ t(row.token.configGrants?.length ? (row.token.canTrigger ? 'mcpConfig.runtimeTrigger' : 'mcpConfig.runtimeRead') : (row.token.canTrigger ? 'settingsMCP.triggerPermission' : 'settingsMCP.readOnly')) }}
          <small v-for="grant in row.token.configGrants" :key="grant.projectId" class="mcp-grant-summary">
            {{ projectNames[grant.projectId] ?? grant.projectId }}:
            {{ [grant.read && t('mcpConfig.read'), grant.propose && t('mcpConfig.propose'), grant.apply && t('mcpConfig.apply')].filter(Boolean).join(' · ') }}
          </small>
        </td><td>{{ date(row.token.expiresAt) }}</td>
        <td><span class="mcp-status" :class="`mcp-status--${row.state}`">{{ t(`settingsMCP.${row.state}`) }}</span></td>
        <td class="mcp-actions"><AppTooltip :content="t('settingsMCP.revoke')"><button type="button" class="mcp-icon-button" :disabled="!!row.token.revokedAt || revoking === row.token.id" :aria-label="t('settingsMCP.revoke')" @click="emit('revoke', row.token)"><Ban aria-hidden="true" /></button></AppTooltip></td>
      </tr></tbody>
    </table>
  </div>
</template>

<style scoped>
.mcp-table-wrap { position: relative; min-width: 0; max-width: 100%; overflow-x: auto; }
.mcp-table { width: 100%; border-collapse: collapse; table-layout: fixed; min-width: 640px; font-size: var(--text-label); }
.mcp-table th { color: var(--color-dim); font-weight: 500; text-align: left; }
.mcp-table th, .mcp-table td { padding: 12px 10px; border-bottom: 1px solid var(--color-border); vertical-align: middle; overflow-wrap: anywhere; }
.mcp-table th:first-child, .mcp-table td:first-child { padding-left: 0; width: 23%; }
.mcp-table th:nth-child(2) { width: 25%; }
.mcp-table strong { font-weight: 600; }
.mcp-table small { display: block; color: var(--color-faint); font-size: 11px; margin-top: 5px; }
.mcp-actions { width: 48px; text-align: right !important; }
.mcp-icon-button { width: 30px; height: 30px; border: 1px solid var(--color-border); border-radius: 6px; color: var(--color-dim); background: var(--color-surface, transparent); display: inline-flex; align-items: center; justify-content: center; cursor: pointer; }
.mcp-icon-button svg { width: 16px; height: 16px; }
.mcp-icon-button:hover:not(:disabled) { color: #bd2538; border-color: #bd2538; }
.mcp-icon-button:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-icon-button:disabled { opacity: .4; cursor: default; }
.mcp-status { display: inline-block; padding: 3px 6px; border-radius: 4px; font-size: 11px; color: var(--color-dim); background: var(--color-inset); white-space: nowrap; }
.mcp-status--active { color: #168343; background: #e5f5eb; }
.mcp-empty { color: var(--color-faint); font-size: var(--text-body); padding: 16px 0; }
.mcp-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
</style>

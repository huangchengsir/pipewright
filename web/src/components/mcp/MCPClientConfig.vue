<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy } from '@vicons/tabler'
import AppTooltip from '../ui/AppTooltip.vue'

const props = defineProps<{ endpoint: string }>()
const emit = defineEmits<{ copy: [value: string] }>()
const { t } = useI18n()
const clients = computed(() => [
  {
    id: 'claude', name: 'Claude Code', path: '.mcp.json', format: 'JSON',
    config: JSON.stringify({ mcpServers: { pipewright: { type: 'http', url: props.endpoint, headers: { Authorization: 'Bearer <MCP_TOKEN>' } } } }, null, 2),
  },
  {
    id: 'codex', name: 'Codex', path: '~/.codex/config.toml', format: 'TOML',
    config: `[mcp_servers.pipewright]\nurl = ${JSON.stringify(props.endpoint)}\nhttp_headers = { Authorization = "Bearer <MCP_TOKEN>" }`,
  },
])
</script>

<template>
  <details class="mcp-config" open>
    <summary>{{ t('settingsMCP.clientConfig') }}</summary>
    <div class="mcp-client-grid">
      <section v-for="client in clients" :key="client.id" class="mcp-client" :data-client="client.id" :aria-label="client.name">
        <header class="mcp-client__header">
          <h3 class="mcp-client__name">{{ client.name }}</h3>
          <code class="mcp-client__path">{{ client.path }}</code>
          <span class="mcp-client__format">{{ client.format }}</span>
        </header>
        <div class="mcp-client__body">
          <pre class="mcp-client__code">{{ client.config }}</pre>
          <AppTooltip class="mcp-client__copy-wrap" :content="`${t('settingsMCP.copy')} ${client.name}`">
            <button type="button" class="mcp-client__copy" :aria-label="`${t('settingsMCP.copy')} ${client.name}`" @click="emit('copy', client.config)">
              <Copy aria-hidden="true" />
            </button>
          </AppTooltip>
        </div>
      </section>
    </div>
  </details>
</template>

<style scoped>
.mcp-config { font-size: var(--text-label); min-width: 0; }
.mcp-config summary { cursor: pointer; color: var(--color-dim); width: fit-content; }
.mcp-client-grid { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; margin-top: 16px; }
.mcp-client { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.mcp-client__header { display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px 12px; }
.mcp-client__name { margin: 0; color: var(--color-text); font-size: 13px; font-weight: 600; letter-spacing: 0; }
.mcp-client__path { min-width: 0; overflow-wrap: anywhere; color: var(--color-dim); font-family: var(--font-mono); font-size: 12px; }
.mcp-client__format { margin-inline-start: auto; font-size: 11px; color: var(--color-dim); }
.mcp-client__body { position: relative; flex: 1; min-width: 0; background: var(--color-inset); padding: 14px 56px 14px 14px; border: 1px solid var(--color-border); border-radius: 6px; }
.mcp-client__code { margin: 0; font-family: var(--font-mono); font-size: 12px; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
.mcp-client__copy-wrap { position: absolute; top: 8px; right: 8px; }
.mcp-client__copy-wrap :deep(.tooltip-bubble) { left: auto; right: 0; translate: none; }
.mcp-client__copy-wrap :deep(.tooltip-bubble::after) { left: auto; right: 10px; translate: none; }
.mcp-client__copy { width: 36px; height: 36px; display: inline-flex; align-items: center; justify-content: center; border: 1px solid var(--color-border); border-radius: 6px; background: transparent; color: var(--color-dim); cursor: pointer; }
.mcp-client__copy svg { width: 16px; height: 16px; }
.mcp-client__copy:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
@media (min-width: 960px) { .mcp-client-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (pointer: coarse) { .mcp-client__copy { width: 44px; height: 44px; } .mcp-client__body { padding-right: 64px; } }
</style>

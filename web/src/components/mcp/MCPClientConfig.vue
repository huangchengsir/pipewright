<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy } from '@vicons/tabler'
import AppTooltip from '../ui/AppTooltip.vue'

const props = defineProps<{ endpoint: string }>()
const emit = defineEmits<{ copy: [value: string] }>()
const { t } = useI18n()
const id = useId()
const tablist = ref<HTMLElement | null>(null)
const activeClient = ref('codex')
const clients = computed(() => [
  {
    id: 'codex', name: 'Codex', path: '~/.codex/config.toml', format: 'TOML',
    config: `[mcp_servers.pipewright]\nurl = ${JSON.stringify(props.endpoint)}\nhttp_headers = { Authorization = "Bearer <MCP_TOKEN>" }`,
  },
  {
    id: 'claude', name: 'Claude Code', path: '.mcp.json', format: 'JSON',
    config: JSON.stringify({ mcpServers: { pipewright: { type: 'http', url: props.endpoint, headers: { Authorization: 'Bearer <MCP_TOKEN>' } } } }, null, 2),
  },
])
function onTabKey(event: KeyboardEvent, index: number) {
  const last = clients.value.length - 1
  let next: number
  if (event.key === 'ArrowRight') next = index === last ? 0 : index + 1
  else if (event.key === 'ArrowLeft') next = index === 0 ? last : index - 1
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = last
  else return
  event.preventDefault()
  activeClient.value = clients.value[next].id
  void nextTick(() => tablist.value?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus())
}
</script>

<template>
  <details class="mcp-config" open>
    <summary>{{ t('settingsMCP.clientConfig') }}</summary>
    <div ref="tablist" class="mcp-client-tabs" role="tablist" :aria-label="t('settingsMCP.clientConfig')">
      <button v-for="(client, index) in clients" :id="`${id}-${client.id}-tab`" :key="client.id" type="button" role="tab" :data-client-tab="client.id" :aria-selected="activeClient === client.id" :aria-controls="`${id}-${client.id}-panel`" :tabindex="activeClient === client.id ? 0 : -1" @click="activeClient = client.id" @keydown="onTabKey($event, index)">{{ client.name }}</button>
    </div>
    <section v-for="client in clients" :id="`${id}-${client.id}-panel`" :key="client.id" class="mcp-client" :data-client="client.id" role="tabpanel" :aria-labelledby="`${id}-${client.id}-tab`" :hidden="activeClient !== client.id" :tabindex="activeClient === client.id ? 0 : -1">
      <header class="mcp-client__header">
        <code class="mcp-client__path">{{ client.path }}</code>
        <span class="mcp-client__format">{{ client.format }}</span>
      </header>
      <div class="mcp-client__body">
        <pre class="mcp-client__code">{{ client.config }}</pre>
        <AppTooltip class="mcp-client__copy-wrap" :content="`${t('settingsMCP.copy')} ${client.name}`">
          <button type="button" class="mcp-client__copy" :aria-label="`${t('settingsMCP.copy')} ${client.name}`" @click="emit('copy', client.config)"><Copy aria-hidden="true" /></button>
        </AppTooltip>
      </div>
    </section>
  </details>
</template>

<style scoped>
.mcp-config { font-size: var(--settings-label-size, 13px); min-width: 0; }
.mcp-config summary { cursor: pointer; color: var(--color-dim); width: fit-content; min-height: 28px; }
.mcp-client-tabs { display: flex; flex-wrap: wrap; gap: 4px; margin: 10px 0 12px; }
.mcp-client-tabs button { border: 0; border-radius: 5px; padding: 7px 13px; min-height: 34px; color: var(--color-dim); background: transparent; font: inherit; cursor: pointer; }
.mcp-client-tabs button[aria-selected='true'] { color: var(--color-primary); background: var(--color-primary-soft); font-weight: 600; }
.mcp-client-tabs button:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-client { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.mcp-client[hidden] { display: none; }
.mcp-client__header { display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px 12px; }
.mcp-client__path { min-width: 0; overflow-wrap: anywhere; color: var(--color-dim); font-family: var(--font-mono); font-size: 12px; }
.mcp-client__format { margin-inline-start: auto; font-size: 11px; color: var(--color-dim); }
.mcp-client__body { position: relative; min-width: 0; background: var(--color-inset); padding: 12px 54px 12px 12px; border: 1px solid var(--color-border); border-radius: 6px; }
.mcp-client__code { margin: 0; font-family: var(--font-mono); font-size: 12px; line-height: 1.65; white-space: pre-wrap; overflow-wrap: anywhere; }
.mcp-client__copy-wrap { position: absolute; top: 8px; right: 8px; }
.mcp-client__copy-wrap :deep(.tooltip-bubble) { left: auto; right: 0; translate: none; }
.mcp-client__copy-wrap :deep(.tooltip-bubble::after) { left: auto; right: 10px; translate: none; }
.mcp-client__copy { width: 32px; height: 32px; display: inline-flex; align-items: center; justify-content: center; border: 1px solid var(--color-border); border-radius: 6px; background: transparent; color: var(--color-dim); cursor: pointer; }
.mcp-client__copy svg { width: 16px; height: 16px; }
.mcp-client__copy:focus-visible, .mcp-client:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
@media (max-width: 639px), (pointer: coarse) { .mcp-client-tabs button, .mcp-client__copy { min-width: 44px; min-height: 44px; } .mcp-client__body { padding-right: 64px; } }
</style>

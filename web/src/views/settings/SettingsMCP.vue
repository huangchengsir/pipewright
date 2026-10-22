<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, Refresh } from '@vicons/tabler'
import AppButton from '../../components/ui/AppButton.vue'
import AppTooltip from '../../components/ui/AppTooltip.vue'
import MCPTokenForm from '../../components/mcp/MCPTokenForm.vue'
import MCPTokenList from '../../components/mcp/MCPTokenList.vue'
import MCPTokenSecret from '../../components/mcp/MCPTokenSecret.vue'
import MCPClientConfig from '../../components/mcp/MCPClientConfig.vue'
import { listMCPTokens, createMCPToken, revokeMCPToken, type MCPToken, type CreateMCPToken } from '../../api/mcp'
import { listProjects, type Project } from '../../api/projects'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { copyText } from '../../utils/clipboard'

const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()
const tokens = shallowRef<MCPToken[]>([])
const projects = shallowRef<Project[]>([])
const loading = shallowRef(true)
const saving = shallowRef(false)
const revoking = shallowRef('')
const mutating = computed(() => saving.value || !!revoking.value)
const loadError = shallowRef('')
const saveError = shallowRef('')
const secret = shallowRef('')
const now = shallowRef(Date.now())
const endpoint = `${window.location.origin}/mcp`
const projectNames = computed(() => Object.fromEntries(projects.value.map(p => [p.id, p.name])))
let alive = true
let epoch = 0
let secretTokenId = ''
let timer: ReturnType<typeof setInterval> | undefined

function invalidateLoad() {
  epoch++
  loading.value = false
}
function clearSecret() {
  secret.value = ''
  secretTokenId = ''
}
async function load() {
  if (!alive || mutating.value) return
  const ticket = ++epoch
  loading.value = true
  loadError.value = ''
  try {
    const items = await listMCPTokens()
    if (!alive || ticket !== epoch) return
    const availableProjects = await listProjects()
    if (!alive || ticket !== epoch) return
    tokens.value = items
    projects.value = availableProjects
    now.value = Date.now()
  } catch {
    if (alive && ticket === epoch) loadError.value = t('settingsMCP.loadFailed')
  } finally {
    if (alive && ticket === epoch) loading.value = false
  }
}
async function create(input: CreateMCPToken) {
  if (mutating.value || !alive) return
  saving.value = true
  invalidateLoad()
  saveError.value = ''
  try {
    const result = await createMCPToken(input)
    if (!alive) return
    secret.value = result.token
    secretTokenId = result.item.id
    tokens.value = [result.item, ...tokens.value]
  } catch {
    if (alive) saveError.value = t('settingsMCP.saveFailed')
  } finally { if (alive) saving.value = false }
}
async function revoke(token: MCPToken) {
  if (mutating.value || !alive) return
  revoking.value = token.id
  try {
    if (!await confirm.open({ title: t('settingsMCP.revokeTitle'), body: t('settingsMCP.revokeBody', { name: token.name }), confirmLabel: t('settingsMCP.revoke'), variant: 'danger' }) || !alive) return
    invalidateLoad()
    await revokeMCPToken(token.id)
    if (!alive) return
    tokens.value = tokens.value.map(item => item.id === token.id ? { ...item, revokedAt: new Date().toISOString() } : item)
    if (secretTokenId === token.id) clearSecret()
  } catch { if (alive) toast.error(t('settingsMCP.revokeFailed')) }
  finally { if (alive) revoking.value = '' }
}
async function copy(value: string) {
  const ok = await copyText(value, () => alive)
  if (!alive) return
  if (ok) toast.success(t('settingsMCP.copied'))
  else toast.error(t('settingsMCP.copyFailed'))
}
onMounted(() => { void load(); timer = setInterval(() => { now.value = Date.now() }, 60000) })
onBeforeUnmount(() => { alive = false; epoch++; clearSecret(); if (timer) clearInterval(timer) })
</script>

<template>
  <div class="mcp-settings">
    <section class="mcp-section" aria-labelledby="mcp-connection-title">
      <h2 id="mcp-connection-title" class="mcp-heading">{{ t('settingsMCP.title') }}</h2>
      <div class="mcp-connection"><span>{{ t('settingsMCP.endpoint') }}</span><code class="mcp-endpoint">{{ endpoint }}</code><AppTooltip :content="t('settingsMCP.copy')"><button class="mcp-copy" :aria-label="t('settingsMCP.copy')" @click="copy(endpoint)"><Copy aria-hidden="true" /></button></AppTooltip></div>
      <dl class="mcp-protocol"><div><dt>{{ t('settingsMCP.transport') }}</dt><dd>Streamable HTTP</dd></div><div><dt>{{ t('settingsMCP.authorization') }}</dt><dd>Bearer</dd></div></dl>
      <p class="mcp-privacy">{{ t('settingsMCP.privacy') }}</p>
      <p class="mcp-privacy"><a href="/mcp/config-maintenance">{{ t('mcpConfig.maintenanceTitle') }}</a></p>
      <MCPClientConfig :endpoint="endpoint" @copy="copy" />
    </section>
    <section class="mcp-section" aria-labelledby="mcp-create-title">
      <h2 id="mcp-create-title" class="mcp-heading">{{ t('settingsMCP.create') }}</h2>
      <MCPTokenForm :projects="projects" :busy="mutating || loading || !!loadError" :error="saveError" @create="create" />
    </section>
    <section class="mcp-section" aria-labelledby="mcp-token-list-title">
      <header class="mcp-list-header"><h2 id="mcp-token-list-title" class="mcp-heading">{{ t('settingsMCP.tokenList') }}</h2><AppButton :loading="loading" :disabled="mutating" @click="load"><Refresh class="mcp-icon" aria-hidden="true" />{{ t('common.refresh') }}</AppButton></header>
      <p v-if="loadError" class="mcp-load-error" role="alert">{{ loadError }}</p>
      <fieldset v-else-if="!loading" class="mcp-token-list" :disabled="mutating">
        <MCPTokenList :tokens="tokens" :project-names="projectNames" :now="now" :revoking="revoking" @revoke="revoke" />
      </fieldset>
    </section>
    <MCPTokenSecret :token="secret" @close="clearSecret" />
  </div>
</template>

<style scoped>
.mcp-settings { min-width: 0; }
.mcp-section { padding: 0 0 24px; margin: 0 0 24px; border-bottom: 1px solid var(--color-border); }
.mcp-section:last-of-type { border-bottom: 0; margin-bottom: 0; }
.mcp-heading { font-size: 16px; font-weight: 600; color: var(--color-text); margin: 0 0 18px; letter-spacing: 0; }
.mcp-connection { display: flex; align-items: center; gap: 12px; font-size: var(--text-label); }
.mcp-connection > span { flex-shrink: 0; color: var(--color-dim); }
.mcp-endpoint { min-width: 0; overflow-wrap: anywhere; font-family: var(--font-mono); font-size: 13px; }
.mcp-copy { width: 30px; height: 30px; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; border: 1px solid var(--color-border); border-radius: 6px; background: transparent; color: var(--color-dim); cursor: pointer; }
.mcp-copy svg, .mcp-icon { width: 16px; height: 16px; }
.mcp-copy:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-protocol { display: flex; gap: 32px; margin: 12px 0; font-size: var(--text-label); flex-wrap: wrap; }
.mcp-protocol > div { display: flex; gap: 12px; }
.mcp-protocol dt { color: var(--color-dim); }
.mcp-protocol dd { margin: 0; }
.mcp-privacy { font-size: var(--text-label); line-height: 1.7; color: var(--color-dim); max-width: 900px; margin: 14px 0; }
.mcp-list-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 16px; }
.mcp-list-header .mcp-heading { margin: 0; }
.mcp-token-list { min-width: 0; margin: 0; padding: 0; border: 0; }
.mcp-load-error { color: var(--color-danger, #bd2538); font-size: var(--text-body); }
@media (max-width: 480px) { .mcp-protocol { gap: 12px; } .mcp-connection { gap: 8px; flex-wrap: wrap; } }
</style>

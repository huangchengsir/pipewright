<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
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
const createPanel = ref<HTMLDetailsElement | null>(null)
async function openCreate() {
  if (!createPanel.value) return
  createPanel.value.open = true
  await nextTick()
  createPanel.value?.querySelector<HTMLInputElement>('#mcp-name')?.focus()
}
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
  <div class="mcp-settings settings-page">
    <div class="mcp-intro-grid">
      <section class="mcp-section mcp-connection-card" aria-labelledby="mcp-connection-title">
        <h2 id="mcp-connection-title" class="mcp-heading">{{ t('settingsMCP.title') }}</h2>
        <div class="mcp-connection"><span>{{ t('settingsMCP.endpoint') }}</span><code class="mcp-endpoint">{{ endpoint }}</code><AppTooltip :content="t('settingsMCP.copy')"><button type="button" class="mcp-copy" :aria-label="t('settingsMCP.copy')" @click="copy(endpoint)"><Copy aria-hidden="true" /></button></AppTooltip></div>
        <dl class="mcp-protocol"><div><dt>{{ t('settingsMCP.transport') }}</dt><dd>Streamable HTTP</dd></div><div><dt>{{ t('settingsMCP.authorization') }}</dt><dd>Bearer</dd></div></dl>
        <MCPClientConfig :endpoint="endpoint" @copy="copy" />
      </section>
      <section class="mcp-section mcp-access-card" aria-labelledby="mcp-access-title">
        <h2 id="mcp-access-title" class="mcp-heading">{{ t('settingsMCP.accessScopeTitle') }}</h2>
        <ul class="mcp-access-list"><li>{{ t('settingsMCP.scopedAccess') }}</li><li>{{ t('settingsMCP.separateApproval') }}</li><li>{{ t('settingsMCP.triggerAccess') }}</li></ul>
        <details class="mcp-help"><summary>{{ t('settingsMCP.privacyTitle') }}</summary><p>{{ t('settingsMCP.privacy') }}</p></details>
        <a class="mcp-maintenance-link" href="/mcp/config-maintenance">{{ t('mcpConfig.maintenanceTitle') }}</a>
      </section>
    </div>
    <details ref="createPanel" class="mcp-section mcp-create" aria-labelledby="mcp-create-title">
      <summary id="mcp-create-title" class="mcp-heading">{{ t('settingsMCP.create') }}</summary>
      <div class="mcp-create-body"><MCPTokenForm :projects="projects" :busy="mutating || loading || !!loadError" :error="saveError" @create="create" /></div>
    </details>
    <section class="mcp-section mcp-list-section" aria-labelledby="mcp-token-list-title">
      <header class="mcp-list-header"><h2 id="mcp-token-list-title" class="mcp-heading">{{ t('settingsMCP.tokenList') }} <span class="mcp-count">{{ tokens.length }}</span></h2><div class="mcp-list-actions"><AppButton :loading="loading" :disabled="mutating" @click="load"><Refresh class="mcp-icon" aria-hidden="true" />{{ t('common.refresh') }}</AppButton><AppButton variant="primary" :disabled="mutating || loading || !!loadError" @click="openCreate">{{ t('settingsMCP.create') }}</AppButton></div></header>
      <p v-if="loadError" class="mcp-load-error" role="alert">{{ loadError }}</p>
      <fieldset v-else-if="!loading" class="mcp-token-list" :disabled="mutating">
        <MCPTokenList :tokens="tokens" :project-names="projectNames" :now="now" :revoking="revoking" @revoke="revoke" />
      </fieldset>
    </section>
    <MCPTokenSecret :token="secret" @close="clearSecret" />
  </div>
</template>

<style scoped>
.mcp-settings { display: grid; gap: var(--settings-page-gap, 20px); min-width: 0; }
.mcp-intro-grid { display: grid; gap: 16px; min-width: 0; }
.mcp-section { min-width: 0; padding: 18px; border: 1px solid var(--color-border); border-radius: 9px; background: var(--color-card); }
.mcp-heading { font-size: 14px; font-weight: 600; color: var(--color-text); margin: 0 0 14px; letter-spacing: 0; overflow-wrap: anywhere; }
.mcp-connection { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; font-size: var(--settings-label-size, 13px); }
.mcp-connection > span { color: var(--color-dim); }
.mcp-endpoint { min-width: 0; overflow-wrap: anywhere; font-family: var(--font-mono); font-size: 12px; }
.mcp-copy { width: 32px; height: 32px; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; border: 1px solid var(--color-border); border-radius: 6px; background: transparent; color: var(--color-dim); cursor: pointer; }
.mcp-copy svg, .mcp-icon { width: 16px; height: 16px; }
.mcp-copy:focus-visible, summary:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.mcp-protocol { display: flex; gap: 12px 24px; margin: 12px 0 16px; font-size: 12px; flex-wrap: wrap; }
.mcp-protocol > div { display: flex; gap: 8px; }
.mcp-protocol dt { color: var(--color-dim); }
.mcp-protocol dd { margin: 0; }
.mcp-access-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 12px; }
.mcp-access-list li { position: relative; padding-inline-start: 16px; font-size: var(--settings-text-size, 13px); line-height: 1.6; }
.mcp-access-list li::before { content: ''; position: absolute; inset-inline-start: 0; top: .65em; width: 5px; height: 5px; border-radius: 50%; background: var(--color-green); }
.mcp-help { margin: 16px 0; color: var(--color-dim); font-size: 12px; line-height: 1.65; }
.mcp-help summary { cursor: pointer; min-height: 28px; }
.mcp-help p { margin: 6px 0 0; }
.mcp-maintenance-link { font-size: 12px; }
.mcp-create > summary { margin: 0; cursor: pointer; min-height: 24px; }
.mcp-create-body { padding-top: 18px; }
.mcp-list-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 14px; flex-wrap: wrap; }
.mcp-list-header .mcp-heading { margin: 0; }
.mcp-list-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.mcp-list-actions :deep(.app-btn) { min-height: var(--settings-action-height, 34px); height: var(--settings-action-height, 34px); }
.mcp-count { font-size: 12px; font-weight: 400; color: var(--color-dim); padding-inline-start: 5px; }
.mcp-token-list { min-width: 0; margin: 0; padding: 0; border: 0; }
.mcp-load-error { color: var(--color-red); font-size: var(--settings-text-size, 13px); }
@media (min-width: 1000px) { .mcp-intro-grid { grid-template-columns: minmax(0, 1.65fr) minmax(240px, 1fr); } }
@media (max-width: 639px) { .mcp-section { padding: 16px; } .mcp-copy { width: 44px; height: 44px; } .mcp-create > summary { min-height: 44px; display: list-item; align-content: center; } }
</style>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppButton from '../ui/AppButton.vue'
import FormField from '../ui/FormField.vue'
import PlatformProviderIcon from './PlatformProviderIcon.vue'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { useSessionStore } from '../../stores/session'
import { HttpError } from '../../api/http'
import {
  PLATFORM_NAMES, PLATFORM_PROVIDERS, isPlatformProvider, getPlatformSettings, savePlatformVisibility,
  savePlatformApp, reauthenticatePlatform, startPlatformBinding, getPlatformCandidate,
  confirmPlatformBinding, setPlatformEnabled, unbindPlatform, platformErrorKey, safePlatformAuthorize,
  type PlatformProvider, type PlatformSnapshot, type PlatformSettings, type PlatformCandidate, type PlatformMutation,
} from '../../api/platformLogin'

const props = defineProps<{ currentSource?: string }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const confirm = useConfirm()
const toast = useToast()
const session = useSessionStore()
const route = useRoute()
const router = useRouter()
const snapshot = ref<PlatformSnapshot | null>(null)
const loading = ref(true)
const loadError = ref(false)
const busy = ref(false)
const uncertain = ref(false)
const notice = ref('')
const noticeError = ref(false)
const visibility = reactive<Record<PlatformProvider, boolean>>({ github: true, gitlab: true, gitee: true, google: true, microsoft: true })
const visibilityDirty = computed(() => !!snapshot.value && snapshot.value.providers.some(p => visibility[p.provider] !== p.visible))
const providers = computed(() => PLATFORM_PROVIDERS.map(p => snapshot.value?.providers.find(row => row.provider === p)).filter((p): p is PlatformSettings => !!p))
const dialog = ref<HTMLDialogElement | null>(null)
const modal = ref<'app' | 'reauth' | 'candidate' | null>(null)
const selected = ref<PlatformSettings | null>(null)
const candidate = ref<PlatformCandidate | null>(null)
const modalError = ref('')
const currentPassword = ref('')
const passwordInput = ref<HTMLInputElement | null>(null)
const callbackInput = ref<HTMLInputElement | null>(null)
const app = reactive({ clientId: '', clientSecret: '', microsoftTenant: 'common', giteeMode: '' })
const tenantMode = ref('common')
const tenantID = ref('')
const secretRequired = computed(() => !!selected.value && (!selected.value.hasSecret
  || app.clientId !== selected.value.clientId
  || (selected.value.provider === 'microsoft' && effectiveTenant() !== selected.value.microsoftTenant)
  || (selected.value.provider === 'gitee' && app.giteeMode !== selected.value.giteeMode)))
const appDirty = computed(() => !!selected.value && (app.clientId !== selected.value.clientId || !!app.clientSecret || (selected.value.provider === 'microsoft' && effectiveTenant() !== selected.value.microsoftTenant) || (selected.value.provider === 'gitee' && app.giteeMode !== selected.value.giteeMode)))
const readBackNeeded = computed(() => uncertain.value)
let focusBeforeDialog: HTMLElement | null = null
let pendingAction: (() => Promise<void>) | null = null
let controller: AbortController | undefined
let sequence = 0
let mounted = false
const bindingReadFailed = ref(false)

function checkResult() { closeModal(); void load() }
function effectiveTenant() { return tenantMode.value === 'custom' ? tenantID.value : tenantMode.value }
function name(provider: PlatformProvider) { return PLATFORM_NAMES[provider] }
function status(row: PlatformSettings) { return row.enabled ? 'enabled' : !row.configured ? 'unconfigured' : !row.binding ? 'unbound' : 'disabled' }
function say(key: string, error = false) { notice.value = key; noticeError.value = error }
function recent() {
  const until = Date.parse(snapshot.value?.reauthenticatedUntil ?? '')
  return Number.isFinite(until) && Date.now() < until
}
async function load(preserveDraft = true) {
  controller?.abort()
  controller = new AbortController()
  const signal = controller.signal
  const seq = ++sequence
  loading.value = true
  loadError.value = false
  try {
    const data = await getPlatformSettings(signal)
    if (!mounted || signal.aborted || sequence !== seq) return
    if (data.providers.length !== 5 || !PLATFORM_PROVIDERS.every(p => data.providers.some(row => row.provider === p))) throw new Error('Invalid settings')
    const keep = preserveDraft && visibilityDirty.value
    snapshot.value = data
    if (!keep) for (const p of data.providers) visibility[p.provider] = p.visible
    if (uncertain.value) { uncertain.value = false; say('readBackHint') }
    const flow = route.query.platform_binding
    if (typeof flow === 'string') await bindingReturn(flow, seq)
  } catch {
    if (mounted && !signal.aborted && sequence === seq) loadError.value = true
  } finally {
    if (mounted && sequence === seq) loading.value = false
  }
}
async function openModal(kind: 'app' | 'reauth' | 'candidate') {
  modalError.value = ''
  if (!dialog.value?.open) focusBeforeDialog = document.activeElement as HTMLElement | null
  modal.value = kind
  await nextTick()
  if (!mounted || !dialog.value) return
  if (!dialog.value.open) dialog.value.showModal()
  await nextTick()
  if (kind === 'reauth') passwordInput.value?.focus()
  else dialog.value.querySelector<HTMLElement>('button, input')?.focus()
}
function closeModal(dropAction = true) {
  if (busy.value) return
  dialog.value?.close()
  modal.value = null
  modalError.value = ''
  app.clientSecret = ''
  currentPassword.value = ''
  if (dropAction) pendingAction = null
  focusBeforeDialog?.focus()
}
function onCancel(event: Event) { event.preventDefault(); closeModal() }
function configure(row: PlatformSettings) {
  if (busy.value || loading.value) return
  selected.value = { ...row }
  app.clientId = row.clientId
  app.clientSecret = ''
  app.microsoftTenant = row.microsoftTenant || 'common'
  app.giteeMode = row.giteeMode
  const preset = ['common', 'organizations', 'consumers'].includes(app.microsoftTenant)
  tenantMode.value = preset ? app.microsoftTenant : 'custom'
  tenantID.value = preset ? '' : app.microsoftTenant
  void openModal('app')
}
async function withPassword(action: () => Promise<void>) {
  if (busy.value || uncertain.value || loading.value) return
  if (recent()) { await action(); return }
  pendingAction = action
  await openModal('reauth')
}
async function verifyPassword() {
  if (busy.value || !currentPassword.value) return
  busy.value = true
  modalError.value = ''
  try {
    const result = await reauthenticatePlatform(currentPassword.value)
    if (!mounted) return
    if (snapshot.value) snapshot.value.reauthenticatedUntil = result.reauthenticatedUntil
    const action = pendingAction
    pendingAction = null
    busy.value = false
    closeModal(false)
    if (action) await action()
  } catch (e) {
    if (mounted) modalError.value = platformErrorKey(e)
  } finally {
    currentPassword.value = ''
    busy.value = false
  }
}
function applyMutation(result: PlatformMutation) {
  if (snapshot.value) {
    const index = snapshot.value.providers.findIndex(p => p.provider === result.provider.provider)
    if (index >= 0) snapshot.value.providers[index] = result.provider
  }
  if (selected.value?.provider === result.provider.provider) selected.value = result.provider
  emit('changed')
}
async function mutate(action: () => Promise<PlatformMutation>) {
  if (busy.value || uncertain.value || loading.value) return
  busy.value = true
  modalError.value = ''
  notice.value = ''
  try {
    const result = await action()
    if (!mounted) return
    applyMutation(result)
    busy.value = false
    closeModal()
    if (result.sessionRevoked) {
      session.clearSession()
      await router.replace({ name: 'login', query: { redirect: '/settings/account' } })
      return
    }
    toast.success(t('platformLogin.saved'))
  } catch (e) {
    if (!mounted) return
    const key = platformErrorKey(e, true)
    if (key === 'unknownResult') uncertain.value = true
    if (key === 'reauthRequired' && snapshot.value) snapshot.value.reauthenticatedUntil = ''
    if (modal.value === 'app' || modal.value === 'candidate') modalError.value = key
    say(key, true)
  } finally {
    app.clientSecret = ''
    busy.value = false
  }
}
async function saveVisibility() {
  if (!snapshot.value || busy.value || uncertain.value || !visibilityDirty.value) return
  busy.value = true
  notice.value = ''
  try {
    const data = await savePlatformVisibility(snapshot.value.visibilityVersion, { ...visibility })
    if (!mounted) return
    snapshot.value = data
    for (const row of data.providers) visibility[row.provider] = row.visible
    toast.success(t('platformLogin.visibilitySaved'))
  } catch (e) {
    if (!mounted) return
    const key = platformErrorKey(e, true)
    uncertain.value = key === 'unknownResult'
    say(key, true)
  } finally { busy.value = false }
}
async function saveApp() {
  const row = selected.value
  if (!row || busy.value || uncertain.value || !appDirty.value) return
  modalError.value = ''
  if (!app.clientId.trim() || (secretRequired.value && !app.clientSecret)) { modalError.value = 'invalidSettings'; return }
  if (row.provider === 'gitee' && !['confidential_code', 's256'].includes(app.giteeMode)) { modalError.value = 'invalidSettings'; return }
  const tenant = effectiveTenant()
  if (row.provider === 'microsoft' && !['common', 'organizations', 'consumers'].includes(tenant) && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(tenant)) { modalError.value = 'invalidSettings'; return }
  const input = {
    expectedVersion: row.configVersion, clientId: app.clientId,
    ...(app.clientSecret ? { clientSecret: app.clientSecret } : {}),
    ...(row.provider === 'microsoft' ? { microsoftTenant: tenant.toLowerCase() } : {}),
    ...(row.provider === 'gitee' ? { giteeMode: app.giteeMode } : {}),
  }
  app.clientSecret = ''
  await withPassword(() => mutate(() => savePlatformApp(row.provider, input)))
}
async function beginBinding() {
  const row = selected.value
  if (!row || busy.value || uncertain.value || !row.configured) return
  closeModal()
  await withPassword(async () => {
    busy.value = true
    try {
      const result = await startPlatformBinding(row.provider, row.configVersion)
      if (!mounted) return
      const url = safePlatformAuthorize(row.provider, result.authorizeURL)
      if (!url) { say('authFailed', true); return }
      window.location.assign(url)
    } catch (e) {
      if (mounted) {
        const key = platformErrorKey(e, true)
        uncertain.value = key === 'unknownResult'
        if (key === 'reauthRequired' && snapshot.value) snapshot.value.reauthenticatedUntil = ''
        say(key, true)
      }
    } finally { busy.value = false }
  })
}
async function enabledChange() {
  const row = selected.value
  if (!row || busy.value || uncertain.value) return
  closeModal()
  if (row.enabled) {
    const accepted = await confirm.open({ title: t('platformLogin.disableTitle', { provider: name(row.provider) }), body: t('platformLogin.disableBody'), confirmLabel: t('platformLogin.disable'), variant: 'danger' })
    if (!accepted || !mounted) return
  }
  await withPassword(() => mutate(() => setPlatformEnabled(row.provider, row.configVersion, !row.enabled)))
}
async function unlink() {
  const row = selected.value
  if (!row || !row.binding || busy.value || uncertain.value) return
  closeModal()
  const accepted = await confirm.open({ title: t('platformLogin.unbindTitle', { provider: name(row.provider) }), body: t('platformLogin.unbindBody'), confirmLabel: t('platformLogin.unbind'), variant: 'danger' })
  if (!accepted || !mounted) return
  await withPassword(() => mutate(() => unbindPlatform(row.provider, row.configVersion)))
}
async function confirmBinding() {
  const value = candidate.value
  if (!value || busy.value || uncertain.value) return
  if (Date.parse(value.expiresAt) <= Date.now()) { modalError.value = 'bindingExpired'; return }
  await withPassword(() => mutate(() => confirmPlatformBinding(value.provider, value.configVersion, value.flowId)))
}
async function bindingReturn(flowId: string, seq: number) {
  bindingReadFailed.value = false
  try {
    const value = await getPlatformCandidate(flowId)
    if (!mounted || sequence !== seq) return
    if (!isPlatformProvider(value.provider)) throw new Error('Invalid candidate')
    const query = { ...route.query }
    delete query.platform_binding
    await router.replace({ path: route.path, query })
    if (!mounted || sequence !== seq) return
    say('')
    candidate.value = value
    selected.value = snapshot.value?.providers.find(p => p.provider === value.provider) ?? null
    await openModal('candidate')
  } catch (e) { if (mounted && sequence === seq) { bindingReadFailed.value = true; say(e instanceof HttpError && (e.status === 0 || e.status >= 500) ? 'candidateLoadFailed' : platformErrorKey(e), true) } }
}
async function copyCallback() {
  const url = selected.value?.callbackURL
  if (!url) return
  try { await navigator.clipboard.writeText(url); toast.success(t('platformLogin.copied')) }
  catch { modalError.value = 'copyFailed'; await nextTick(); callbackInput.value?.focus(); callbackInput.value?.select() }
}
function bindingTime(raw: string) {
  const value = new Date(raw)
  return Number.isNaN(value.getTime()) ? '' : value.toLocaleString(locale.value)
}
onMounted(async () => {
  mounted = true
  await load(false)
})
onBeforeUnmount(() => {
  mounted = false
  sequence++
  controller?.abort()
  pendingAction = null
  currentPassword.value = ''
  app.clientSecret = ''
  dialog.value?.close()
})
</script>

<template>
  <section class="platform-settings" aria-labelledby="platform-settings-heading">
    <header class="platform-settings__head">
      <div><h2 id="platform-settings-heading">{{ t('platformLogin.section') }}</h2><p>{{ t('platformLogin.sectionHint') }}</p></div>
      <AppButton variant="ghost" :disabled="loading || busy" @click="load()">{{ t('platformLogin.reload') }}</AppButton>
    </header>
    <p v-if="loading && !snapshot" class="platform-feedback" role="status">{{ t('platformLogin.loading') }}</p>
    <p v-if="loadError" class="platform-feedback platform-feedback--error" role="alert">
      {{ t('platformLogin.loadFailed') }}
      <button type="button" class="platform-text-button" :disabled="busy || loading" @click="load()">{{ t('platformLogin.retry') }}</button>
    </p>
    <template v-if="snapshot">
      <div class="platform-rows" :aria-busy="loading || undefined">
        <div v-for="row in providers" :key="row.provider" class="platform-row" :data-provider="row.provider">
          <div class="platform-row__identity">
            <div class="platform-mark" aria-hidden="true"><PlatformProviderIcon :provider="row.provider" /></div>
            <div class="platform-row__text">
              <h3>{{ name(row.provider) }}</h3>
              <p class="platform-row__summary"><span :class="{ 'platform-state--enabled': row.enabled }">{{ t('platformLogin.' + status(row)) }}</span><span v-if="row.binding" class="platform-account" :title="row.binding.displayLabel"> · {{ row.binding.displayLabel }}</span></p>
            </div>
          </div>
          <div class="platform-visibility">
            <button :id="'platform-visible-' + row.provider" type="button" role="switch" class="platform-switch"
              :class="{ 'platform-switch--on': visibility[row.provider] }" :aria-checked="visibility[row.provider]"
              :aria-labelledby="'platform-label-' + row.provider" :disabled="busy || readBackNeeded"
              @click="visibility[row.provider] = !visibility[row.provider]"><span aria-hidden="true" /></button>
            <label :id="'platform-label-' + row.provider" :for="'platform-visible-' + row.provider">{{ t('platformLogin.visible') }}<span class="sr-only"> · {{ name(row.provider) }}</span></label>
          </div>
          <AppButton class="platform-configure" :disabled="busy || readBackNeeded || loading" @click="configure(row)">{{ t('platformLogin.configure') }}</AppButton>
        </div>
      </div>
      <div class="platform-settings__footer">
        <p>{{ visibilityDirty ? t('platformLogin.unsaved') : t('platformLogin.passwordHint') }}</p>
        <AppButton variant="primary" :disabled="!visibilityDirty || readBackNeeded || loading" :loading="busy && !modal" @click="saveVisibility">{{ t('platformLogin.saveVisibility') }}</AppButton>
      </div>
    </template>
    <p v-if="notice" class="platform-feedback" :class="{ 'platform-feedback--error': noticeError }" :role="modal ? undefined : noticeError ? 'alert' : 'status'">
      {{ t('platformLogin.' + notice) }}
      <button v-if="readBackNeeded || notice === 'conflict' || bindingReadFailed" type="button" class="platform-text-button" :disabled="busy || loading" @click="load()">{{ t('platformLogin.reload') }}</button>
    </p>

    <Teleport to="body">
      <dialog ref="dialog" class="platform-dialog settings-dialog" aria-labelledby="platform-dialog-heading" @cancel="onCancel">
        <template v-if="modal === 'app' && selected">
          <header class="platform-dialog__head">
            <div class="platform-dialog__title"><PlatformProviderIcon :provider="selected.provider" /><h2 id="platform-dialog-heading">{{ t('platformLogin.appTitle', { provider: name(selected.provider) }) }}</h2></div>
            <AppButton variant="ghost" :disabled="busy" @click="closeModal()">{{ t('platformLogin.close') }}</AppButton>
          </header>
          <form class="platform-app-form" novalidate @submit.prevent="saveApp">
            <FormField :label="t('platformLogin.clientId')" field-id="platform-client-id" required>
              <input id="platform-client-id" v-model="app.clientId" class="platform-input" maxlength="2048" autocomplete="off" :disabled="busy || readBackNeeded" />
            </FormField>
            <FormField :label="t('platformLogin.clientSecret')" field-id="platform-client-secret" :required="secretRequired" :hint="secretRequired ? t('platformLogin.secretRequired') : t('platformLogin.secretKept')">
              <input id="platform-client-secret" v-model="app.clientSecret" class="platform-input" type="password" autocomplete="new-password" maxlength="8192" :disabled="busy || readBackNeeded" />
            </FormField>
            <FormField v-if="selected.provider === 'microsoft'" :label="t('platformLogin.tenant')" field-id="platform-tenant-mode" :hint="t('platformLogin.tenantHint')">
              <select id="platform-tenant-mode" v-model="tenantMode" class="platform-input" :disabled="busy || readBackNeeded">
                <option value="common">{{ t('platformLogin.allAccounts') }}</option><option value="organizations">{{ t('platformLogin.organizations') }}</option><option value="consumers">{{ t('platformLogin.personalAccounts') }}</option><option value="custom">{{ t('platformLogin.specificTenant') }}</option>
              </select>
            </FormField>
            <FormField v-if="selected.provider === 'microsoft' && tenantMode === 'custom'" :label="t('platformLogin.tenantId')" field-id="platform-tenant-id" required>
              <input id="platform-tenant-id" v-model="tenantID" class="platform-input mono" maxlength="36" autocomplete="off" :disabled="busy || readBackNeeded" />
            </FormField>
            <FormField v-if="selected.provider === 'gitee'" :label="t('platformLogin.giteeMode')" field-id="platform-gitee-mode" :hint="t('platformLogin.giteeHint')" required>
              <select id="platform-gitee-mode" v-model="app.giteeMode" class="platform-input" :disabled="busy || readBackNeeded"><option disabled value="">{{ t('platformLogin.choose') }}</option><option value="confidential_code">{{ t('platformLogin.giteeConfidential') }}</option><option value="s256">{{ t('platformLogin.giteeS256') }}</option></select>
            </FormField>
            <FormField v-if="selected.callbackURL" :label="t('platformLogin.callback')" field-id="platform-callback">
              <div class="platform-copy-field"><input id="platform-callback" ref="callbackInput" class="platform-input mono" :value="selected.callbackURL" readonly @focus="callbackInput?.select()" /><AppButton @click="copyCallback">{{ t('platformLogin.copy') }}</AppButton></div>
            </FormField>
            <p v-else class="platform-help">{{ t('platformLogin.publicURLHint') }}</p>
            <p v-if="secretRequired && selected.hasSecret" class="platform-help">{{ t('platformLogin.appChangeHint') }}</p>
            <p v-if="props.currentSource === selected.provider" class="platform-help platform-help--strong">{{ t('platformLogin.currentSessionWarning') }}</p>
            <p v-if="modalError" class="platform-feedback platform-feedback--error" role="alert">{{ t('platformLogin.' + modalError) }}</p>
            <div class="platform-dialog__actions">
              <AppButton v-if="readBackNeeded || modalError === 'conflict'" :disabled="busy" @click="checkResult">{{ t('platformLogin.reload') }}</AppButton>
              <AppButton :disabled="busy" @click="closeModal()">{{ t('platformLogin.cancel') }}</AppButton>
              <AppButton variant="primary" type="submit" :loading="busy" :disabled="readBackNeeded || !appDirty">{{ t('platformLogin.save') }}</AppButton>
            </div>
          </form>
          <section v-if="selected.configured || selected.binding" class="platform-binding" aria-labelledby="platform-binding-heading">
            <div class="platform-binding__heading"><h3 id="platform-binding-heading">{{ t('platformLogin.identity') }}</h3><span>{{ t('platformLogin.' + status(selected)) }}</span></div>
            <p class="platform-binding__account">{{ selected.binding?.displayLabel ?? t('platformLogin.unbound') }}</p>
            <details v-if="selected.binding" class="platform-binding__details"><summary>{{ t('platformLogin.details') }}</summary><dl><dt>Issuer</dt><dd>{{ selected.binding.issuer }}</dd><dt>Subject</dt><dd>{{ selected.binding.subject }}</dd></dl></details>
            <p class="platform-help">{{ t('platformLogin.sourceSessionHint') }}</p>
            <p v-if="appDirty" class="platform-help">{{ t('platformLogin.saveBeforeBinding') }}</p>
            <div class="platform-binding__actions">
              <AppButton :disabled="busy || readBackNeeded || !selected.configured || appDirty" @click="beginBinding">{{ t('platformLogin.bind') }}</AppButton>
              <AppButton v-if="selected.binding" :disabled="busy || readBackNeeded || appDirty" @click="enabledChange">{{ t('platformLogin.' + (selected.enabled ? 'disable' : 'enable')) }}</AppButton>
              <AppButton v-if="selected.binding" variant="danger" :disabled="busy || readBackNeeded || appDirty" @click="unlink">{{ t('platformLogin.unbind') }}</AppButton>
            </div>
          </section>
        </template>
        <template v-else-if="modal === 'reauth'">
          <header class="platform-dialog__head"><h2 id="platform-dialog-heading">{{ t('platformLogin.reauthTitle') }}</h2><AppButton variant="ghost" :disabled="busy" @click="closeModal()">{{ t('platformLogin.close') }}</AppButton></header>
          <form class="platform-app-form" @submit.prevent="verifyPassword">
            <p class="platform-help">{{ t('platformLogin.reauthHint') }}</p>
            <FormField :label="t('platformLogin.currentPassword')" field-id="platform-current-password" :error="modalError ? t('platformLogin.' + modalError) : undefined" required>
              <input id="platform-current-password" ref="passwordInput" v-model="currentPassword" class="platform-input" type="password" autocomplete="current-password" :disabled="busy" :aria-invalid="!!modalError || undefined" :aria-describedby="modalError ? 'platform-current-password-err' : undefined" />
            </FormField>
            <div class="platform-dialog__actions"><AppButton :disabled="busy" @click="closeModal()">{{ t('platformLogin.cancel') }}</AppButton><AppButton variant="primary" type="submit" :loading="busy" :disabled="!currentPassword">{{ t('platformLogin.verify') }}</AppButton></div>
          </form>
        </template>
        <template v-else-if="modal === 'candidate' && candidate">
          <header class="platform-dialog__head"><h2 id="platform-dialog-heading">{{ t('platformLogin.bindingTitle', { provider: name(candidate.provider) }) }}</h2><AppButton variant="ghost" :disabled="busy" @click="closeModal()">{{ t('platformLogin.close') }}</AppButton></header>
          <div class="platform-app-form">
            <p class="platform-help">{{ t('platformLogin.bindingHint') }}</p>
            <p v-if="props.currentSource === candidate.provider" class="platform-help platform-help--strong">{{ t('platformLogin.currentSessionWarning') }}</p>
            <div class="platform-candidate"><PlatformProviderIcon :provider="candidate.provider" /><strong>{{ candidate.displayLabel }}</strong></div>
            <p class="platform-help">{{ t('platformLogin.candidateExpires', { time: bindingTime(candidate.expiresAt) }) }}</p>
            <p v-if="modalError" class="platform-feedback platform-feedback--error" role="alert">{{ t('platformLogin.' + modalError) }}</p>
            <div class="platform-dialog__actions"><AppButton v-if="readBackNeeded" :disabled="busy" @click="checkResult">{{ t('platformLogin.reload') }}</AppButton><AppButton :disabled="busy" @click="closeModal()">{{ t('platformLogin.cancel') }}</AppButton><AppButton variant="primary" :loading="busy" :disabled="readBackNeeded" @click="confirmBinding">{{ t('platformLogin.confirmBind') }}</AppButton></div>
          </div>
        </template>
      </dialog>
    </Teleport>
  </section>
</template>

<style scoped>
.platform-settings { min-width: 0; border: 1px solid var(--color-border); border-radius: var(--rounded-lg); background: var(--color-card); padding: var(--settings-panel-padding, 18px); }
.platform-settings__head { display: flex; justify-content: space-between; align-items: flex-start; flex-wrap: wrap; gap: 12px; padding-bottom: 12px; }
.platform-settings__head h2 { margin: 0; font-size: var(--settings-panel-title-size, 14px); font-weight: 600; }
.platform-settings__head p { max-width: 58ch; margin: 5px 0 0; color: var(--color-dim); font-size: 12px; line-height: 1.5; }
.platform-rows { border-top: 1px solid var(--color-border); }
.platform-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px 12px; padding: 13px 0; border-bottom: 1px solid var(--color-border); align-items: center; }
.platform-row__identity { display: flex; align-items: center; gap: 10px; min-width: 0; }
.platform-mark { width: 34px; height: 34px; flex: none; display: grid; place-items: center; color: var(--color-text); background: var(--color-inset); border: 1px solid var(--color-border-strong); border-radius: 9px; }
.platform-row__text { min-width: 0; }
.platform-row h3 { margin: 0; font-size: 13px; line-height: 1.5; font-weight: 600; }
.platform-row__summary { display: flex; margin: 3px 0 0; min-width: 0; font-size: 12px; line-height: 1.4; color: var(--color-dim); }
.platform-row__summary>span:first-child { flex: none; }
.platform-state--enabled { color: var(--color-green); }
.platform-account { text-overflow: ellipsis; white-space: nowrap; overflow: hidden; }
.platform-visibility { display: flex; align-items: center; gap: 5px; grid-row: 2; grid-column: 1; min-width: 0; }
.platform-visibility label { color: var(--color-dim); font-size: 12px; line-height: 1.4; cursor: pointer; overflow-wrap: anywhere; }
.platform-switch { display: grid; place-items: center; position: relative; flex: none; width: 44px; height: 44px; border: 0; background: none; border-radius: 8px; cursor: pointer; }
.platform-switch::before { content: ''; position: absolute; width: 28px; height: 16px; border-radius: 20px; background: var(--color-border-strong); }
.platform-switch>span { width: 12px; height: 12px; border-radius: 50%; background: var(--color-text); position: relative; transform: translateX(-6px); transition: transform .15s; }
.platform-switch--on::before { background: var(--color-primary); }
.platform-switch--on>span { background: #fff; transform: translateX(6px); }
.platform-switch:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.platform-switch:disabled { opacity: .5; cursor: not-allowed; }
.platform-configure { grid-row: 1 / span 2; grid-column: 2; }
.platform-settings__footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; padding-top: 14px; }
.platform-settings__footer p { color: var(--color-dim); font-size: 12px; margin: 0; line-height: 1.5; }
.platform-feedback { margin: 12px 0 0; font-size: 13px; line-height: 1.5; color: var(--color-dim); overflow-wrap: anywhere; }
.platform-feedback--error { color: var(--color-red); }
.platform-text-button { display: inline; background: none; border: 0; color: var(--color-primary); font: inherit; padding: 4px; cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
.platform-dialog { position: fixed; inset: 0; box-sizing: border-box; margin: auto; width: min(540px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); padding: 0; overflow-y: auto; overscroll-behavior: contain; background: var(--color-card); color: var(--color-text); border: 1px solid var(--color-border-strong); border-radius: var(--rounded-lg, 14px); box-shadow: var(--shadow-modal); font-family: var(--font-sans); }
.platform-dialog::backdrop { background: rgb(0 0 0 / .55); backdrop-filter: blur(3px); }
.platform-dialog__head { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 16px 18px; border-bottom: 1px solid var(--color-border); }
.platform-dialog__head h2 { margin: 0; font-size: 15px; line-height: 1.5; font-weight: 600; overflow-wrap: anywhere; }
.platform-dialog__title { display: flex; align-items: center; min-width: 0; gap: 9px; }
.platform-dialog__head .app-btn { flex: none; }
.platform-app-form { display: flex; flex-direction: column; gap: 15px; padding: 18px; }
.platform-input { width: 100%; min-width: 0; min-height: var(--settings-control-height, 36px); box-sizing: border-box; padding: 8px 10px; font-family: var(--font-sans); font-size: 13px; line-height: 1.4; color: var(--color-text); background: var(--color-inset); border: 1px solid var(--color-border-strong); border-radius: var(--rounded, 8px); }
.platform-input:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.platform-input:disabled { opacity: .6; }
.platform-input[readonly] { color: var(--color-dim); }
.platform-copy-field { display: flex; gap: 7px; min-width: 0; align-items: center; }
.platform-copy-field .platform-input { flex: 1; }
.platform-copy-field .app-btn { flex: none; }
.platform-help { margin: 0; color: var(--color-dim); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.platform-help--strong { color: var(--color-text); }
.platform-dialog__actions { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; padding-top: 5px; }
.platform-binding { border-top: 1px solid var(--color-border); padding: 16px 18px 18px; }
.platform-binding__heading { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 8px; align-items: center; }
.platform-binding__heading h3 { font-size: 13px; margin: 0; font-weight: 600; }
.platform-binding__heading>span { color: var(--color-dim); font-size: 12px; }
.platform-binding__account { font-size: 14px; margin: 10px 0; font-weight: 600; overflow-wrap: anywhere; }
.platform-binding__details { color: var(--color-dim); font-size: 12px; margin: 8px 0 12px; }
.platform-binding__details summary { cursor: pointer; }
.platform-binding__details dl { display: grid; grid-template-columns: 55px minmax(0,1fr); gap: 7px 10px; padding: 10px 0 0; }
.platform-binding__details dd { margin: 0; font-family: var(--font-mono); overflow-wrap: anywhere; }
.platform-binding__actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
.platform-candidate { display: flex; align-items: center; gap: 12px; padding: 15px; border: 1px solid var(--color-border-strong); border-radius: var(--rounded); background: var(--color-inset); }
.platform-candidate strong { font-size: 17px; overflow-wrap: anywhere; min-width: 0; }
.mono { font-family: var(--font-mono); }
@media (min-width: 760px) { .platform-row { grid-template-columns: minmax(0,1fr) auto auto; gap: 14px; min-height: 62px; padding: 10px 0; } .platform-visibility { grid-row: 1; grid-column: 2; max-width: 190px; } .platform-switch { width: 34px; height: 34px; } .platform-configure { grid-row: 1; grid-column: 3; } }
@media (max-width: 639px) { .platform-dialog .platform-input { font-size: 16px; min-height: 44px; } .platform-settings .app-btn, .platform-dialog .app-btn, .platform-text-button { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .platform-switch>span { transition: none; } }
</style>

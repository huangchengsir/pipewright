<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useSessionStore } from '../../stores/session'
import { onboardingLoginTarget } from '../../composables/useOnboarding'
import {
  PLATFORM_NAMES, getPublicPlatforms, startPlatformLogin, platformErrorKey,
  safePlatformAuthorize, safePlatformReturn, rememberPlatformReturn, consumePlatformReturn,
  type PublicPlatform, type PlatformProvider,
} from '../../api/platformLogin'
import PlatformProviderIcon from './PlatformProviderIcon.vue'

const emit = defineEmits<{ space: [height: number] }>()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const root = ref<HTMLElement | null>(null)
const providers = ref<PublicPlatform[]>([])
const loading = ref(true)
const loadError = ref(false)
const note = ref('')
const starting = ref<PlatformProvider | null>(null)
const visible = computed(() => providers.value.filter(p => p.visible))
let controller: AbortController | undefined
let generation = 0
let mounted = false
let observer: ResizeObserver | undefined
let observed: HTMLElement | null = null

async function measure() {
  await nextTick()
  if (!mounted) return
  if (observed !== root.value) {
    if (observed) observer?.unobserve(observed)
    observed = root.value
    if (observed) observer?.observe(observed)
  }
  emit('space', root.value?.offsetHeight ?? 0)
}
async function refresh() {
  controller?.abort()
  const seq = ++generation
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true
  loadError.value = false
  try {
    const data = await getPublicPlatforms(signal)
    if (mounted && seq === generation && !signal.aborted) providers.value = data
  } catch {
    if (mounted && seq === generation && !signal.aborted) loadError.value = true
  } finally {
    if (mounted && seq === generation) loading.value = false
  }
}
function focusRefresh() { if (!document.hidden) void refresh() }
async function start(item: PublicPlatform) {
  if (starting.value) return
  note.value = ''
  if (!item.available) {
    note.value = item.reason === 'not_configured' ? 'notConfigured' : 'unavailable'
    return
  }
  starting.value = item.provider
  try {
    const target = safePlatformReturn(route.query.redirect)
    const result = await startPlatformLogin(item.provider, target ?? '')
    if (!mounted) return
    const url = safePlatformAuthorize(item.provider, result.authorizeURL)
    if (!url) { note.value = 'authFailed'; return }
    rememberPlatformReturn(target)
    window.location.assign(url)
  } catch (e) {
    if (mounted) note.value = platformErrorKey(e)
  } finally {
    if (mounted) starting.value = null
  }
}
async function completeReturn() {
  const result = route.query.platform_result
  if (typeof result !== 'string') return
  const saved = consumePlatformReturn()
  const returned = safePlatformReturn(route.query.returnTo)
  const redirect = saved ?? safePlatformReturn(route.query.redirect)
  await router.replace({ name: 'login', query: redirect ? { redirect } : {} })
  if (!mounted) return
  if (result === 'success') {
    const checked = await session.ensureSession(true)
    if (!mounted) return
    if (checked.kind === 'ok') {
      const target = saved ?? (returned === '/' ? undefined : returned ?? undefined)
      await router.replace(await onboardingLoginTarget(target))
    } else note.value = checked.kind === 'error' ? 'unavailable' : 'authFailed'
    return
  }
  const messages: Record<string, string> = {
    cancelled: 'canceled', expired: 'expired', denied: 'notAuthorized',
    uncertain: 'expired', unavailable: 'unavailable', invalid: 'expired',
  }
  note.value = messages[result] ?? 'authFailed'
}
watch([visible, loading, loadError, note], () => { void measure() }, { flush: 'post' })
onMounted(() => {
  mounted = true
  observer = new ResizeObserver(() => { emit('space', root.value?.offsetHeight ?? 0) })
  window.addEventListener('focus', focusRefresh)
  void refresh()
  void completeReturn()
  void measure()
})
onBeforeUnmount(() => {
  mounted = false
  generation++
  controller?.abort()
  observer?.disconnect()
  window.removeEventListener('focus', focusRefresh)
  emit('space', 0)
})
</script>

<template>
  <div v-if="visible.length || loading || loadError || note" ref="root" class="platform-entry">
    <template v-if="visible.length">
      <div class="platform-divider"><span>{{ t('platformLogin.alternate') }}</span></div>
      <div class="platform-options" :style="{ gridTemplateColumns: 'repeat(' + visible.length + ', minmax(0, 1fr))' }" role="group" :aria-label="t('platformLogin.alternate')">
        <button v-for="item in visible" :key="item.provider" type="button" class="platform-button"
          :data-provider="item.provider" :disabled="!!starting"
          :aria-busy="starting === item.provider || undefined"
          :aria-label="t('platformLogin.providerLogin', { provider: PLATFORM_NAMES[item.provider] })"
          :title="t('platformLogin.providerLogin', { provider: PLATFORM_NAMES[item.provider] })" @click="start(item)">
          <span v-if="starting === item.provider" class="platform-spinner" aria-hidden="true" />
          <PlatformProviderIcon v-else :provider="item.provider" />
          <span>{{ PLATFORM_NAMES[item.provider] }}</span>
        </button>
      </div>
    </template>
    <p v-if="loading && !providers.length" class="platform-status" role="status">{{ t('platformLogin.loading') }}</p>
    <p v-if="loadError" class="platform-status" role="status">
      {{ t('platformLogin.loadFailed') }}
      <button type="button" class="platform-link" @click="refresh">{{ t('platformLogin.retry') }}</button>
    </p>
    <p v-if="note" class="platform-note" role="status">
      <span>{{ t('platformLogin.' + note) }}</span>
      <button type="button" class="platform-dismiss" :aria-label="t('platformLogin.close')" @click="note = ''">×</button>
    </p>
  </div>
</template>

<style scoped>
.platform-entry { position: absolute; top: calc(100% + 14px); left: 0; right: 0; color: var(--lp-text, #eef4ff); }
.platform-divider { display: flex; align-items: center; gap: 12px; color: var(--lp-dim, #aebbd6); font: 400 11px/16px var(--font-sans); margin-bottom: 8px; white-space: nowrap; }
.platform-divider::before, .platform-divider::after { content: ''; height: 1px; flex: 1; background: rgba(174,187,214,.2); }
.platform-options { display: grid; gap: 8px; }
.platform-button { min-width: 0; width: 100%; min-height: 48px; padding: 4px 3px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 3px; border: 1px solid rgba(174,187,214,.26); border-radius: 12px; background: rgba(13,22,42,.68); backdrop-filter: blur(20px); color: var(--lp-text, #eef4ff); font: 500 11px/16px var(--font-sans); cursor: pointer; transition: background .18s, border-color .18s; }
.platform-button:hover:not(:disabled) { background: rgba(25,40,64,.8); border-color: rgba(174,187,214,.48); }
.platform-button:active:not(:disabled) { background: rgba(20,33,54,.85); }
.platform-button:focus-visible, .platform-link:focus-visible, .platform-dismiss:focus-visible { outline: 2px solid #60a5fa; outline-offset: 3px; }
.platform-button:disabled { cursor: wait; }
.platform-status { margin: 8px 0 0; text-align: center; color: var(--lp-dim, #aebbd6); font-size: 12px; line-height: 1.5; }
.platform-link { border: 0; background: transparent; color: #93c5fd; font: inherit; cursor: pointer; padding: 3px 5px; text-decoration: underline; text-underline-offset: 3px; }
.platform-note { display: flex; align-items: flex-start; gap: 8px; margin: 10px 0 0; padding: 9px 10px; border: 1px solid rgba(174,187,214,.25); border-radius: 10px; background: #102039; color: #e2eaff; font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
.platform-note>span { flex: 1; }
.platform-dismiss { width: 24px; min-width: 24px; height: 24px; display: grid; place-items: center; margin: -3px -4px 0 0; border: 0; background: transparent; color: #c0cee4; font-size: 18px; cursor: pointer; }
.platform-spinner { width: 18px; height: 18px; border: 2px solid rgba(174,187,214,.3); border-top-color: #eef4ff; border-radius: 50%; animation: platform-spin .7s linear infinite; }
@keyframes platform-spin { to { transform: rotate(360deg); } }
@media (max-width: 520px) { .platform-button { min-height: 44px; } .platform-button>span:not(.platform-spinner) { display: none; } .platform-status, .platform-note { font-size: 13px; } .platform-dismiss { min-width: 44px; height: 44px; } .platform-link { min-height: 44px; } }
@media (prefers-reduced-motion: reduce) { .platform-button { transition: none; } .platform-spinner { animation: none; } }
</style>

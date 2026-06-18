<script setup lang="ts">
/*
  InstantSubdomain.vue — 「一键分配子域名」(R3 / E3.3-E3.4 · 零 DNS 的招牌体验)。

  反代面板顶部的醒目入口:点开后选 DNS 提供商(显示其根域)+ 上游容器/端口,
  点「分配」→ 后端在该根域下铸造 app-xxxx 子域、写 A 记录、绑路由,一步到位。
  成功后进入庆祝态:大字展示生成的子域 + 实时可点链接(https://…)。

  本组件只负责发起分配与展示结果;serverId 由父(单机面板)提供。分配成功后
  emit('allocated', 新路由) 让父把新路由插进列表。
*/
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { NIcon } from 'naive-ui'
import { Wand, World, ExternalLink, X, Confetti, ArrowRight } from '@vicons/tabler'
import { allocateSubdomain, type ProxyRoute } from '../../api/reverseProxy'
import { listDnsProviders, type DnsProvider } from '../../api/dnsProviders'
import type { ContainerInfo } from '../../api/containers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  serverId: string
  /** Containers on this host, for the upstream selector + default-port inference. */
  containers: ContainerInfo[]
}>()

const emit = defineEmits<{
  /** 分配成功:把新路由交给父插入列表。 */
  (e: 'allocated', route: ProxyRoute): void
}>()

const { t } = useI18n()
const toast = useToast()

const open = ref(false)

// ─── DNS 提供商 ────────────────────────────────────────────────────────────────
const providers = ref<DnsProvider[]>([])
const providersLoaded = ref(false)
async function loadProviders(): Promise<void> {
  try {
    providers.value = await listDnsProviders()
  } catch {
    providers.value = []
  } finally {
    providersLoaded.value = true
  }
}
onMounted(loadProviders)

const hasProviders = computed(() => providers.value.length > 0)

// ─── 表单 ──────────────────────────────────────────────────────────────────────
const providerId = ref('')
const upstreamContainer = ref('')
const upstreamPort = ref('')
const allocating = ref(false)

const selectedProvider = computed(() => providers.value.find((p) => p.id === providerId.value))

const runningContainers = computed(() =>
  props.containers.filter((c) => c.state === 'running').map((c) => c.names),
)
const hasContainerChoices = computed(() => runningContainers.value.length > 0)

function inferPort(containerName: string): string {
  const c = props.containers.find((x) => x.names === containerName)
  if (!c || !c.ports) return ''
  const m = c.ports.match(/->(\d+)/)
  if (m) return m[1]
  const any = c.ports.match(/(\d+)\/(?:tcp|udp)/)
  return any ? any[1] : ''
}
function onContainerPick(): void {
  if (upstreamContainer.value && !upstreamPort.value.trim()) {
    const p = inferPort(upstreamContainer.value)
    if (p) upstreamPort.value = p
  }
}

const portNum = computed(() => Number.parseInt(upstreamPort.value.trim(), 10))
const portValid = computed(
  () => Number.isInteger(portNum.value) && portNum.value >= 1 && portNum.value <= 65535,
)
const canAllocate = computed(
  () =>
    !allocating.value &&
    providerId.value.length > 0 &&
    upstreamContainer.value.trim().length > 0 &&
    portValid.value,
)

// ─── 结果(庆祝态) ───────────────────────────────────────────────────────────────
const result = ref<ProxyRoute | null>(null)

function openModal(): void {
  providerId.value = providers.value.length === 1 ? providers.value[0].id : ''
  upstreamContainer.value = ''
  upstreamPort.value = ''
  result.value = null
  open.value = true
}
function closeModal(): void {
  if (allocating.value) return
  open.value = false
  result.value = null
}

async function allocate(): Promise<void> {
  if (!canAllocate.value) return
  allocating.value = true
  try {
    const route = await allocateSubdomain({
      providerId: providerId.value,
      serverId: props.serverId,
      upstreamContainer: upstreamContainer.value.trim(),
      upstreamPort: portNum.value,
    })
    result.value = route
    emit('allocated', route)
    toast.success(t('reverseProxy.sub.allocated'), { detail: route.domain })
  } catch (err) {
    toast.error(t('reverseProxy.sub.allocateFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('reverseProxy.errReq', { status: err.status }))
          : t('reverseProxy.errNetwork'),
    })
  } finally {
    allocating.value = false
  }
}
</script>

<template>
  <div class="isub">
    <!-- 招牌入口 -->
    <button class="isub__cta" :title="t('reverseProxy.sub.title')" @click="openModal">
      <span class="isub__cta-ic"><NIcon :size="16"><Wand /></NIcon></span>
      {{ t('reverseProxy.sub.cta') }}
    </button>
  </div>

  <Teleport to="body">
    <div
      v-if="open"
      class="modal-scrim"
      role="dialog"
      :aria-label="t('reverseProxy.sub.title')"
      aria-modal="true"
      @keydown.esc="closeModal"
      @click.self="closeModal"
    >
      <div class="modal" :class="{ 'modal--success': result }">
        <button class="modal-close" :aria-label="t('reverseProxy.sub.cancel')" :disabled="allocating" @click="closeModal">
          <NIcon :size="15"><X /></NIcon>
        </button>

        <!-- ── 庆祝态 ── -->
        <template v-if="result">
          <div class="celebrate">
            <div class="celebrate__burst" aria-hidden="true"><NIcon :size="30"><Confetti /></NIcon></div>
            <h3 class="celebrate__title">{{ t('reverseProxy.sub.successTitle') }}</h3>
            <p class="celebrate__lede">{{ t('reverseProxy.sub.successLede') }}</p>
            <a
              class="celebrate__domain mono"
              :href="`https://${result.domain}`"
              target="_blank"
              rel="noopener noreferrer"
            >
              {{ result.domain }}
              <NIcon :size="15" class="celebrate__ext"><ExternalLink /></NIcon>
            </a>
            <div class="celebrate__flow mono">
              <span>{{ t('reverseProxy.proxiesTo') }}</span>
              <NIcon :size="12"><ArrowRight /></NIcon>
              <span class="celebrate__chip">{{ result.upstreamContainer }}:{{ result.upstreamPort }}</span>
            </div>
            <div class="celebrate__actions">
              <a
                class="btn-primary"
                :href="`https://${result.domain}`"
                target="_blank"
                rel="noopener noreferrer"
              >
                <NIcon :size="14"><ExternalLink /></NIcon>
                {{ t('reverseProxy.sub.visit') }}
              </a>
              <button class="btn-secondary" @click="closeModal">{{ t('reverseProxy.sub.done') }}</button>
            </div>
          </div>
        </template>

        <!-- ── 表单态 ── -->
        <template v-else>
          <div class="modal-head">
            <div class="modal-icon" aria-hidden="true"><NIcon :size="18"><Wand /></NIcon></div>
            <div>
              <h3 class="modal-title">{{ t('reverseProxy.sub.title') }}</h3>
              <p class="modal-sub">{{ t('reverseProxy.sub.lede') }}</p>
            </div>
          </div>

          <!-- 无提供商:引导去配置 -->
          <div v-if="providersLoaded && !hasProviders" class="noprov">
            <NIcon :size="18" class="noprov__ic"><World /></NIcon>
            <p class="noprov__txt">{{ t('reverseProxy.sub.noProvider') }}</p>
            <RouterLink to="/settings/dns-providers" class="noprov__link">
              {{ t('reverseProxy.sub.manageProviders') }}
            </RouterLink>
          </div>

          <form v-else class="modal-form" @submit.prevent="allocate">
            <!-- provider -->
            <div class="field">
              <label class="field-label">{{ t('reverseProxy.sub.providerLabel') }}</label>
              <select v-model="providerId" class="field-input">
                <option value="">{{ t('reverseProxy.sub.providerPick') }}</option>
                <option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }} · {{ p.baseDomain }}</option>
              </select>
              <span v-if="selectedProvider" class="field-hint">
                {{ t('reverseProxy.sub.providerUnderDomain', { domain: selectedProvider.baseDomain }) }}
              </span>
            </div>

            <!-- upstream container -->
            <div class="field">
              <label class="field-label">{{ t('reverseProxy.sub.upstreamLabel') }}</label>
              <select
                v-if="hasContainerChoices"
                v-model="upstreamContainer"
                class="field-input"
                @change="onContainerPick"
              >
                <option value="">{{ t('reverseProxy.upstreamPick') }}</option>
                <option v-for="name in runningContainers" :key="name" :value="name">{{ name }}</option>
              </select>
              <input
                v-else
                v-model="upstreamContainer"
                class="field-input field-input--mono"
                :placeholder="t('reverseProxy.upstreamPlaceholder')"
                autocomplete="off"
                spellcheck="false"
              />
            </div>

            <!-- port -->
            <div class="field">
              <label class="field-label">{{ t('reverseProxy.sub.portLabel') }}</label>
              <input
                v-model="upstreamPort"
                class="field-input field-input--mono"
                :class="{ 'field-input--error': upstreamPort.trim().length > 0 && !portValid }"
                inputmode="numeric"
                placeholder="8080"
                autocomplete="off"
              />
              <span v-if="upstreamPort.trim().length > 0 && !portValid" class="field-error">{{ t('reverseProxy.hintInvalidPort') }}</span>
            </div>

            <div class="modal-footer">
              <button type="button" class="btn-secondary" :disabled="allocating" @click="closeModal">{{ t('reverseProxy.sub.cancel') }}</button>
              <button type="submit" class="btn-primary" :disabled="!canAllocate" :aria-busy="allocating">
                <span v-if="allocating" class="spinner" aria-hidden="true" />
                <NIcon v-else :size="14"><Wand /></NIcon>
                {{ allocating ? t('reverseProxy.sub.allocating') : t('reverseProxy.sub.allocate') }}
              </button>
            </div>
          </form>
        </template>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.isub {
  display: inline-flex;
}

/* 招牌入口:渐变描边 + 魔杖图标,比普通按钮更「闪」。 */
.isub__cta {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 34px;
  padding: 0 15px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-primary);
  background: var(--color-primary-soft);
  border: 1px solid var(--color-primary);
  border-radius: var(--rounded-full);
  cursor: pointer;
  transition:
    background var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    transform var(--duration-fast, 150ms) var(--ease-out-expo, ease),
    box-shadow var(--duration-fast, 150ms) var(--ease-out-expo, ease);
}
.isub__cta:hover {
  background: var(--color-primary);
  color: #fff;
  transform: translateY(-1px);
  box-shadow: 0 6px 18px var(--color-primary-soft);
}
.isub__cta:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
.isub__cta-ic {
  display: inline-flex;
}

/* ── modal ── */
.modal-scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.62);
  display: grid;
  place-items: center;
  z-index: 100;
  padding: 24px;
  animation: scrim-in var(--duration-fast, 150ms) ease both;
}
@keyframes scrim-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
.modal {
  position: relative;
  width: 100%;
  max-width: 480px;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-xl);
  box-shadow: var(--shadow-modal);
  overflow: hidden;
  animation: modal-in 0.35s var(--ease-out-expo) both;
}
.modal--success {
  border-color: var(--color-green-line);
  box-shadow: var(--shadow-modal), 0 0 0 1px var(--color-green-line);
}
@keyframes modal-in {
  from { opacity: 0; transform: translateY(14px) scale(0.98); }
  to { opacity: 1; transform: none; }
}
.modal-close {
  position: absolute;
  top: 14px;
  right: 14px;
  width: 30px;
  height: 30px;
  border-radius: var(--rounded-md);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  display: grid;
  place-items: center;
  z-index: 2;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}
.modal-close:hover {
  color: var(--color-text);
  background: var(--color-inset);
}
.modal-close:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.modal-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 20px 48px 16px 20px;
  border-bottom: 1px solid var(--color-border);
}
.modal-icon {
  width: 36px;
  height: 36px;
  border-radius: var(--rounded-lg);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}
.modal-title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
  margin-top: 2px;
  letter-spacing: -0.01em;
}
.modal-sub {
  font-size: 0.78rem;
  color: var(--color-faint);
  margin-top: 3px;
  line-height: 1.5;
}

/* 无提供商引导 */
.noprov {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  text-align: center;
  padding: 32px 24px;
}
.noprov__ic {
  color: var(--color-faint);
}
.noprov__txt {
  font-size: 0.86rem;
  color: var(--color-dim);
}
.noprov__link {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-primary);
  text-decoration: none;
}
.noprov__link:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}

.modal-form {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.field-label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--color-dim);
}
.field-input {
  width: 100%;
  height: 38px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 0 12px;
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.86rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}
.field-input--error {
  border-color: var(--color-red);
}
.field-hint {
  font-size: 0.74rem;
  color: var(--color-faint);
  line-height: 1.4;
}
.field-error {
  font-size: 0.76rem;
  color: var(--color-red);
  line-height: 1.4;
}

/* ── 庆祝态 ── */
.celebrate {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 36px 28px 28px;
  gap: 10px;
}
.celebrate__burst {
  width: 60px;
  height: 60px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  color: var(--color-green);
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  margin-bottom: 4px;
  animation: pop 0.5s var(--ease-out-expo) both;
}
@keyframes pop {
  0% { transform: scale(0.4); opacity: 0; }
  70% { transform: scale(1.08); }
  100% { transform: scale(1); opacity: 1; }
}
.celebrate__title {
  font-size: 1.1rem;
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
}
.celebrate__lede {
  font-size: 0.82rem;
  color: var(--color-faint);
  max-width: 40ch;
  line-height: 1.5;
}
.celebrate__domain {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  padding: 10px 16px;
  font-size: 0.98rem;
  font-weight: 600;
  color: var(--color-green);
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  border-radius: var(--rounded);
  text-decoration: none;
  word-break: break-all;
  transition: filter var(--duration-fast);
}
.celebrate__domain:hover {
  filter: brightness(1.05);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.celebrate__ext {
  opacity: 0.75;
  flex-shrink: 0;
}
.celebrate__flow {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 0.74rem;
  color: var(--color-faint);
  margin-top: 2px;
}
.celebrate__chip {
  padding: 2px 8px;
  border-radius: var(--rounded-sm);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  color: var(--color-dim);
}
.celebrate__actions {
  display: flex;
  gap: 8px;
  margin-top: 16px;
}

/* buttons */
.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 16px;
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  text-decoration: none;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast);
  white-space: nowrap;
}
.btn-primary:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}
.btn-primary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
  transform: none;
  box-shadow: none;
}
.btn-secondary {
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 0 16px;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 500;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: border-color var(--duration-fast);
}
.btn-secondary:hover:not(:disabled) {
  border-color: var(--color-faint);
}
.btn-secondary:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.spinner {
  display: inline-block;
  width: 13px;
  height: 13px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>

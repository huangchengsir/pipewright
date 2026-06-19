<script setup lang="ts">
/*
  SettingsDnsProviders.vue — DNS 提供商管理(R3 / E3.1 · 零 DNS 体验)。

  挂接 Cloudflare / DNSPod / 阿里云 DNS,解锁两件事:① 路由走 DNS-01 验证 → 通配符证书;
  ② 一键分配子域名(app-xxxx.<根域> + 自动 A 记录 + 路由)。API Token 只写不读 —— 存入保险库,
  列表只显示「已配置 / 未配置」,绝不回显。

  - 列表:类型徽章、名称、根域、凭据状态;每行「验证」(探测 token 是否能触达该 zone)+ 删除。
  - 添加弹窗:类型(三选一)+ 名称 + 根域(FQDN 校验)+ API Token(必填,password)。
  - 删除二次确认。验证结果以 toast 反馈。
  数据来自 GET /api/dns/providers(只读聚合,从不含 token)。
*/
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NIcon } from 'naive-ui'
import { World, Plus, Trash, CircleCheck, CircleX, ShieldCheck } from '@vicons/tabler'
import {
  listDnsProviders,
  createDnsProvider,
  deleteDnsProvider,
  verifyDnsProvider,
  type DnsProvider,
  type DnsProviderType,
  type CreateDnsProviderInput,
} from '../../api/dnsProviders'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const { t } = useI18n()
const toast = useToast()

// ─── 列表加载 ──────────────────────────────────────────────────────────────────
type LoadState = 'idle' | 'loading' | 'error'
const loadState = ref<LoadState>('idle')
const loadError = ref('')
const providers = ref<DnsProvider[]>([])

const PROVIDER_TYPES: DnsProviderType[] = ['cloudflare', 'dnspod', 'alidns']
const typeLabels = computed<Record<DnsProviderType, string>>(() => ({
  cloudflare: t('dnsProviders.typeCloudflare'),
  dnspod: t('dnsProviders.typeDnspod'),
  alidns: t('dnsProviders.typeAlidns'),
}))

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    providers.value = await listDnsProviders()
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('dnsProviders.errLoad', { status: err.status }))
        : t('dnsProviders.errNetwork')
  }
}

onMounted(load)

// ─── 添加弹窗 ──────────────────────────────────────────────────────────────────
const modalOpen = ref(false)
const submitting = ref(false)
const formBanner = ref('')
const form = ref<{ type: DnsProviderType; name: string; baseDomain: string; token: string }>({
  type: 'cloudflare',
  name: '',
  baseDomain: '',
  token: '',
})
const errors = ref<{ name: string; baseDomain: string; token: string }>({
  name: '',
  baseDomain: '',
  token: '',
})

// 根域:与路由域名相同的 FQDN 校验(根域本身不带通配符)。
const FQDN_RE = /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/i

function openAdd(): void {
  form.value = { type: 'cloudflare', name: '', baseDomain: '', token: '' }
  errors.value = { name: '', baseDomain: '', token: '' }
  formBanner.value = ''
  modalOpen.value = true
}

function closeModal(): void {
  if (submitting.value) return
  modalOpen.value = false
  form.value.token = ''
}

function validate(): boolean {
  errors.value = { name: '', baseDomain: '', token: '' }
  let ok = true
  if (!form.value.name.trim()) {
    errors.value.name = t('dnsProviders.valNameRequired')
    ok = false
  }
  const dom = form.value.baseDomain.trim().toLowerCase()
  if (!dom) {
    errors.value.baseDomain = t('dnsProviders.valBaseDomainRequired')
    ok = false
  } else if (!FQDN_RE.test(dom)) {
    errors.value.baseDomain = t('dnsProviders.valBaseDomainInvalid')
    ok = false
  }
  if (!form.value.token) {
    errors.value.token = t('dnsProviders.valTokenRequired')
    ok = false
  }
  return ok
}

async function submit(): Promise<void> {
  if (!validate()) return
  submitting.value = true
  formBanner.value = ''
  try {
    const payload: CreateDnsProviderInput = {
      type: form.value.type,
      name: form.value.name.trim(),
      baseDomain: form.value.baseDomain.trim().toLowerCase(),
      token: form.value.token,
    }
    const created = await createDnsProvider(payload)
    providers.value = [created, ...providers.value]
    form.value.token = ''
    modalOpen.value = false
  } catch (err) {
    formBanner.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('dnsProviders.errSave', { status: err.status }))
        : t('dnsProviders.errSaveRetry')
  } finally {
    submitting.value = false
  }
}

// ─── 验证 ──────────────────────────────────────────────────────────────────────
const verifyingId = ref<string | null>(null)
async function verify(p: DnsProvider): Promise<void> {
  if (verifyingId.value) return
  verifyingId.value = p.id
  try {
    const res = await verifyDnsProvider(p.id)
    if (res.ok) {
      toast.success(t('dnsProviders.verifyOk'), { detail: res.message ?? p.baseDomain })
    } else {
      toast.error(t('dnsProviders.verifyFail'), { detail: res.message ?? p.name })
    }
  } catch (err) {
    toast.error(t('dnsProviders.verifyFail'), {
      detail:
        err instanceof HttpError
          ? (err.apiError?.message ?? t('dnsProviders.errNetwork'))
          : t('dnsProviders.errNetwork'),
    })
  } finally {
    verifyingId.value = null
  }
}

// ─── 删除 ──────────────────────────────────────────────────────────────────────
const deleteOpen = ref(false)
const deleting = ref<DnsProvider | null>(null)
const deleteSubmitting = ref(false)
const deleteBanner = ref('')

function openDelete(p: DnsProvider): void {
  deleting.value = p
  deleteBanner.value = ''
  deleteOpen.value = true
}
function closeDelete(): void {
  if (deleteSubmitting.value) return
  deleteOpen.value = false
  deleting.value = null
}
async function confirmDelete(): Promise<void> {
  if (!deleting.value) return
  deleteSubmitting.value = true
  deleteBanner.value = ''
  const id = deleting.value.id
  try {
    const res = await deleteDnsProvider(id)
    if (res.ok) {
      providers.value = providers.value.filter((p) => p.id !== id)
      deleteOpen.value = false
      deleting.value = null
    } else {
      deleteBanner.value = t('dnsProviders.errDelete', { status: 0 })
    }
  } catch (err) {
    deleteBanner.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('dnsProviders.errDelete', { status: err.status }))
        : t('dnsProviders.errNetwork')
  } finally {
    deleteSubmitting.value = false
  }
}
</script>

<template>
  <div class="dns-root">
    <!-- section header -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">{{ t('dnsProviders.title') }}</h2>
        <p class="section-desc">{{ t('dnsProviders.desc') }}</p>
      </div>
      <button class="btn-primary" :disabled="loadState === 'loading'" @click="openAdd">
        <NIcon :size="14"><Plus /></NIcon>
        {{ t('dnsProviders.addProvider') }}
      </button>
    </div>

    <!-- load error -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <span>⚠ {{ loadError }}</span>
      <button class="banner-retry" @click="load">↻ {{ t('dnsProviders.retry') }}</button>
    </div>

    <!-- panel -->
    <div class="panel">
      <div class="panel-head">
        <span>{{ t('dnsProviders.panelTitle') }}</span>
        <span v-if="loadState === 'idle'" class="panel-meta">{{ t('dnsProviders.countLabel', { n: providers.length }) }}</span>
      </div>

      <!-- loading -->
      <div v-if="loadState === 'loading'" class="state-msg">{{ t('common.refresh') }}…</div>

      <!-- empty -->
      <div v-else-if="loadState === 'idle' && providers.length === 0" class="empty-state">
        <div class="empty-icon" aria-hidden="true"><NIcon :size="22"><World /></NIcon></div>
        <p class="empty-label">{{ t('dnsProviders.emptyLabel') }}</p>
        <p class="empty-hint">{{ t('dnsProviders.emptyHint') }}</p>
        <button class="btn-primary" @click="openAdd">+ {{ t('dnsProviders.addFirstProvider') }}</button>
      </div>

      <!-- list -->
      <template v-else-if="loadState === 'idle'">
        <div class="dns-row dns-row--head" aria-hidden="true">
          <span>{{ t('dnsProviders.colType') }}</span>
          <span>{{ t('dnsProviders.colName') }}</span>
          <span>{{ t('dnsProviders.colBaseDomain') }}</span>
          <span>{{ t('dnsProviders.colCredential') }}</span>
          <span />
        </div>
        <div v-for="p in providers" :key="p.id" class="dns-row">
          <span class="type-badge" :class="`type-badge--${p.type}`">{{ typeLabels[p.type] }}</span>
          <strong class="dns-name">{{ p.name }}</strong>
          <span class="dns-domain mono">{{ p.baseDomain }}</span>
          <span class="cred-state" :class="p.credentialConfigured ? 'cred-state--ok' : 'cred-state--missing'">
            <NIcon :size="13"><CircleCheck v-if="p.credentialConfigured" /><CircleX v-else /></NIcon>
            {{ p.credentialConfigured ? t('dnsProviders.credConfigured') : t('dnsProviders.credMissing') }}
          </span>
          <span class="dns-ops">
            <button
              class="op-btn"
              :disabled="verifyingId === p.id"
              :title="t('dnsProviders.verify')"
              @click="verify(p)"
            >
              <NIcon :size="13"><ShieldCheck /></NIcon>
              <span class="op-btn-txt">{{ verifyingId === p.id ? t('dnsProviders.verifying') : t('dnsProviders.verify') }}</span>
            </button>
            <button
              class="op-btn op-btn--danger"
              :title="t('dnsProviders.deleteTitle', { name: p.name })"
              :aria-label="t('dnsProviders.deleteAria', { name: p.name })"
              @click="openDelete(p)"
            >
              <NIcon :size="13"><Trash /></NIcon>
            </button>
          </span>
        </div>
      </template>
    </div>
  </div>

  <!-- add modal -->
  <Teleport to="body">
    <div
      v-if="modalOpen"
      class="modal-scrim"
      role="dialog"
      :aria-label="t('dnsProviders.addTitle')"
      aria-modal="true"
      @keydown.esc="closeModal"
      @click.self="closeModal"
    >
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon" aria-hidden="true"><NIcon :size="18"><World /></NIcon></div>
          <div>
            <h3 class="modal-title">{{ t('dnsProviders.addTitle') }}</h3>
            <p class="modal-sub">{{ t('dnsProviders.modalSub') }}</p>
          </div>
          <button class="modal-close" :aria-label="t('dnsProviders.closeDialog')" :disabled="submitting" @click="closeModal">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg>
          </button>
        </div>

        <div v-if="formBanner" class="banner banner--error modal-banner" role="alert">{{ formBanner }}</div>

        <form class="modal-form" novalidate @submit.prevent="submit">
          <!-- type -->
          <div class="field">
            <label class="field-label">{{ t('dnsProviders.fieldType') }}</label>
            <div class="segmented" role="group">
              <button
                v-for="opt in PROVIDER_TYPES"
                :key="opt"
                type="button"
                class="seg-item"
                :class="{ 'seg-item--active': form.type === opt }"
                :disabled="submitting"
                @click="form.type = opt"
              >{{ typeLabels[opt] }}</button>
            </div>
          </div>

          <!-- name -->
          <div class="field">
            <label class="field-label" for="dns-name">{{ t('dnsProviders.fieldName') }}</label>
            <input
              id="dns-name"
              v-model="form.name"
              class="field-input"
              :class="{ 'field-input--error': errors.name }"
              type="text"
              :placeholder="t('dnsProviders.namePlaceholder')"
              :disabled="submitting"
              autocomplete="off"
              @input="errors.name = ''"
            />
            <span v-if="errors.name" class="field-error" role="alert">{{ errors.name }}</span>
          </div>

          <!-- base domain -->
          <div class="field">
            <label class="field-label" for="dns-domain">{{ t('dnsProviders.fieldBaseDomain') }}</label>
            <input
              id="dns-domain"
              v-model="form.baseDomain"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': errors.baseDomain }"
              type="text"
              :placeholder="t('dnsProviders.baseDomainPlaceholder')"
              :disabled="submitting"
              autocomplete="off"
              spellcheck="false"
              @input="errors.baseDomain = ''"
            />
            <span v-if="errors.baseDomain" class="field-error" role="alert">{{ errors.baseDomain }}</span>
          </div>

          <!-- token (write-only) -->
          <div class="field">
            <label class="field-label" for="dns-token">{{ t('dnsProviders.fieldToken') }}</label>
            <input
              id="dns-token"
              v-model="form.token"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': errors.token }"
              type="password"
              :placeholder="t('dnsProviders.tokenPlaceholder')"
              :disabled="submitting"
              autocomplete="new-password"
              @input="errors.token = ''"
            />
            <span v-if="errors.token" class="field-error" role="alert">{{ errors.token }}</span>
            <span class="field-hint">{{ t('dnsProviders.tokenHint') }}</span>
          </div>

          <div class="modal-footer">
            <button type="button" class="btn-secondary" :disabled="submitting" @click="closeModal">{{ t('dnsProviders.cancel') }}</button>
            <button type="submit" class="btn-primary" :disabled="submitting" :aria-busy="submitting">
              <span v-if="submitting" class="spinner" aria-hidden="true" />
              {{ submitting ? t('dnsProviders.saving') : t('dnsProviders.create') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- delete modal -->
  <Teleport to="body">
    <div
      v-if="deleteOpen && deleting"
      class="modal-scrim"
      role="dialog"
      :aria-label="t('dnsProviders.deleteConfirmTitle')"
      aria-modal="true"
      @keydown.esc="closeDelete"
      @click.self="closeDelete"
    >
      <div class="modal modal--sm">
        <div class="modal-head">
          <div class="modal-icon modal-icon--danger" aria-hidden="true"><NIcon :size="18"><Trash /></NIcon></div>
          <div>
            <h3 class="modal-title">{{ t('dnsProviders.deleteConfirmTitle') }}</h3>
            <p class="modal-sub">{{ t('dnsProviders.deleteIrreversible') }}</p>
          </div>
          <button class="modal-close" :aria-label="t('dnsProviders.closeDialog')" :disabled="deleteSubmitting" @click="closeDelete">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg>
          </button>
        </div>
        <div class="modal-body">
          <p class="delete-text">
            {{ t('dnsProviders.deleteConfirmPrefix') }}
            <strong class="delete-name">{{ deleting.name }}</strong>
            {{ t('dnsProviders.deleteConfirmSuffix') }}
          </p>
          <div v-if="deleteBanner" class="banner banner--error modal-banner" role="alert">{{ deleteBanner }}</div>
        </div>
        <div class="modal-footer modal-footer--standalone">
          <button type="button" class="btn-secondary" :disabled="deleteSubmitting" @click="closeDelete">{{ t('dnsProviders.cancel') }}</button>
          <button type="button" class="btn-danger" :disabled="deleteSubmitting" :aria-busy="deleteSubmitting" @click="confirmDelete">
            <span v-if="deleteSubmitting" class="spinner spinner--red" aria-hidden="true" />
            {{ deleteSubmitting ? t('dnsProviders.deleting') : t('dnsProviders.confirmDelete') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.dns-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* section head */
.section-head {
  display: flex;
  align-items: flex-start;
  gap: 16px;
}
.section-head-text {
  flex: 1;
}
.section-title {
  font-size: 1.12rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--color-text);
}
.section-desc {
  font-size: 0.82rem;
  color: var(--color-faint);
  margin-top: 4px;
  max-width: 68ch;
  line-height: 1.55;
}

/* panel */
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: panel-in 0.45s var(--ease-out-expo) both;
}
@keyframes panel-in {
  from { opacity: 0; transform: translateY(13px); }
  to { opacity: 1; transform: none; }
}
.panel-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}
.panel-meta {
  margin-left: auto;
  font-size: 0.74rem;
  color: var(--color-faint);
  font-weight: 400;
}
.state-msg {
  padding: 28px 18px;
  font-size: 0.84rem;
  color: var(--color-faint);
  text-align: center;
}

/* empty */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 48px 32px;
  text-align: center;
}
.empty-icon {
  width: 52px;
  height: 52px;
  border-radius: var(--rounded-xl);
  background: var(--color-inset);
  border: 1.5px dashed var(--color-border-strong);
  display: grid;
  place-items: center;
  color: var(--color-dim);
  margin-bottom: 4px;
}
.empty-label {
  font-size: 0.92rem;
  font-weight: 600;
  color: var(--color-text);
}
.empty-hint {
  font-size: 0.8rem;
  color: var(--color-faint);
  max-width: 46ch;
  line-height: 1.55;
  margin-bottom: 6px;
}

/* table */
.dns-row {
  display: grid;
  grid-template-columns: 120px minmax(140px, 1.3fr) minmax(140px, 1.4fr) 130px auto;
  align-items: center;
  gap: 14px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}
.dns-row:last-child {
  border-bottom: none;
}
.dns-row:not(.dns-row--head):hover {
  background: var(--color-inset);
}
.dns-row--head {
  height: 34px;
  padding-top: 0;
  padding-bottom: 0;
  font-size: 0.71rem;
  color: var(--color-faint);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  background: var(--color-card-2);
  pointer-events: none;
}

.type-badge {
  display: inline-flex;
  align-items: center;
  justify-self: start;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: var(--rounded-full);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-dim);
  white-space: nowrap;
}
.type-badge--cloudflare {
  color: #f38020;
  border-color: oklch(72% 0.16 56 / 0.4);
  background: oklch(72% 0.16 56 / 0.1);
}
.type-badge--dnspod {
  color: var(--color-primary);
  border-color: var(--color-primary-soft);
  background: var(--color-primary-soft);
}
.type-badge--alidns {
  color: var(--color-amber);
  border-color: var(--color-amber-line);
  background: var(--color-amber-soft);
}

.dns-name {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.dns-domain {
  font-size: 0.78rem;
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mono {
  font-family: var(--font-mono);
}

.cred-state {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.74rem;
  font-weight: 600;
  white-space: nowrap;
}
.cred-state--ok {
  color: var(--color-green);
}
.cred-state--missing {
  color: var(--color-faint);
}

.dns-ops {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
}
.op-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 28px;
  padding: 0 10px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-dim);
  border-radius: var(--rounded-md);
  cursor: pointer;
  font-size: 0.74rem;
  font-weight: 500;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background-color var(--duration-fast);
}
.op-btn:hover:not(:disabled) {
  color: var(--color-primary);
  border-color: var(--color-primary);
}
.op-btn:disabled {
  opacity: 0.55;
  cursor: progress;
}
.op-btn--danger {
  padding: 0 8px;
}
.op-btn--danger:hover:not(:disabled) {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}
.op-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* banner */
.banner {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 11px 14px;
  border-radius: var(--rounded);
  font-size: 0.83rem;
  line-height: 1.5;
}
.banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}
.banner-retry {
  margin-left: auto;
  flex-shrink: 0;
  background: none;
  border: none;
  color: var(--color-red);
  font-size: 0.83rem;
  font-weight: 600;
  cursor: pointer;
  padding: 0;
  text-decoration: underline;
  text-underline-offset: 2px;
}

/* buttons */
.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 15px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition:
    background-color var(--duration-fast),
    transform var(--duration-fast);
  white-space: nowrap;
  flex-shrink: 0;
}
.btn-primary:hover:not(:disabled) {
  background: var(--color-primary-press);
  transform: translateY(-1px);
}
.btn-primary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 3px;
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
  padding: 0 15px;
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
.btn-danger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 34px;
  padding: 0 15px;
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid var(--color-red-line);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: background-color var(--duration-fast), transform var(--duration-fast);
}
.btn-danger:hover:not(:disabled) {
  background: oklch(62% 0.18 22 / 0.25);
  transform: translateY(-1px);
}
.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

/* modal */
.modal-scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.62);
  display: grid;
  place-items: center;
  z-index: 100;
  padding: 24px;
  animation: scrim-in var(--duration-fast) ease both;
}
@keyframes scrim-in {
  from { opacity: 0; }
  to { opacity: 1; }
}
.modal {
  width: 100%;
  max-width: 520px;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-xl);
  box-shadow: var(--shadow-modal);
  overflow: hidden;
  animation: modal-in 0.35s var(--ease-out-expo) both;
}
.modal--sm {
  max-width: 420px;
}
@keyframes modal-in {
  from { opacity: 0; transform: translateY(14px) scale(0.98); }
  to { opacity: 1; transform: none; }
}
.modal-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 20px 20px 16px;
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
.modal-icon--danger {
  background: var(--color-red-soft);
  color: var(--color-red);
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
  line-height: 1.4;
}
.modal-close {
  margin-left: auto;
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: var(--rounded-md);
  border: none;
  background: transparent;
  color: var(--color-faint);
  cursor: pointer;
  display: grid;
  place-items: center;
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
.modal-banner {
  margin: 16px 20px 0;
  border-radius: var(--rounded);
}
.modal-form {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.modal-body {
  padding: 20px;
}
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 4px;
}
.modal-footer--standalone {
  padding: 4px 20px 20px;
}

/* fields */
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
.field-input::placeholder {
  color: var(--color-faint);
}
.field-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.field-input--error {
  border-color: var(--color-red);
}
.field-input--error:focus {
  border-color: var(--color-red);
  box-shadow: 0 0 0 3px var(--color-red-soft);
}
.field-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}
.field-error {
  font-size: 0.76rem;
  color: var(--color-red);
  line-height: 1.4;
}
.field-hint {
  font-size: 0.74rem;
  color: var(--color-faint);
  line-height: 1.4;
}

/* segmented */
.segmented {
  display: inline-flex;
  background: var(--color-inset);
  border-radius: var(--rounded);
  padding: 3px;
  gap: 2px;
  width: 100%;
}
.seg-item {
  flex: 1;
  height: 30px;
  border: none;
  background: transparent;
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 500;
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast), box-shadow var(--duration-fast);
}
.seg-item:hover:not(:disabled) {
  color: var(--color-text);
  background: oklch(100% 0 0 / 0.04);
}
.seg-item--active {
  background: var(--color-card);
  color: var(--color-text);
  box-shadow: var(--shadow);
}
.seg-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* delete body */
.delete-text {
  font-size: 0.86rem;
  color: var(--color-dim);
  line-height: 1.55;
}
.delete-name {
  color: var(--color-text);
  font-weight: 600;
}

/* spinner */
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
.spinner--red {
  border-color: oklch(69% 0.17 22 / 0.3);
  border-top-color: var(--color-red);
}
@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 640px) {
  .dns-row {
    grid-template-columns: 1fr 1fr;
    gap: 8px 12px;
  }
  .dns-row--head {
    display: none;
  }
  .dns-ops {
    grid-column: 1 / -1;
    justify-content: flex-start;
  }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>

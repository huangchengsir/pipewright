<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  listCredentials,
  createCredential,
  updateCredential,
  deleteCredential,
} from '../../api/credentials'
import type { Credential, CredentialType, CreateCredentialInput, UpdateCredentialInput } from '../../api/credentials'
import { HttpError } from '../../api/http'
import AuditTimeline from '../../components/AuditTimeline.vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from '../../composables/useToast'
import {
  getOAuthApps,
  authorizeUrl,
  type OAuthApp,
  type OAuthProvider,
} from '../../api/oauth'

// ─── state ──────────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const credentials = ref<Credential[]>([])

// ─── add / edit modal ───────────────────────────────────────────────────────

const modalOpen = ref(false)
const modalMode = ref<'add' | 'edit'>('add')
const editingId = ref<string | null>(null)

const form = ref({
  name: '',
  type: 'git_token' as CredentialType,
  scope: '',
  secret: '',
})

const formErrors = ref({
  name: '',
  type: '',
  scope: '',
  secret: '',
})

const formBanner = ref('')
const formSubmitting = ref(false)

// ─── delete confirm ─────────────────────────────────────────────────────────

const deleteModalOpen = ref(false)
const deletingCredential = ref<Credential | null>(null)
const deleteSubmitting = ref(false)
const deleteBanner = ref('')

// ─── copy feedback ──────────────────────────────────────────────────────────

const copiedId = ref<string | null>(null)

// ─── OAuth connect (enabled providers only) ──────────────────────────────────

const { t } = useI18n()
const toast = useToast()
const route = useRoute()
const router = useRouter()

const oauthApps = ref<OAuthApp[]>([])

const PROVIDER_LABELS = computed<Record<OAuthProvider, string>>(() => ({
  gitee: 'Gitee',
  github: 'GitHub',
  gitlab: 'GitLab',
  custom: t('settingsVault.providerCustom'),
}))

const PROVIDER_LOGO: Record<OAuthProvider, { text: string; style: string }> = {
  gitee: { text: 'G', style: 'background:#c71d23;color:#fff' },
  github: { text: '', style: 'background:#1f2328;color:#fff' },
  gitlab: { text: '', style: 'background:#fc6d26;color:#fff' },
  custom: { text: '⚙', style: 'background:var(--color-inset);color:var(--color-dim)' },
}

const connectableProviders = computed(() =>
  oauthApps.value.filter((a) => a.enabled && a.configured),
)

async function loadOAuthApps(): Promise<void> {
  try {
    oauthApps.value = await getOAuthApps()
  } catch {
    // Non-fatal: the connect section just stays empty if OAuth apps can't load.
    oauthApps.value = []
  }
}

/** Kick off the OAuth dance via a FULL-PAGE navigation (not fetch). */
function connectProvider(provider: OAuthProvider): void {
  window.location.href = authorizeUrl(provider)
}

/**
 * Read OAuth callback result from the URL query:
 *   ?connected={provider}&account={login}  → success toast + refresh
 *   ?oauth_error={reason}                   → error toast
 * The query is then stripped so a refresh doesn't re-fire the toast.
 */
function handleOAuthCallback(): void {
  const connected = route.query.connected
  const oauthError = route.query.oauth_error

  if (typeof connected === 'string' && connected) {
    const account = typeof route.query.account === 'string' ? route.query.account : ''
    const label = PROVIDER_LABELS.value[connected as OAuthProvider] ?? connected
    toast.success(
      account
        ? t('settingsVault.toastConnectedAccount', { label, account })
        : t('settingsVault.toastConnected', { label }),
      { detail: t('settingsVault.toastConnectedDetail') },
    )
    void loadCredentials()
    clearOAuthQuery()
  } else if (typeof oauthError === 'string' && oauthError) {
    toast.error(t('settingsVault.toastConnectError'), { detail: oauthError })
    clearOAuthQuery()
  }
}

/** Remove OAuth-related query params, preserving any others. */
function clearOAuthQuery(): void {
  const query = { ...route.query }
  delete query.connected
  delete query.account
  delete query.oauth_error
  void router.replace({ query })
}

// ─── helpers ────────────────────────────────────────────────────────────────

const IDLE_DAY_THRESHOLD = 60 // flag if unused for ≥60 days

function idleWarning(c: Credential): boolean {
  if (!c.lastUsedAt) return false
  const diff = Date.now() - new Date(c.lastUsedAt).getTime()
  return diff > IDLE_DAY_THRESHOLD * 24 * 60 * 60 * 1000
}

function relativeTime(isoStr: string | null): string {
  if (!isoStr) return t('settingsVault.never')
  const diff = Date.now() - new Date(isoStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return t('time.justNow')
  const m = Math.floor(s / 60)
  if (m < 60) return t('time.minAgo', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('time.hourAgo', { n: h })
  const d = Math.floor(h / 24)
  return t('time.dayAgo', { n: d })
}

const typeLabels = computed<Record<CredentialType, string>>(() => ({
  git_token: t('settingsVault.typeGitToken'),
  ssh_key: t('settingsVault.typeSshKey'),
  ssh_password: t('settingsVault.typeSshPassword'),
  registry: t('settingsVault.typeRegistry'),
}))

const idleCount = computed(
  () => credentials.value.filter(idleWarning).length,
)

// ─── data loading ────────────────────────────────────────────────────────────

async function loadCredentials(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    credentials.value = await listCredentials()
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = t('settingsVault.errNetwork')
      } else if (err.apiError?.code === 'vault_unconfigured') {
        loadError.value = t('settingsVault.errVaultUnconfigured')
      } else {
        loadError.value = err.apiError?.message ?? t('settingsVault.errLoadStatus', { status: err.status })
      }
    } else {
      loadError.value = t('settingsVault.errLoadRetry')
    }
    loadState.value = 'error'
  }
}

onMounted(() => {
  void loadCredentials()
  void loadOAuthApps()
  handleOAuthCallback()
})

// ─── modal open / close ─────────────────────────────────────────────────────

function openAddModal(): void {
  modalMode.value = 'add'
  editingId.value = null
  form.value = { name: '', type: 'git_token', scope: '', secret: '' }
  clearFormErrors()
  formBanner.value = ''
  modalOpen.value = true
}

function openEditModal(c: Credential): void {
  modalMode.value = 'edit'
  editingId.value = c.id
  // secret is intentionally blank — user must re-enter to rotate
  form.value = { name: c.name, type: c.type, scope: c.scope, secret: '' }
  clearFormErrors()
  formBanner.value = ''
  modalOpen.value = true
}

function closeModal(): void {
  if (formSubmitting.value) return
  modalOpen.value = false
  // Clear secret immediately when modal closes
  form.value.secret = ''
}

// ─── form validation ─────────────────────────────────────────────────────────

function clearFormErrors(): void {
  formErrors.value = { name: '', type: '', scope: '', secret: '' }
}

function validateForm(): boolean {
  clearFormErrors()
  let ok = true
  if (!form.value.name.trim()) {
    formErrors.value.name = t('settingsVault.valNameRequired')
    ok = false
  }
  if (!form.value.type) {
    formErrors.value.type = t('settingsVault.valTypeRequired')
    ok = false
  }
  if (modalMode.value === 'add' && !form.value.secret) {
    formErrors.value.secret = t('settingsVault.valSecretRequired')
    ok = false
  }
  return ok
}

// ─── form submit ─────────────────────────────────────────────────────────────

async function handleFormSubmit(): Promise<void> {
  if (!validateForm()) return
  formSubmitting.value = true
  formBanner.value = ''
  try {
    if (modalMode.value === 'add') {
      const payload: CreateCredentialInput = {
        name: form.value.name.trim(),
        type: form.value.type,
        scope: form.value.scope.trim(),
        secret: form.value.secret,
      }
      const created = await createCredential(payload)
      credentials.value = [created, ...credentials.value]
    } else if (editingId.value) {
      const payload: UpdateCredentialInput = {
        name: form.value.name.trim(),
        scope: form.value.scope.trim(),
      }
      // Only include secret if user typed something (rotation)
      if (form.value.secret) {
        payload.secret = form.value.secret
      }
      const updated = await updateCredential(editingId.value, payload)
      credentials.value = credentials.value.map((c) =>
        c.id === updated.id ? updated : c,
      )
    }
    // Clear secret before closing
    form.value.secret = ''
    modalOpen.value = false
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        formBanner.value = t('settingsVault.errNetworkRetry')
      } else if (err.apiError?.code === 'vault_unconfigured') {
        formBanner.value = t('settingsVault.errVaultUnconfiguredSave')
      } else {
        formBanner.value = err.apiError?.message ?? t('settingsVault.errSaveStatus', { status: err.status })
      }
    } else {
      formBanner.value = t('settingsVault.errSaveRetry')
    }
  } finally {
    formSubmitting.value = false
  }
}

// ─── delete ──────────────────────────────────────────────────────────────────

function openDeleteModal(c: Credential): void {
  deletingCredential.value = c
  deleteBanner.value = ''
  deleteModalOpen.value = true
}

function closeDeleteModal(): void {
  if (deleteSubmitting.value) return
  deleteModalOpen.value = false
  deletingCredential.value = null
}

async function confirmDelete(): Promise<void> {
  if (!deletingCredential.value) return
  deleteSubmitting.value = true
  deleteBanner.value = ''
  const id = deletingCredential.value.id
  try {
    await deleteCredential(id)
    credentials.value = credentials.value.filter((c) => c.id !== id)
    deleteModalOpen.value = false
    deletingCredential.value = null
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        deleteBanner.value = t('settingsVault.errNetworkRetry')
      } else {
        deleteBanner.value = err.apiError?.message ?? t('settingsVault.errDeleteStatus', { status: err.status })
      }
    } else {
      deleteBanner.value = t('settingsVault.errDeleteRetry')
    }
  } finally {
    deleteSubmitting.value = false
  }
}

// ─── copy reference (id — not plaintext) ───────────────────────────────────

async function copyReference(c: Credential): Promise<void> {
  try {
    // Copy the credential ID as the reference handle — never the secret
    await navigator.clipboard.writeText(c.id)
    copiedId.value = c.id
    setTimeout(() => {
      if (copiedId.value === c.id) copiedId.value = null
    }, 1800)
  } catch {
    // clipboard API not available — silently ignore
  }
}
</script>

<template>
  <!-- ─── section header ──────────────────────────────────────────────────── -->
  <div class="vault-root">
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">{{ t('settingsVault.title') }}</h2>
        <p class="section-desc">
          {{ t('settingsVault.desc') }}
        </p>
      </div>
      <button
        class="btn-primary"
        :disabled="loadState === 'loading'"
        @click="openAddModal"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
          <path d="M12 5v14M5 12h14"/>
        </svg>
        {{ t('settingsVault.addCredential') }}
      </button>
    </div>

    <!-- ─── OAuth connect (only for enabled & configured providers) ────────── -->
    <div v-if="connectableProviders.length > 0" class="connect-panel">
      <div class="connect-head">
        <div class="connect-head-text">
          <strong>{{ t('settingsVault.connectGitAccount') }}</strong>
          <span>{{ t('settingsVault.connectGitDesc') }}</span>
        </div>
        <router-link to="/settings/oauth" class="connect-config-link">{{ t('settingsVault.manageOAuthApps') }}</router-link>
      </div>
      <div class="connect-grid">
        <button
          v-for="app in connectableProviders"
          :key="app.provider"
          type="button"
          class="connect-btn"
          :aria-label="t('settingsVault.connectProviderAria', { provider: PROVIDER_LABELS[app.provider] })"
          @click="connectProvider(app.provider)"
        >
          <span class="connect-logo" :style="PROVIDER_LOGO[app.provider].style" aria-hidden="true">
            {{ PROVIDER_LOGO[app.provider].text }}
          </span>
          {{ t('settingsVault.connectProvider', { provider: PROVIDER_LABELS[app.provider] }) }}
        </button>
      </div>
    </div>

    <!-- ─── load error banner ─────────────────────────────────────────────── -->
    <div
      v-if="loadState === 'error'"
      class="banner banner--error"
      role="alert"
    >
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadCredentials">↻ {{ t('settingsVault.retry') }}</button>
    </div>

    <!-- ─── credentials panel ─────────────────────────────────────────────── -->
    <div class="panel" :class="{ 'panel--loading': loadState === 'loading' }">
      <div class="panel-head">
        <span>{{ t('settingsVault.panelCredentials') }}</span>
        <span class="panel-meta" v-if="loadState === 'idle'">
          {{ t('settingsVault.countLabel', { n: credentials.length }) }}
          <span v-if="idleCount > 0" class="meta-warn">· {{ t('settingsVault.idleMeta', { n: idleCount, days: 60 }) }}</span>
        </span>
      </div>

      <!-- Loading skeleton -->
      <template v-if="loadState === 'loading'">
        <div class="skel-row" v-for="i in 3" :key="i" aria-hidden="true">
          <span class="skel skel--icon" />
          <span class="skel skel--name" />
          <span class="skel skel--tag" />
          <span class="skel skel--scope" />
          <span class="skel skel--time" />
          <span class="skel skel--ops" />
        </div>
      </template>

      <!-- Empty state -->
      <template v-else-if="loadState === 'idle' && credentials.length === 0">
        <div class="empty-state">
          <div class="empty-icon" aria-hidden="true">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <rect x="4" y="11" width="16" height="9" rx="2"/>
              <path d="M8 11V8a4 4 0 0 1 8 0v3"/>
              <circle cx="12" cy="15.5" r="1" fill="currentColor"/>
            </svg>
          </div>
          <p class="empty-label">{{ t('settingsVault.emptyLabel') }}</p>
          <p class="empty-hint">{{ t('settingsVault.emptyHint') }}</p>
          <button class="btn-primary" @click="openAddModal">+ {{ t('settingsVault.addFirstCredential') }}</button>
        </div>
      </template>

      <!-- Credentials list -->
      <template v-else-if="loadState === 'idle'">
        <!-- Table header -->
        <div class="cred-row cred-row--head" aria-hidden="true">
          <span />
          <span>{{ t('settingsVault.colNameMasked') }}</span>
          <span>{{ t('settingsVault.colType') }}</span>
          <span>{{ t('settingsVault.colScope') }}</span>
          <span>{{ t('settingsVault.colLastUsed') }}</span>
          <span />
        </div>

        <!-- Credential rows -->
        <div
          v-for="cred in credentials"
          :key="cred.id"
          class="cred-row"
          :class="{ 'cred-row--idle': idleWarning(cred) }"
        >
          <!-- Type icon -->
          <span
            class="type-icon"
            :class="`type-icon--${cred.type}`"
            :title="typeLabels[cred.type]"
            aria-hidden="true"
          >
            <!-- git_token -->
            <svg v-if="cred.type === 'git_token'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M14.5 9.5 21 3M21 3h-5M21 3v5"/>
              <path d="M10 14a5 5 0 1 1-7 4.6"/>
            </svg>
            <!-- ssh_key -->
            <svg v-else-if="cred.type === 'ssh_key'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M4 17l6-6-6-6M12 19h8"/>
            </svg>
            <!-- ssh_password(锁) -->
            <svg v-else-if="cred.type === 'ssh_password'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <rect x="4" y="11" width="16" height="9" rx="2"/>
              <path d="M8 11V7a4 4 0 0 1 8 0v4"/>
            </svg>
            <!-- registry -->
            <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <rect x="3" y="3" width="8" height="8" rx="1"/>
              <rect x="13" y="3" width="8" height="8" rx="1"/>
              <rect x="3" y="13" width="8" height="8" rx="1"/>
              <rect x="13" y="13" width="8" height="8" rx="1"/>
            </svg>
          </span>

          <!-- Name + masked value -->
          <div class="cred-name">
            <strong class="cred-label">{{ cred.name }}</strong>
            <!-- maskedValue in JetBrains Mono — never plaintext, not interactive -->
            <span class="cred-mask mono" :title="t('settingsVault.maskTitle')" :aria-label="t('settingsVault.maskAria', { value: cred.maskedValue })">{{ cred.maskedValue }}</span>
          </div>

          <!-- Type label -->
          <span class="cred-type">{{ typeLabels[cred.type] }}</span>

          <!-- Scope -->
          <div class="cred-scope">
            <span
              v-if="cred.scope"
              class="scope-tag"
              :class="{ 'scope-tag--all': cred.scope === '*' || cred.scope.toLowerCase() === '全部' }"
            >{{ cred.scope === '*' ? t('settingsVault.allProjects') : cred.scope }}</span>
            <span v-else class="cred-dim">—</span>
          </div>

          <!-- Last used -->
          <span
            class="cred-time"
            :class="{ 'cred-time--warn': idleWarning(cred) }"
            :title="cred.lastUsedAt ?? t('settingsVault.neverUsed')"
          >
            {{ relativeTime(cred.lastUsedAt) }}
            <span v-if="idleWarning(cred)" class="idle-tag">{{ t('settingsVault.idleTag') }}</span>
          </span>

          <!-- Actions -->
          <span class="cred-ops">
            <!-- Copy reference (id, not secret) -->
            <button
              class="op-btn"
              :class="{ 'op-btn--copied': copiedId === cred.id }"
              :title="copiedId === cred.id ? t('settingsVault.copiedReference') : t('settingsVault.copyReferenceTitle')"
              :aria-label="t('settingsVault.copyReferenceAria', { name: cred.name })"
              @click="copyReference(cred)"
            >
              <svg v-if="copiedId !== cred.id" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                <rect x="9" y="9" width="13" height="13" rx="2"/>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
              </svg>
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
                <path d="M20 6 9 17l-5-5"/>
              </svg>
            </button>
            <!-- Edit -->
            <button
              class="op-btn"
              :title="t('settingsVault.editTitle', { name: cred.name })"
              :aria-label="t('settingsVault.editAria', { name: cred.name })"
              @click="openEditModal(cred)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
              </svg>
            </button>
            <!-- Delete -->
            <button
              class="op-btn op-btn--danger"
              :title="t('settingsVault.deleteTitle', { name: cred.name })"
              :aria-label="t('settingsVault.deleteAria', { name: cred.name })"
              @click="openDeleteModal(cred)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
                <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
              </svg>
            </button>
          </span>
        </div>
      </template>
    </div>

    <!-- ─── Audit timeline (Story 1.4) ──────────────────────────────────── -->
    <AuditTimeline />
  </div>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Add / Edit modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="modalOpen"
      class="modal-scrim"
      role="dialog"
      :aria-label="modalMode === 'add' ? t('settingsVault.addCredential') : t('settingsVault.editCredential')"
      aria-modal="true"
      @keydown.esc="closeModal"
      @click.self="closeModal"
    >
      <div class="modal">
        <!-- Modal header -->
        <div class="modal-head">
          <div class="modal-icon" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <rect x="4" y="11" width="16" height="9" rx="2"/>
              <path d="M8 11V8a4 4 0 0 1 8 0v3"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">{{ modalMode === 'add' ? t('settingsVault.addCredential') : t('settingsVault.editCredential') }}</h3>
            <p class="modal-sub">
              {{ modalMode === 'add'
                ? t('settingsVault.modalSubAdd')
                : t('settingsVault.modalSubEdit') }}
            </p>
          </div>
          <button
            class="modal-close"
            :aria-label="t('settingsVault.closeDialog')"
            :disabled="formSubmitting"
            @click="closeModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <!-- Form error banner -->
        <div
          v-if="formBanner"
          class="banner banner--error modal-banner"
          role="alert"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ formBanner }}
        </div>

        <!-- Form fields -->
        <form
          class="modal-form"
          novalidate
          @submit.prevent="handleFormSubmit"
        >
          <!-- Name -->
          <div class="field">
            <label class="field-label" for="cred-name">{{ t('settingsVault.fieldName') }}</label>
            <input
              id="cred-name"
              v-model="form.name"
              class="field-input"
              :class="{ 'field-input--error': formErrors.name }"
              type="text"
              :placeholder="t('settingsVault.namePlaceholder')"
              :disabled="formSubmitting"
              :aria-invalid="formErrors.name ? 'true' : undefined"
              :aria-describedby="formErrors.name ? 'cred-name-err' : undefined"
              autocomplete="off"
              @input="formErrors.name = ''"
            />
            <span v-if="formErrors.name" id="cred-name-err" class="field-error" role="alert">{{ formErrors.name }}</span>
          </div>

          <!-- Type -->
          <div class="field">
            <label class="field-label" for="cred-type">{{ t('settingsVault.fieldType') }}</label>
            <div class="segmented" role="group" :aria-label="t('settingsVault.credentialTypeAria')">
              <button
                v-for="opt in (['git_token', 'ssh_key', 'ssh_password', 'registry'] as CredentialType[])"
                :key="opt"
                type="button"
                class="seg-item"
                :class="{ 'seg-item--active': form.type === opt }"
                :disabled="formSubmitting"
                @click="form.type = opt; formErrors.type = ''"
              >{{ typeLabels[opt] }}</button>
            </div>
            <span v-if="formErrors.type" class="field-error" role="alert">{{ formErrors.type }}</span>
          </div>

          <!-- Scope -->
          <div class="field">
            <label class="field-label" for="cred-scope">
              {{ t('settingsVault.fieldScope') }}
              <span class="field-optional">{{ t('settingsVault.optional') }}</span>
            </label>
            <input
              id="cred-scope"
              v-model="form.scope"
              class="field-input"
              type="text"
              :placeholder="t('settingsVault.scopePlaceholder')"
              :disabled="formSubmitting"
              autocomplete="off"
            />
          </div>

          <!-- Secret — password type, never echoed back -->
          <div class="field">
            <label class="field-label" for="cred-secret">
              {{ modalMode === 'add' ? t('settingsVault.fieldSecret') : t('settingsVault.fieldSecretNew') }}
              <span v-if="modalMode === 'edit'" class="field-optional">{{ t('settingsVault.secretOptionalEdit') }}</span>
            </label>
<!-- SSH 私钥是多行 PEM/OpenSSH 文本:必须用 textarea,单行 <input> 会按 HTML 规范清除换行
                 → 私钥结构破坏、ssh.ParsePrivateKey 失败「凭据不是可用的 SSH 私钥」。令牌/镜像仓库仍用
                 password input(单行密文 + 掩码输入)。 -->
            <textarea
              v-if="form.type === 'ssh_key'"
              id="cred-secret"
              v-model="form.secret"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': formErrors.secret }"
              rows="8"
              :placeholder="modalMode === 'add' ? t('settingsVault.secretPlaceholderSshKey') : t('settingsVault.secretPlaceholderKeep')"
              :disabled="formSubmitting"
              :aria-invalid="formErrors.secret ? 'true' : undefined"
              :aria-describedby="formErrors.secret ? 'cred-secret-err' : undefined"
              autocomplete="off"
              spellcheck="false"
              @input="formErrors.secret = ''"
            ></textarea>
            <input
              v-else
              id="cred-secret"
              v-model="form.secret"
              class="field-input field-input--mono"
              :class="{ 'field-input--error': formErrors.secret }"
              type="password"
              :placeholder="modalMode === 'add' ? (form.type === 'ssh_password' ? t('settingsVault.secretPlaceholderSshPassword') : t('settingsVault.secretPlaceholderToken')) : t('settingsVault.secretPlaceholderKeep')"
              :disabled="formSubmitting"
              :aria-invalid="formErrors.secret ? 'true' : undefined"
              :aria-describedby="formErrors.secret ? 'cred-secret-err' : undefined"
              autocomplete="new-password"
              @input="formErrors.secret = ''"
            />
            <span v-if="formErrors.secret" id="cred-secret-err" class="field-error" role="alert">{{ formErrors.secret }}</span>
            <span v-if="form.type === 'ssh_password'" class="field-hint">{{ t('settingsVault.hintSshPassword') }}</span>
            <span v-else class="field-hint">{{ t('settingsVault.hintSecret') }}</span>
          </div>

          <!-- Footer -->
          <div class="modal-footer">
            <button
              type="button"
              class="btn-secondary"
              :disabled="formSubmitting"
              @click="closeModal"
            >{{ t('settingsVault.cancel') }}</button>
            <button
              type="submit"
              class="btn-primary"
              :disabled="formSubmitting"
              :aria-busy="formSubmitting"
            >
              <span v-if="formSubmitting" class="spinner" aria-hidden="true" />
              {{ formSubmitting ? t('settingsVault.saving') : (modalMode === 'add' ? t('settingsVault.createCredential') : t('settingsVault.saveChanges')) }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </Teleport>

  <!-- ═══════════════════════════════════════════════════════════════════════
       Delete confirm modal
  ════════════════════════════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div
      v-if="deleteModalOpen && deletingCredential"
      class="modal-scrim"
      role="dialog"
      :aria-label="t('settingsVault.deleteConfirmAria')"
      aria-modal="true"
      @keydown.esc="closeDeleteModal"
      @click.self="closeDeleteModal"
    >
      <div class="modal modal--sm">
        <div class="modal-head">
          <div class="modal-icon modal-icon--danger" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
              <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
            </svg>
          </div>
          <div>
            <h3 class="modal-title">{{ t('settingsVault.deleteCredential') }}</h3>
            <p class="modal-sub">{{ t('settingsVault.deleteIrreversible') }}</p>
          </div>
          <button
            class="modal-close"
            :aria-label="t('settingsVault.closeDialog')"
            :disabled="deleteSubmitting"
            @click="closeDeleteModal"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M18 6 6 18M6 6l12 12"/>
            </svg>
          </button>
        </div>

        <div class="modal-body">
          <p class="delete-confirm-text">
            {{ t('settingsVault.deleteConfirmPrefix') }}
            <strong class="delete-cred-name">{{ deletingCredential.name }}</strong>
            {{ t('settingsVault.deleteConfirmSuffix') }}
          </p>
          <div
            v-if="deleteBanner"
            class="banner banner--error modal-banner"
            role="alert"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
            </svg>
            {{ deleteBanner }}
          </div>
        </div>

        <div class="modal-footer">
          <button
            type="button"
            class="btn-secondary"
            :disabled="deleteSubmitting"
            @click="closeDeleteModal"
          >{{ t('settingsVault.cancel') }}</button>
          <button
            type="button"
            class="btn-danger"
            :disabled="deleteSubmitting"
            :aria-busy="deleteSubmitting"
            @click="confirmDelete"
          >
            <span v-if="deleteSubmitting" class="spinner spinner--red" aria-hidden="true" />
            {{ deleteSubmitting ? t('settingsVault.deleting') : t('settingsVault.confirmDelete') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* ─── layout ──────────────────────────────────────────────────────────────── */
.vault-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

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

/* ─── OAuth connect panel ─────────────────────────────────────────────────── */
.connect-panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  padding: 16px 18px;
  animation: panel-in 0.45s var(--ease-out-expo) both;
}

.connect-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 13px;
}

.connect-head-text {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.connect-head-text strong {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-text);
}

.connect-head-text span {
  font-size: 0.78rem;
  color: var(--color-faint);
  line-height: 1.5;
}

.connect-config-link {
  flex-shrink: 0;
  font-size: 0.76rem;
  font-weight: 500;
  color: var(--color-primary);
  text-decoration: none;
  white-space: nowrap;
}

.connect-config-link:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}

.connect-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.connect-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 38px;
  padding: 0 16px 0 8px;
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  color: var(--color-text);
  font-family: var(--font-sans);
  font-size: 0.84rem;
  font-weight: 600;
  cursor: pointer;
  transition:
    border-color var(--duration-fast),
    transform var(--duration-fast),
    box-shadow var(--duration-fast);
}

.connect-btn:hover {
  border-color: var(--color-faint);
  transform: translateY(-1px);
  box-shadow: var(--shadow);
}

.connect-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.connect-logo {
  width: 26px;
  height: 26px;
  border-radius: var(--rounded-md);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.8rem;
  flex-shrink: 0;
}

/* ─── panel ───────────────────────────────────────────────────────────────── */
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: panel-in 0.45s var(--ease-out-expo) both;
}

.panel--loading {
  pointer-events: none;
}

@keyframes panel-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
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

.meta-warn {
  color: var(--color-amber);
}

/* ─── skeleton rows ───────────────────────────────────────────────────────── */
.skel-row {
  display: grid;
  grid-template-columns: 30px minmax(180px, 1.2fr) 100px 1fr 110px 80px;
  align-items: center;
  gap: 14px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
}

.skel-row:last-child {
  border-bottom: none;
}

.skel {
  display: block;
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.06) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: shimmer 1.4s ease-in-out infinite;
}

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

.skel--icon  { width: 30px; height: 30px; border-radius: var(--rounded); }
.skel--name  { height: 14px; width: 70%; }
.skel--tag   { height: 12px; width: 60px; }
.skel--scope { height: 12px; width: 80px; }
.skel--time  { height: 12px; width: 70px; }
.skel--ops   { height: 12px; width: 60px; }

/* ─── empty state ─────────────────────────────────────────────────────────── */
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
  max-width: 42ch;
  line-height: 1.55;
  margin-bottom: 6px;
}

/* ─── credential table ────────────────────────────────────────────────────── */
.cred-row {
  display: grid;
  grid-template-columns: 30px minmax(200px, 1.4fr) 100px 1fr 120px 96px;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}

.cred-row:last-child {
  border-bottom: none;
}

.cred-row:not(.cred-row--head):hover {
  background: var(--color-inset);
}

.cred-row--head {
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

.cred-row--idle {
  background: linear-gradient(90deg, oklch(83% 0.13 82 / 0.05), transparent 60%);
}

.cred-row--idle:hover {
  background: linear-gradient(90deg, oklch(83% 0.13 82 / 0.08), var(--color-inset) 60%);
}

/* ─── type icon ───────────────────────────────────────────────────────────── */
.type-icon {
  width: 30px;
  height: 30px;
  border-radius: var(--rounded);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.type-icon--git_token {
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}

.type-icon--ssh_key {
  background: var(--color-green-soft);
  color: var(--color-green);
}

.type-icon--registry {
  background: var(--color-amber-soft);
  color: var(--color-amber);
}

/* ─── credential cells ────────────────────────────────────────────────────── */
.cred-name {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.cred-label {
  font-size: 0.85rem;
  font-weight: 500;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Masked value — JetBrains Mono, intentionally non-interactive */
.cred-mask {
  font-size: 0.72rem;
  color: var(--color-faint);
  letter-spacing: 0.05em;
  line-height: 1.4;
  user-select: none;
  cursor: default;
}

.mono {
  font-family: var(--font-mono);
}

.cred-type {
  font-size: 0.74rem;
  color: var(--color-dim);
}

.cred-scope {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}

.scope-tag {
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--color-dim);
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 6px;
  padding: 2px 7px;
  white-space: nowrap;
}

.scope-tag--all {
  color: var(--color-cyan);
  border-color: var(--color-cyan-line);
}

.cred-dim {
  color: var(--color-faint);
  font-size: 0.8rem;
}

.cred-time {
  font-size: 0.74rem;
  color: var(--color-faint);
  white-space: nowrap;
  display: flex;
  align-items: center;
  gap: 5px;
}

.cred-time--warn {
  color: var(--color-amber);
}

.idle-tag {
  font-size: 0.65rem;
  font-weight: 600;
  background: var(--color-amber-soft);
  color: var(--color-amber);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded-md);
  padding: 1px 5px;
}

/* ─── action buttons ──────────────────────────────────────────────────────── */
.cred-ops {
  display: flex;
  justify-content: flex-end;
  gap: 5px;
}

.op-btn {
  width: 28px;
  height: 26px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition:
    color var(--duration-fast),
    border-color var(--duration-fast),
    background-color var(--duration-fast),
    transform var(--duration-fast);
}

.op-btn:hover {
  color: var(--color-text);
  border-color: var(--color-faint);
}

.op-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.op-btn--copied {
  color: var(--color-green);
  border-color: var(--color-green-line);
}

.op-btn--danger:hover {
  color: var(--color-red);
  border-color: var(--color-red-line);
  background: var(--color-red-soft);
}

/* ─── banner ──────────────────────────────────────────────────────────────── */
.banner {
  display: flex;
  align-items: flex-start;
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

.banner-retry:hover {
  opacity: 0.8;
}

/* ─── buttons ─────────────────────────────────────────────────────────────── */
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
    transform var(--duration-fast),
    box-shadow var(--duration-fast);
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
  transition:
    border-color var(--duration-fast),
    background-color var(--duration-fast);
}

.btn-secondary:hover:not(:disabled) {
  border-color: var(--color-faint);
}

.btn-secondary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
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
  transition:
    background-color var(--duration-fast),
    transform var(--duration-fast);
}

.btn-danger:hover:not(:disabled) {
  background: oklch(62% 0.18 22 / 0.25);
  transform: translateY(-1px);
}

.btn-danger:focus-visible {
  outline: 2px solid var(--color-red);
  outline-offset: 2px;
}

.btn-danger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none;
}

/* ─── modal ───────────────────────────────────────────────────────────────── */
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
  to   { opacity: 1; }
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
  to   { opacity: 1; transform: none; }
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

.modal-close:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
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

/* ─── form fields ─────────────────────────────────────────────────────────── */
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

.field-optional {
  font-weight: 400;
  color: var(--color-faint);
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
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
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

/* Mono font for the secret input */
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

/* ─── segmented control (type selector) ───────────────────────────────────── */
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
  transition:
    color var(--duration-fast),
    background-color var(--duration-fast),
    box-shadow var(--duration-fast);
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

/* ─── delete confirm modal body ───────────────────────────────────────────── */
.delete-confirm-text {
  font-size: 0.86rem;
  color: var(--color-dim);
  line-height: 1.55;
}

.delete-cred-name {
  color: var(--color-text);
  font-weight: 600;
}

/* ─── spinner ─────────────────────────────────────────────────────────────── */
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

/* ─── reduced motion ──────────────────────────────────────────────────────── */
@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 0.01ms !important;
  }
}
</style>

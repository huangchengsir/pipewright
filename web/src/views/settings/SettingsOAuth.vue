<script setup lang="ts">
/**
 * SettingsOAuth — OAuth application configuration for git providers.
 *
 * One config card per provider (Gitee / GitHub / GitLab / 自建):
 *   - clientId text input
 *   - clientSecret password input — WRITE-ONLY, never echoed; placeholder
 *     hints "留空保留已存" when a secret is already configured
 *   - baseUrl (only the self-hosted `custom` provider)
 *   - enabled toggle
 *   - Save (PUT) → toast "OAuth 应用已保存" + masked secret echo (mirrors SettingsAI)
 *
 * Once a provider is enabled, users connect a git account from the vault page
 * (SettingsVault) where the "连接" buttons live and the callback is handled.
 *
 * Reuses: AppButton tokens + vault/AI patterns. No new UI libraries.
 * Animation: transform/opacity + prefers-reduced-motion.
 */
import { reactive, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from '../../components/ui/AppButton.vue'
import { useToast } from '../../composables/useToast'
import { HttpError } from '../../api/http'
import {
  getOAuthApps,
  saveOAuthApp,
  type OAuthApp,
  type OAuthProvider,
  type SaveOAuthAppInput,
} from '../../api/oauth'

// ─── types ───────────────────────────────────────────────────────────────────

type LoadState = 'loading' | 'ready' | 'error'

interface ProviderMeta {
  id: OAuthProvider
  label: string
  desc: string
  logoText: string
  logoStyle: string
  /** Self-hosted provider exposes a base URL field. */
  hasBaseUrl: boolean
}

/** Per-provider editable form + saved snapshot. */
interface CardState {
  meta: ProviderMeta
  saved: OAuthApp | null
  clientId: string
  clientSecret: string // write-only: blank = keep existing
  baseUrl: string
  enabled: boolean
  errors: { clientId: string; baseUrl: string }
  saving: boolean
}

// ─── provider metadata ───────────────────────────────────────────────────────

// `label` / `desc` are resolved via i18n at render time (see labelFor / descFor).
const PROVIDERS: ProviderMeta[] = [
  {
    id: 'gitee',
    label: 'Gitee',
    desc: '',
    logoText: 'G',
    logoStyle: 'background:#c71d23;color:#fff',
    hasBaseUrl: false,
  },
  {
    id: 'github',
    label: 'GitHub',
    desc: 'github.com',
    logoText: '',
    logoStyle: 'background:#1f2328;color:#fff',
    hasBaseUrl: false,
  },
  {
    id: 'gitlab',
    label: 'GitLab',
    desc: 'gitlab.com',
    logoText: '',
    logoStyle: 'background:#fc6d26;color:#fff',
    hasBaseUrl: false,
  },
  {
    id: 'custom',
    label: '',
    desc: '',
    logoText: '⚙',
    logoStyle: 'background:var(--color-inset);color:var(--color-dim)',
    hasBaseUrl: true,
  },
]

// ─── i18n + toast ──────────────────────────────────────────────────────────────

const { t } = useI18n()
const toast = useToast()

/** Provider display label — brand names kept verbatim, `custom` localized. */
function labelFor(id: OAuthProvider): string {
  return id === 'custom' ? t('settingsOAuth.providerCustomLabel') : cards[id].meta.label
}

/** Provider sub-description — `gitee` / `custom` localized, others verbatim. */
function descFor(id: OAuthProvider): string {
  if (id === 'gitee') return t('settingsOAuth.providerGiteeDesc')
  if (id === 'custom') return t('settingsOAuth.providerCustomDesc')
  return cards[id].meta.desc
}

// ─── load state ───────────────────────────────────────────────────────────────

const loadState = ref<LoadState>('loading')
const loadError = ref('')

// ─── per-provider card state ──────────────────────────────────────────────────

const cards = reactive<Record<OAuthProvider, CardState>>(
  Object.fromEntries(
    PROVIDERS.map((meta) => [
      meta.id,
      {
        meta,
        saved: null,
        clientId: '',
        clientSecret: '',
        baseUrl: '',
        enabled: false,
        errors: { clientId: '', baseUrl: '' },
        saving: false,
      } satisfies CardState,
    ]),
  ) as Record<OAuthProvider, CardState>,
)

const cardList = PROVIDERS.map((m) => m.id)

// ─── load ─────────────────────────────────────────────────────────────────────

async function loadApps(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const apps = await getOAuthApps()
    for (const app of apps) {
      const card = cards[app.provider]
      if (card) applyApp(card, app)
    }
    loadState.value = 'ready'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = t('settingsOAuth.errNoServer')
      } else if (err.apiError?.code === 'vault_unconfigured') {
        loadError.value = t('settingsOAuth.errVaultUnconfigured')
      } else {
        loadError.value = err.apiError?.message ?? t('settingsOAuth.errLoadStatus', { status: err.status })
      }
    } else {
      loadError.value = t('settingsOAuth.errLoadGeneric')
    }
    loadState.value = 'error'
  }
}

function applyApp(card: CardState, app: OAuthApp): void {
  card.saved = app
  card.clientId = app.clientId
  card.clientSecret = '' // never pre-fill the secret
  card.baseUrl = app.baseUrl
  card.enabled = app.enabled
  card.errors = { clientId: '', baseUrl: '' }
}

onMounted(loadApps)

// ─── save ─────────────────────────────────────────────────────────────────────

async function handleSave(provider: OAuthProvider): Promise<void> {
  const card = cards[provider]
  card.errors = { clientId: '', baseUrl: '' }

  // Client-side validation: an enabled provider must have a clientId, and a
  // secret must exist (either already configured or freshly entered).
  const clientId = card.clientId.trim()
  const baseUrl = card.baseUrl.trim()
  const hasExistingSecret = !!card.saved?.maskedSecret

  if (card.enabled && !clientId) {
    card.errors.clientId = t('settingsOAuth.errClientIdRequiredEnabled')
    return
  }
  if (card.enabled && !card.clientSecret && !hasExistingSecret) {
    card.errors.clientId = card.errors.clientId || ''
    toast.error(t('settingsOAuth.toastSaveFailed'), { detail: t('settingsOAuth.errSecretRequiredEnabled') })
    return
  }
  if (card.meta.hasBaseUrl && card.enabled && !baseUrl) {
    card.errors.baseUrl = t('settingsOAuth.errBaseUrlRequiredCustom')
    return
  }

  card.saving = true
  try {
    const payload: SaveOAuthAppInput = {
      clientId,
      ...(card.clientSecret ? { clientSecret: card.clientSecret } : {}),
      ...(card.meta.hasBaseUrl ? { baseUrl } : {}),
      enabled: card.enabled,
    }
    const updated = await saveOAuthApp(provider, payload)
    applyApp(card, updated)
    toast.success(t('settingsOAuth.toastSaved'), { detail: labelFor(provider) })
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 422 && err.apiError) {
        const code = err.apiError.code
        if (code === 'client_id_required') {
          card.errors.clientId = t('settingsOAuth.errClientIdRequired')
        } else if (code === 'base_url_required') {
          card.errors.baseUrl = t('settingsOAuth.errBaseUrlRequired')
        } else {
          toast.error(t('settingsOAuth.toastSaveFailed'), { detail: err.apiError.message })
        }
        return
      }
      if (err.status === 0) {
        toast.error(t('settingsOAuth.toastSaveFailed'), { detail: t('settingsOAuth.errNoServerShort') })
      } else {
        toast.error(t('settingsOAuth.toastSaveFailed'), { detail: err.apiError?.message ?? `HTTP ${err.status}` })
      }
    } else {
      toast.error(t('settingsOAuth.toastSaveFailed'), { detail: err instanceof Error ? err.message : t('settingsOAuth.unknownError') })
    }
  } finally {
    card.saving = false
  }
}

// ─── helpers ─────────────────────────────────────────────────────────────────

function relativeTime(iso: string | null): string {
  if (!iso) return ''
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return t('settingsOAuth.justNow')
  const m = Math.floor(s / 60)
  if (m < 60) return t('settingsOAuth.minutesAgo', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('settingsOAuth.hoursAgo', { n: h })
  return t('settingsOAuth.daysAgo', { n: Math.floor(h / 24) })
}
</script>

<template>
  <div class="oauth-root">
    <!-- ─── section header ──────────────────────────────────────────────────── -->
    <div class="section-head">
      <div class="section-head-text">
        <h2 class="section-title">{{ t('settingsOAuth.title') }}</h2>
        <p class="section-desc">
          {{ t('settingsOAuth.sectionDesc') }}
        </p>
      </div>
    </div>

    <!-- ─── load error ──────────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadApps">↻ {{ t('settingsOAuth.retry') }}</button>
    </div>

    <!-- ─── loading skeleton ────────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div v-for="i in 4" :key="i" class="panel panel--anim">
        <div class="panel-head">
          <span class="skel skel--text" style="width:120px" aria-hidden="true" />
        </div>
        <div class="panel-body">
          <div class="skel skel--field" style="height:38px" aria-hidden="true" />
          <div class="skel skel--field" style="height:38px;margin-top:14px" aria-hidden="true" />
        </div>
      </div>
    </template>

    <!-- ─── provider cards ──────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'ready'">
      <div
        v-for="pid in cardList"
        :key="pid"
        class="panel panel--anim"
        :data-provider="pid"
      >
        <div class="panel-head">
          <div class="ph-logo" :style="cards[pid].meta.logoStyle" aria-hidden="true">
            {{ cards[pid].meta.logoText }}
          </div>
          <span class="ph-label">{{ labelFor(pid) }}</span>
          <span class="ph-desc">{{ descFor(pid) }}</span>

          <span
            v-if="cards[pid].saved?.configured"
            class="status-chip"
            :class="cards[pid].enabled ? 'status-chip--ok' : 'status-chip--idle'"
          >
            <span
              class="status-dot"
              :class="{ 'status-dot--ok': cards[pid].enabled }"
              aria-hidden="true"
            />
            {{ cards[pid].enabled ? t('settingsOAuth.statusEnabled') : t('settingsOAuth.statusConfiguredIdle') }}
          </span>
          <span v-else class="status-chip status-chip--idle">
            <span class="status-dot" aria-hidden="true" />
            {{ t('settingsOAuth.statusUnconfigured') }}
          </span>
        </div>

        <div class="panel-body">
          <form class="config-form" novalidate @submit.prevent="handleSave(pid)">
            <!-- Client ID -->
            <div class="field">
              <label class="field-label" :for="`oauth-clientid-${pid}`">Client ID</label>
              <input
                :id="`oauth-clientid-${pid}`"
                v-model="cards[pid].clientId"
                type="text"
                class="field-input field-input--mono"
                :class="{ 'field-input--error': cards[pid].errors.clientId }"
                :placeholder="t('settingsOAuth.clientIdPlaceholder')"
                autocomplete="off"
                :aria-invalid="!!cards[pid].errors.clientId || undefined"
                :disabled="cards[pid].saving"
                @input="cards[pid].errors.clientId = ''"
              />
              <span v-if="cards[pid].errors.clientId" class="field-error" role="alert">
                {{ cards[pid].errors.clientId }}
              </span>
            </div>

            <!-- Client Secret — write-only -->
            <div class="field">
              <label class="field-label" :for="`oauth-secret-${pid}`">
                Client Secret
                <span class="field-optional">{{ t('settingsOAuth.secretOptional') }}</span>
              </label>
              <input
                :id="`oauth-secret-${pid}`"
                v-model="cards[pid].clientSecret"
                type="password"
                class="field-input field-input--mono"
                :placeholder="cards[pid].saved?.maskedSecret
                  ? t('settingsOAuth.secretPlaceholderKeep')
                  : t('settingsOAuth.secretPlaceholderNew')"
                autocomplete="new-password"
                :disabled="cards[pid].saving"
              />
              <span
                v-if="cards[pid].saved?.maskedSecret && !cards[pid].clientSecret"
                class="field-hint mono"
              >{{ t('settingsOAuth.secretStored', { masked: cards[pid].saved?.maskedSecret }) }}</span>
            </div>

            <!-- Base URL — self-hosted only -->
            <div v-if="cards[pid].meta.hasBaseUrl" class="field">
              <label class="field-label" :for="`oauth-baseurl-${pid}`">Base URL</label>
              <input
                :id="`oauth-baseurl-${pid}`"
                v-model="cards[pid].baseUrl"
                type="url"
                class="field-input field-input--mono"
                :class="{ 'field-input--error': cards[pid].errors.baseUrl }"
                placeholder="https://git.example.com"
                autocomplete="off"
                :aria-invalid="!!cards[pid].errors.baseUrl || undefined"
                :disabled="cards[pid].saving"
                @input="cards[pid].errors.baseUrl = ''"
              />
              <span v-if="cards[pid].errors.baseUrl" class="field-error" role="alert">
                {{ cards[pid].errors.baseUrl }}
              </span>
            </div>

            <!-- Enabled toggle + save -->
            <div class="card-footer">
              <div class="toggle-row">
                <button
                  type="button"
                  class="toggle-track"
                  :class="{ 'toggle-track--on': cards[pid].enabled }"
                  role="switch"
                  :aria-checked="cards[pid].enabled"
                  :aria-label="t('settingsOAuth.toggleAria', { provider: labelFor(pid) })"
                  :disabled="cards[pid].saving"
                  @click="cards[pid].enabled = !cards[pid].enabled"
                >
                  <span class="toggle-thumb" />
                </button>
                <div class="toggle-label">
                  <strong>{{ t('settingsOAuth.toggleTitle') }}</strong>
                  <span>{{ t('settingsOAuth.toggleDesc') }}</span>
                </div>
              </div>

              <div class="card-actions">
                <span v-if="cards[pid].saved?.updatedAt" class="updated-note">
                  {{ t('settingsOAuth.lastSaved', { time: relativeTime(cards[pid].saved?.updatedAt ?? null) }) }}
                </span>
                <AppButton
                  variant="primary"
                  type="submit"
                  :loading="cards[pid].saving"
                >
                  {{ t('settingsOAuth.saveBtn') }}
                </AppButton>
              </div>
            </div>
          </form>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* ─── layout ──────────────────────────────────────────────────────────────── */
.oauth-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── section header ──────────────────────────────────────────────────────── */
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
  max-width: 70ch;
  line-height: 1.55;
}

/* ─── status chip ─────────────────────────────────────────────────────────── */
.status-chip {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-radius: var(--rounded-full);
  font-size: 0.72rem;
  font-weight: 600;
  border: 1px solid;
  white-space: nowrap;
  flex-shrink: 0;
}

.status-chip--ok {
  background: var(--color-green-soft);
  color: var(--color-green);
  border-color: var(--color-green-line);
}

.status-chip--idle {
  background: var(--color-inset);
  color: var(--color-faint);
  border-color: var(--color-border);
}

.status-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-faint);
  flex-shrink: 0;
}

.status-dot--ok {
  background: var(--color-green);
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

.banner-retry:hover { opacity: 0.8; }

/* ─── panel ───────────────────────────────────────────────────────────────── */
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
}

.panel--anim {
  animation: panel-in 0.45s var(--ease-out-expo) both;
}

@keyframes panel-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
}

.ph-logo {
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-md);
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-weight: 700;
  font-size: 0.86rem;
  flex-shrink: 0;
}

.ph-label {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-text);
}

.ph-desc {
  font-size: 0.74rem;
  color: var(--color-faint);
}

.panel-body {
  padding: 18px;
}

/* ─── skeleton ────────────────────────────────────────────────────────────── */
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

.skel--text { height: 14px; }
.skel--field { width: 100%; }

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

/* ─── config form ─────────────────────────────────────────────────────────── */
.config-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
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

.field-input--mono {
  font-family: var(--font-mono);
  font-size: 0.8rem;
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
  border-color: var(--color-red) !important;
}

.field-input--error:focus {
  box-shadow: 0 0 0 3px var(--color-red-soft) !important;
}

.field-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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

.mono {
  font-family: var(--font-mono);
  letter-spacing: 0.04em;
  user-select: none;
}

/* ─── card footer ─────────────────────────────────────────────────────────── */
.card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  border-top: 1px solid var(--color-border);
  padding-top: 16px;
}

.card-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.updated-note {
  font-size: 0.72rem;
  color: var(--color-faint);
}

/* ─── toggle ──────────────────────────────────────────────────────────────── */
.toggle-row {
  display: flex;
  align-items: flex-start;
  gap: 11px;
}

.toggle-track {
  position: relative;
  width: 40px;
  height: 22px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  border: none;
  cursor: pointer;
  flex-shrink: 0;
  margin-top: 1px;
  transition: background-color var(--duration-normal);
}

.toggle-track:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.toggle-track:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.toggle-track--on {
  background: var(--color-primary);
}

.toggle-thumb {
  position: absolute;
  top: 3px;
  left: 3px;
  width: 16px;
  height: 16px;
  border-radius: var(--rounded-full);
  background: #fff;
  transition: transform var(--duration-normal) var(--ease-out-expo);
  pointer-events: none;
}

.toggle-track--on .toggle-thumb {
  transform: translateX(18px);
}

.toggle-label {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.toggle-label strong {
  font-size: 0.82rem;
  font-weight: 500;
  color: var(--color-text);
}

.toggle-label span {
  font-size: 0.74rem;
  color: var(--color-faint);
  line-height: 1.45;
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

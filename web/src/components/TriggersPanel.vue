<script setup lang="ts">
/**
 * TriggersPanel — extracted trigger config form.
 * Accepts a projectId prop and renders the full trigger configuration UI
 * (Webhook, events, branch mappings, unmatched policy, save bar).
 *
 * Used by:
 *   - ProjectTriggers.vue  (standalone page at /projects/:id/triggers)
 *   - ProjectPipeline.vue  (embedded in the "触发设置" tab)
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getTrigger,
  saveTrigger,
  resetSecret,
  type TriggerConfig,
  type BranchMapping,
  type UnmatchedPolicy,
} from '../api/triggers'
import { HttpError } from '../api/http'
import CronPanel from './CronPanel.vue'
import EnvironmentsPanel from './EnvironmentsPanel.vue'
import ConcurrencyPanel from './ConcurrencyPanel.vue'
import ParametersPanel from './ParametersPanel.vue'
import ChainPanel from './ChainPanel.vue'
import RunnerPanel from './RunnerPanel.vue'

// ─── Props ────────────────────────────────────────────────────────────────────

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()

// ─── Load state ───────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')

// ─── Trigger data (from server) ───────────────────────────────────────────────

const webhookUrl = ref('')
const webhookSecretMasked = ref('')

const eventPush = ref(false)
const eventTag = ref(false)
const eventPullRequest = ref(false)
const eventRelease = ref(false)

interface MappingRow {
  _key: number
  id?: string
  branchPattern: string
  environment: string
  targetServerIds: string[]
  patternError: string
}

let _keySeq = 0
const mappings = ref<MappingRow[]>([])
const unmatchedPolicy = ref<UnmatchedPolicy>('record')

// 路径过滤(monorepo · P0):多行 glob 文本框;每行一条,空行忽略。
// 仅当本次 push 改动文件匹配任一 glob 才触发(拿不到改动文件列表时放行,诚实降级)。
const pathFiltersText = ref('')

/** 解析多行 glob 文本为去空、trim 后的数组(去重保序)。 */
function parsePathFilters(text: string): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of text.split('\n')) {
    const p = raw.trim()
    if (p && !seen.has(p)) {
      seen.add(p)
      out.push(p)
    }
  }
  return out
}

// ─── Copy state ───────────────────────────────────────────────────────────────

type CopyTarget = 'url' | 'secret'
const copiedTarget = ref<CopyTarget | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | null = null

function clearCopyFeedback(): void {
  if (copyTimer) {
    clearTimeout(copyTimer)
    copyTimer = null
  }
  copiedTarget.value = null
}

async function copyText(text: string, target: CopyTarget): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    clearCopyFeedback()
    copiedTarget.value = target
    copyTimer = setTimeout(() => { copiedTarget.value = null }, 2200)
  } catch {
    const el = document.createElement('textarea')
    el.value = text
    el.style.position = 'fixed'
    el.style.opacity = '0'
    document.body.appendChild(el)
    el.focus()
    el.select()
    try {
      document.execCommand('copy')
      clearCopyFeedback()
      copiedTarget.value = target
      copyTimer = setTimeout(() => { copiedTarget.value = null }, 2200)
    } finally {
      document.body.removeChild(el)
    }
  }
}

const revealedSecret = ref<string | null>(null)

function copyWebhookUrl(): void { void copyText(webhookUrl.value, 'url') }
function copySecret(): void {
  if (revealedSecret.value) void copyText(revealedSecret.value, 'secret')
}

// ─── Reset secret modal ───────────────────────────────────────────────────────

const resetModalOpen = ref(false)
const resetSubmitting = ref(false)
const resetBanner = ref('')

function openResetModal(): void {
  resetBanner.value = ''
  revealedSecret.value = null
  resetModalOpen.value = true
}

function closeResetModal(): void {
  if (resetSubmitting.value) return
  resetModalOpen.value = false
  resetBanner.value = ''
}

async function confirmReset(): Promise<void> {
  resetSubmitting.value = true
  resetBanner.value = ''
  try {
    const result = await resetSecret(props.projectId)
    webhookSecretMasked.value = result.webhookSecretMasked
    revealedSecret.value = result.webhookSecret
    resetModalOpen.value = false
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        resetBanner.value = t('projectPanels.triggers.errConnect')
      } else if (err.apiError?.code === 'vault_unconfigured') {
        resetBanner.value = t('projectPanels.triggers.errVaultUnconfigured')
      } else {
        resetBanner.value = err.apiError?.message ?? t('projectPanels.triggers.errResetFailed', { status: err.status })
      }
    } else {
      resetBanner.value = t('projectPanels.triggers.errResetGeneric')
    }
  } finally {
    resetSubmitting.value = false
  }
}

// ─── Branch mapping helpers ───────────────────────────────────────────────────

function validatePattern(pattern: string): string {
  const p = pattern.trim()
  if (!p) return t('projectPanels.triggers.patternEmpty')
  if (p.startsWith('/') || p.endsWith('/')) return t('projectPanels.triggers.patternSlash')
  if (p.includes('//')) return t('projectPanels.triggers.patternDoubleSlash')
  if (!/^[\w\-./*?[\]]+$/.test(p)) return t('projectPanels.triggers.patternIllegal')
  return ''
}

function addRow(): void {
  mappings.value.push({ _key: ++_keySeq, branchPattern: '', environment: '', targetServerIds: [], patternError: '' })
}

function removeRow(key: number): void {
  mappings.value = mappings.value.filter((r) => r._key !== key)
}

function onPatternInput(row: MappingRow): void {
  if (row.patternError) row.patternError = validatePattern(row.branchPattern)
}

// ─── Save ─────────────────────────────────────────────────────────────────────

const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)
let saveSuccessTimer: ReturnType<typeof setTimeout> | null = null

function showSaveSuccess(): void {
  saveSuccess.value = true
  if (saveSuccessTimer) clearTimeout(saveSuccessTimer)
  saveSuccessTimer = setTimeout(() => { saveSuccess.value = false }, 3200)
}

async function handleSave(): Promise<void> {
  let hasError = false
  for (const row of mappings.value) {
    row.patternError = validatePattern(row.branchPattern)
    if (row.patternError) hasError = true
  }
  if (hasError) return

  saveSubmitting.value = true
  saveBanner.value = ''
  saveSuccess.value = false

  try {
    const updated = await saveTrigger(props.projectId, {
      events: { push: eventPush.value, tag: eventTag.value, pullRequest: eventPullRequest.value, release: eventRelease.value },
      branchMappings: mappings.value.map((r) => ({
        branchPattern: r.branchPattern.trim(),
        environment:   r.environment.trim(),
        targetServerIds: r.targetServerIds,
      })),
      unmatchedPolicy: unmatchedPolicy.value,
      pathFilters: parsePathFilters(pathFiltersText.value),
    })
    applyConfig(updated)
    showSaveSuccess()
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        saveBanner.value = t('projectPanels.triggers.errSaveConnect')
      } else if (err.status === 400 || err.status === 422) {
        saveBanner.value = err.apiError?.message ?? t('projectPanels.triggers.errSaveInvalid', { status: err.status })
      } else {
        saveBanner.value = err.apiError?.message ?? t('projectPanels.triggers.errSaveFailed', { status: err.status })
      }
    } else {
      saveBanner.value = t('projectPanels.triggers.errSaveGeneric')
    }
  } finally {
    saveSubmitting.value = false
  }
}

// ─── Data loading ─────────────────────────────────────────────────────────────

function applyConfig(config: TriggerConfig): void {
  webhookUrl.value            = config.webhookUrl
  webhookSecretMasked.value   = config.webhookSecretMasked
  eventPush.value             = config.events.push
  eventTag.value              = config.events.tag
  eventPullRequest.value      = config.events.pullRequest
  eventRelease.value          = config.events.release ?? false
  unmatchedPolicy.value       = config.unmatchedPolicy
  pathFiltersText.value       = (config.pathFilters ?? []).join('\n')
  mappings.value = config.branchMappings.map((m) => ({
    _key: ++_keySeq,
    id: m.id,
    branchPattern: m.branchPattern,
    environment: m.environment,
    targetServerIds: m.targetServerIds,
    patternError: '',
  }))
}

async function loadTrigger(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  revealedSecret.value = null

  try {
    const config = await getTrigger(props.projectId)
    applyConfig(config)
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      if (err.status === 0) {
        loadError.value = t('projectPanels.triggers.errSaveConnect')
      } else if (err.status === 404) {
        loadError.value = t('projectPanels.triggers.errLoadNotFound')
      } else {
        loadError.value = err.apiError?.message ?? t('projectPanels.triggers.errLoadFailed', { status: err.status })
      }
    } else {
      loadError.value = t('projectPanels.triggers.errLoadGeneric')
    }
    loadState.value = 'error'
  }
}

// Reload when projectId changes (e.g. panel embedded in tab shell)
watch(() => props.projectId, loadTrigger)
onMounted(loadTrigger)

// ─── Disclosed webhook URL ────────────────────────────────────────────────────

const displayWebhookUrl = computed(() => {
  if (!webhookUrl.value) return ''
  if (webhookUrl.value.startsWith('/')) return window.location.origin + webhookUrl.value
  return webhookUrl.value
})
</script>

<template>
  <div class="triggers-panel">
    <!-- ─── Load error ───────────────────────────────────────────────────── -->
    <div v-if="loadState === 'error'" class="banner banner--error" role="alert">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
      </svg>
      <span>{{ loadError }}</span>
      <button class="banner-retry" @click="loadTrigger">{{ t('projectPanels.triggers.retry') }}</button>
    </div>

    <!-- ─── Loading skeleton ─────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="skel-stack" aria-busy="true" :aria-label="t('projectPanels.triggers.loading')">
        <div class="skel-card" aria-hidden="true">
          <div class="skel skel--title"/><div class="skel skel--row"/><div class="skel skel--row"/>
        </div>
        <div class="skel-card" aria-hidden="true">
          <div class="skel skel--title"/><div class="skel skel--row"/><div class="skel skel--row"/><div class="skel skel--row"/>
        </div>
      </div>
    </template>

    <!-- ─── Main config sections ─────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle'">
      <!-- Save error banner -->
      <div v-if="saveBanner" class="banner banner--error" role="alert" aria-live="assertive">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
        </svg>
        <span>{{ saveBanner }}</span>
      </div>

      <!-- Save success banner -->
      <div v-if="saveSuccess" class="banner banner--success" role="status" aria-live="polite">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M20 6 9 17l-5-5"/>
        </svg>
        <span>{{ t('projectPanels.triggers.savedToast') }}</span>
      </div>

      <!-- ═══ Section 1: Webhook ═══════════════════════════════════════════ -->
      <section class="config-card" aria-labelledby="tp-webhook-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M18 2a3 3 0 0 0-3 3c0 .8.3 1.5.9 2L13 12M6.5 12a3 3 0 1 0 3 3M12 20a3 3 0 1 0 0-6"/>
            </svg>
          </span>
          <h2 id="tp-webhook-heading" class="card-title">{{ t('projectPanels.triggers.webhookTitle') }}</h2>
          <span class="card-sub">{{ t('projectPanels.triggers.webhookSub') }}</span>
        </div>
        <div class="card-body">
          <div class="webhook-row">
            <span class="wk-label">{{ t('projectPanels.triggers.endpointLabel') }}</span>
            <div class="url-box">
              <span class="url-text mono" :title="displayWebhookUrl">{{ displayWebhookUrl }}</span>
              <button
                class="url-action-btn"
                :class="{ 'url-action-btn--copied': copiedTarget === 'url' }"
                :aria-label="copiedTarget === 'url' ? t('projectPanels.triggers.copied') : t('projectPanels.triggers.copyUrlAria')"
                @click="copyWebhookUrl"
              >
                <template v-if="copiedTarget === 'url'">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>
                  {{ t('projectPanels.triggers.copied') }}
                </template>
                <template v-else>
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                  {{ t('projectPanels.triggers.copy') }}
                </template>
              </button>
            </div>
          </div>
          <div class="webhook-row">
            <span class="wk-label">{{ t('projectPanels.triggers.secretLabel') }}</span>
            <div class="secret-wrap">
              <span class="secret-masked mono" :aria-label="t('projectPanels.triggers.secretMaskedAria')">{{ webhookSecretMasked }}</span>
              <div v-if="revealedSecret" class="secret-reveal" role="status" aria-live="polite" :aria-label="t('projectPanels.triggers.secretRevealAria')">
                <span class="secret-reveal-label">{{ t('projectPanels.triggers.secretNewLabel') }}</span>
                <span class="secret-reveal-value mono">{{ revealedSecret }}</span>
                <button
                  class="btn-mini"
                  :class="{ 'btn-mini--copied': copiedTarget === 'secret' }"
                  :aria-label="copiedTarget === 'secret' ? t('projectPanels.triggers.copied') : t('projectPanels.triggers.copyNewSecretAria')"
                  @click="copySecret"
                >
                  <template v-if="copiedTarget === 'secret'">
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>{{ t('projectPanels.triggers.copied') }}
                  </template>
                  <template v-else>
                    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>{{ t('projectPanels.triggers.copySecret') }}
                  </template>
                </button>
              </div>
              <button class="btn-mini" :disabled="resetSubmitting" :aria-label="t('projectPanels.triggers.resetSecretAria')" @click="openResetModal">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>
                </svg>
                {{ t('projectPanels.triggers.reset') }}
              </button>
            </div>
          </div>
          <div class="webhook-row">
            <span class="wk-label">{{ t('projectPanels.triggers.pushConfigLabel') }}</span>
            <p class="wk-hint">{{ t('projectPanels.triggers.pushConfigHint') }}</p>
          </div>
        </div>
      </section>

      <!-- ═══ Section 2: Trigger events ════════════════════════════════════ -->
      <section class="config-card" aria-labelledby="tp-events-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M13 2 3 14h7l-1 8 10-12h-7z"/>
            </svg>
          </span>
          <h2 id="tp-events-heading" class="card-title">{{ t('projectPanels.triggers.eventsTitle') }}</h2>
          <span class="card-sub">{{ t('projectPanels.triggers.eventsSub') }}</span>
        </div>
        <div class="card-body">
          <div class="event-row">
            <button class="ev-toggle" :class="{ 'ev-toggle--on': eventPush }" role="switch" :aria-checked="eventPush" :aria-label="t('projectPanels.triggers.pushAria')" @click="eventPush = !eventPush">
              <span class="ev-toggle-knob" aria-hidden="true"/>
            </button>
            <div class="ev-text"><span class="ev-name">Push</span><span class="ev-desc">{{ t('projectPanels.triggers.pushDesc') }}</span></div>
            <span class="ev-code mono">push</span>
          </div>
          <div class="event-row">
            <button class="ev-toggle" :class="{ 'ev-toggle--on': eventTag }" role="switch" :aria-checked="eventTag" :aria-label="t('projectPanels.triggers.tagAria')" @click="eventTag = !eventTag">
              <span class="ev-toggle-knob" aria-hidden="true"/>
            </button>
            <div class="ev-text"><span class="ev-name">Tag</span><span class="ev-desc">{{ t('projectPanels.triggers.tagDesc') }}</span></div>
            <span class="ev-code mono">tag</span>
          </div>
          <div class="event-row">
            <button class="ev-toggle" :class="{ 'ev-toggle--on': eventRelease }" role="switch" :aria-checked="eventRelease" :aria-label="t('projectPanels.triggers.releaseAria')" @click="eventRelease = !eventRelease">
              <span class="ev-toggle-knob" aria-hidden="true"/>
            </button>
            <div class="ev-text"><span class="ev-name">Release</span><span class="ev-desc">{{ t('projectPanels.triggers.releaseDesc') }}</span></div>
            <span class="ev-code mono">release</span>
          </div>
          <div class="event-row">
            <button class="ev-toggle" :class="{ 'ev-toggle--on': eventPullRequest }" role="switch" :aria-checked="eventPullRequest" :aria-label="t('projectPanels.triggers.prAria')" @click="eventPullRequest = !eventPullRequest">
              <span class="ev-toggle-knob" aria-hidden="true"/>
            </button>
            <div class="ev-text"><span class="ev-name">{{ t('projectPanels.triggers.prName') }}</span><span class="ev-desc">{{ t('projectPanels.triggers.prDesc') }}</span></div>
            <span class="ev-code mono">pull_request</span>
          </div>
          <div class="event-row event-row--always-on">
            <button class="ev-toggle ev-toggle--always-on" role="switch" aria-checked="true" :aria-label="t('projectPanels.triggers.manualAria')" disabled>
              <span class="ev-toggle-knob" aria-hidden="true"/>
            </button>
            <div class="ev-text"><span class="ev-name">{{ t('projectPanels.triggers.manualName') }}</span><span class="ev-desc">{{ t('projectPanels.triggers.manualDesc') }}</span></div>
            <span class="ev-code mono ev-code--always">{{ t('projectPanels.triggers.alwaysOn') }}</span>
          </div>
        </div>
      </section>

      <!-- ═══ Section 3: Branch mappings ═══════════════════════════════════ -->
      <section class="config-card" aria-labelledby="tp-mappings-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/>
              <path d="M18 9a9 9 0 0 1-9 9"/>
            </svg>
          </span>
          <h2 id="tp-mappings-heading" class="card-title">{{ t('projectPanels.triggers.mappingsTitle') }}</h2>
          <span class="card-sub">{{ t('projectPanels.triggers.mappingsSub') }}</span>
        </div>
        <div class="map-header" aria-hidden="true">
          <span>{{ t('projectPanels.triggers.colBranch') }}</span><span>{{ t('projectPanels.triggers.colEnvironment') }}</span><span>{{ t('projectPanels.triggers.colTargetServers') }}</span><span></span>
        </div>
        <div v-for="row in mappings" :key="row._key" class="map-row">
          <div class="map-cell map-cell--pattern">
            <div class="pattern-input-wrap">
              <svg class="pattern-icon" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>
              </svg>
              <input v-model="row.branchPattern" type="text" class="map-input map-input--mono" :class="{ 'map-input--error': row.patternError }" :placeholder="t('projectPanels.triggers.patternPlaceholder')" :aria-label="t('projectPanels.triggers.patternAria', { key: row._key })" :aria-invalid="row.patternError ? 'true' : undefined" @input="onPatternInput(row)" @blur="row.patternError = validatePattern(row.branchPattern)"/>
            </div>
            <span v-if="row.patternError" class="map-field-error" role="alert">{{ row.patternError }}</span>
          </div>
          <div class="map-cell">
            <input v-model="row.environment" type="text" class="map-input" :placeholder="t('projectPanels.triggers.envPlaceholder')" :aria-label="t('projectPanels.triggers.envNameAria', { key: row._key })"/>
          </div>
          <div class="map-cell map-cell--servers">
            <div class="servers-placeholder" :aria-label="t('projectPanels.triggers.serversPlaceholderAria')">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="4" width="18" height="6" rx="1.6"/><rect x="3" y="14" width="18" height="6" rx="1.6"/><path d="M7 7h.01M7 17h.01"/></svg>
              <span class="servers-placeholder-text">{{ t('projectPanels.triggers.serversPlaceholderText') }}</span>
            </div>
          </div>
          <div class="map-cell map-cell--actions">
            <button class="row-del-btn" :aria-label="t('projectPanels.triggers.deleteRowAria', { pattern: row.branchPattern || t('projectPanels.triggers.emptyPattern') })" @click="removeRow(row._key)">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true"><path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/></svg>
            </button>
          </div>
        </div>
        <div v-if="mappings.length === 0" class="map-empty"><span>{{ t('projectPanels.triggers.noMappings') }}</span></div>
        <button class="add-row-btn" @click="addRow">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>
          {{ t('projectPanels.triggers.addMapping') }}
        </button>
        <p class="map-note">{{ t('projectPanels.triggers.mappingNotePrefix') }}<code class="mono">release/*</code> {{ t('projectPanels.triggers.mappingNoteSuffix') }}</p>
      </section>

      <!-- ═══ Section 4: Unmatched policy ══════════════════════════════════ -->
      <section class="config-card" aria-labelledby="tp-policy-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
            </svg>
          </span>
          <h2 id="tp-policy-heading" class="card-title">{{ t('projectPanels.triggers.policyTitle') }}</h2>
          <span class="card-sub">{{ t('projectPanels.triggers.policySub') }}</span>
        </div>
        <div class="card-body card-body--pad">
          <div class="segmented" role="group" :aria-label="t('projectPanels.triggers.policyGroupAria')">
            <button class="seg-btn" :class="{ 'seg-btn--active': unmatchedPolicy === 'record' }" role="radio" :aria-checked="unmatchedPolicy === 'record'" @click="unmatchedPolicy = 'record'">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/></svg>
              {{ t('projectPanels.triggers.policyRecord') }}
            </button>
            <button class="seg-btn" :class="{ 'seg-btn--active': unmatchedPolicy === 'ignore' }" role="radio" :aria-checked="unmatchedPolicy === 'ignore'" @click="unmatchedPolicy = 'ignore'">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>
              {{ t('projectPanels.triggers.policyIgnore') }}
            </button>
          </div>
          <p class="policy-desc">
            <template v-if="unmatchedPolicy === 'record'">{{ t('projectPanels.triggers.policyDescRecord') }}</template>
            <template v-else>{{ t('projectPanels.triggers.policyDescIgnore') }}</template>
          </p>
        </div>
      </section>

      <!-- ═══ Section 5: Path filter (monorepo · P0) ══════════════════════ -->
      <section class="config-card" aria-labelledby="tp-paths-heading">
        <div class="card-head">
          <span class="card-icon" aria-hidden="true">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
              <path d="M3 7l3-3h5l2 2h8v12a2 2 0 0 1-2 2H3z"/>
            </svg>
          </span>
          <h2 id="tp-paths-heading" class="card-title">{{ t('projectPanels.triggers.pathsTitle') }}</h2>
          <span class="card-sub">{{ t('projectPanels.triggers.pathsSub') }}</span>
        </div>
        <div class="card-body card-body--pad">
          <textarea
            v-model="pathFiltersText"
            class="map-input map-input--mono path-filters-input"
            rows="4"
            placeholder="backend/**&#10;shared/**&#10;*.go"
            :aria-label="t('projectPanels.triggers.pathsAria')"
          ></textarea>
          <p class="policy-desc">
            {{ t('projectPanels.triggers.pathsHint', { doubleStar: '**', star: '*', egDir: 'backend/**', egGo: '**/*.go' }) }}
          </p>
        </div>
      </section>

      <!-- ═══ Scheduled (cron) trigger · Story 8-6 ════════════════════════ -->
      <CronPanel :project-id="props.projectId" />

      <!-- ═══ Environment promotion chain · Story 8-7 / FR-8-7 ════════════ -->
      <EnvironmentsPanel :project-id="props.projectId" />

      <!-- ═══ Typed run parameters · P0 ════════════════════════════════════ -->
      <ParametersPanel :project-id="props.projectId" />

      <!-- ═══ Concurrency limit · Story 8-10 / FR-8-10 ═════════════════════ -->
      <ConcurrencyPanel :project-id="props.projectId" />

      <!-- ═══ Downstream chaining · Story 8-11 / FR-8-11 ══════════════════ -->
      <ChainPanel :project-id="props.projectId" />

      <!-- 远程构建 runner(FR-8-14 续) -->
      <RunnerPanel :project-id="props.projectId" />

      <!-- ═══ Save bar ════════════════════════════════════════════════════ -->
      <div class="save-bar">
        <button class="btn-primary" :disabled="saveSubmitting" :aria-busy="saveSubmitting" @click="handleSave">
          <span v-if="saveSubmitting" class="spinner" aria-hidden="true"/>
          {{ saveSubmitting ? t('projectPanels.triggers.saving') : t('projectPanels.triggers.saveTriggers') }}
        </button>
      </div>
    </template>
  </div>

  <!-- ═══ Reset secret modal ══════════════════════════════════════════════════ -->
  <Teleport to="body">
    <div v-if="resetModalOpen" class="modal-scrim" role="dialog" :aria-label="t('projectPanels.triggers.resetModalAria')" aria-modal="true" @keydown.esc="closeResetModal" @click.self="closeResetModal">
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon modal-icon--warn" aria-hidden="true">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
          </div>
          <div>
            <h3 class="modal-title">{{ t('projectPanels.triggers.resetModalTitle') }}</h3>
            <p class="modal-sub">{{ t('projectPanels.triggers.resetModalSub') }}</p>
          </div>
          <button class="modal-close" :aria-label="t('projectPanels.triggers.closeDialogAria')" :disabled="resetSubmitting" @click="closeResetModal">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 6 6 18M6 6l12 12"/></svg>
          </button>
        </div>
        <div class="modal-body">
          <p class="modal-warn-text">{{ t('projectPanels.triggers.resetWarnText') }}</p>
          <p class="modal-warn-secondary">{{ t('projectPanels.triggers.resetWarnSecondary') }}</p>
          <div v-if="resetBanner" class="banner banner--error modal-banner" role="alert">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
            {{ resetBanner }}
          </div>
        </div>
        <div class="modal-footer">
          <button type="button" class="btn-secondary" :disabled="resetSubmitting" @click="closeResetModal">{{ t('projectPanels.triggers.cancel') }}</button>
          <button type="button" class="btn-danger" :disabled="resetSubmitting" :aria-busy="resetSubmitting" @click="confirmReset">
            <span v-if="resetSubmitting" class="spinner spinner--red" aria-hidden="true"/>
            {{ resetSubmitting ? t('projectPanels.triggers.resetting') : t('projectPanels.triggers.confirmReset') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* All styles are inherited from the original ProjectTriggers.vue
   Only structural wrapper is added here */
.triggers-panel {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── Config card ─────────────────────────────── */
.config-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: card-in 0.45s var(--ease-out-expo) both;
}
@keyframes card-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) { .config-card { animation: none; } }
.card-head { display: flex; align-items: center; gap: 9px; padding: 14px 18px; border-bottom: 1px solid var(--color-border); }
.card-icon { width: 22px; height: 22px; border-radius: 6px; background: var(--color-primary-soft); color: var(--color-primary); display: grid; place-items: center; flex-shrink: 0; }
.card-title { font-size: 0.88rem; font-weight: 600; color: var(--color-text); }
.card-sub { margin-left: 6px; font-size: 0.75rem; color: var(--color-faint); font-weight: 400; }
.card-body { padding: 0; }
.card-body--pad { padding: 16px 18px; }

/* ─── Webhook ─────────────────────────────────── */
.webhook-row { display: grid; grid-template-columns: 80px 1fr; gap: 14px; align-items: start; padding: 12px 18px; border-bottom: 1px solid var(--color-border); }
.webhook-row:last-child { border-bottom: none; }
.wk-label { font-size: 0.79rem; color: var(--color-dim); padding-top: 9px; white-space: nowrap; }
.url-box { display: flex; align-items: stretch; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: var(--rounded); overflow: hidden; }
.url-text { flex: 1; height: 38px; display: flex; align-items: center; padding: 0 13px; font-size: 0.79rem; color: var(--color-text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.mono { font-family: var(--font-mono); }
.url-action-btn { height: 38px; border: none; border-left: 1px solid var(--color-border); background: var(--color-card-2); color: var(--color-dim); padding: 0 13px; cursor: pointer; font-size: 0.78rem; font-family: var(--font-sans); font-weight: 500; white-space: nowrap; display: inline-flex; align-items: center; gap: 5px; transition: color var(--duration-fast), background-color var(--duration-fast); }
.url-action-btn:hover { color: var(--color-text); background: var(--color-inset); }
.url-action-btn--copied { color: var(--color-green); }
.secret-wrap { display: flex; align-items: flex-start; gap: 9px; flex-wrap: wrap; }
.secret-masked { height: 38px; display: flex; align-items: center; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: var(--rounded); padding: 0 13px; font-size: 0.79rem; color: var(--color-dim); letter-spacing: 0.06em; user-select: none; cursor: default; flex: 1; min-width: 0; }
.secret-reveal { width: 100%; display: flex; align-items: center; gap: 10px; padding: 9px 13px; background: var(--color-amber-soft); border: 1px solid var(--color-amber-line); border-radius: var(--rounded); flex-wrap: wrap; }
.secret-reveal-label { font-size: 0.74rem; font-weight: 600; color: var(--color-amber); white-space: nowrap; }
.secret-reveal-value { flex: 1; font-size: 0.79rem; color: var(--color-text); word-break: break-all; }
.btn-mini { height: 38px; border: 1px solid var(--color-border-strong); background: var(--color-card-2); color: var(--color-text); font-size: 0.79rem; font-family: var(--font-sans); font-weight: 500; border-radius: var(--rounded); padding: 0 13px; cursor: pointer; white-space: nowrap; display: inline-flex; align-items: center; gap: 5px; flex-shrink: 0; transition: border-color var(--duration-fast), color var(--duration-fast), background-color var(--duration-fast); }
.btn-mini:hover:not(:disabled) { border-color: var(--color-faint); background: var(--color-inset); }
.btn-mini:disabled { opacity: 0.45; cursor: not-allowed; }
.btn-mini--copied { color: var(--color-green); border-color: var(--color-green-line); }
.wk-hint { font-size: 0.78rem; color: var(--color-dim); line-height: 1.6; padding-top: 9px; }

/* ─── Events ──────────────────────────────────── */
.event-row { display: grid; grid-template-columns: 40px 1fr auto; gap: 14px; align-items: center; padding: 12px 18px; border-bottom: 1px solid var(--color-border); transition: background-color var(--duration-fast); }
.event-row:last-child { border-bottom: none; }
.event-row:not(.event-row--always-on):hover { background: var(--color-inset); }
.event-row--always-on { opacity: 0.65; }
.ev-toggle { position: relative; width: 36px; height: 20px; border-radius: var(--rounded-full); background: var(--color-border-strong); border: none; cursor: pointer; flex-shrink: 0; transition: background-color var(--duration-fast); display: block; }
.ev-toggle--on { background: var(--color-primary); }
.ev-toggle--always-on { background: var(--color-primary); cursor: not-allowed; }
.ev-toggle-knob { position: absolute; top: 2px; left: 2px; width: 16px; height: 16px; border-radius: var(--rounded-full); background: #fff; transition: transform var(--duration-fast); pointer-events: none; box-shadow: 0 1px 4px oklch(0% 0 0 / 0.35); }
.ev-toggle--on .ev-toggle-knob, .ev-toggle--always-on .ev-toggle-knob { transform: translateX(16px); }
@media (prefers-reduced-motion: reduce) { .ev-toggle, .ev-toggle-knob { transition: none; } }
.ev-text { display: flex; flex-direction: column; gap: 2px; }
.ev-name { font-size: 0.84rem; font-weight: 500; color: var(--color-text); }
.ev-desc { font-size: 0.75rem; color: var(--color-faint); line-height: 1.4; }
.ev-code { font-size: 0.72rem; color: var(--color-faint); white-space: nowrap; }
.ev-code--always { color: var(--color-primary); }

/* ─── Branch mapping ──────────────────────────── */
.map-header { display: grid; grid-template-columns: 1fr 140px 1fr 52px; gap: 12px; padding: 0 18px; height: 38px; align-items: center; background: var(--color-card-2); border-bottom: 1px solid var(--color-border); font-size: var(--text-caps); font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; color: var(--color-faint); }
.map-row { display: grid; grid-template-columns: 1fr 140px 1fr 52px; gap: 12px; padding: 10px 18px; align-items: start; border-bottom: 1px solid var(--color-border); transition: background-color var(--duration-fast); }
.map-row:hover { background: var(--color-inset); }
.map-cell { display: flex; flex-direction: column; gap: 4px; }
.map-cell--servers { justify-content: center; }
.map-cell--actions { align-items: flex-end; justify-content: center; padding-top: 4px; }
.pattern-input-wrap { position: relative; }
.pattern-icon { position: absolute; left: 10px; top: 50%; transform: translateY(-50%); color: var(--color-faint); pointer-events: none; }
.map-input { width: 100%; height: 36px; background: var(--color-inset); border: 1px solid var(--color-border); border-radius: var(--rounded); padding: 0 10px; color: var(--color-text); font-family: var(--font-sans); font-size: 0.84rem; transition: border-color var(--duration-fast), box-shadow var(--duration-fast); }
.map-input--mono { font-family: var(--font-mono); font-size: 0.8rem; padding-left: 28px; }
.map-input::placeholder { color: var(--color-faint); }
.map-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.map-input--error { border-color: var(--color-red); }
.map-field-error { font-size: 0.73rem; color: var(--color-red); line-height: 1.4; }
.servers-placeholder { display: inline-flex; align-items: center; gap: 6px; height: 36px; padding: 0 10px; background: var(--color-inset); border: 1px dashed var(--color-border-strong); border-radius: var(--rounded); color: var(--color-faint); }
.servers-placeholder-text { font-size: 0.74rem; line-height: 1.3; }
.row-del-btn { width: 30px; height: 30px; border: 1px solid var(--color-border); background: transparent; color: var(--color-faint); border-radius: var(--rounded-md); cursor: pointer; display: grid; place-items: center; transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast); margin-top: 3px; }
.row-del-btn:hover { color: var(--color-red); border-color: var(--color-red-line); background: var(--color-red-soft); }
.map-empty { padding: 20px 18px; font-size: 0.82rem; color: var(--color-faint); font-style: italic; border-bottom: 1px solid var(--color-border); text-align: center; }
.add-row-btn { display: flex; align-items: center; gap: 7px; padding: 12px 18px; width: 100%; border: none; background: transparent; color: var(--color-primary); font-family: var(--font-sans); font-size: 0.8rem; font-weight: 500; cursor: pointer; text-align: left; transition: color var(--duration-fast), background-color var(--duration-fast); border-top: 1px solid var(--color-border); }
.add-row-btn:hover { background: var(--color-inset); }
.map-note { padding: 12px 18px 14px; font-size: 0.76rem; color: var(--color-faint); line-height: 1.6; border-top: 1px solid var(--color-border); }
.map-note code { color: var(--color-cyan); }

/* ─── Unmatched policy ────────────────────────── */
.segmented { display: inline-flex; background: var(--color-inset); border-radius: var(--rounded); padding: 3px; gap: 3px; border: 1px solid var(--color-border); }
.seg-btn { height: 32px; padding: 0 14px; border: none; background: transparent; color: var(--color-dim); font-family: var(--font-sans); font-size: 0.82rem; font-weight: 500; border-radius: var(--rounded-md); cursor: pointer; display: inline-flex; align-items: center; gap: 6px; transition: color var(--duration-fast), background-color var(--duration-fast), box-shadow var(--duration-fast); }
.seg-btn:hover:not(.seg-btn--active) { color: var(--color-text); background: oklch(100% 0 0 / 0.04); }
.seg-btn--active { background: var(--color-card); color: var(--color-text); box-shadow: var(--shadow); }
.policy-desc { margin-top: 12px; font-size: 0.8rem; color: var(--color-faint); line-height: 1.6; }
.policy-desc code { font-family: var(--font-mono); font-size: 0.78em; padding: 1px 4px; border-radius: 4px; background: var(--color-inset); color: var(--color-dim); }
.path-filters-input { height: auto; min-height: 92px; padding: 8px 10px; line-height: 1.5; resize: vertical; }

/* ─── Save bar ────────────────────────────────── */
.save-bar { display: flex; justify-content: flex-start; padding-bottom: 8px; }
.btn-primary { display: inline-flex; align-items: center; gap: 7px; height: 34px; padding: 0 16px; border: none; background: var(--color-primary); color: #fff; font-family: var(--font-sans); font-size: 0.83rem; font-weight: 600; border-radius: var(--rounded); cursor: pointer; box-shadow: 0 5px 16px var(--color-primary-soft); transition: background-color var(--duration-fast), transform var(--duration-fast); white-space: nowrap; }
.btn-primary:hover:not(:disabled) { background: var(--color-primary-press); transform: translateY(-1px); }
.btn-primary:disabled { opacity: 0.45; cursor: not-allowed; transform: none; box-shadow: none; }

/* ─── Skeleton ────────────────────────────────── */
.skel-stack { display: flex; flex-direction: column; gap: var(--card-gap); }
.skel-card { background: var(--color-card); border: 1px solid var(--color-border); border-radius: var(--rounded-card); padding: 18px; display: flex; flex-direction: column; gap: 14px; }
.skel { display: block; background: linear-gradient(90deg, var(--color-inset) 0%, oklch(100% 0 0 / 0.06) 50%, var(--color-inset) 100%); background-size: 200% 100%; border-radius: var(--rounded-md); animation: shimmer 1.4s ease-in-out infinite; }
@keyframes shimmer { 0% { background-position: 200% center; } 100% { background-position: -200% center; } }
@media (prefers-reduced-motion: reduce) { .skel { animation: none; background: var(--color-inset); } }
.skel--title { height: 16px; width: 40%; }
.skel--row   { height: 38px; width: 100%; }

/* ─── Banners ─────────────────────────────────── */
.banner { display: flex; align-items: flex-start; gap: 9px; padding: 11px 14px; border-radius: var(--rounded); font-size: 0.83rem; line-height: 1.5; }
.banner--error { background: var(--color-red-soft); border: 1px solid var(--color-red-line); color: var(--color-red); }
.banner--success { background: var(--color-green-soft); border: 1px solid var(--color-green-line); color: var(--color-green); }
.banner-retry { margin-left: auto; flex-shrink: 0; background: none; border: none; color: var(--color-red); font-size: 0.83rem; font-weight: 600; cursor: pointer; padding: 0; text-decoration: underline; text-underline-offset: 2px; }

/* ─── Modal ───────────────────────────────────── */
.modal-scrim { position: fixed; inset: 0; background: oklch(0% 0 0 / 0.62); display: grid; place-items: center; z-index: 100; padding: 24px; animation: scrim-in var(--duration-fast) ease both; }
@keyframes scrim-in { from { opacity: 0; } to { opacity: 1; } }
.modal { width: 100%; max-width: 440px; background: var(--color-card); border: 1px solid var(--color-border-strong); border-radius: var(--rounded-xl); box-shadow: var(--shadow-modal); overflow: hidden; animation: modal-in 0.35s var(--ease-out-expo) both; }
@keyframes modal-in { from { opacity: 0; transform: translateY(14px) scale(0.98); } to { opacity: 1; transform: none; } }
@media (prefers-reduced-motion: reduce) { .modal-scrim { animation: none; } .modal { animation: none; } }
.modal-head { display: flex; align-items: flex-start; gap: 12px; padding: 20px 20px 16px; border-bottom: 1px solid var(--color-border); }
.modal-icon { width: 36px; height: 36px; border-radius: var(--rounded-lg); display: grid; place-items: center; flex-shrink: 0; }
.modal-icon--warn { background: var(--color-amber-soft); color: var(--color-amber); border: 1px solid var(--color-amber-line); }
.modal-title { font-size: 1rem; font-weight: 600; color: var(--color-text); margin-top: 2px; letter-spacing: -0.01em; }
.modal-sub { font-size: 0.78rem; color: var(--color-faint); margin-top: 3px; line-height: 1.4; }
.modal-close { margin-left: auto; flex-shrink: 0; width: 30px; height: 30px; border-radius: var(--rounded-md); border: none; background: transparent; color: var(--color-faint); cursor: pointer; display: grid; place-items: center; transition: color var(--duration-fast), background-color var(--duration-fast); }
.modal-close:hover { color: var(--color-text); background: var(--color-inset); }
.modal-close:disabled { opacity: 0.4; cursor: not-allowed; }
.modal-body { padding: 20px; display: flex; flex-direction: column; gap: 10px; }
.modal-warn-text { font-size: 0.86rem; color: var(--color-dim); line-height: 1.6; }
.modal-warn-secondary { font-size: 0.82rem; color: var(--color-faint); line-height: 1.5; }
.modal-banner { border-radius: var(--rounded); }
.modal-footer { display: flex; justify-content: flex-end; gap: 8px; padding: 0 20px 20px; }
.btn-secondary { display: inline-flex; align-items: center; height: 34px; padding: 0 15px; background: var(--color-card-2); color: var(--color-text); border: 1px solid var(--color-border-strong); font-family: var(--font-sans); font-size: 0.83rem; font-weight: 500; border-radius: var(--rounded); cursor: pointer; transition: border-color var(--duration-fast), background-color var(--duration-fast); }
.btn-secondary:hover:not(:disabled) { border-color: var(--color-faint); }
.btn-secondary:disabled { opacity: 0.45; cursor: not-allowed; }
.btn-danger { display: inline-flex; align-items: center; gap: 7px; height: 34px; padding: 0 15px; background: var(--color-red-soft); color: var(--color-red); border: 1px solid var(--color-red-line); font-family: var(--font-sans); font-size: 0.83rem; font-weight: 600; border-radius: var(--rounded); cursor: pointer; transition: background-color var(--duration-fast), transform var(--duration-fast); }
.btn-danger:hover:not(:disabled) { background: oklch(62% 0.18 22 / 0.25); transform: translateY(-1px); }
.btn-danger:disabled { opacity: 0.5; cursor: not-allowed; transform: none; }
.spinner { display: inline-block; width: 13px; height: 13px; border: 2px solid rgba(255, 255, 255, 0.35); border-top-color: #fff; border-radius: var(--rounded-full); animation: spin 0.7s linear infinite; flex-shrink: 0; }
.spinner--red { border-color: oklch(69% 0.17 22 / 0.3); border-top-color: var(--color-red); }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation: none; border-top-color: currentColor; } }
</style>

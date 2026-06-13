<script setup lang="ts">
/**
 * ConcurrencyPanel — per-project concurrent run limit (Epic 8 · Story 8-10 / FR-8-10).
 *
 * Self-contained card with its own load/save against /api/projects/{id}/concurrency.
 * maxConcurrent 0 = no project-level limit; 1..64 = hard cap on simultaneous runs.
 * Runs beyond the cap are queued until a slot opens.
 *
 * Embedded inside TriggersPanel after CronPanel and EnvironmentsPanel.
 */
import { ref, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getConcurrency, saveConcurrency } from '../api/concurrency'
import { validateConcurrency, CONCURRENCY_MAX } from '../api/concurrency.helpers'
import { HttpError } from '../api/http'

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')

const maxConcurrent = ref(0)

const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)
const validationError = ref('')

// ─── Helpers ──────────────────────────────────────────────────────────────────

function applyConfig(cfg: { maxConcurrent: number }): void {
  maxConcurrent.value = cfg.maxConcurrent
}

// ─── Load ──────────────────────────────────────────────────────────────────────

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    applyConfig(await getConcurrency(props.projectId))
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value = err instanceof HttpError ? err.message : t('projectPanels.concurrency.errLoad')
  }
}

// ─── Input handling ────────────────────────────────────────────────────────────

function onInput(): void {
  saveBanner.value = ''
  saveSuccess.value = false
  validationError.value = validateConcurrency(maxConcurrent.value)
}

function stepDown(): void {
  if (maxConcurrent.value > 0) {
    maxConcurrent.value -= 1
    onInput()
  }
}

function stepUp(): void {
  if (maxConcurrent.value < CONCURRENCY_MAX) {
    maxConcurrent.value += 1
    onInput()
  }
}

// ─── Save ──────────────────────────────────────────────────────────────────────

async function handleSave(): Promise<void> {
  validationError.value = validateConcurrency(maxConcurrent.value)
  if (validationError.value) return

  saveBanner.value = ''
  saveSuccess.value = false
  saveSubmitting.value = true
  try {
    applyConfig(
      await saveConcurrency(props.projectId, { maxConcurrent: maxConcurrent.value }),
    )
    saveSuccess.value = true
    saveBanner.value = t('projectPanels.concurrency.savedOk')
  } catch (err) {
    saveSuccess.value = false
    if (err instanceof HttpError) {
      saveBanner.value =
        err.apiError?.code === 'invalid_concurrency'
          ? t('projectPanels.concurrency.errInvalid')
          : (err.apiError?.message ?? t('projectPanels.concurrency.errSaveFailed', { status: err.status }))
    } else {
      saveBanner.value = t('projectPanels.concurrency.errSaveRetry')
    }
  } finally {
    saveSubmitting.value = false
  }
}

onMounted(load)
watch(() => props.projectId, load)
</script>

<template>
  <section class="config-card" aria-labelledby="conc-heading">
    <div class="card-head">
      <span class="card-icon" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <rect x="3" y="3" width="7" height="7" rx="1.5" />
          <rect x="14" y="3" width="7" height="7" rx="1.5" />
          <rect x="3" y="14" width="7" height="7" rx="1.5" />
          <rect x="14" y="14" width="7" height="7" rx="1.5" />
        </svg>
      </span>
      <h2 id="conc-heading" class="card-title">{{ t('projectPanels.concurrency.title') }}</h2>
      <span class="card-sub">{{ t('projectPanels.concurrency.sub') }}</span>
    </div>

    <div class="card-body card-body--pad">
      <p v-if="loadState === 'loading'" class="conc-loading">{{ t('projectPanels.concurrency.loading') }}</p>
      <p v-else-if="loadState === 'error'" class="conc-error" role="alert">{{ loadError }}</p>

      <template v-else>
        <!-- Stepper -->
        <div class="conc-field">
          <label class="conc-label" for="conc-input">
            {{ t('projectPanels.concurrency.maxLabel') }}
            <span class="conc-hint-inline">{{ t('projectPanels.concurrency.maxHint', { max: CONCURRENCY_MAX }) }}</span>
          </label>
          <div class="stepper" role="group" :aria-label="t('projectPanels.concurrency.stepperAria')">
            <button
              type="button"
              class="stepper-btn stepper-btn--dec"
              :aria-label="t('projectPanels.concurrency.decrease')"
              :disabled="maxConcurrent <= 0 || saveSubmitting"
              @click="stepDown"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
                <path d="M5 12h14"/>
              </svg>
            </button>
            <input
              id="conc-input"
              v-model.number="maxConcurrent"
              type="number"
              class="stepper-input"
              :class="{ 'stepper-input--error': validationError }"
              min="0"
              :max="CONCURRENCY_MAX"
              step="1"
              :disabled="saveSubmitting"
              aria-describedby="conc-hint conc-validation"
              @input="onInput"
            />
            <button
              type="button"
              class="stepper-btn stepper-btn--inc"
              :aria-label="t('projectPanels.concurrency.increase')"
              :disabled="maxConcurrent >= CONCURRENCY_MAX || saveSubmitting"
              @click="stepUp"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
                <path d="M12 5v14M5 12h14"/>
              </svg>
            </button>
          </div>

          <!-- Validation error -->
          <p v-if="validationError" id="conc-validation" class="conc-validation" role="alert">
            {{ validationError }}
          </p>
        </div>

        <!-- Contextual hint -->
        <p id="conc-hint" class="conc-hint-block">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          <template v-if="maxConcurrent === 0">
            {{ t('projectPanels.concurrency.hintUnlimited') }}<strong>{{ t('projectPanels.concurrency.hintUnlimitedWord') }}</strong>{{ t('projectPanels.concurrency.hintUnlimitedSuffix', { var: 'PIPEWRIGHT_MAX_CONCURRENT' }) }}
          </template>
          <template v-else>
            {{ t('projectPanels.concurrency.hintLimitedPrefix') }}<strong>{{ maxConcurrent }}</strong>{{ t('projectPanels.concurrency.hintLimitedSuffix', { next: maxConcurrent + 1 }) }}
          </template>
        </p>

        <!-- Save banner -->
        <p
          v-if="saveBanner"
          class="conc-banner"
          :class="saveSuccess ? 'conc-banner--ok' : 'conc-banner--err'"
          role="status"
        >{{ saveBanner }}</p>

        <div class="conc-save">
          <button
            class="btn-primary"
            :disabled="saveSubmitting || !!validationError"
            :aria-busy="saveSubmitting"
            @click="handleSave"
          >
            <span v-if="saveSubmitting" class="spinner" aria-hidden="true" />
            {{ saveSubmitting ? t('projectPanels.concurrency.saving') : t('projectPanels.concurrency.save') }}
          </button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.config-card {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-card);
  overflow: hidden;
}
.card-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--color-border);
}
.card-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--rounded-md);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  flex: none;
}
.card-title { font-size: 0.92rem; font-weight: 650; color: var(--color-text); }
.card-sub { font-size: 0.76rem; color: var(--color-faint); flex: 1; min-width: 0; }

.card-body--pad { padding: 16px; display: flex; flex-direction: column; gap: 14px; }
.conc-loading, .conc-error { margin: 0; font-size: 0.82rem; }
.conc-error { color: var(--color-danger, #dc2626); }
.conc-loading { color: var(--color-faint); }

/* ─── Label ───────────────────────────────────────────────────────────────── */
.conc-field { display: flex; flex-direction: column; gap: 8px; }
.conc-label { font-size: 0.8rem; font-weight: 600; color: var(--color-text); }
.conc-hint-inline { font-weight: 400; color: var(--color-faint); margin-left: 6px; }

/* ─── Stepper ─────────────────────────────────────────────────────────────── */
.stepper {
  display: inline-flex;
  align-items: stretch;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  overflow: hidden;
  width: 160px;
}
.stepper-btn {
  display: grid;
  place-items: center;
  width: 36px;
  border: none;
  background: var(--color-bg-subtle, var(--color-card));
  color: var(--color-dim);
  cursor: pointer;
  transition: background-color var(--duration-fast), color var(--duration-fast);
  flex: none;
}
.stepper-btn:hover:not(:disabled) {
  background: var(--color-inset);
  color: var(--color-primary);
}
.stepper-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.stepper-btn--dec { border-right: 1px solid var(--color-border-strong); }
.stepper-btn--inc { border-left: 1px solid var(--color-border-strong); }

.stepper-input {
  flex: 1;
  min-width: 0;
  height: 36px;
  padding: 0 6px;
  font: inherit;
  font-size: 0.92rem;
  font-weight: 600;
  text-align: center;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: none;
  outline: none;
  transition: background-color var(--duration-fast);
  /* Hide native spinners */
  -moz-appearance: textfield;
}
.stepper-input::-webkit-inner-spin-button,
.stepper-input::-webkit-outer-spin-button { -webkit-appearance: none; margin: 0; }
.stepper-input:focus { background: var(--color-inset); }
.stepper-input:disabled { opacity: 0.55; cursor: not-allowed; }
.stepper-input--error { color: var(--color-danger, #dc2626); }
.stepper:focus-within { outline: 2px solid var(--color-primary); outline-offset: 1px; }

/* ─── Hint block ──────────────────────────────────────────────────────────── */
.conc-hint-block {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  padding: 9px 11px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.5;
}
.conc-hint-block svg { flex: none; color: var(--color-primary); }
.conc-hint-block strong { color: var(--color-text); font-weight: 650; }
.conc-hint-block code {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.78rem;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: 4px;
  padding: 0 4px;
  color: var(--color-cyan, #0891b2);
}

/* ─── Validation ──────────────────────────────────────────────────────────── */
.conc-validation {
  margin: 0;
  font-size: 0.76rem;
  color: var(--color-danger, #dc2626);
  font-weight: 500;
}

/* ─── Banner ──────────────────────────────────────────────────────────────── */
.conc-banner { margin: 0; font-size: 0.8rem; font-weight: 500; }
.conc-banner--ok { color: var(--color-success, #16a34a); }
.conc-banner--err { color: var(--color-danger, #dc2626); }

/* ─── Save ────────────────────────────────────────────────────────────────── */
.conc-save { display: flex; justify-content: flex-end; }

/* ─── Btn-primary (mirrors TriggersPanel global) ──────────────────────────── */
.btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  padding: 0 16px;
  border: none;
  background: var(--color-primary);
  color: #fff;
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  box-shadow: 0 5px 16px var(--color-primary-soft);
  transition: background-color var(--duration-fast), transform var(--duration-fast);
  white-space: nowrap;
}
.btn-primary:hover:not(:disabled) { background: var(--color-primary-press); transform: translateY(-1px); }
.btn-primary:disabled { opacity: 0.45; cursor: not-allowed; transform: none; box-shadow: none; }

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
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation: none; border-top-color: currentColor; } }
</style>

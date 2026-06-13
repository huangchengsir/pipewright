<script setup lang="ts">
/**
 * ChainPanel — downstream pipeline chaining config (Epic 8 · Story 8-11 / FR-8-11).
 *
 * Self-contained card with its own load/save against /api/projects/{id}/chain.
 * Each row picks a downstream project (from the project list) + optional branch +
 * enabled toggle. When this project's pipeline succeeds, all enabled downstream
 * entries are triggered automatically.
 *
 * Embedded inside TriggersPanel after ConcurrencyPanel.
 */
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getChain, saveChain, type ChainTarget } from '../api/chain'
import { listProjects, type Project } from '../api/projects'
import { HttpError } from '../api/http'

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()

type LoadState = 'idle' | 'loading' | 'error'

const loadState = ref<LoadState>('idle')
const loadError = ref('')

// ─── Project list for the dropdown ────────────────────────────────────────────

const allProjects = ref<Project[]>([])

/** Projects available as downstream targets (excludes self). */
const availableProjects = computed<Project[]>(() =>
  allProjects.value.filter((p) => p.id !== props.projectId),
)

// ─── Row model ────────────────────────────────────────────────────────────────

interface ChainRow {
  _key: number
  downstreamProjectId: string
  branch: string
  enabled: boolean
  /** Validation error for the project select. */
  projectError: string
}

let _keySeq = 0
const rows = ref<ChainRow[]>([])

// ─── Save state ───────────────────────────────────────────────────────────────

const saveSubmitting = ref(false)
const saveBanner = ref('')
const saveSuccess = ref(false)

// ─── Helpers ──────────────────────────────────────────────────────────────────

function makeRow(): ChainRow {
  return { _key: ++_keySeq, downstreamProjectId: '', branch: '', enabled: true, projectError: '' }
}

function applyConfig(cfg: { downstream: ChainTarget[] }): void {
  rows.value = cfg.downstream.map((t) => ({
    _key: ++_keySeq,
    downstreamProjectId: t.downstreamProjectId,
    branch: t.branch,
    enabled: t.enabled,
    projectError: '',
  }))
}

function projectName(id: string): string {
  const p = allProjects.value.find((x) => x.id === id)
  return p ? p.name : id
}

function addRow(): void {
  rows.value.push(makeRow())
  saveBanner.value = ''
  saveSuccess.value = false
}

function removeRow(key: number): void {
  rows.value = rows.value.filter((r) => r._key !== key)
  saveBanner.value = ''
  saveSuccess.value = false
}

function onProjectChange(row: ChainRow): void {
  saveBanner.value = ''
  saveSuccess.value = false
  row.projectError = row.downstreamProjectId ? '' : t('projectPanels.chain.errSelectDownstream')
}

// ─── Load ──────────────────────────────────────────────────────────────────────

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    // Load both in parallel — project list is shared data we don't cache locally.
    const [chainCfg, projects] = await Promise.all([
      getChain(props.projectId),
      listProjects(),
    ])
    allProjects.value = projects
    applyConfig(chainCfg)
    loadState.value = 'idle'
  } catch (err) {
    loadState.value = 'error'
    loadError.value = err instanceof HttpError ? err.message : t('projectPanels.chain.errLoad')
  }
}

// ─── Save ──────────────────────────────────────────────────────────────────────

async function handleSave(): Promise<void> {
  // Validate all rows before submitting.
  let hasError = false
  for (const row of rows.value) {
    if (!row.downstreamProjectId) {
      row.projectError = t('projectPanels.chain.errSelectDownstream')
      hasError = true
    }
  }
  if (hasError) return

  saveBanner.value = ''
  saveSuccess.value = false
  saveSubmitting.value = true
  try {
    applyConfig(
      await saveChain(props.projectId, {
        downstream: rows.value.map((r) => ({
          downstreamProjectId: r.downstreamProjectId,
          branch: r.branch.trim(),
          enabled: r.enabled,
        })),
      }),
    )
    saveSuccess.value = true
    saveBanner.value = t('projectPanels.chain.savedOk')
  } catch (err) {
    saveSuccess.value = false
    if (err instanceof HttpError) {
      const code = err.apiError?.code
      if (code === 'self_chain') {
        saveBanner.value = t('projectPanels.chain.errSelfChain')
      } else if (code === 'downstream_not_found') {
        saveBanner.value = t('projectPanels.chain.errDownstreamNotFound')
      } else if (code === 'too_many_targets') {
        saveBanner.value = t('projectPanels.chain.errTooManyTargets')
      } else if (code === 'duplicate_target') {
        saveBanner.value = t('projectPanels.chain.errDuplicateTarget')
      } else {
        saveBanner.value = err.apiError?.message ?? t('projectPanels.chain.errSaveFailed', { status: err.status })
      }
    } else {
      saveBanner.value = t('projectPanels.chain.errSaveRetry')
    }
  } finally {
    saveSubmitting.value = false
  }
}

onMounted(load)
watch(() => props.projectId, load)
</script>

<template>
  <section class="config-card" aria-labelledby="chain-heading">
    <!-- Card header -->
    <div class="card-head">
      <span class="card-icon" aria-hidden="true">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </span>
      <h2 id="chain-heading" class="card-title">{{ t('projectPanels.chain.title') }}</h2>
      <span class="card-sub">{{ t('projectPanels.chain.sub') }}</span>
    </div>

    <div class="card-body">
      <!-- Loading -->
      <p v-if="loadState === 'loading'" class="chain-loading">{{ t('projectPanels.chain.loading') }}</p>

      <!-- Load error -->
      <p v-else-if="loadState === 'error'" class="chain-error" role="alert">{{ loadError }}</p>

      <template v-else>
        <!-- Column header (only shown when rows exist) -->
        <div v-if="rows.length > 0" class="chain-header" aria-hidden="true">
          <span>{{ t('projectPanels.chain.colDownstream') }}</span>
          <span>{{ t('projectPanels.chain.colTriggerBranch') }}</span>
          <span>{{ t('projectPanels.chain.colEnabled') }}</span>
          <span></span>
        </div>

        <!-- Rows -->
        <div
          v-for="row in rows"
          :key="row._key"
          class="chain-row"
          :class="{ 'chain-row--disabled': !row.enabled }"
        >
          <!-- Project select -->
          <div class="chain-cell chain-cell--project">
            <select
              v-model="row.downstreamProjectId"
              class="chain-select"
              :class="{ 'chain-select--error': row.projectError }"
              :aria-label="t('projectPanels.chain.downstreamAria', { key: row._key })"
              :aria-invalid="row.projectError ? 'true' : undefined"
              :disabled="saveSubmitting"
              @change="onProjectChange(row)"
            >
              <option value="" disabled>{{ t('projectPanels.chain.selectProject') }}</option>
              <option
                v-for="proj in availableProjects"
                :key="proj.id"
                :value="proj.id"
              >{{ proj.name }}</option>
            </select>
            <p v-if="row.projectError" class="chain-field-error" role="alert">{{ row.projectError }}</p>
          </div>

          <!-- Branch input -->
          <div class="chain-cell chain-cell--branch">
            <div class="chain-branch-wrap">
              <svg class="chain-branch-icon" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <line x1="6" y1="3" x2="6" y2="15"/>
                <circle cx="18" cy="6" r="3"/>
                <circle cx="6" cy="18" r="3"/>
                <path d="M18 9a9 9 0 0 1-9 9"/>
              </svg>
              <input
                v-model="row.branch"
                type="text"
                class="chain-input chain-input--mono"
                :placeholder="t('projectPanels.chain.branchPlaceholder')"
                autocomplete="off"
                :aria-label="t('projectPanels.chain.triggerBranchAria', { key: row._key })"
                :disabled="saveSubmitting"
              />
            </div>
          </div>

          <!-- Enabled toggle -->
          <div class="chain-cell chain-cell--toggle">
            <button
              type="button"
              class="chain-toggle"
              :class="{ 'chain-toggle--on': row.enabled }"
              role="switch"
              :aria-checked="row.enabled"
              :aria-label="t('projectPanels.chain.enableDownstreamAria', { name: row.downstreamProjectId ? projectName(row.downstreamProjectId) : t('projectPanels.chain.unselected') })"
              :disabled="saveSubmitting"
              @click="row.enabled = !row.enabled"
            >
              <span class="chain-toggle-knob" aria-hidden="true" />
            </button>
            <span class="chain-toggle-label">{{ row.enabled ? t('projectPanels.chain.enable') : t('projectPanels.chain.disable') }}</span>
          </div>

          <!-- Remove row -->
          <div class="chain-cell chain-cell--actions">
            <button
              type="button"
              class="row-del-btn"
              :aria-label="t('projectPanels.chain.deleteDownstreamAria', { name: row.downstreamProjectId ? projectName(row.downstreamProjectId) : t('projectPanels.chain.unselected') })"
              :disabled="saveSubmitting"
              @click="removeRow(row._key)"
            >
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
                <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
              </svg>
            </button>
          </div>
        </div>

        <!-- Empty state -->
        <div v-if="rows.length === 0" class="chain-empty">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" class="chain-empty-icon" aria-hidden="true">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
          <span>{{ t('projectPanels.chain.empty') }}</span>
        </div>

        <!-- Add row -->
        <button type="button" class="add-row-btn" :disabled="saveSubmitting" @click="addRow">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          {{ t('projectPanels.chain.addDownstream') }}
        </button>

        <!-- Contextual note -->
        <p class="chain-note">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ t('projectPanels.chain.notePrefix') }}<strong>{{ t('projectPanels.chain.noteRunSuccess') }}</strong>{{ t('projectPanels.chain.noteSuffix') }}
        </p>

        <!-- Save banner -->
        <div v-if="saveBanner" class="chain-banner-wrap">
          <p
            class="chain-banner"
            :class="saveSuccess ? 'chain-banner--ok' : 'chain-banner--err'"
            role="status"
          >{{ saveBanner }}</p>
        </div>

        <div class="chain-save">
          <button
            class="btn-primary"
            :disabled="saveSubmitting"
            :aria-busy="saveSubmitting"
            @click="handleSave"
          >
            <span v-if="saveSubmitting" class="spinner" aria-hidden="true" />
            {{ saveSubmitting ? t('projectPanels.chain.saving') : t('projectPanels.chain.save') }}
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

.card-body { display: flex; flex-direction: column; }

/* ─── Loading / error ─────────────────────────────────────────────────────── */
.chain-loading, .chain-error {
  margin: 0;
  font-size: 0.82rem;
  padding: 14px 16px;
}
.chain-error { color: var(--color-danger, #dc2626); }
.chain-loading { color: var(--color-faint); }

/* ─── Column header ───────────────────────────────────────────────────────── */
.chain-header {
  display: grid;
  grid-template-columns: 1fr 180px 72px 44px;
  gap: 10px;
  padding: 0 16px;
  height: 36px;
  align-items: center;
  background: var(--color-card-2, var(--color-inset));
  border-bottom: 1px solid var(--color-border);
  font-size: 0.72rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-faint);
}

/* ─── Row ─────────────────────────────────────────────────────────────────── */
.chain-row {
  display: grid;
  grid-template-columns: 1fr 180px 72px 44px;
  gap: 10px;
  padding: 10px 16px;
  align-items: start;
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}
.chain-row:hover { background: var(--color-inset); }
.chain-row--disabled { opacity: 0.6; }

.chain-cell { display: flex; flex-direction: column; gap: 4px; }
.chain-cell--toggle { flex-direction: row; align-items: center; gap: 7px; padding-top: 6px; }
.chain-cell--actions { justify-content: center; padding-top: 6px; }

/* ─── Project select ──────────────────────────────────────────────────────── */
.chain-select {
  width: 100%;
  height: 36px;
  padding: 0 10px;
  font: inherit;
  font-size: 0.84rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  appearance: auto;
  transition: border-color var(--duration-fast);
  cursor: pointer;
}
.chain-select:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.chain-select--error { border-color: var(--color-danger, #dc2626); }
.chain-select:disabled { opacity: 0.55; cursor: not-allowed; }
.chain-field-error { font-size: 0.73rem; color: var(--color-danger, #dc2626); line-height: 1.4; }

/* ─── Branch input ────────────────────────────────────────────────────────── */
.chain-branch-wrap { position: relative; }
.chain-branch-icon {
  position: absolute;
  left: 9px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-faint);
  pointer-events: none;
}
.chain-input {
  width: 100%;
  height: 36px;
  padding: 0 10px;
  font: inherit;
  font-size: 0.84rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  box-sizing: border-box;
  transition: border-color var(--duration-fast);
}
.chain-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.chain-input:disabled { opacity: 0.55; cursor: not-allowed; }
.chain-input--mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.8rem; padding-left: 27px; }
.chain-input::placeholder { color: var(--color-faint); }

/* ─── Enabled toggle ──────────────────────────────────────────────────────── */
.chain-toggle {
  position: relative;
  width: 34px;
  height: 19px;
  border-radius: 10px;
  background: var(--color-border-strong);
  border: none;
  cursor: pointer;
  flex: none;
  transition: background-color var(--duration-fast);
}
.chain-toggle--on { background: var(--color-primary); }
.chain-toggle:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.chain-toggle:disabled { opacity: 0.45; cursor: not-allowed; }
.chain-toggle-knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.3);
  transition: transform var(--duration-fast);
  pointer-events: none;
}
.chain-toggle--on .chain-toggle-knob { transform: translateX(15px); }
@media (prefers-reduced-motion: reduce) { .chain-toggle, .chain-toggle-knob { transition: none; } }
.chain-toggle-label { font-size: 0.75rem; font-weight: 600; color: var(--color-dim); }

/* ─── Delete button ───────────────────────────────────────────────────────── */
.row-del-btn {
  width: 30px;
  height: 30px;
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-faint);
  border-radius: var(--rounded-md);
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: color var(--duration-fast), border-color var(--duration-fast), background-color var(--duration-fast);
}
.row-del-btn:hover:not(:disabled) {
  color: var(--color-danger, #dc2626);
  border-color: var(--color-red-line, #fca5a5);
  background: var(--color-red-soft, oklch(97% 0.01 22));
}
.row-del-btn:disabled { opacity: 0.4; cursor: not-allowed; }

/* ─── Empty state ─────────────────────────────────────────────────────────── */
.chain-empty {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 20px 16px;
  font-size: 0.82rem;
  color: var(--color-faint);
  font-style: italic;
  border-bottom: 1px solid var(--color-border);
}
.chain-empty-icon { flex: none; color: var(--color-faint); opacity: 0.5; }

/* ─── Add row button ──────────────────────────────────────────────────────── */
.add-row-btn {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 12px 16px;
  width: 100%;
  border: none;
  background: transparent;
  color: var(--color-primary);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 500;
  cursor: pointer;
  text-align: left;
  transition: color var(--duration-fast), background-color var(--duration-fast);
  border-top: 1px solid var(--color-border);
}
.add-row-btn:hover:not(:disabled) { background: var(--color-inset); }
.add-row-btn:disabled { opacity: 0.45; cursor: not-allowed; }

/* ─── Contextual note ─────────────────────────────────────────────────────── */
.chain-note {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 10px 16px 12px;
  font-size: 0.76rem;
  color: var(--color-faint);
  line-height: 1.6;
  border-top: 1px solid var(--color-border);
  margin: 0;
}
.chain-note svg { flex: none; margin-top: 2px; }
.chain-note strong { color: var(--color-text); font-weight: 600; }

/* ─── Save banner ─────────────────────────────────────────────────────────── */
.chain-banner-wrap { padding: 0 16px; }
.chain-banner { margin: 0; font-size: 0.8rem; font-weight: 500; }
.chain-banner--ok { color: var(--color-success, #16a34a); }
.chain-banner--err { color: var(--color-danger, #dc2626); }

/* ─── Save bar ────────────────────────────────────────────────────────────── */
.chain-save {
  display: flex;
  justify-content: flex-end;
  padding: 12px 16px 14px;
}

/* ─── Btn-primary ─────────────────────────────────────────────────────────── */
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

<script setup lang="ts">
/**
 * PacPreviewModal — fetch & validate the repo's `.pipewright.yml` at a chosen ref
 * BEFORE relying on it to drive runs (GitOps Slice 3).
 *
 * At runtime, an invalid `.pipewright.yml` is silently ignored (the run falls back to the stored
 * UI pipeline) with no feedback. This modal gives that feedback up front: pick a ref, fetch, and
 * see whether the file is found, whether it is valid, the parse error if not, and a summary of the
 * stages the runtime would use. Read-only — it never saves anything and never reveals secrets.
 */
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { previewPacConfig, type PacPreviewResult } from '../../api/projects'
import { HttpError } from '../../api/http'

const props = defineProps<{
  projectId: string
  /** Project default branch — pre-fills the ref input. */
  defaultBranch: string
}>()

const { t } = useI18n()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const refInput = ref(props.defaultBranch ?? '')
const busy = ref(false)
const errorMsg = ref('')
const result = ref<PacPreviewResult | null>(null)

async function runPreview(): Promise<void> {
  if (busy.value) return
  busy.value = true
  errorMsg.value = ''
  result.value = null
  try {
    result.value = await previewPacConfig(props.projectId, refInput.value)
  } catch (err) {
    if (err instanceof HttpError) {
      errorMsg.value = err.status === 0
        ? t('projectPipeline.pacPreviewConnFailed')
        : (err.apiError?.message ?? t('projectPipeline.pacPreviewFailed', { status: err.status }))
    } else {
      errorMsg.value = t('projectPipeline.pacPreviewFailedRetry')
    }
  } finally {
    busy.value = false
  }
}

function onBackdrop(e: MouseEvent): void {
  if (e.target === e.currentTarget) emit('close')
}

// Auto-run on open against the default branch so the user sees a result immediately.
onMounted(() => {
  void runPreview()
})
</script>

<template>
  <div class="pp-backdrop" role="dialog" aria-modal="true" aria-labelledby="pp-title" @mousedown="onBackdrop">
    <div class="pp-modal">
      <header class="pp-head">
        <div>
          <h2 id="pp-title" class="pp-title">{{ t('projectPipeline.pacPreviewTitle') }}</h2>
          <p class="pp-sub">
            {{ t('projectPipeline.pacPreviewSub') }}
            <code>.pipewright.yml</code>
          </p>
        </div>
        <button class="pp-close" :aria-label="t('projectPipeline.pacPreviewCloseAria')" @click="emit('close')">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"/></svg>
        </button>
      </header>

      <div class="pp-body">
        <div class="pp-ref-row">
          <label for="pp-ref" class="pp-label">{{ t('projectPipeline.pacPreviewRefLabel') }}</label>
          <input
            id="pp-ref"
            v-model="refInput"
            class="pp-ref-input"
            type="text"
            spellcheck="false"
            autocomplete="off"
            :placeholder="defaultBranch || 'main'"
            :disabled="busy"
            @keydown.enter.prevent="runPreview"
          />
          <button class="pp-btn pp-btn--primary" :disabled="busy" @click="runPreview">
            <span v-if="busy" class="pp-spin" aria-hidden="true"/>
            {{ t('projectPipeline.pacPreviewFetch') }}
          </button>
        </div>

        <!-- request-level error (network / unexpected) -->
        <div v-if="errorMsg" class="pp-banner pp-banner--error" role="alert">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
          <span>{{ errorMsg }}</span>
        </div>

        <!-- result -->
        <template v-else-if="result">
          <!-- not found -->
          <div v-if="!result.found" class="pp-banner pp-banner--warn" role="status">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
            <span>{{ t('projectPipeline.pacPreviewNotFound', { ref: result.ref }) }}</span>
          </div>

          <!-- found but invalid -->
          <template v-else-if="!result.valid">
            <div class="pp-banner pp-banner--error" role="alert">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
              <span>{{ t('projectPipeline.pacPreviewInvalid', { ref: result.ref }) }}</span>
            </div>
            <pre v-if="result.error" class="pp-error-detail">{{ result.error }}</pre>
          </template>

          <!-- found and valid -->
          <template v-else>
            <div class="pp-banner pp-banner--ok" role="status">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M20 6 9 17l-5-5"/></svg>
              <span>{{ t('projectPipeline.pacPreviewValid', { ref: result.ref, count: result.stageCount }) }}</span>
            </div>
            <ul v-if="result.stages.length" class="pp-stages">
              <li v-for="(stage, i) in result.stages" :key="i" class="pp-stage">
                <span class="pp-stage-name">{{ stage.name }}</span>
                <span class="pp-stage-kind">{{ stage.kind }}</span>
                <span class="pp-stage-jobs">{{ t('projectPipeline.pacPreviewJobCount', { count: stage.jobCount }) }}</span>
              </li>
            </ul>
          </template>
        </template>
      </div>

      <footer class="pp-foot">
        <button class="pp-btn" @click="emit('close')">{{ t('projectPipeline.pacPreviewCloseBtn') }}</button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.pp-backdrop {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: oklch(0% 0 0 / 0.42);
  backdrop-filter: blur(2px);
  display: grid;
  place-items: center;
  padding: 24px;
}

.pp-modal {
  width: min(620px, 100%);
  max-height: min(86vh, 720px);
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-card);
  box-shadow: 0 24px 64px oklch(0% 0 0 / 0.36);
  overflow: hidden;
}

.pp-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 20px 14px;
  border-bottom: 1px solid var(--color-border);
}

.pp-title {
  font-size: 1.02rem;
  font-weight: 700;
  color: var(--color-text);
  letter-spacing: -0.01em;
}

.pp-sub {
  margin-top: 4px;
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.5;
}

.pp-sub code {
  font-family: var(--font-mono, monospace);
  background: var(--color-inset);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 0.92em;
}

.pp-close {
  margin-left: auto;
  flex: none;
  width: 28px;
  height: 28px;
  display: grid;
  place-items: center;
  border: none;
  background: none;
  color: var(--color-faint);
  border-radius: 6px;
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.pp-close:hover { color: var(--color-text); background: var(--color-inset); }
.pp-close:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

.pp-body {
  padding: 16px 20px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.pp-ref-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pp-label {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-dim);
  flex: none;
}

.pp-ref-input {
  flex: 1;
  height: 34px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.82rem;
  color: var(--color-text);
  background: var(--color-canvas, var(--color-inset));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  padding: 0 12px;
}

.pp-ref-input:focus { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px var(--color-primary-soft); }
.pp-ref-input:disabled { opacity: 0.6; }

.pp-banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 0.8rem;
  line-height: 1.5;
  padding: 9px 12px;
  border-radius: var(--rounded-md);
  border: 1px solid transparent;
}

.pp-banner--error {
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
  color: var(--color-red);
}

.pp-banner--ok {
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
  color: var(--color-green);
}

.pp-banner--warn {
  background: var(--color-amber-soft, var(--color-inset));
  border-color: var(--color-amber-line, var(--color-border-strong));
  color: var(--color-amber, var(--color-dim));
}

.pp-banner svg { flex: none; margin-top: 2px; }

.pp-error-detail {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.76rem;
  line-height: 1.5;
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  border-radius: var(--rounded-md);
  padding: 10px 12px;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
}

.pp-stages {
  display: flex;
  flex-direction: column;
  gap: 6px;
  list-style: none;
  margin: 0;
  padding: 0;
}

.pp-stage {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  font-size: 0.82rem;
}

.pp-stage-name {
  font-weight: 600;
  color: var(--color-text);
}

.pp-stage-kind {
  font-family: var(--font-mono, monospace);
  font-size: 0.72rem;
  color: var(--color-cyan);
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 1px 6px;
}

.pp-stage-jobs {
  margin-left: auto;
  font-size: 0.76rem;
  color: var(--color-faint);
}

.pp-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--color-border);
}

.pp-btn {
  height: 34px;
  font-size: 0.83rem;
  font-weight: 500;
  font-family: var(--font-sans);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card-2);
  color: var(--color-text);
  border-radius: var(--rounded);
  padding: 0 14px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.pp-btn:hover:not(:disabled) { border-color: var(--color-faint); }
.pp-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.pp-btn:disabled { opacity: 0.45; cursor: not-allowed; }

.pp-btn--primary {
  background: var(--color-primary);
  color: #fff;
  border-color: transparent;
  font-weight: 600;
  flex: none;
}

.pp-btn--primary:hover:not(:disabled) { background: var(--color-primary-press); }

.pp-spin {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: currentColor;
  border-radius: var(--rounded-full);
  animation: pp-spin 0.7s linear infinite;
}

@keyframes pp-spin { to { transform: rotate(360deg); } }

@media (prefers-reduced-motion: reduce) {
  .pp-spin { animation: none; }
}
</style>

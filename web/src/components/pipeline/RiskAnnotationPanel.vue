<script setup lang="ts">
/**
 * RiskAnnotationPanel — AI script-risk annotation (AI moat).
 *
 * On-demand audit of the pipeline's script-step commands. Calls
 * POST /api/projects/{id}/pipeline/analyze-risks and renders findings grouped
 * by severity. Works even with AI unconfigured — the backend always returns
 * deterministic rule findings (rm -rf /, curl|sh, chmod 777, plaintext
 * secrets, unpinned `latest` images); the LLM pass is additive. Findings are
 * masked server-side and never echo a detected plaintext secret.
 *
 * Design language mirrors DiagnosisPanel (cyan = AI semantic color; severity
 * dots red/amber/grey; mono for the risky line ref). Motion is transform +
 * opacity only, with prefers-reduced-motion guard.
 */
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { analyzeRisks, type RiskFinding, type RiskLevel } from '../../api/aiRisk'
import { HttpError } from '../../api/http'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{
  projectId: string
}>()

const { t } = useI18n()

type RunState = 'idle' | 'running' | 'done' | 'error'

const state = ref<RunState>('idle')
const errorMsg = ref('')
const findings = ref<RiskFinding[]>([])
const aiEnhanced = ref(false)
const aiReason = ref('')

const LEVEL_ORDER: Record<RiskLevel, number> = { high: 0, medium: 1, low: 2 }

const sortedFindings = computed(() =>
  [...findings.value].sort((a, b) => {
    const byLevel = LEVEL_ORDER[a.level] - LEVEL_ORDER[b.level]
    if (byLevel !== 0) return byLevel
    return a.line - b.line
  }),
)

const counts = computed(() => {
  const c = { high: 0, medium: 0, low: 0 }
  for (const f of findings.value) c[f.level]++
  return c
})

const hasFindings = computed(() => findings.value.length > 0)

function levelLabel(level: RiskLevel): string {
  switch (level) {
    case 'high':   return t('pipelinePanels.rapHigh')
    case 'medium': return t('pipelinePanels.rapMedium')
    case 'low':    return t('pipelinePanels.rapLow')
  }
}

async function runAnalysis(): Promise<void> {
  if (state.value === 'running') return
  state.value = 'running'
  errorMsg.value = ''
  try {
    const res = await analyzeRisks(props.projectId)
    findings.value = res.findings
    aiEnhanced.value = res.aiEnhanced
    aiReason.value = res.aiReason
    state.value = 'done'
  } catch (err) {
    if (err instanceof HttpError) {
      errorMsg.value = err.apiError?.message ?? t('pipelinePanels.rapFailed', { status: err.status })
    } else {
      errorMsg.value = t('pipelinePanels.rapFailedRetry')
    }
    state.value = 'error'
  }
}
</script>

<template>
  <section class="rap" :aria-label="t('pipelinePanels.rapAria')">
    <!-- Header -->
    <div class="rap-head">
      <div class="rap-head-left">
        <span class="rap-icon" aria-hidden="true">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
            <path d="M12 8v4M12 16h.01" />
          </svg>
        </span>
        <span class="rap-title">{{ t('pipelinePanels.rapTitle') }}</span>
        <span class="rap-subtitle">{{ t('pipelinePanels.rapSubtitle') }}</span>
      </div>

      <!-- Severity summary (only after a run) -->
      <div v-if="state === 'done' && hasFindings" class="rap-summary" aria-hidden="true">
        <span v-if="counts.high" class="rap-chip rap-chip--high">{{ t('pipelinePanels.rapHighChip', { count: counts.high }) }}</span>
        <span v-if="counts.medium" class="rap-chip rap-chip--medium">{{ t('pipelinePanels.rapMediumChip', { count: counts.medium }) }}</span>
        <span v-if="counts.low" class="rap-chip rap-chip--low">{{ t('pipelinePanels.rapLowChip', { count: counts.low }) }}</span>
      </div>
    </div>

    <!-- Body -->
    <div class="rap-body">
      <!-- Idle / re-run control -->
      <div class="rap-actions">
        <AppButton variant="ai" :loading="state === 'running'" @click="runAnalysis">
          <svg v-if="state !== 'running'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
          </svg>
          {{ state === 'running' ? t('pipelinePanels.rapScanning') : (state === 'idle' ? t('pipelinePanels.rapScan') : t('pipelinePanels.rapRescan')) }}
        </AppButton>
        <p v-if="state === 'idle'" class="rap-hint">
          {{ t('pipelinePanels.rapHint') }}
        </p>
        <span v-else-if="state === 'done'" class="rap-source" :class="{ 'rap-source--ai': aiEnhanced }">
          {{ aiEnhanced ? t('pipelinePanels.rapAiEnhanced') : (aiReason || t('pipelinePanels.rapRulesOnly')) }}
        </span>
      </div>

      <!-- Error -->
      <p v-if="state === 'error'" class="rap-error" role="alert">{{ errorMsg }}</p>

      <!-- Clean -->
      <div v-else-if="state === 'done' && !hasFindings" class="rap-clean" role="status">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M20 6 9 17l-5-5" />
        </svg>
        <span>{{ t('pipelinePanels.rapClean') }}</span>
      </div>

      <!-- Findings list -->
      <ul v-else-if="state === 'done' && hasFindings" class="rap-list" role="list">
        <li
          v-for="(f, idx) in sortedFindings"
          :key="idx"
          class="rap-item"
          :class="`rap-item--${f.level}`"
        >
          <span class="rap-dot" :class="`rap-dot--${f.level}`" aria-hidden="true" />
          <div class="rap-item-body">
            <div class="rap-item-head">
              <span class="rap-badge" :class="`rap-badge--${f.level}`">{{ levelLabel(f.level) }}</span>
              <span class="rap-item-title">{{ f.title }}</span>
              <span v-if="f.source === 'ai'" class="rap-tag-ai" :title="t('pipelinePanels.rapAiTitle')">AI</span>
              <span class="rap-loc">
                {{ f.stepName || t('pipelinePanels.rapStep') }}<template v-if="f.line > 0">{{ t('pipelinePanels.rapLine', { line: f.line }) }}</template>
              </span>
            </div>
            <p class="rap-why">{{ f.why }}</p>
            <p v-if="f.suggestion" class="rap-fix">
              <span class="rap-fix-arrow" aria-hidden="true">→</span>{{ f.suggestion }}
            </p>
          </div>
        </li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.rap {
  border-radius: var(--rounded-card);
  border: 1px solid var(--color-cyan-line);
  background: var(--color-card);
  overflow: hidden;
  box-shadow: var(--shadow);
  margin-top: 18px;
}

.rap-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 16px;
  background: var(--color-cyan-soft);
  border-bottom: 1px solid var(--color-cyan-line);
}

.rap-head-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.rap-icon {
  color: var(--color-cyan);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.rap-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-cyan);
  white-space: nowrap;
}

.rap-subtitle {
  font-size: 0.71rem;
  color: var(--color-faint);
  font-style: italic;
}

.rap-summary {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.rap-chip {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 2px 9px;
  border-radius: var(--rounded-full);
  border: 1px solid transparent;
  white-space: nowrap;
}

.rap-chip--high   { color: var(--color-red);   background: var(--color-red-soft);   border-color: var(--color-red-line, var(--color-red)); }
.rap-chip--medium { color: var(--color-amber); background: var(--color-amber-soft); border-color: var(--color-amber-line); }
.rap-chip--low    { color: var(--color-faint); background: var(--color-card-2);     border-color: var(--color-border-strong); }

.rap-body {
  padding: 16px 18px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.rap-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.rap-hint {
  font-size: 0.76rem;
  color: var(--color-faint);
  line-height: 1.5;
  flex: 1;
  min-width: 18ch;
}

.rap-source {
  font-size: 0.74rem;
  color: var(--color-faint);
  font-weight: 500;
}

.rap-source--ai {
  color: var(--color-cyan);
}

.rap-error {
  font-size: 0.8rem;
  color: var(--color-red);
  line-height: 1.5;
}

.rap-clean {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.84rem;
  font-weight: 500;
  color: var(--color-green);
  padding: 8px 0;
}

.rap-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
  padding: 0;
}

.rap-item {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 11px;
  padding: 12px 14px;
  border-radius: var(--rounded-lg);
  border: 1px solid var(--color-border);
  background: var(--color-inset);
  border-left-width: 3px;
  animation: rap-in 0.35s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1)) both;
}

.rap-item--high   { border-left-color: var(--color-red); }
.rap-item--medium { border-left-color: var(--color-amber); }
.rap-item--low    { border-left-color: var(--color-border-strong); }

@keyframes rap-in {
  from { opacity: 0; transform: translateY(6px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .rap-item { animation: none; }
}

.rap-dot {
  width: 9px;
  height: 9px;
  border-radius: var(--rounded-full);
  margin-top: 5px;
  flex-shrink: 0;
}

.rap-dot--high   { background: var(--color-red); }
.rap-dot--medium { background: var(--color-amber); }
.rap-dot--low    { background: var(--color-border-strong); }

.rap-item-body {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.rap-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.rap-badge {
  font-size: 0.66rem;
  font-weight: 700;
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  text-transform: uppercase;
  letter-spacing: 0.03em;
  flex-shrink: 0;
}

.rap-badge--high   { color: var(--color-red);   background: var(--color-red-soft); }
.rap-badge--medium { color: var(--color-amber); background: var(--color-amber-soft); }
.rap-badge--low    { color: var(--color-faint); background: var(--color-card-2); }

.rap-item-title {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-text);
}

.rap-tag-ai {
  font-size: 0.62rem;
  font-weight: 700;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  padding: 0 5px;
  border-radius: var(--rounded-full);
  letter-spacing: 0.04em;
}

.rap-loc {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--color-faint);
  margin-left: auto;
  white-space: nowrap;
}

.rap-why {
  font-size: 0.8rem;
  line-height: 1.55;
  color: var(--color-dim);
}

.rap-fix {
  display: flex;
  gap: 6px;
  font-size: 0.79rem;
  line-height: 1.5;
  color: var(--color-text);
  padding: 6px 10px;
  background: var(--color-cyan-soft);
  border-radius: var(--rounded);
}

.rap-fix-arrow {
  color: var(--color-cyan);
  font-weight: 700;
  font-family: var(--font-mono);
  flex-shrink: 0;
}
</style>

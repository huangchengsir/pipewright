<script setup lang="ts">
/**
 * SettingsDiagnosisStats — Story 7-5: diagnosis feedback-loop stats (FR-26).
 *
 * Read-only dashboard for the diagnosis 👍/👎 feedback loop:
 *   - accuracy ring (👍 / total)
 *   - counts (total / up / down)
 *   - recent trend (most-recent-N buckets)
 *   - recent corrections (👎 with correct root cause — knowledge-base seeds)
 *
 * No feedback yet ⇒ graceful empty state (never an error). The correctRootCause
 * shown here is already masked server-side; no secrets reach this view.
 */

import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getDiagnosisStats } from '../../api/settings'
import type { DiagnosisStats } from '../../api/settings'
import { HttpError } from '../../api/http'

type LoadState = 'loading' | 'ready' | 'error'

const { t, locale } = useI18n()

const loadState = ref<LoadState>('loading')
const loadError = ref('')
const stats = ref<DiagnosisStats | null>(null)

const hasFeedback = computed(() => (stats.value?.totalFeedback ?? 0) > 0)

// Accuracy as percentage (null → 0 for ring geometry; UI shows "—" when null).
const accuracyPct = computed(() => {
  const a = stats.value?.accuracy
  return a === null || a === undefined ? 0 : Math.round(a * 100)
})

// SVG ring geometry (r=52 → circumference ≈ 326.7).
const RING_CIRC = 2 * Math.PI * 52
const ringDash = computed(() => {
  const frac = accuracyPct.value / 100
  return `${(frac * RING_CIRC).toFixed(1)} ${RING_CIRC.toFixed(1)}`
})

function fmtTime(rfc: string): string {
  if (!rfc) return ''
  const d = new Date(rfc)
  if (Number.isNaN(d.getTime())) return rfc
  return d.toLocaleString(locale.value, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

async function load(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    stats.value = await getDiagnosisStats()
    loadState.value = 'ready'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value = err.apiError?.message ?? t('settingsDiagnosisStats.errLoadFailed', { status: err.status })
    } else {
      loadError.value = t('settingsDiagnosisStats.errLoadGeneric')
    }
    loadState.value = 'error'
  }
}

onMounted(load)
</script>

<template>
  <section class="ds" aria-labelledby="ds-heading">
    <header class="ds-head">
      <div>
        <h2 id="ds-heading" class="ds-title">{{ t('settingsDiagnosisStats.title') }}</h2>
        <p class="ds-sub">{{ t('settingsDiagnosisStats.subtitle') }}</p>
      </div>
      <button class="ds-refresh" :disabled="loadState === 'loading'" @click="load">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M1 4v6h6M23 20v-6h-6" /><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
        </svg>
        {{ t('common.refresh') }}
      </button>
    </header>

    <!-- loading -->
    <div v-if="loadState === 'loading'" class="ds-state" aria-busy="true">
      <span class="ds-spinner" aria-hidden="true" />
      <span>{{ t('settingsDiagnosisStats.loading') }}</span>
    </div>

    <!-- error -->
    <div v-else-if="loadState === 'error'" class="ds-state ds-state--error" role="alert">
      <p>{{ loadError }}</p>
      <button class="ds-retry" @click="load">{{ t('settingsDiagnosisStats.retry') }}</button>
    </div>

    <!-- ready -->
    <template v-else-if="stats">
      <!-- empty -->
      <div v-if="!hasFeedback" class="ds-empty">
        <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true">
          <path d="M14 9V5a3 3 0 0 0-3-3l-4 9v11h11.28a2 2 0 0 0 2-1.7l1.38-9a2 2 0 0 0-2-2.3zM7 22H4a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2h3" />
        </svg>
        <p class="ds-empty-title">{{ t('settingsDiagnosisStats.emptyTitle') }}</p>
        <p class="ds-empty-hint">{{ t('settingsDiagnosisStats.emptyHint') }}</p>
      </div>

      <template v-else>
        <!-- top: accuracy ring + counts -->
        <div class="ds-top">
          <!-- accuracy ring -->
          <div class="ds-ring-card">
            <div class="ds-ring-wrap">
              <svg viewBox="0 0 120 120" class="ds-ring" role="img" :aria-label="t('settingsDiagnosisStats.accuracyAria', { pct: accuracyPct })">
                <circle class="ds-ring-track" cx="60" cy="60" r="52" />
                <circle
                  class="ds-ring-fill"
                  cx="60" cy="60" r="52"
                  :stroke-dasharray="ringDash"
                  transform="rotate(-90 60 60)"
                />
              </svg>
              <div class="ds-ring-center">
                <span class="ds-ring-val">{{ stats.accuracy === null ? '—' : `${accuracyPct}%` }}</span>
                <span class="ds-ring-label">{{ t('settingsDiagnosisStats.accuracy') }}</span>
              </div>
            </div>
          </div>

          <!-- counts -->
          <div class="ds-counts">
            <div class="ds-count">
              <span class="ds-count-val">{{ stats.totalFeedback }}</span>
              <span class="ds-count-label">{{ t('settingsDiagnosisStats.countTotal') }}</span>
            </div>
            <div class="ds-count ds-count--up">
              <span class="ds-count-val">{{ stats.thumbsUp }}</span>
              <span class="ds-count-label">{{ t('settingsDiagnosisStats.countUp') }}</span>
            </div>
            <div class="ds-count ds-count--down">
              <span class="ds-count-val">{{ stats.thumbsDown }}</span>
              <span class="ds-count-label">{{ t('settingsDiagnosisStats.countDown') }}</span>
            </div>
          </div>
        </div>

        <!-- trend -->
        <div v-if="stats.recentTrend.length > 0" class="ds-trend">
          <h3 class="ds-section-title">{{ t('settingsDiagnosisStats.trendTitle') }}</h3>
          <div class="ds-trend-bars">
            <div
              v-for="(b, i) in stats.recentTrend"
              :key="i"
              class="ds-trend-col"
            >
              <div class="ds-trend-bar-track">
                <div
                  class="ds-trend-bar-fill"
                  :style="{ height: `${Math.round(b.accuracy * 100)}%` }"
                  :title="t('settingsDiagnosisStats.trendBarTitle', { pct: Math.round(b.accuracy * 100), count: b.count })"
                />
              </div>
              <span class="ds-trend-pct">{{ Math.round(b.accuracy * 100) }}%</span>
              <span class="ds-trend-n">{{ t('settingsDiagnosisStats.countUnit', { count: b.count }) }}</span>
            </div>
          </div>
        </div>

        <!-- corrections -->
        <div class="ds-corrections">
          <h3 class="ds-section-title">{{ t('settingsDiagnosisStats.correctionsTitle') }}</h3>
          <p v-if="stats.recentCorrections.length === 0" class="ds-corr-empty">
            {{ t('settingsDiagnosisStats.correctionsEmpty') }}
          </p>
          <ul v-else class="ds-corr-list" role="list">
            <li v-for="(c, i) in stats.recentCorrections" :key="i" class="ds-corr-item">
              <div class="ds-corr-head">
                <router-link :to="`/runs/${c.runId}`" class="ds-corr-run">{{ t('settingsDiagnosisStats.runLabel', { id: c.runId.slice(0, 8) }) }}</router-link>
                <span class="ds-corr-at">{{ fmtTime(c.at) }}</span>
              </div>
              <p class="ds-corr-text">{{ c.correctRootCause }}</p>
            </li>
          </ul>
        </div>
      </template>
    </template>
  </section>
</template>

<style scoped>
.ds {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.ds-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.ds-title {
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text);
}

.ds-sub {
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.5;
  margin-top: 4px;
  max-width: 60ch;
}

.ds-refresh {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-dim);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  padding: 7px 13px;
  cursor: pointer;
  flex-shrink: 0;
  transition: color var(--duration-fast), border-color var(--duration-fast);
}

.ds-refresh:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-cyan-line);
}

.ds-refresh:disabled { opacity: 0.5; cursor: default; }

/* states */
.ds-state {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 40px 0;
  color: var(--color-faint);
  font-size: 0.85rem;
}

.ds-state--error {
  flex-direction: column;
  color: var(--color-red);
}

.ds-retry, .ds-spinner { /* shared base for retry/spinner */ }

.ds-spinner {
  width: 16px; height: 16px;
  border: 2px solid var(--color-inset);
  border-top-color: var(--color-cyan);
  border-radius: var(--rounded-full);
  animation: ds-spin 0.7s linear infinite;
}

@keyframes ds-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .ds-spinner { animation: none; } }

.ds-retry {
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-cyan);
  background: none;
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded);
  padding: 6px 14px;
  cursor: pointer;
}

/* empty */
.ds-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  text-align: center;
  padding: 48px 16px;
  color: var(--color-faint);
}

.ds-empty svg { opacity: 0.45; }

.ds-empty-title {
  font-size: 0.92rem;
  font-weight: 600;
  color: var(--color-dim);
}

.ds-empty-hint { font-size: 0.8rem; line-height: 1.5; }

/* top: ring + counts */
.ds-top {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 24px;
  align-items: center;
}

@media (max-width: 640px) {
  .ds-top { grid-template-columns: 1fr; }
}

.ds-ring-card {
  display: grid;
  place-items: center;
  padding: 12px;
  background: var(--color-card-2, var(--color-inset));
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
}

.ds-ring-wrap {
  position: relative;
  width: 132px;
  height: 132px;
}

.ds-ring { width: 100%; height: 100%; }

.ds-ring-track {
  fill: none;
  stroke: var(--color-border);
  stroke-width: 10;
}

.ds-ring-fill {
  fill: none;
  stroke: var(--color-cyan);
  stroke-width: 10;
  stroke-linecap: round;
  transition: stroke-dasharray 0.8s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1));
}

@media (prefers-reduced-motion: reduce) { .ds-ring-fill { transition: none; } }

.ds-ring-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.ds-ring-val {
  font-size: 1.7rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}

.ds-ring-label {
  font-size: 0.72rem;
  color: var(--color-faint);
  margin-top: 2px;
}

.ds-counts {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
}

.ds-count {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
}

.ds-count-val {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}

.ds-count-label {
  font-size: 0.74rem;
  color: var(--color-faint);
}

.ds-count--up { border-color: var(--color-green-line); }
.ds-count--up .ds-count-val { color: var(--color-green); }
.ds-count--down { border-color: var(--color-amber-line); }
.ds-count--down .ds-count-val { color: var(--color-amber); }

/* section title */
.ds-section-title {
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-faint);
  margin-bottom: 12px;
}

/* trend */
.ds-trend-bars {
  display: flex;
  align-items: flex-end;
  gap: 16px;
  height: 120px;
  padding: 8px 4px 0;
}

.ds-trend-col {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  flex: 1;
  max-width: 80px;
}

.ds-trend-bar-track {
  width: 100%;
  flex: 1;
  display: flex;
  align-items: flex-end;
  background: var(--color-inset);
  border-radius: var(--rounded-sm, 4px);
  overflow: hidden;
  min-height: 60px;
}

.ds-trend-bar-fill {
  width: 100%;
  background: linear-gradient(to top, var(--color-cyan), var(--color-cyan-line));
  border-radius: var(--rounded-sm, 4px) var(--rounded-sm, 4px) 0 0;
  min-height: 3px;
  transition: height 0.6s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1));
}

@media (prefers-reduced-motion: reduce) { .ds-trend-bar-fill { transition: none; } }

.ds-trend-pct {
  font-size: 0.78rem;
  font-weight: 700;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}

.ds-trend-n { font-size: 0.68rem; color: var(--color-faint); }

/* corrections */
.ds-corr-empty {
  font-size: 0.8rem;
  color: var(--color-faint);
}

.ds-corr-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ds-corr-item {
  padding: 12px 14px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-amber);
  border-radius: var(--rounded);
}

.ds-corr-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 6px;
}

.ds-corr-run {
  font-family: var(--font-mono);
  font-size: 0.74rem;
  font-weight: 600;
  color: var(--color-cyan);
  text-decoration: none;
}

.ds-corr-run:hover { text-decoration: underline; }

.ds-corr-at {
  font-size: 0.7rem;
  color: var(--color-faint);
}

.ds-corr-text {
  font-size: 0.84rem;
  line-height: 1.55;
  color: var(--color-dim);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>

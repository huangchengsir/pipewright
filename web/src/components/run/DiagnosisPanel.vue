<script setup lang="ts">
/**
 * DiagnosisPanel — Story 7-2: AI failure diagnosis panel (FR-22).
 *
 * Renders three states driven by diagnosis.status:
 *   ready       → two-column layout: "AI 认为" (left) + "原始日志证据" (right)
 *   unavailable → friendly fallback with reason + re-diagnose button
 *   pending     → skeleton / "AI 分析中" indicator
 *
 * If diagnosis is null (failed run but not yet diagnosed), shows a
 * "触发诊断" button so the user can request the first diagnosis.
 *
 * Design anchors (mock-run-diagnosis-v6.html):
 *   - Cyan = AI semantic color (--color-cyan)
 *   - Pure-black terminal background for evidence (--color-term)
 *   - Hit-line: red-soft background + full-row highlight
 *   - JetBrains Mono for all code/log text
 *   - Confidence badge: high=green, medium=amber, low=faint+italic
 *   - Motion: transform + opacity only; prefers-reduced-motion guard
 *
 * Constraints:
 *   - No new UI libraries; reuses design tokens + AppButton
 *   - Evidence already masked server-side; no secrets reach this component
 *   - Hypothesis wording: "假说,非结论" — preserved from server response
 */

import { ref } from 'vue'
import type { DiagnosisDTO } from '../../api/runs'
import { diagnoseRun } from '../../api/runs'
import { HttpError } from '../../api/http'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{
  diagnosis: DiagnosisDTO | null
  runId: string
}>()

const emit = defineEmits<{
  diagnosed: [diagnosis: DiagnosisDTO]
}>()

// ─── Re-diagnose / initial diagnose state ────────────────────────────────────

const diagnosing = ref(false)
const diagnoseError = ref('')

// Expand/collapse alternate causes
const altCausesExpanded = ref(false)

async function handleDiagnose(): Promise<void> {
  if (diagnosing.value) return
  diagnosing.value = true
  diagnoseError.value = ''
  try {
    const result = await diagnoseRun(props.runId)
    emit('diagnosed', result)
  } catch (err) {
    if (err instanceof HttpError) {
      diagnoseError.value = err.apiError?.message ?? `诊断请求失败(${err.status})`
    } else {
      diagnoseError.value = '诊断请求失败,请稍后重试'
    }
  } finally {
    diagnosing.value = false
  }
}

// ─── Confidence helpers ───────────────────────────────────────────────────────

type ConfLabel = { text: string; cls: string }

function confidenceLabel(level: DiagnosisDTO['confidence']): ConfLabel {
  switch (level) {
    case 'high':   return { text: '置信度·高',  cls: 'conf-badge--high'   }
    case 'medium': return { text: '置信度·中',  cls: 'conf-badge--medium' }
    case 'low':    return { text: '置信度·低',  cls: 'conf-badge--low'    }
  }
}
</script>

<template>
  <section class="dp" aria-label="AI 失败诊断">

    <!-- ─── Panel header ──────────────────────────────────────────────── -->
    <div class="dp-head">
      <div class="dp-head-left">
        <!-- Activity-pulse icon (AI semantic) -->
        <span class="dp-icon" aria-hidden="true">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="2 12 6 12 8 4 10 20 12 10 14 15 16 12 22 12"/>
          </svg>
        </span>
        <span class="dp-title">AI 失败诊断</span>
        <span class="dp-subtitle">假说,非结论</span>
      </div>

      <!-- Confidence badge (only when ready) -->
      <span
        v-if="diagnosis?.status === 'ready'"
        class="conf-badge"
        :class="confidenceLabel(diagnosis.confidence).cls"
        :aria-label="`置信度: ${diagnosis.confidence}`"
      >
        {{ confidenceLabel(diagnosis.confidence).text }}
        <!-- Visual bar -->
        <span class="conf-bar" aria-hidden="true">
          <span
            class="conf-bar-fill"
            :style="{
              width: diagnosis.confidence === 'high' ? '88%'
                   : diagnosis.confidence === 'medium' ? '55%'
                   : '25%'
            }"
          />
        </span>
      </span>
    </div>

    <!-- ─── STATE: null — no diagnosis yet ──────────────────────────── -->
    <template v-if="!diagnosis">
      <div class="dp-body dp-body--trigger">
        <div class="dp-trigger-prompt">
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true" class="dp-trigger-icon">
            <polyline points="2 12 6 12 8 4 10 20 12 10 14 15 16 12 22 12"/>
          </svg>
          <p class="dp-trigger-text">尚未生成 AI 诊断</p>
          <p class="dp-trigger-hint">点击「分析失败原因」，AI 将从日志中提取根因假说与修复建议</p>
          <AppButton
            variant="ai"
            :loading="diagnosing"
            @click="handleDiagnose"
          >
            <svg v-if="!diagnosing" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
              <polyline points="2 12 6 12 8 4 10 20 12 10 14 15 16 12 22 12"/>
            </svg>
            {{ diagnosing ? 'AI 分析中…' : '分析失败原因' }}
          </AppButton>
          <p v-if="diagnoseError" class="dp-error" role="alert">{{ diagnoseError }}</p>
        </div>
      </div>
    </template>

    <!-- ─── STATE: pending ───────────────────────────────────────────── -->
    <template v-else-if="diagnosis.status === 'pending'">
      <div class="dp-body dp-body--pending" aria-busy="true" aria-label="AI 分析中">
        <div class="dp-pending-row">
          <span class="dp-spinner" aria-hidden="true" />
          <span class="dp-pending-text">AI 分析中，正在提取根因假说…</span>
        </div>
        <!-- Skeleton rows -->
        <div class="dp-skel-group">
          <div class="dp-skel dp-skel--title" />
          <div class="dp-skel dp-skel--body" />
          <div class="dp-skel dp-skel--body dp-skel--short" />
        </div>
      </div>
    </template>

    <!-- ─── STATE: unavailable ───────────────────────────────────────── -->
    <template v-else-if="diagnosis.status === 'unavailable'">
      <div class="dp-body dp-body--unavailable">
        <div class="dp-unavail-inner">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true" class="dp-unavail-icon">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          <p class="dp-unavail-title">诊断不可用</p>
          <p v-if="diagnosis.reason" class="dp-unavail-reason">{{ diagnosis.reason }}</p>
          <AppButton
            variant="ai"
            :loading="diagnosing"
            @click="handleDiagnose"
          >
            <svg v-if="!diagnosing" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
              <path d="M1 4v6h6M23 20v-6h-6"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>
            </svg>
            {{ diagnosing ? 'AI 分析中…' : '重新诊断' }}
          </AppButton>
          <p v-if="diagnoseError" class="dp-error" role="alert">{{ diagnoseError }}</p>
        </div>
      </div>
    </template>

    <!-- ─── STATE: ready — two-column AI / evidence layout ───────────── -->
    <template v-else-if="diagnosis.status === 'ready'">
      <div class="dp-body dp-body--ready">

        <!-- ── Left column: AI认为 ─────────────────────────────────── -->
        <div class="dp-col dp-col--ai" role="region" aria-label="AI 认为">
          <!-- Column label -->
          <div class="dp-col-label dp-col-label--ai" aria-hidden="true">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
              <polyline points="2 12 6 12 8 4 10 20 12 10 14 15 16 12 22 12"/>
            </svg>
            AI 认为
          </div>

          <!-- Hypothesis: large, prominent -->
          <div class="dp-hypothesis">
            <p class="dp-hypothesis-intro">最可能的根因是</p>
            <p class="dp-hypothesis-text">{{ diagnosis.hypothesis }}</p>
          </div>

          <!-- Fix suggestions -->
          <div v-if="diagnosis.fixSuggestions.length > 0" class="dp-fixes">
            <div class="dp-fixes-label">
              <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>
              </svg>
              修复建议
            </div>
            <ul class="dp-fix-list" role="list">
              <li
                v-for="(fix, idx) in diagnosis.fixSuggestions"
                :key="idx"
                class="dp-fix-item"
              >
                <span class="dp-fix-bullet" aria-hidden="true">→</span>
                <span>{{ fix }}</span>
              </li>
            </ul>
          </div>

          <!-- Alternate causes (low confidence) -->
          <div v-if="diagnosis.confidence === 'low' && diagnosis.alternateCauses.length > 0" class="dp-alt-causes">
            <button
              class="dp-alt-toggle"
              :aria-expanded="altCausesExpanded"
              @click="altCausesExpanded = !altCausesExpanded"
            >
              <svg
                width="11"
                height="11"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2.5"
                aria-hidden="true"
                class="dp-alt-chevron"
                :class="{ 'dp-alt-chevron--open': altCausesExpanded }"
              >
                <path d="M9 18l6-6-6-6"/>
              </svg>
              存在其它可能根因 ({{ diagnosis.alternateCauses.length }})
            </button>
            <ul v-if="altCausesExpanded" class="dp-alt-list" role="list">
              <li
                v-for="(cause, idx) in diagnosis.alternateCauses"
                :key="idx"
                class="dp-alt-item"
              >
                <span class="dp-alt-bullet" aria-hidden="true">·</span>
                <span>{{ cause }}</span>
              </li>
            </ul>
          </div>

          <!-- Generated timestamp -->
          <p v-if="diagnosis.generatedAt" class="dp-generated-at">
            诊断生成于 {{ new Date(diagnosis.generatedAt).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }}
          </p>
        </div>

        <!-- ── Right column: 原始日志证据 ─────────────────────────── -->
        <div class="dp-col dp-col--evidence" role="region" aria-label="原始日志证据">
          <!-- Column label -->
          <div class="dp-col-label dp-col-label--evidence" aria-hidden="true">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
              <rect x="2" y="3" width="20" height="18" rx="3"/><path d="M7 8l4 4-4 4M13 16h4"/>
            </svg>
            原始日志证据
          </div>

          <!-- Evidence terminal block -->
          <div class="dp-term" role="region" aria-label="日志证据行">
            <!-- Mac-style title bar -->
            <div class="dp-term-bar" aria-hidden="true">
              <span class="dp-term-dot dp-term-dot--red" />
              <span class="dp-term-dot dp-term-dot--amber" />
              <span class="dp-term-dot dp-term-dot--green" />
              <span class="dp-term-name">failure.log</span>
            </div>

            <!-- Log lines -->
            <div
              v-if="diagnosis.evidence.length > 0"
              class="dp-term-feed"
              role="list"
            >
              <div
                v-for="ev in diagnosis.evidence"
                :key="ev.line"
                class="dp-term-line"
                :class="{ 'dp-term-line--hit': ev.highlight }"
                role="listitem"
                :aria-label="ev.highlight ? `命中行 ${ev.line}: ${ev.text}` : `行 ${ev.line}: ${ev.text}`"
              >
                <span class="dp-term-ln" aria-hidden="true">{{ ev.line }}</span>
                <span class="dp-term-code">{{ ev.text }}</span>
              </div>
            </div>

            <!-- Empty evidence -->
            <div v-else class="dp-term-empty">
              <span>无证据行</span>
            </div>
          </div>
        </div>

      </div>
    </template>

  </section>
</template>

<style scoped>
/* ─── Panel host ─────────────────────────────────────────────────────────── */
.dp {
  border-radius: var(--rounded-card);
  border: 1px solid var(--color-cyan-line);
  background: var(--color-card);
  overflow: hidden;
  box-shadow: var(--shadow);
  animation: dp-in 0.45s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1)) both;
}

@keyframes dp-in {
  from { opacity: 0; transform: translateY(12px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .dp { animation: none; }
}

/* ─── Panel header ───────────────────────────────────────────────────────── */
.dp-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 16px;
  background: var(--color-cyan-soft);
  border-bottom: 1px solid var(--color-cyan-line);
}

.dp-head-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.dp-icon {
  color: var(--color-cyan);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.dp-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-cyan);
  white-space: nowrap;
}

.dp-subtitle {
  font-size: 0.71rem;
  color: var(--color-faint);
  font-style: italic;
}

/* ─── Confidence badge ───────────────────────────────────────────────────── */
.conf-badge {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 3px 10px 3px 11px;
  border-radius: var(--rounded-full);
  border: 1px solid transparent;
  white-space: nowrap;
  flex-shrink: 0;
}

.conf-badge--high {
  color: var(--color-green);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}

.conf-badge--medium {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}

.conf-badge--low {
  color: var(--color-faint);
  background: var(--color-card-2);
  border-color: var(--color-border-strong);
  font-style: italic;
}

.conf-bar {
  display: block;
  width: 56px;
  height: 4px;
  border-radius: var(--rounded-full);
  background: oklch(100% 0 0 / 0.12);
  overflow: hidden;
  flex-shrink: 0;
}

.conf-bar-fill {
  display: block;
  height: 100%;
  border-radius: var(--rounded-full);
  background: currentColor;
  animation: conf-grow 1s 0.3s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1)) both;
}

@keyframes conf-grow {
  from { width: 0 !important; }
}

@media (prefers-reduced-motion: reduce) {
  .conf-bar-fill { animation: none; }
}

/* ─── Body wrapper ───────────────────────────────────────────────────────── */
.dp-body {
  padding: 18px 20px;
}

/* ─── null / trigger state ───────────────────────────────────────────────── */
.dp-body--trigger {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
}

.dp-trigger-prompt {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
  max-width: 36ch;
}

.dp-trigger-icon {
  color: var(--color-faint);
  opacity: 0.5;
}

.dp-trigger-text {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-dim);
}

.dp-trigger-hint {
  font-size: 0.78rem;
  color: var(--color-faint);
  line-height: 1.6;
}

/* ─── pending state ──────────────────────────────────────────────────────── */
.dp-body--pending {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dp-pending-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded);
  color: var(--color-cyan);
}

.dp-spinner {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-cyan-soft);
  border-top-color: var(--color-cyan);
  border-radius: var(--rounded-full);
  animation: dp-spin 0.7s linear infinite;
  flex-shrink: 0;
}

@keyframes dp-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .dp-spinner { animation: none; border-top-color: currentColor; }
}

.dp-pending-text {
  font-size: 0.83rem;
  font-weight: 500;
}

/* Skeleton shimmer */
.dp-skel-group {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.dp-skel {
  display: block;
  border-radius: var(--rounded-md);
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.05) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  animation: dp-shimmer 1.4s ease-in-out infinite;
}

@keyframes dp-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .dp-skel { animation: none; background: var(--color-inset); }
}

.dp-skel--title { height: 20px; width: 65%; }
.dp-skel--body  { height: 14px; }
.dp-skel--short { width: 45%; }

/* ─── unavailable state ──────────────────────────────────────────────────── */
.dp-body--unavailable {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
}

.dp-unavail-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
  max-width: 40ch;
}

.dp-unavail-icon {
  color: var(--color-faint);
  opacity: 0.6;
}

.dp-unavail-title {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-dim);
}

.dp-unavail-reason {
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.6;
}

/* ─── error message ──────────────────────────────────────────────────────── */
.dp-error {
  font-size: 0.78rem;
  color: var(--color-red);
  text-align: center;
  line-height: 1.5;
}

/* ─── ready: two-column layout ──────────────────────────────────────────── */
.dp-body--ready {
  display: grid;
  grid-template-columns: 1.45fr 1fr;
  gap: 18px;
  align-items: start;
  padding: 20px;
}

@media (max-width: 860px) {
  .dp-body--ready {
    grid-template-columns: 1fr;
  }
}

/* ─── column shared ──────────────────────────────────────────────────────── */
.dp-col {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.dp-col-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.dp-col-label--ai {
  color: var(--color-cyan);
}

.dp-col-label--evidence {
  color: var(--color-faint);
}

/* ─── hypothesis block ───────────────────────────────────────────────────── */
.dp-hypothesis {
  padding: 14px 16px;
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: var(--rounded-lg);
}

.dp-hypothesis-intro {
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-cyan);
  opacity: 0.75;
  margin-bottom: 6px;
}

.dp-hypothesis-text {
  font-size: 1.0rem;
  font-weight: 600;
  line-height: 1.55;
  letter-spacing: -0.005em;
  color: var(--color-text);
}

/* ─── fix suggestions ────────────────────────────────────────────────────── */
.dp-fixes {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.dp-fixes-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-faint);
}

.dp-fix-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.dp-fix-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 0.83rem;
  line-height: 1.5;
  color: var(--color-dim);
  padding: 8px 12px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  transition: background-color var(--duration-fast), border-color var(--duration-fast);
}

.dp-fix-item:hover {
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
  color: var(--color-text);
}

@media (prefers-reduced-motion: reduce) {
  .dp-fix-item { transition: none; }
}

.dp-fix-bullet {
  color: var(--color-cyan);
  font-weight: 700;
  flex-shrink: 0;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  margin-top: 1px;
}

/* ─── alternate causes ───────────────────────────────────────────────────── */
.dp-alt-causes {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.dp-alt-toggle {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-family: var(--font-sans);
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-faint);
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
  transition: color var(--duration-fast);
  text-align: left;
}

.dp-alt-toggle:hover {
  color: var(--color-dim);
}

.dp-alt-toggle:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: 3px;
}

.dp-alt-chevron {
  flex-shrink: 0;
  transition: transform var(--duration-fast);
}

.dp-alt-chevron--open {
  transform: rotate(90deg);
}

@media (prefers-reduced-motion: reduce) {
  .dp-alt-chevron { transition: none; }
}

.dp-alt-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding-left: 4px;
}

.dp-alt-item {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--color-faint);
}

.dp-alt-bullet {
  color: var(--color-border-strong);
  font-size: 1rem;
  line-height: 1.4;
  flex-shrink: 0;
}

/* ─── generated-at timestamp ────────────────────────────────────────────── */
.dp-generated-at {
  font-size: 0.7rem;
  color: var(--color-faint);
  margin-top: 2px;
}

/* ─── terminal (evidence) ────────────────────────────────────────────────── */
.dp-term {
  background: var(--color-term);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  box-shadow: var(--shadow-inner);
  overflow: hidden;
}

/* Mac-style titlebar */
.dp-term-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  border-bottom: 1px solid oklch(100% 0 0 / 0.05);
  background: oklch(11% 0.004 270);
}

.dp-term-dot {
  width: 11px;
  height: 11px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.dp-term-dot--red   { background: #ff5f56; }
.dp-term-dot--amber { background: #ffbd2e; }
.dp-term-dot--green { background: #27c93f; }

.dp-term-name {
  font-family: var(--font-mono);
  font-size: 0.7rem;
  color: var(--color-line-num);
  margin-left: 6px;
}

/* Log lines feed */
.dp-term-feed {
  font-family: var(--font-mono);
  font-size: var(--text-mono);
  line-height: 2;
  padding: 8px 0;
}

.dp-term-line {
  display: grid;
  grid-template-columns: 46px 1fr;
  padding: 0 14px;
  transition: background-color var(--duration-fast);
}

/* Normal line */
.dp-term-line .dp-term-ln {
  color: var(--color-line-num);
  text-align: right;
  padding-right: 16px;
  user-select: none;
  font-size: 0.75rem;
}

.dp-term-line .dp-term-code {
  color: oklch(66% 0.015 270);
  white-space: pre;
  overflow-x: auto;
}

/* Hit line: highlighted */
.dp-term-line--hit {
  background: var(--color-red-soft);
}

.dp-term-line--hit .dp-term-ln {
  color: var(--color-red);
}

.dp-term-line--hit .dp-term-code {
  color: var(--color-text);
  font-weight: 500;
}

/* Empty evidence */
.dp-term-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px 16px;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--color-line-num);
}
</style>

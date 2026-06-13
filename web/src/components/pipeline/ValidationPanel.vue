<script setup lang="ts">
/**
 * ValidationPanel — Aggregated pipeline config validation result (Story 2-6, FR-9).
 *
 * Displays the server-authoritative validation state:
 *   - A prominent ready/not-ready header badge.
 *   - Issue list grouped by severity (error → warning → info).
 *   - Each issue row is clickable; emits 'locate' with the issue scope so the
 *     parent can switch to the relevant tab.
 *
 * Props:
 *   loading  – show skeleton while fetching
 *   ready    – whether all issues are non-error
 *   issues   – array from ValidationDTO.issues
 *
 * Emits:
 *   locate(scope: IssueScope) – parent should switch to that tab
 *   close                     – parent should hide/collapse the panel
 *
 * Constraints:
 *   - No client-side validation logic; purely presentational.
 *   - Colors use design tokens only (no hardcoded palette values).
 *   - Motion: transform + opacity only; @media prefers-reduced-motion kills animations.
 *   - Reuses tokens from tokens.css; no new UI libraries.
 */

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ValidationIssue, IssueScope } from '../../api/pipelineValidation'

const props = defineProps<{
  loading: boolean
  ready: boolean
  issues: ValidationIssue[]
}>()

const emit = defineEmits<{
  locate: [scope: IssueScope]
  close: []
}>()

const { t } = useI18n()

function severityLabel(severity: 'error' | 'warning' | 'info'): string {
  return severity === 'error'
    ? t('pipelinePanels.vpGroupError')
    : severity === 'warning'
      ? t('pipelinePanels.vpGroupWarning')
      : t('pipelinePanels.vpGroupInfo')
}

// ─── Derived counts ───────────────────────────────────────────────────────────

const errorCount   = computed(() => props.issues.filter((i) => i.severity === 'error').length)
const warningCount = computed(() => props.issues.filter((i) => i.severity === 'warning').length)
const infoCount    = computed(() => props.issues.filter((i) => i.severity === 'info').length)

// ─── Grouped issues (error → warning → info) ─────────────────────────────────

type SeverityGroup = { severity: 'error' | 'warning' | 'info'; items: ValidationIssue[] }

const groups = computed<SeverityGroup[]>(() => {
  const order: Array<'error' | 'warning' | 'info'> = ['error', 'warning', 'info']
  return order
    .map((sev) => ({
      severity: sev,
      items: props.issues.filter((i) => i.severity === sev),
    }))
    .filter((g) => g.items.length > 0)
})

// ─── Scope → tab label map ────────────────────────────────────────────────────

const scopeLabel = computed<Record<IssueScope, string>>(() => ({
  canvas:   t('pipelinePanels.vpScopeCanvas'),
  vars:     t('pipelinePanels.vpScopeVars'),
  triggers: t('pipelinePanels.vpScopeTriggers'),
  envs:     t('pipelinePanels.vpScopeEnvs'),
}))

// ─── Helpers ──────────────────────────────────────────────────────────────────

function handleLocate(scope: IssueScope): void {
  emit('locate', scope)
}
</script>

<template>
  <aside class="vp" :aria-label="t('pipelinePanels.vpAside')">

    <!-- ─── Header ─────────────────────────────────────────────────────────── -->
    <div class="vp-head">
      <div class="vp-head-left">
        <!-- Ready badge -->
        <span
          v-if="!loading"
          class="vp-ready-badge"
          :class="ready ? 'vp-ready-badge--ok' : 'vp-ready-badge--err'"
          aria-live="polite"
        >
          <!-- ok dot -->
          <span class="vp-ready-dot" aria-hidden="true" />
          <span v-if="ready">{{ t('pipelinePanels.vpReady') }}</span>
          <span v-else>{{ t('pipelinePanels.vpNeedFix', { count: errorCount }) }}</span>
        </span>

        <!-- Loading skeleton for badge -->
        <span v-else class="vp-skel vp-skel--badge" aria-hidden="true" />

        <span class="vp-head-title">{{ t('pipelinePanels.vpTitle') }}</span>
      </div>

      <!-- Summary chips (only when not loading and has issues) -->
      <div v-if="!loading && issues.length > 0" class="vp-chips" aria-hidden="true">
        <span v-if="errorCount > 0"   class="vp-chip vp-chip--error">{{ t('pipelinePanels.vpErrorChip', { count: errorCount }) }}</span>
        <span v-if="warningCount > 0" class="vp-chip vp-chip--warn">{{ t('pipelinePanels.vpWarnChip', { count: warningCount }) }}</span>
        <span v-if="infoCount > 0"    class="vp-chip vp-chip--info">{{ t('pipelinePanels.vpInfoChip', { count: infoCount }) }}</span>
      </div>

      <!-- Close button -->
      <button class="vp-close" :aria-label="t('pipelinePanels.vpCloseAria')" @click="emit('close')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>

    <!-- ─── Loading skeleton ───────────────────────────────────────────────── -->
    <div v-if="loading" class="vp-body" aria-busy="true" :aria-label="t('pipelinePanels.vpLoadingAria')">
      <div class="vp-skel-list">
        <div v-for="i in 3" :key="i" class="vp-skel-row" aria-hidden="true">
          <span class="vp-skel vp-skel--dot" />
          <span class="vp-skel vp-skel--line" :style="{ width: `${55 + i * 12}%` }" />
        </div>
      </div>
    </div>

    <!-- ─── No issues (ready) ─────────────────────────────────────────────── -->
    <div v-else-if="issues.length === 0" class="vp-empty" role="status">
      <span class="vp-empty-icon" aria-hidden="true">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M20 6 9 17l-5-5"/>
        </svg>
      </span>
      <span class="vp-empty-text">{{ t('pipelinePanels.vpAllPassed') }}</span>
    </div>

    <!-- ─── Issue groups ───────────────────────────────────────────────────── -->
    <div v-else class="vp-body">
      <section
        v-for="group in groups"
        :key="group.severity"
        class="vp-group"
        :aria-label="t('pipelinePanels.vpGroupAria', { label: severityLabel(group.severity) })"
      >
        <!-- Group heading -->
        <div class="vp-group-head">
          <span class="vp-group-sev" :class="`vp-group-sev--${group.severity}`" aria-hidden="true">
            <!-- error icon -->
            <svg v-if="group.severity === 'error'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
            </svg>
            <!-- warning icon -->
            <svg v-else-if="group.severity === 'warning'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 16h.01"/>
            </svg>
            <!-- info icon -->
            <svg v-else width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="9"/><path d="M12 8h.01M11 12h1v4h1"/>
            </svg>
          </span>
          <span class="vp-group-label" :class="`vp-group-label--${group.severity}`">
            {{ severityLabel(group.severity) }}
            <span class="vp-group-count">{{ group.items.length }}</span>
          </span>
        </div>

        <!-- Issue list -->
        <ul class="vp-issue-list" role="list">
          <li
            v-for="issue in group.items"
            :key="`${issue.code}-${issue.field}`"
            class="vp-issue"
            :class="`vp-issue--${issue.severity}`"
            role="button"
            tabindex="0"
            :aria-label="t('pipelinePanels.vpLocateAria', { message: issue.message, scope: scopeLabel[issue.scope] })"
            @click="handleLocate(issue.scope)"
            @keydown.enter.prevent="handleLocate(issue.scope)"
            @keydown.space.prevent="handleLocate(issue.scope)"
          >
            <!-- Scope badge -->
            <span class="vp-scope-tag" :class="`vp-scope-tag--${issue.scope}`">
              {{ scopeLabel[issue.scope] }}
            </span>

            <!-- Message -->
            <span class="vp-issue-msg">{{ issue.message }}</span>

            <!-- Field path (only when non-empty) -->
            <code v-if="issue.field" class="vp-issue-field">{{ issue.field }}</code>

            <!-- Locate arrow -->
            <span class="vp-issue-arrow" aria-hidden="true">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M5 12h14M13 6l6 6-6 6"/>
              </svg>
            </span>
          </li>
        </ul>
      </section>
    </div>

  </aside>
</template>

<style scoped>
/* ─── Panel host ─────────────────────────────────────────────────────────── */
.vp {
  width: 360px;
  flex: none;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--color-border);
  background: var(--color-card);
  overflow: hidden;
  animation: vp-slide-in 0.22s var(--ease-out-expo, cubic-bezier(0.16,1,0.3,1)) both;
}

@keyframes vp-slide-in {
  from { opacity: 0; transform: translateX(16px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .vp { animation: none; }
}

/* ─── Header ─────────────────────────────────────────────────────────────── */
.vp-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px 12px 16px;
  border-bottom: 1px solid var(--color-border);
  flex: none;
}

.vp-head-left {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  overflow: hidden;
}

.vp-head-title {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-faint);
  white-space: nowrap;
  letter-spacing: 0.02em;
  text-transform: uppercase;
}

/* ─── Ready badge ────────────────────────────────────────────────────────── */
.vp-ready-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.72rem;
  font-weight: 600;
  padding: 3px 8px;
  border-radius: 100px;
  white-space: nowrap;
  border: 1px solid transparent;
  transition: opacity var(--duration-fast, 150ms);
}

.vp-ready-badge--ok {
  color: var(--color-green);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}
.vp-ready-badge--ok .vp-ready-dot {
  background: var(--color-green);
}

.vp-ready-badge--err {
  color: var(--color-red);
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}
.vp-ready-badge--err .vp-ready-dot {
  background: var(--color-red);
}

.vp-ready-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  flex-shrink: 0;
}

/* ─── Summary chips ──────────────────────────────────────────────────────── */
.vp-chips {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.vp-chip {
  font-size: 0.68rem;
  font-weight: 600;
  padding: 2px 7px;
  border-radius: 100px;
  border: 1px solid transparent;
  white-space: nowrap;
}

.vp-chip--error {
  color: var(--color-red);
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}

.vp-chip--warn {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}

.vp-chip--info {
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
}

/* ─── Close button ───────────────────────────────────────────────────────── */
.vp-close {
  flex-shrink: 0;
  width: 26px;
  height: 26px;
  border: none;
  background: none;
  cursor: pointer;
  color: var(--color-faint);
  border-radius: 6px;
  display: grid;
  place-items: center;
  transition: color var(--duration-fast, 150ms), background-color var(--duration-fast, 150ms);
}

.vp-close:hover {
  color: var(--color-text);
  background: var(--color-inset);
}

.vp-close:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ─── Body scroll area ───────────────────────────────────────────────────── */
.vp-body {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
}

/* ─── Empty / ready state ────────────────────────────────────────────────── */
.vp-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 40px 20px;
  text-align: center;
  flex: 1;
}

.vp-empty-icon {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  color: var(--color-green);
  display: grid;
  place-items: center;
}

.vp-empty-text {
  font-size: 0.82rem;
  color: var(--color-faint);
}

/* ─── Issue groups ───────────────────────────────────────────────────────── */
.vp-group {
  padding: 10px 0 4px;
  border-bottom: 1px solid var(--color-border);
}

.vp-group:last-child {
  border-bottom: none;
}

.vp-group-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 16px 6px;
}

.vp-group-sev {
  width: 18px;
  height: 18px;
  border-radius: 5px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.vp-group-sev--error   { background: var(--color-red-soft);   color: var(--color-red);   }
.vp-group-sev--warning { background: var(--color-amber-soft); color: var(--color-amber); }
.vp-group-sev--info    { background: var(--color-cyan-soft);  color: var(--color-cyan);  }

.vp-group-label {
  font-size: 0.74rem;
  font-weight: 700;
  display: flex;
  align-items: center;
  gap: 5px;
  letter-spacing: 0.01em;
}

.vp-group-label--error   { color: var(--color-red);   }
.vp-group-label--warning { color: var(--color-amber); }
.vp-group-label--info    { color: var(--color-cyan);  }

.vp-group-count {
  font-weight: 500;
  opacity: 0.7;
}

/* ─── Issue list ─────────────────────────────────────────────────────────── */
.vp-issue-list {
  list-style: none;
  margin: 0;
  padding: 0 8px 6px;
}

/* ─── Single issue row ───────────────────────────────────────────────────── */
.vp-issue {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: 7px;
  padding: 7px 8px;
  border-radius: 8px;
  cursor: pointer;
  border: 1px solid transparent;
  transition:
    background-color var(--duration-fast, 150ms),
    border-color var(--duration-fast, 150ms),
    transform var(--duration-fast, 150ms);
  position: relative;
}

.vp-issue:hover {
  transform: translateX(2px);
}

.vp-issue:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.vp-issue--error:hover {
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}

.vp-issue--warning:hover {
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}

.vp-issue--info:hover {
  background: var(--color-cyan-soft);
  border-color: var(--color-cyan-line);
}

@media (prefers-reduced-motion: reduce) {
  .vp-issue:hover { transform: none; }
}

/* ─── Scope tag ──────────────────────────────────────────────────────────── */
.vp-scope-tag {
  flex-shrink: 0;
  font-size: 0.64rem;
  font-weight: 600;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid transparent;
  white-space: nowrap;
  margin-top: 1px;
}

.vp-scope-tag--canvas   { background: var(--color-primary-soft); color: var(--color-primary); border-color: oklch(66% 0.155 258 / 0.25); }
.vp-scope-tag--vars     { background: var(--color-cyan-soft);    color: var(--color-cyan);    border-color: var(--color-cyan-line); }
.vp-scope-tag--triggers { background: var(--color-amber-soft);   color: var(--color-amber);   border-color: var(--color-amber-line); }
.vp-scope-tag--envs     { background: var(--color-green-soft);   color: var(--color-green);   border-color: var(--color-green-line); }

/* ─── Message ────────────────────────────────────────────────────────────── */
.vp-issue-msg {
  flex: 1 1 auto;
  min-width: 7rem;
  font-size: 0.8rem;
  line-height: 1.5;
  color: var(--color-text);
  overflow-wrap: break-word;
}

/* ─── Field path ─────────────────────────────────────────────────────────── */
.vp-issue-field {
  display: block;
  flex-basis: 100%;
  font-family: var(--font-mono, monospace);
  font-size: 0.7rem;
  color: var(--color-faint);
  margin-top: 2px;
  word-break: break-all;
}

/* ─── Locate arrow ───────────────────────────────────────────────────────── */
.vp-issue-arrow {
  flex-shrink: 0;
  color: var(--color-faint);
  opacity: 0;
  margin-top: 2px;
  transition: opacity var(--duration-fast, 150ms), transform var(--duration-fast, 150ms);
}

.vp-issue:hover .vp-issue-arrow {
  opacity: 1;
  transform: translateX(2px);
}

@media (prefers-reduced-motion: reduce) {
  .vp-issue:hover .vp-issue-arrow { transform: none; }
}

/* ─── Loading skeleton ───────────────────────────────────────────────────── */
.vp-skel {
  display: block;
  border-radius: 6px;
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.05) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  animation: vp-shimmer 1.4s ease-in-out infinite;
}

@keyframes vp-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .vp-skel { animation: none; background: var(--color-inset); }
}

.vp-skel--badge { height: 22px; width: 88px; border-radius: 100px; }
.vp-skel--dot   { width: 14px; height: 14px; border-radius: 4px; flex-shrink: 0; }
.vp-skel--line  { height: 14px; }

.vp-skel-list {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.vp-skel-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>

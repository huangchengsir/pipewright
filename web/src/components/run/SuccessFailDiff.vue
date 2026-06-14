<script setup lang="ts">
/**
 * SuccessFailDiff — Story 7-3: 成功/失败差异对比 (FR-25).
 *
 * For a failed run, fetches GET /api/runs/{id}/diff and renders the file-level
 * diff between the last successful run (baseline) and this run (current):
 *   - baseline-commit → current-commit header
 *   - file list with status coloring (added=green / modified=amber /
 *     deleted=red / renamed=cyan) + per-file ±bar
 *   - human summary (may carry a "most suspect" heuristic hint)
 *   - truncated notice when the backend caps the file count
 *   - available=false → friendly fallback (no baseline / no commit / clone
 *     failed) — never an error toast; diff is a diagnostic aid, not a blocker.
 *
 * Design anchors (aligns with DiagnosisPanel — same surface family):
 *   - Card surface + JetBrains Mono for paths/commits
 *   - Status semantic colors from tokens.css (green/amber/red/cyan)
 *   - Additions/deletions bar = compositor-friendly width transition only
 *   - Diff data is path + line-counts only (no file content) — no secrets reach
 *     this component by construction.
 *
 * Constraints:
 *   - No new UI libraries; reuses design tokens
 *   - Container-fetches on mount; degrades silently on network error
 */

import { onMounted, ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { getRunDiff, type RunDiffDTO, type RunDiffFile } from '../../api/runs'

const { t } = useI18n()

const props = defineProps<{
  runId: string
}>()

const loading = ref(true)
const diff = ref<RunDiffDTO | null>(null)
// Network-level failure (endpoint unreachable). The backend never 500s for the
// diff itself — it returns available:false — so this only catches transport.
const loadError = ref(false)

onMounted(async () => {
  try {
    diff.value = await getRunDiff(props.runId)
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
})

const shortCommit = (sha: string): string => (sha ? sha.slice(0, 8) : '—')

// Aggregate totals for the header strip.
const totals = computed(() => {
  const files = diff.value?.files ?? []
  let add = 0
  let del = 0
  for (const f of files) {
    add += f.additions
    del += f.deletions
  }
  return { add, del, count: files.length }
})

// Max churn across files → normalizes the per-row ±bar widths so the busiest
// file fills the bar and the rest scale relative to it.
const maxChurn = computed(() => {
  let m = 0
  for (const f of diff.value?.files ?? []) {
    m = Math.max(m, f.additions + f.deletions)
  }
  return m
})

function barPct(f: RunDiffFile, kind: 'add' | 'del'): string {
  if (maxChurn.value === 0) return '0%'
  const v = kind === 'add' ? f.additions : f.deletions
  // Scale to the busiest file; floor a hair so a non-zero count is always visible.
  const pct = (v / maxChurn.value) * 100
  return `${v > 0 ? Math.max(pct, 4) : 0}%`
}

function statusClass(status: RunDiffFile['status']): string {
  return `sfd-file--${status}`
}

const statusGlyph: Record<RunDiffFile['status'], string> = {
  added: 'A',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
}

const STATUS_LABEL_KEY: Record<RunDiffFile['status'], string> = {
  added: 'run.fileStatusAdded',
  modified: 'run.fileStatusModified',
  deleted: 'run.fileStatusDeleted',
  renamed: 'run.fileStatusRenamed',
}

function statusLabel(status: RunDiffFile['status']): string {
  return t(STATUS_LABEL_KEY[status])
}

// Expand/collapse each file's redacted +/- patch body (tracked by path).
const expanded = ref<Set<string>>(new Set())
function toggle(path: string): void {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

interface PatchLine {
  sign: '+' | '-'
  text: string
}
// Split the redacted patch body into +/- lines for per-line coloring.
function patchLines(patch: string): PatchLine[] {
  const out: PatchLine[] = []
  for (const raw of patch.split('\n')) {
    if (!raw) continue
    const sign: '+' | '-' = raw[0] === '-' ? '-' : '+'
    out.push({ sign, text: raw.slice(1) })
  }
  return out
}
</script>

<template>
  <section class="sfd" :aria-label="t('run.diffRegion')">
    <!-- ─── Header ──────────────────────────────────────────────────── -->
    <div class="sfd-head">
      <div class="sfd-head-left">
        <span class="sfd-icon" aria-hidden="true">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="6" y1="3" x2="6" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />
          </svg>
        </span>
        <span class="sfd-title">{{ t('run.codeDiff') }}</span>
        <span class="sfd-subtitle">{{ t('run.diffSubtitle') }}</span>
      </div>
      <span
        v-if="diff?.available && totals.count > 0"
        class="sfd-totals mono"
        :aria-label="t('run.diffTotalsAria', { count: totals.count, add: totals.add, del: totals.del })"
      >
        <span class="sfd-totals-add">+{{ totals.add }}</span>
        <span class="sfd-totals-del">−{{ totals.del }}</span>
      </span>
    </div>

    <!-- ─── STATE: loading ──────────────────────────────────────────── -->
    <div v-if="loading" class="sfd-body sfd-body--loading" aria-busy="true">
      <span class="sfd-spinner" aria-hidden="true" />
      <span class="sfd-loading-text">{{ t('run.computingDiff') }}</span>
    </div>

    <!-- ─── STATE: transport error ──────────────────────────────────── -->
    <div v-else-if="loadError" class="sfd-body sfd-body--muted">
      <p class="sfd-muted-text">{{ t('run.diffLoadFailed') }}</p>
    </div>

    <!-- ─── STATE: available=false (graceful) ───────────────────────── -->
    <div v-else-if="!diff?.available" class="sfd-body sfd-body--muted">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true" class="sfd-muted-icon">
        <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
      </svg>
      <p class="sfd-muted-text">{{ diff?.reason || t('run.noBaseline') }}</p>
      <p class="sfd-muted-hint">{{ t('run.diffEmptyHint') }}</p>
    </div>

    <!-- ─── STATE: available — diff list ────────────────────────────── -->
    <div v-else class="sfd-body">
      <!-- commit range -->
      <div class="sfd-range" role="group" :aria-label="t('run.commitRangeAria')">
        <span class="sfd-commit sfd-commit--base mono" :title="diff.baselineCommit">
          <span class="sfd-commit-tag">{{ t('run.baselineTag') }}</span>{{ shortCommit(diff.baselineCommit) }}
        </span>
        <svg class="sfd-arrow" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" />
        </svg>
        <span class="sfd-commit sfd-commit--cur mono" :title="diff.currentCommit">
          <span class="sfd-commit-tag sfd-commit-tag--cur">{{ t('run.currentTag') }}</span>{{ shortCommit(diff.currentCommit) }}
        </span>
      </div>

      <!-- summary -->
      <p v-if="diff.summary" class="sfd-summary">{{ diff.summary }}</p>

      <!-- empty (commits differ but no file delta) -->
      <p v-if="diff.files.length === 0" class="sfd-muted-text sfd-empty">
        {{ t('run.noFileDelta') }}
      </p>

      <!-- file list -->
      <ul v-else class="sfd-files" role="list">
        <li
          v-for="f in diff.files"
          :key="f.path"
          class="sfd-file-item"
          :class="statusClass(f.status)"
        >
          <button
            type="button"
            class="sfd-file"
            :class="{ 'sfd-file--expandable': !!f.patch, 'is-open': expanded.has(f.path) }"
            :disabled="!f.patch"
            :aria-expanded="f.patch ? expanded.has(f.path) : undefined"
            @click="f.patch && toggle(f.path)"
          >
            <span class="sfd-file-caret mono" aria-hidden="true">{{ f.patch ? (expanded.has(f.path) ? '▾' : '▸') : '·' }}</span>
            <span class="sfd-file-status" :aria-label="statusLabel(f.status)">{{ statusGlyph[f.status] }}</span>
            <span class="sfd-file-path mono" :title="f.path">{{ f.path }}</span>
            <span class="sfd-file-counts mono" aria-hidden="true">
              <span v-if="f.additions" class="sfd-c-add">+{{ f.additions }}</span>
              <span v-if="f.deletions" class="sfd-c-del">−{{ f.deletions }}</span>
            </span>
            <span class="sfd-file-bar" aria-hidden="true">
              <span class="sfd-bar-add" :style="{ width: barPct(f, 'add') }" />
              <span class="sfd-bar-del" :style="{ width: barPct(f, 'del') }" />
            </span>
          </button>
          <div v-if="f.patch && expanded.has(f.path)" class="sfd-patch">
            <pre class="sfd-patch-pre" :aria-label="t('run.patchAria', { path: f.path })"><span
              v-for="(ln, i) in patchLines(f.patch)"
              :key="i"
              class="sfd-pl"
              :class="ln.sign === '+' ? 'sfd-pl--add' : 'sfd-pl--del'"
            >{{ ln.sign }}{{ ln.text }}</span></pre>
            <p v-if="f.patchTruncated" class="sfd-patch-trunc" role="note">{{ t('run.patchTruncated') }}</p>
          </div>
        </li>
      </ul>

      <!-- truncated notice -->
      <p v-if="diff.truncated" class="sfd-trunc" role="note">
        {{ t('run.diffTruncated', { n: diff.files.length }) }}
      </p>
    </div>
  </section>
</template>

<style scoped>
.sfd {
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-card);
  overflow: hidden;
}

/* ─── Header ─────────────────────────────────────────────────────────── */
.sfd-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-card-2);
}
.sfd-head-left {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}
.sfd-icon {
  display: inline-flex;
  color: var(--color-cyan);
}
.sfd-title {
  font-weight: 600;
  font-size: 0.875rem;
  color: var(--color-text);
}
.sfd-subtitle {
  font-size: 0.75rem;
  color: var(--color-faint);
}
.sfd-totals {
  display: inline-flex;
  gap: var(--space-2);
  font-size: 0.8rem;
  flex-shrink: 0;
}
.sfd-totals-add { color: var(--color-green); }
.sfd-totals-del { color: var(--color-red); }

/* ─── Body states ────────────────────────────────────────────────────── */
.sfd-body { padding: var(--space-4); }

.sfd-body--loading {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  color: var(--color-dim);
  font-size: 0.8125rem;
}
.sfd-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid var(--color-border-strong);
  border-top-color: var(--color-cyan);
  border-radius: 50%;
  animation: sfd-spin 0.7s linear infinite;
}
@keyframes sfd-spin { to { transform: rotate(360deg); } }

.sfd-body--muted {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
}
.sfd-muted-icon { color: var(--color-faint); }
.sfd-muted-text {
  font-size: 0.875rem;
  color: var(--color-dim);
}
.sfd-muted-hint {
  font-size: 0.75rem;
  color: var(--color-faint);
}

/* ─── Commit range ───────────────────────────────────────────────────── */
.sfd-range {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
  flex-wrap: wrap;
}
.sfd-commit {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.8125rem;
  color: var(--color-text);
  padding: 3px 8px;
  border-radius: 7px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
}
.sfd-commit-tag {
  font-size: 0.625rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--color-green-soft);
  color: var(--color-green);
}
.sfd-commit-tag--cur {
  background: var(--color-red-soft);
  color: var(--color-red);
}
.sfd-arrow { color: var(--color-faint); flex-shrink: 0; }

/* ─── Summary ────────────────────────────────────────────────────────── */
.sfd-summary {
  font-size: 0.8125rem;
  color: var(--color-dim);
  margin-bottom: var(--space-3);
  line-height: 1.5;
}
.sfd-empty { padding: var(--space-2) 0; }

/* ─── File list ──────────────────────────────────────────────────────── */
.sfd-files {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border);
  border-radius: 9px;
  overflow: hidden;
}
.sfd-file-item {
  display: flex;
  flex-direction: column;
  border-bottom: 1px solid var(--color-border);
}
.sfd-file-item:last-child { border-bottom: none; }
.sfd-file {
  display: grid;
  grid-template-columns: 16px 22px minmax(0, 1fr) auto 72px;
  align-items: center;
  gap: var(--space-3);
  padding: 7px var(--space-3);
  border: none;
  background: var(--color-inset);
  width: 100%;
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: default;
  transition: background var(--duration-fast, 150ms) ease;
}
.sfd-file--expandable { cursor: pointer; }
.sfd-file--expandable:hover { background: var(--color-card-2); }
.sfd-file.is-open { background: var(--color-card-2); }
.sfd-file-caret {
  color: var(--color-text-soft, var(--color-text-muted, #888));
  font-size: 0.75rem;
  text-align: center;
}

/* ─── Expanded patch body (redacted +/- lines) ─────────────────────── */
.sfd-patch {
  border-top: 1px dashed var(--color-border);
  background: var(--color-inset);
}
.sfd-patch-pre {
  margin: 0;
  padding: var(--space-2) 0;
  max-height: 360px;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  line-height: 1.5;
  tab-size: 2;
}
.sfd-pl {
  display: block;
  padding: 0 var(--space-3);
  white-space: pre;
}
.sfd-pl--add { background: var(--color-green-soft); color: var(--color-green); }
.sfd-pl--del { background: var(--color-red-soft); color: var(--color-red); }
.sfd-patch-trunc {
  margin: 0;
  padding: 6px var(--space-3);
  font-size: 0.75rem;
  color: var(--color-text-muted, #888);
  border-top: 1px dashed var(--color-border);
}

.sfd-file-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 5px;
  font-size: 0.6875rem;
  font-weight: 700;
  font-family: var(--font-mono);
}
.sfd-file--added   .sfd-file-status { background: var(--color-green-soft); color: var(--color-green); }
.sfd-file--modified .sfd-file-status { background: var(--color-amber-soft); color: var(--color-amber); }
.sfd-file--deleted .sfd-file-status { background: var(--color-red-soft);   color: var(--color-red); }
.sfd-file--renamed .sfd-file-status { background: var(--color-cyan-soft);  color: var(--color-cyan); }

.sfd-file-path {
  font-size: 0.8125rem;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sfd-file--deleted .sfd-file-path {
  color: var(--color-dim);
  text-decoration: line-through;
  text-decoration-color: var(--color-red-line);
}

.sfd-file-counts {
  display: inline-flex;
  gap: var(--space-2);
  font-size: 0.75rem;
  justify-self: end;
}
.sfd-c-add { color: var(--color-green); }
.sfd-c-del { color: var(--color-red); }

.sfd-file-bar {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 6px;
}
.sfd-bar-add,
.sfd-bar-del {
  height: 100%;
  border-radius: 2px;
  transition: width var(--duration-normal, 300ms) var(--ease-out-expo, cubic-bezier(0.16, 1, 0.3, 1));
}
.sfd-bar-add { background: var(--color-green); }
.sfd-bar-del { background: var(--color-red); }

/* ─── Truncated notice ───────────────────────────────────────────────── */
.sfd-trunc {
  margin-top: var(--space-3);
  font-size: 0.75rem;
  color: var(--color-amber);
}

@media (prefers-reduced-motion: reduce) {
  .sfd-spinner { animation: none; }
  .sfd-bar-add,
  .sfd-bar-del { transition: none; }
}
</style>

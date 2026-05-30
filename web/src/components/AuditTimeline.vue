<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listAudit } from '../api/audit'
import type { AuditEntry } from '../api/audit'
import { HttpError } from '../api/http'

// ─── state ──────────────────────────────────────────────────────────────────

type LoadState = 'idle' | 'loading' | 'error'

const PAGE_SIZE = 8

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const entries = ref<AuditEntry[]>([])
const nextBefore = ref<string | null>(null)
const loadingMore = ref(false)

// ─── action → presentation mapping ───────────────────────────────────────────

type DotKind = 'add' | 'del' | 'use' | 'cfg'

interface ActionMeta {
  dot: DotKind
  verb: string
  noun: string
}

// Maps each audit action to a verb/noun + a dot category that drives semantic
// coloring (green=create, red=delete, primary=run, cyan=config change).
const ACTION_META: Record<string, ActionMeta> = {
  credential_create: { dot: 'add', verb: '新增', noun: '凭据' },
  credential_update: { dot: 'cfg', verb: '修改', noun: '凭据' },
  credential_delete: { dot: 'del', verb: '删除', noun: '凭据' },
  trigger_secret_reset: { dot: 'cfg', verb: '重置', noun: 'webhook 签名密钥' },
  project_create: { dot: 'add', verb: '接入', noun: '项目' },
  project_update: { dot: 'cfg', verb: '修改', noun: '项目' },
  project_delete: { dot: 'del', verb: '删除', noun: '项目' },
  run_trigger_manual: { dot: 'use', verb: '手动触发', noun: '运行' },
}

function meta(action: string): ActionMeta {
  return ACTION_META[action] ?? { dot: 'cfg', verb: '操作', noun: action }
}

// Best-effort human label for the affected object: prefer detail.name, then
// branch (for runs), else the target id. detail is already masked server-side.
function objectLabel(e: AuditEntry): string {
  const d = e.detail ?? {}
  const name = d['name']
  if (typeof name === 'string' && name) return name
  const branch = d['branch']
  if (typeof branch === 'string' && branch) return branch
  return e.targetId || '—'
}

function actorLabel(actor: string): string {
  return actor === 'admin' ? '你' : actor
}

// ─── relative time ────────────────────────────────────────────────────────────

function relativeTime(isoStr: string): string {
  const diff = Date.now() - new Date(isoStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 5) return '刚刚'
  if (s < 60) return `${s} 秒前`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  const day = Math.floor(h / 24)
  if (day < 30) return `${day} 天前`
  return new Date(isoStr).toLocaleDateString()
}

// ─── data loading ─────────────────────────────────────────────────────────────

function describeError(err: unknown): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return '无法连接到服务器,请检查后端是否运行后重试'
    return err.apiError?.message ?? `加载审计日志失败(${err.status})`
  }
  return '加载审计日志失败,请稍后重试'
}

async function loadFirst(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    const res = await listAudit({ limit: PAGE_SIZE })
    entries.value = res.entries
    nextBefore.value = res.nextBefore
    loadState.value = 'idle'
  } catch (err) {
    loadError.value = describeError(err)
    loadState.value = 'error'
  }
}

async function loadMore(): Promise<void> {
  if (!nextBefore.value || loadingMore.value) return
  loadingMore.value = true
  try {
    const res = await listAudit({ limit: PAGE_SIZE, before: nextBefore.value })
    entries.value = [...entries.value, ...res.entries]
    nextBefore.value = res.nextBefore
  } catch (err) {
    loadError.value = describeError(err)
  } finally {
    loadingMore.value = false
  }
}

onMounted(loadFirst)
</script>

<template>
  <div class="panel audit-panel">
    <div class="panel-head">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
        <path d="M12 8v4l3 2" /><circle cx="12" cy="12" r="9" />
      </svg>
      审计日志
      <span class="panel-sub">谁 · 何时 · 对什么</span>
    </div>

    <!-- Error -->
    <div v-if="loadState === 'error' && entries.length === 0" class="audit-error" role="alert">
      <span>{{ loadError }}</span>
      <button class="audit-retry" @click="loadFirst">↻ 重试</button>
    </div>

    <!-- Loading skeleton -->
    <template v-else-if="loadState === 'loading'">
      <div class="aev aev--skel" v-for="i in 3" :key="i" aria-hidden="true">
        <span class="skel adot-skel" />
        <span class="skel line-skel" />
        <span class="skel time-skel" />
      </div>
    </template>

    <!-- Empty -->
    <template v-else-if="loadState === 'idle' && entries.length === 0">
      <div class="audit-empty">
        <p class="audit-empty-label">还没有审计记录</p>
        <p class="audit-empty-hint">
          创建、修改、删除凭据或项目,重置 webhook 密钥,手动触发运行等敏感操作都会在此留痕,
          记录不可篡改。
        </p>
      </div>
    </template>

    <!-- Timeline -->
    <template v-else>
      <ol class="audit" aria-label="审计时间线">
        <li v-for="e in entries" :key="e.id" class="aev">
          <span class="adot" :class="`adot--${meta(e.action).dot}`" aria-hidden="true">
            <!-- add -->
            <svg v-if="meta(e.action).dot === 'add'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M12 5v14M5 12h14" /></svg>
            <!-- del -->
            <svg v-else-if="meta(e.action).dot === 'del'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M5 12h14" /></svg>
            <!-- use (run) -->
            <svg v-else-if="meta(e.action).dot === 'use'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><path d="M5 3v18M5 7h10l-2 3 2 3H5" /></svg>
            <!-- cfg -->
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9"><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3" /></svg>
          </span>

          <div class="atx">
            <span class="atx-line">
              <b>{{ actorLabel(e.actor) }}</b>
              {{ meta(e.action).verb }}{{ meta(e.action).noun }}
              <span class="obj">{{ objectLabel(e) }}</span>
            </span>
            <span class="who">
              {{ actorLabel(e.actor) }} · Web 控制台<template v-if="e.ip"> · <span class="ip">{{ e.ip }}</span></template>
            </span>
          </div>

          <time class="atm" :datetime="e.timestamp" :title="e.timestamp">{{ relativeTime(e.timestamp) }}</time>
        </li>
      </ol>

      <button
        v-if="nextBefore"
        class="viewall"
        :disabled="loadingMore"
        @click="loadMore"
      >
        <span v-if="loadingMore" class="spinner" aria-hidden="true" />
        {{ loadingMore ? '加载中…' : '加载更多审计记录 →' }}
      </button>
    </template>
  </div>
</template>

<style scoped>
.audit-panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: panel-in 0.45s var(--ease-out-expo) both;
}

@keyframes panel-in {
  from { opacity: 0; transform: translateY(13px); }
  to   { opacity: 1; transform: none; }
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 13px 18px;
  border-bottom: 1px solid var(--color-border);
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}

.panel-sub {
  margin-left: auto;
  font-size: 0.74rem;
  font-weight: 400;
  color: var(--color-faint);
}

/* ─── timeline ──────────────────────────────────────────────────────────────── */
.audit {
  list-style: none;
  margin: 0;
  padding: 6px 0;
}

.aev {
  display: grid;
  grid-template-columns: 34px 1fr auto;
  gap: 13px;
  padding: 11px 18px;
  align-items: start;
  position: relative;
}

/* Connecting spine between dots. */
.aev::before {
  content: '';
  position: absolute;
  left: 33px;
  top: 30px;
  bottom: -11px;
  width: 1.5px;
  background: var(--color-border);
}

.aev:last-child::before {
  display: none;
}

.adot {
  width: 34px;
  height: 34px;
  border-radius: var(--rounded-lg);
  display: grid;
  place-items: center;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  z-index: 1;
  color: var(--color-dim);
}

.adot svg {
  width: 15px;
  height: 15px;
}

.adot--add {
  background: var(--color-green-soft);
  border-color: transparent;
  color: var(--color-green);
}

.adot--del {
  background: var(--color-red-soft);
  border-color: transparent;
  color: var(--color-red);
}

.adot--use {
  background: var(--color-primary-soft);
  border-color: transparent;
  color: var(--color-primary);
}

.adot--cfg {
  background: var(--color-cyan-soft);
  border-color: transparent;
  color: var(--color-cyan);
}

.atx {
  font-size: 0.83rem;
  line-height: 1.5;
  min-width: 0;
}

.atx-line b {
  font-weight: 600;
  color: var(--color-text);
}

.obj {
  font-family: var(--font-mono);
  color: var(--color-dim);
  font-size: 0.92em;
  word-break: break-all;
}

.who {
  display: block;
  color: var(--color-faint);
  font-size: 0.75rem;
  margin-top: 2px;
}

.who .ip {
  font-family: var(--font-mono);
}

.atm {
  font-size: 0.74rem;
  color: var(--color-faint);
  white-space: nowrap;
  font-family: var(--font-mono);
}

/* ─── load more ─────────────────────────────────────────────────────────────── */
.viewall {
  width: 100%;
  padding: 13px 18px;
  text-align: center;
  font-size: 0.79rem;
  color: var(--color-primary);
  font-weight: 500;
  cursor: pointer;
  border: none;
  border-top: 1px solid var(--color-border);
  background: transparent;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  transition: background-color var(--duration-fast);
}

.viewall:hover:not(:disabled) {
  background: var(--color-inset);
}

.viewall:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.viewall:disabled {
  opacity: 0.6;
  cursor: progress;
}

/* ─── empty / error ─────────────────────────────────────────────────────────── */
.audit-empty {
  padding: 36px 32px;
  text-align: center;
}

.audit-empty-label {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-dim);
}

.audit-empty-hint {
  font-size: 0.78rem;
  color: var(--color-faint);
  max-width: 52ch;
  margin: 6px auto 0;
  line-height: 1.55;
}

.audit-error {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 18px;
  font-size: 0.82rem;
  color: var(--color-red);
}

.audit-retry {
  margin-left: auto;
  background: none;
  border: none;
  color: var(--color-red);
  font-weight: 600;
  cursor: pointer;
  text-decoration: underline;
  text-underline-offset: 2px;
}

/* ─── skeleton ──────────────────────────────────────────────────────────────── */
.aev--skel {
  align-items: center;
}

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

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

.adot-skel { width: 34px; height: 34px; border-radius: var(--rounded-lg); }
.line-skel { height: 13px; width: 72%; }
.time-skel { height: 11px; width: 48px; }

/* ─── spinner ───────────────────────────────────────────────────────────────── */
.spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid var(--color-primary-soft);
  border-top-color: var(--color-primary);
  border-radius: var(--rounded-full);
  animation: spin 0.7s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

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

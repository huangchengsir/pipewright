<!--
  RunTerminal.vue — Story 3-6: 纯黑实时日志终端

  逐行渲染运行日志的只读终端组件。两种使用方式由 `live` 决定:
    · live=true  (running/queued) → 订阅 SSE:建连先回放历史 log,再实时 tail。
    · live=false (终态)          → 仅历史回放,一次性 getRunLogs 拉全量,只读。

  关键行为:
    · seq 去重 + 升序:SSE 回放历史与实时行可能重叠,统一以 seq 为主键去重、
      按 seq 升序维护(用 Map<seq,line> + 已知最大 seq 守门,O(1) 去重)。
    · 自动滚底:新行到达滚到底;但用户手动上滚(离开底部)时暂停自动滚——
      这是终端的常见行为;用户滚回底部自动恢复。滚动用 scrollTop 写入,
      不触发布局抖动。
    · stdout 默认色 / stderr 红色着色;行号(line-num token);命中 [MASKED]
      子串高亮标识(脱敏由后端完成,前端仅渲染标记)。

  脱敏铁律:text 已由后端脱敏,前端绝不二次处理 secret,仅按 [MASKED] 标记渲染。
  动效:仅 opacity(新行淡入);prefers-reduced-motion 降级。
-->
<script setup lang="ts">
import { ref, computed, shallowRef, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getRunLogs,
  subscribeRunEvents,
  type RunLogLine,
} from '../../api/runs'

const { t } = useI18n()

const props = defineProps<{
  runId: string
  /** true ⇒ 进行中,订阅 SSE 实时 tail;false ⇒ 终态,仅历史回放只读。 */
  live: boolean
  /** 仅显示该步骤序号(stepOrdinal)的日志;null/undefined = 全部步骤。点步骤详情切换。 */
  filterOrdinal?: number | null
}>()

// ─── log buffer: seq-keyed, ascending ─────────────────────────────────────────
// Map keeps insertion cheap and de-dupes by seq; we re-derive a sorted array
// only when membership changes. seq is monotonic per-run so once we've seen a
// seq we ignore any duplicate (SSE history replay overlapping live tail).

const bySeq = new Map<number, RunLogLine>()
const lines = shallowRef<RunLogLine[]>([])

function ingest(incoming: RunLogLine[]): void {
  let added = false
  for (const line of incoming) {
    if (bySeq.has(line.seq)) continue
    bySeq.set(line.seq, line)
    added = true
  }
  if (!added) return
  // Ascending by seq — authoritative ordering even if SSE/replay arrives jumbled.
  lines.value = Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq)
}

// ─── load / connection state ──────────────────────────────────────────────────

type LoadState = 'loading' | 'streaming' | 'done' | 'error'
const loadState = ref<LoadState>('loading')
const loadError = ref('')

let cleanupSse: (() => void) | null = null

function stopSse(): void {
  if (cleanupSse) {
    cleanupSse()
    cleanupSse = null
  }
}

async function bootstrap(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''

  if (props.live) {
    // Live mode: SSE handles both history replay (on connect) and live tail.
    // We don't pre-pull via REST to avoid double-fetching history — but if the
    // SSE connection drops, subscribeRunEvents falls back to polling getRun,
    // which does not carry logs; so we also seed once from getRunLogs to be
    // robust to a missed replay, relying on seq de-dup to avoid duplicates.
    try {
      const initial = await getRunLogs(props.runId, 0)
      ingest(initial.lines)
    } catch {
      // Non-fatal: SSE replay will still deliver history. Swallow silently.
    }
    loadState.value = 'streaming'
    cleanupSse = subscribeRunEvents(props.runId, {
      // status/step are owned by the parent view; here we only consume logs.
      onStatus() {},
      onStep() {},
      onLog(line) {
        ingest([line])
        scheduleAutoScroll()
      },
    })
  } else {
    // Terminal mode: one-shot full historical replay, read-only.
    try {
      let sinceSeq = 0
      // Drain pages until complete (logs endpoint paginates large logs).
      // Guard against a non-advancing cursor to avoid an infinite loop.
      for (let guard = 0; guard < 10_000; guard++) {
        const page = await getRunLogs(props.runId, sinceSeq)
        ingest(page.lines)
        if (page.complete || page.nextSeq <= sinceSeq) break
        if (page.lines.length === 0) break
        sinceSeq = page.nextSeq
      }
      loadState.value = 'done'
      scheduleAutoScroll()
    } catch (err) {
      loadError.value = err instanceof Error ? err.message : t('run.logLoadFailed')
      loadState.value = 'error'
    }
  }
}

onMounted(bootstrap)
onUnmounted(stopSse)

// Re-bootstrap if the run flips identity or live⇄terminal (status transition).
watch(
  () => [props.runId, props.live] as const,
  () => {
    stopSse()
    bySeq.clear()
    lines.value = []
    void bootstrap()
  },
)

// ─── auto-scroll: stick to bottom unless the user scrolled up ─────────────────

const scrollEl = ref<HTMLElement | null>(null)
const stickToBottom = ref(true)
// px tolerance: treat "near bottom" as bottom (sub-pixel / rounding slack).
const BOTTOM_EPS = 24

// 终端封顶 56vh、框内纵向滚动:贴底判断与跟随都作用于终端元素内部滚动(实时 tail 在框内滚到底,
// 不动整页)。用户在框内上滚 → 暂停跟随;滚回底部 → 恢复。
function onScroll(): void {
  const el = scrollEl.value
  if (!el) return
  const distance = el.scrollHeight - el.scrollTop - el.clientHeight
  stickToBottom.value = distance <= BOTTOM_EPS
}

let scrollQueued = false
function scheduleAutoScroll(): void {
  if (!props.live || !stickToBottom.value || scrollQueued) return
  scrollQueued = true
  void nextTick(() => {
    scrollQueued = false
    const el = scrollEl.value
    if (el && props.live && stickToBottom.value) {
      el.scrollTop = el.scrollHeight
    }
  })
}

function jumpToBottom(): void {
  stickToBottom.value = true
  const el = scrollEl.value
  if (el) el.scrollTop = el.scrollHeight
}

// 仅展示当前所选步骤的日志(filterOrdinal 为 null = 全部)。
const visibleLines = computed(() =>
  props.filterOrdinal == null
    ? lines.value
    : lines.value.filter((l) => l.stepOrdinal === props.filterOrdinal),
)
const hasLines = computed(() => visibleLines.value.length > 0)
const showFollowButton = computed(() => !stickToBottom.value && hasLines.value)

// ─── [MASKED] segmentation ─────────────────────────────────────────────────────
// Split a line into plain + masked segments so [MASKED] can be highlighted
// without dangerouslySetInnerHTML. Pure rendering of backend-provided markers.

interface Segment {
  text: string
  masked: boolean
}

const MASK_TOKEN = '[MASKED]'

function segments(text: string): Segment[] {
  if (!text.includes(MASK_TOKEN)) return [{ text, masked: false }]
  const out: Segment[] = []
  let rest = text
  let idx = rest.indexOf(MASK_TOKEN)
  while (idx !== -1) {
    if (idx > 0) out.push({ text: rest.slice(0, idx), masked: false })
    out.push({ text: MASK_TOKEN, masked: true })
    rest = rest.slice(idx + MASK_TOKEN.length)
    idx = rest.indexOf(MASK_TOKEN)
  }
  if (rest.length > 0) out.push({ text: rest, masked: false })
  return out
}

// Stable line key — seq is unique per run.
function lineKey(line: RunLogLine): number {
  return line.seq
}
</script>

<template>
  <div class="term" :class="{ 'term--live': props.live }" role="region" :aria-label="t('run.terminalRegion')">
    <!-- Terminal chrome bar -->
    <div class="term-bar">
      <span class="term-dots" aria-hidden="true">
        <span class="term-dot term-dot--r" />
        <span class="term-dot term-dot--y" />
        <span class="term-dot term-dot--g" />
      </span>
      <span class="term-label">{{ t('run.runLog') }}</span>
      <span
        v-if="props.live && loadState === 'streaming'"
        class="term-live"
        :aria-label="t('run.liveAria')"
      >
        <span class="term-live-dot" aria-hidden="true" />
        LIVE
      </span>
      <span class="term-count mono" v-if="hasLines">{{ t('run.lineCount', { n: visibleLines.length }) }}</span>
    </div>

    <!-- Log surface — 封顶 56vh,框内纵向滚动;实时 tail 框内贴底跟随 -->
    <div
      ref="scrollEl"
      class="term-scroll"
      tabindex="0"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      @scroll.passive="onScroll"
    >
      <!-- Loading -->
      <div v-if="loadState === 'loading'" class="term-state mono">
        {{ t('run.loadingLog') }}
      </div>

      <!-- Error (terminal-mode fetch failure) -->
      <div v-else-if="loadState === 'error'" class="term-state term-state--err mono">
        {{ loadError || t('run.logLoadFailed') }}
      </div>

      <!-- Empty -->
      <div
        v-else-if="!hasLines"
        class="term-state mono"
      >
        <template v-if="props.live">{{ t('run.waitingLog') }}</template>
        <template v-else>{{ t('run.noLogRecords') }}</template>
      </div>

      <!-- Lines -->
      <ol v-else class="term-lines" :aria-label="t('run.totalLinesAria', { n: visibleLines.length })">
        <li
          v-for="line in visibleLines"
          :key="lineKey(line)"
          class="term-line"
          :class="{ 'term-line--err': line.stream === 'stderr' }"
        >
          <span class="term-ln mono" aria-hidden="true">{{ line.seq }}</span>
          <span class="term-text mono">
            <template v-for="(seg, i) in segments(line.text)" :key="i">
              <span v-if="seg.masked" class="term-masked" :title="t('run.maskedTitle')">{{ seg.text }}</span>
              <template v-else>{{ seg.text }}</template>
            </template>
          </span>
        </li>
      </ol>
    </div>

    <!-- Jump-to-bottom affordance when auto-scroll is paused -->
    <button
      v-if="showFollowButton"
      class="term-follow"
      type="button"
      @click="jumpToBottom"
      :aria-label="t('run.jumpToLatest')"
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
        <path d="M12 5v14M19 12l-7 7-7-7" />
      </svg>
      {{ t('run.jumpToBottom') }}
    </button>
  </div>
</template>

<style scoped>
/* ─── shell ───────────────────────────────────────────────────────────────── */
.term {
  position: relative;
  display: flex;
  flex-direction: column;
  background: var(--color-term);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg);
  overflow: hidden;
  box-shadow: var(--shadow-inner);
  /* 终端高度贴合内容(不拉伸、不设上限):空/少行时是小盒子、底边紧跟最后一行,随日志增长
     而长高,整页单一滚动条。不再用大 min-height 撑出「拉不到底边」的空黑盒。 */
}

/* ─── chrome bar ──────────────────────────────────────────────────────────── */
.term-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  background: oklch(11% 0.004 270);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.term-dots {
  display: inline-flex;
  gap: 6px;
}

.term-dot {
  width: 10px;
  height: 10px;
  border-radius: var(--rounded-full);
  display: inline-block;
}

.term-dot--r { background: oklch(63% 0.2 25); }
.term-dot--y { background: oklch(80% 0.15 80); }
.term-dot--g { background: oklch(72% 0.16 150); }

.term-label {
  font-family: var(--font-mono);
  font-size: 0.74rem;
  letter-spacing: 0.02em;
  color: var(--color-faint);
}

.term-live {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: 4px;
  font-family: var(--font-mono);
  font-size: 0.66rem;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--color-amber);
}

.term-live-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  background: var(--color-amber);
  animation: term-pulse 1.1s ease-in-out infinite;
}

@keyframes term-pulse {
  0%, 100% { opacity: 1; }
  50%      { opacity: 0.35; }
}

.term-count {
  margin-left: auto;
  font-size: 0.68rem;
  color: var(--color-line-num);
}

/* ─── scroll surface ──────────────────────────────────────────────────────── */
.term-scroll {
  /* 高度贴合内容,但封顶 56vh:超过则框内纵向滚动(主区 overflow-x:clip 已修好整页滚动,
     不再有「嵌套双滚动滑不到底」的老问题)。空状态给个小下限避免太矮;长行横向滚动。 */
  overflow-x: auto;
  overflow-y: auto;
  min-height: 72px;
  max-height: 56vh;
  /* Comfortable but dense terminal feel */
  padding: 8px 0 12px;
  scrollbar-width: thin;
  scrollbar-color: var(--color-border-strong) transparent;
}

.term-scroll:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.term-scroll::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.term-scroll::-webkit-scrollbar-thumb {
  background: var(--color-border-strong);
  border-radius: var(--rounded-full);
  border: 2px solid transparent;
  background-clip: padding-box;
}

/* ─── states ──────────────────────────────────────────────────────────────── */
.term-state {
  padding: 22px 16px;
  font-size: 0.78rem;
  color: var(--color-faint);
}

.term-state--err {
  color: var(--color-red);
}

/* ─── lines ───────────────────────────────────────────────────────────────── */
.term-lines {
  list-style: none;
  margin: 0;
  padding: 0;
  counter-reset: none;
  /* keep long lines on one row; horizontal scroll handles overflow */
  min-width: max-content;
}

.term-line {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 0 16px;
  /* dense terminal line-height */
  line-height: 1.55;
  white-space: pre;
  /* compositor-friendly entrance for newly streamed rows */
  animation: line-in 140ms ease-out both;
}

@keyframes line-in {
  from { opacity: 0; }
  to   { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .term-line { animation: none; }
  .term-live-dot { animation: none; }
}

.term-line:hover {
  background: oklch(100% 0 0 / 0.035);
}

/* line number / seq gutter */
.term-ln {
  flex-shrink: 0;
  width: 3.5ch;
  text-align: right;
  font-size: 0.72rem;
  color: var(--color-line-num);
  user-select: none;
  -webkit-user-select: none;
}

.term-text {
  flex: 1;
  font-size: 0.78rem;
  color: oklch(88% 0.006 270);  /* terminal default foreground on pure black */
  white-space: pre;
  word-break: normal;
}

/* stderr coloring */
.term-line--err .term-text {
  color: var(--color-red);
}

.term-line--err .term-ln {
  color: var(--color-red);
  opacity: 0.6;
}

/* [MASKED] highlight — never reveals secrets, just marks redaction */
.term-masked {
  display: inline-block;
  padding: 0 4px;
  border-radius: var(--rounded-sm);
  background: var(--color-amber-soft);
  color: var(--color-amber);
  font-weight: 700;
  letter-spacing: 0.02em;
}

/* ─── follow button ───────────────────────────────────────────────────────── */
.term-follow {
  position: absolute;
  right: 16px;
  bottom: 14px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 11px;
  font-family: var(--font-sans);
  font-size: 0.74rem;
  font-weight: 600;
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-full);
  box-shadow: var(--shadow);
  cursor: pointer;
  transition: transform var(--duration-fast) var(--ease-out-expo),
              border-color var(--duration-fast);
}

.term-follow:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}

.term-follow:active {
  transform: translateY(0);
}

.term-follow:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

@media (prefers-reduced-motion: reduce) {
  .term-follow { transition: border-color var(--duration-fast); }
  .term-follow:hover { transform: none; }
}
</style>

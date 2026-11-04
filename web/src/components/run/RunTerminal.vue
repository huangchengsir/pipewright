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
import { findLogMatches, highlightLogLine } from './logSearch'
import { kubePhaseEvidence, type KubePhase } from '../kubernetes/kubernetesEvidence'
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
  kubernetesPhase?: KubePhase
}>()

const emit = defineEmits<{ kubernetesPhases: [texts: string[]] }>()

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
let generation = 0
let disposed = false

function stopSse(): void {
  if (cleanupSse) {
    cleanupSse()
    cleanupSse = null
  }
}

async function bootstrap(): Promise<void> {
  const gen = ++generation; const id = props.runId; const live = props.live
  loadState.value = 'loading'
  loadError.value = ''

  if (live) {
    // Live mode: SSE handles both history replay (on connect) and live tail.
    // We don't pre-pull via REST to avoid double-fetching history — but if the
    // SSE connection drops, subscribeRunEvents falls back to polling getRun,
    // which does not carry logs; so we also seed once from getRunLogs to be
    // robust to a missed replay, relying on seq de-dup to avoid duplicates.
    try {
      const initial = await getRunLogs(id, 0)
      if (gen !== generation || disposed) return
      ingest(initial.lines)
    } catch {
      // Non-fatal: SSE replay will still deliver history. Swallow silently.
    }
    if (gen !== generation || disposed) return
    loadState.value = 'streaming'
    cleanupSse = subscribeRunEvents(id, {
      // status/step are owned by the parent view; here we only consume logs.
      onStatus() {},
      onStep() {},
      onLog(line) {
        if (gen !== generation || disposed) return
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
        const page = await getRunLogs(id, sinceSeq)
        if (gen !== generation || disposed) return
        ingest(page.lines)
        if (page.complete || page.nextSeq <= sinceSeq) break
        if (page.lines.length === 0) break
        sinceSeq = page.lines.reduce((max, line) => Math.max(max, line.seq), sinceSeq)
      }
      loadState.value = 'done'
      scheduleAutoScroll()
    } catch (err) {
      if (gen !== generation || disposed) return
      loadError.value = err instanceof Error ? err.message : t('run.logLoadFailed')
      loadState.value = 'error'
    }
  }
}

onMounted(bootstrap)
onUnmounted(() => { disposed = true; generation++; stopSse() })

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
  const viewingTail = windowStart.value + CHUNK_SIZE >= visibleLines.value.length
  stickToBottom.value = viewingTail && distance <= BOTTOM_EPS
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
  windowStart.value = Math.max(0, visibleLines.value.length - CHUNK_SIZE)
  void nextTick(() => { if (el) el.scrollTop = el.scrollHeight })
}

// 仅展示当前所选步骤的日志(filterOrdinal 为 null = 全部)。
const nodeLines = computed(() =>
  props.filterOrdinal == null
    ? lines.value
    : lines.value.filter((l) => l.stepOrdinal === props.filterOrdinal),
)
watch(nodeLines, value => emit('kubernetesPhases', value.map(line=>line.text).filter(text=>text.startsWith('kubernetes '))), { immediate:true })
const visibleLines = computed(() => {
 if(!props.kubernetesPhase) return nodeLines.value
 const selected=props.kubernetesPhase
 const known=kubePhaseEvidence(nodeLines.value.map(line=>line.text))[selected]
 return nodeLines.value.filter(line=>line.text.startsWith(`kubernetes ${selected}: `)).map(line=>{
  const code=line.text.slice(`kubernetes ${selected}: `.length).trim()
  return {...line,text:known.includes(code)?t('kubernetes.phaseCode.'+code):t('kubernetes.notRecordedHint')}
 })
})
const hasLines = computed(() => visibleLines.value.length > 0)
const showFollowButton = computed(() => !stickToBottom.value && hasLines.value)

// Search and excerpts always use the current run + step's already redacted lines.
const searchQuery = ref("");
const wrapLines = ref(false);
const selectedStart = ref("");
const selectedEnd = ref("");
const copyMessage = ref("");
const manualCopy = ref("");
const manualCopyEl = ref<HTMLTextAreaElement | null>(null);
let copyGeneration = 0;
watch([selectedStart, selectedEnd], () => {
  copyGeneration++;
  copyMessage.value = "";
  manualCopy.value = "";
});
const CHUNK_SIZE = 500;
const windowStart = ref(0);
const renderedLines = computed(() =>
  visibleLines.value.slice(windowStart.value, windowStart.value + CHUNK_SIZE),
);
const searchResult = computed(() => findLogMatches(visibleLines.value, searchQuery.value))
const hits = computed(() => searchResult.value.hits)
const hitsBySeq = computed(() => {
  const grouped = new Map<number, typeof hits.value>()
  for (const hit of hits.value) { const group = grouped.get(hit.seq) ?? []; group.push(hit); grouped.set(hit.seq, group) }
  return grouped
})
const activeHit = ref(-1);
const activeSeq = ref<number | null>(null);
async function reveal(index: number, hitIndex?: number): Promise<void> {
  const line = visibleLines.value[index]
  if (!line) return
  const runId = props.runId, ordinal = props.filterOrdinal, query = searchQuery.value
  stickToBottom.value = false; activeSeq.value = line.seq
  if (index < windowStart.value || index >= windowStart.value + CHUNK_SIZE) windowStart.value = Math.floor(index / CHUNK_SIZE) * CHUNK_SIZE
  await nextTick()
  if (disposed || props.runId !== runId || props.filterOrdinal !== ordinal || searchQuery.value !== query || (hitIndex != null && activeHit.value !== hitIndex)) return
  const el = scrollEl.value
  const target = hitIndex == null ? el?.querySelector<HTMLElement>(`[data-log-seq="${line.seq}"]`) : el?.querySelector<HTMLElement>(`[data-log-hit="${hitIndex}"]`)
  if (!el || !target) return
  const rect = target.getBoundingClientRect(), bounds = el.getBoundingClientRect()
  el.scrollTop += rect.top - bounds.top - 16
  if (rect.left < bounds.left + 40 || rect.right > bounds.right - 16) el.scrollLeft += rect.left - bounds.left - 40
}
function moveHit(delta: number): void {
  if (!hits.value.length) return;
  activeHit.value =
    (activeHit.value + delta + hits.value.length) % hits.value.length;
  const hit = hits.value[activeHit.value];
  if (hit) void reveal(hit.index, activeHit.value);
}
watch(wrapLines, () => { const hit = hits.value[activeHit.value]; if (hit) void reveal(hit.index, activeHit.value) })
watch(searchQuery, () => {
  activeHit.value = -1;
  activeSeq.value = null;
  if (hits.value.length) moveHit(1);
});
function locateError(): void {
  const index = visibleLines.value.findIndex(
    (l) =>
      l.stream === "stderr" ||
      /\b(error|fail(?:ed|ure)?|fatal|exception)\b|\bexit(?:ed)?(?: code| status)?[:= ]+[1-9]\d*/i.test(
        l.text,
      ),
  );
  if (index >= 0) void reveal(index);
  else copyMessage.value = t("runExperience.noErrorOutput");
}
function changeChunk(delta: number): void {
  stickToBottom.value = false;
  windowStart.value = Math.max(
    0,
    Math.min(
      Math.max(0, visibleLines.value.length - 1),
      windowStart.value + delta * CHUNK_SIZE,
    ),
  );
  void nextTick(() => {
    if (scrollEl.value) scrollEl.value.scrollTop = 0;
  });
}
function selectLine(seq: number, shift: boolean): void {
  if (shift && selectedStart.value) selectedEnd.value = String(seq);
  else {
    selectedStart.value = String(seq);
    selectedEnd.value = String(seq);
  }
  copyMessage.value = "";
  manualCopy.value = "";
}
function selected(seq: number): boolean {
  if (!selectedStart.value || !selectedEnd.value) return false;
  return (
    seq >= Math.min(Number(selectedStart.value), Number(selectedEnd.value)) &&
    seq <= Math.max(Number(selectedStart.value), Number(selectedEnd.value))
  );
}
async function copyExcerpt(): Promise<void> {
  const generation = ++copyGeneration;
  copyMessage.value = "";
  manualCopy.value = "";
  const first = Number(selectedStart.value),
    last = Number(selectedEnd.value);
  if (
    !Number.isSafeInteger(first) ||
    !Number.isSafeInteger(last) ||
    !visibleLines.value.some((l) => l.seq === first) ||
    !visibleLines.value.some((l) => l.seq === last)
  ) {
    copyMessage.value = t("runExperience.selectLines");
    return;
  }
  const excerpt = visibleLines.value.filter(
    (l) => l.seq >= Math.min(first, last) && l.seq <= Math.max(first, last),
  );
  const text = excerpt.map((l) => `${l.seq}: ${l.text}`).join("\n");
  if (excerpt.length > 500 || text.length > 64000) {
    copyMessage.value = t("runExperience.copyLimit");
    return;
  }
  try {
    if (!navigator.clipboard?.writeText)
      throw new Error("clipboard unavailable");
    await navigator.clipboard.writeText(text);
    if (generation === copyGeneration && !disposed)
      copyMessage.value = t("runExperience.copied");
  } catch {
    if (generation !== copyGeneration || disposed) return;
    manualCopy.value = text;
    copyMessage.value = t("runExperience.manualCopy");
    await nextTick();
    manualCopyEl.value?.focus();
    manualCopyEl.value?.select();
  }
}
watch(
  () => [props.runId, props.filterOrdinal, props.kubernetesPhase] as const,
  () => {
    copyGeneration++;
    searchQuery.value = "";
    selectedStart.value = "";
    selectedEnd.value = "";
    manualCopy.value = "";
    copyMessage.value = "";
    windowStart.value = 0;
    activeHit.value = -1;
    activeSeq.value = null;
    stickToBottom.value = true;
  },
);
watch(
  () => visibleLines.value.length,
  (length) => {
    if (props.live && stickToBottom.value)
      windowStart.value = Math.max(0, length - CHUNK_SIZE);
  },
);

function segments(line: RunLogLine) { return highlightLogLine(line.text, hitsBySeq.value.get(line.seq) ?? []) }

// Stable line key — seq is unique per run.
function lineKey(line: RunLogLine): number {
  return line.seq;
}
</script>

<template>
  <div
    class="term"
    :class="{ 'term--live': props.live, 'term--wrap': wrapLines }"
    role="region"
    :aria-label="t('run.terminalRegion')"
  >
    <!-- Terminal chrome bar -->
    <div class="term-bar">
      <span class="term-dots" aria-hidden="true">
        <span class="term-dot term-dot--r" />
        <span class="term-dot term-dot--y" />
        <span class="term-dot term-dot--g" />
      </span>
      <span class="term-label">{{ t("run.runLog") }}</span>
      <span
        v-if="props.live && loadState === 'streaming'"
        class="term-live"
        :aria-label="t('run.liveAria')"
      >
        <span class="term-live-dot" aria-hidden="true" />
        LIVE
      </span>
      <span class="term-count mono" v-if="hasLines">{{
        t("run.lineCount", { n: visibleLines.length })
      }}</span>
    </div>

    <div
      class="term-tools"
      role="group"
      :aria-label="t('runExperience.logTools')"
    >
      <label class="term-search"
        ><span class="sr-only">{{ t(props.filterOrdinal == null ? "runExperience.searchAllLogs" : "runExperience.searchLogs") }}</span
        ><input
          v-model="searchQuery"
          maxlength="200"
          type="search"
          :placeholder="t(props.filterOrdinal == null ? 'runExperience.searchAllLogs' : 'runExperience.searchLogs')"
          @keydown.enter.prevent="moveHit($event.shiftKey ? -1 : 1)"
      /></label>
      <span class="term-hits" role="status">{{
        t("runExperience.hits", {
          n: hits.length ? activeHit + 1 : 0,
          total: hits.length,
        })
      }}</span>
      <button
        type="button"
        :disabled="!hits.length"
        :aria-label="t('runExperience.previousMatch')"
        @click="moveHit(-1)"
      >
        ↑</button
      ><button
        type="button"
        :disabled="!hits.length"
        :aria-label="t('runExperience.nextMatch')"
        @click="moveHit(1)"
      >
        ↓
      </button>
      <button class="term-action" type="button" @click="locateError">
        {{ t("runExperience.locateError") }}
      </button>
      <button
        class="term-action"
        type="button"
        :aria-pressed="wrapLines"
        @click="wrapLines = !wrapLines"
      >
        {{ t("runExperience.wrap") }}
      </button>
    </div>
    <p v-if="searchResult.queryTooLong" class="term-search-limit" role="status">{{ t('runExperience.queryTooLong', { n: 200 }) }}</p>
    <p v-else-if="searchResult.truncated" class="term-search-limit" role="status">{{ t('runExperience.hitsLimited', { n: hits.length }) }}</p>
    <div class="term-tools term-excerpt">
      <label
        >{{ t("runExperience.fromSeq")
        }}<input
          v-model="selectedStart"
          type="number"
          min="1"
          :aria-label="t('runExperience.fromSeq')"
      /></label>
      <label
        >{{ t("runExperience.toSeq")
        }}<input
          v-model="selectedEnd"
          type="number"
          min="1"
          :aria-label="t('runExperience.toSeq')"
      /></label>
      <button type="button" @click="copyExcerpt">
        {{ t("runExperience.copyExcerpt") }}
      </button>
      <span v-if="copyMessage" class="term-copy-status" role="status">{{
        copyMessage
      }}</span>
    </div>
    <textarea
      v-if="manualCopy"
      ref="manualCopyEl"
      class="term-manual"
      readonly
      :value="manualCopy"
      :aria-label="t('runExperience.manualCopy')"
    />
    <div v-if="visibleLines.length > CHUNK_SIZE" class="term-tools term-pages">
      <button
        type="button"
        :disabled="windowStart === 0"
        @click="changeChunk(-1)"
      >
        {{ t("runExperience.previous") }}
      </button>
      <span>{{
        t("runExperience.logRange", {
          from: windowStart + 1,
          to: Math.min(windowStart + CHUNK_SIZE, visibleLines.length),
          total: visibleLines.length,
        })
      }}</span>
      <button
        type="button"
        :disabled="windowStart + CHUNK_SIZE >= visibleLines.length"
        @click="changeChunk(1)"
      >
        {{ t("runExperience.next") }}
      </button>
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
        {{ t("run.loadingLog") }}
      </div>

      <!-- Error (terminal-mode fetch failure) -->
      <div
        v-else-if="loadState === 'error'"
        class="term-state term-state--err mono"
      >
        {{ loadError || t("run.logLoadFailed") }}
      </div>

      <!-- Empty -->
      <div v-else-if="!hasLines" class="term-state mono">
        <template v-if="props.live">{{ t("run.waitingLog") }}</template>
        <template v-else>{{ t("run.noLogRecords") }}</template>
      </div>

      <!-- Lines -->
      <ol
        v-else
        class="term-lines"
        :aria-label="t('run.totalLinesAria', { n: visibleLines.length })"
      >
        <li
          v-for="line in renderedLines"
          :key="lineKey(line)"
          class="term-line"
          :class="{
            'term-line--err': line.stream === 'stderr',
            'term-line--active': line.seq === activeSeq,
            'term-line--selected': selected(line.seq),
          }"
          :data-log-seq="line.seq"
        >
          <button
            class="term-ln mono"
            type="button"
            :aria-label="t('runExperience.selectSeq', { n: line.seq })"
            :aria-pressed="selected(line.seq)"
            @click="selectLine(line.seq, $event.shiftKey)"
          >
            {{ line.seq }}
          </button>
          <span class="term-text mono">
            <template v-for="(segment, i) in segments(line)" :key="i">
              <mark v-if="segment.hitIndex != null" class="term-match" :class="{ 'term-match--active': segment.hitIndex === activeHit }" :data-log-hit="segment.hitIndex"><template v-for="(part, j) in segment.parts" :key="j"><span v-if="part.masked" class="term-masked" :title="t('run.maskedTitle')">{{ part.text }}</span><template v-else>{{ part.text }}</template></template></mark>
              <template v-else><template v-for="(part, j) in segment.parts" :key="j"><span v-if="part.masked" class="term-masked" :title="t('run.maskedTitle')">{{ part.text }}</span><template v-else>{{ part.text }}</template></template></template>
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
      <svg
        width="13"
        height="13"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2.2"
        aria-hidden="true"
      >
        <path d="M12 5v14M19 12l-7 7-7-7" />
      </svg>
      {{ t("run.jumpToBottom") }}
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
.term {
  min-width: 0;
  max-width: 100%;
  container-type: inline-size;
}
.term-tools {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 7px 10px;
  background: #111318;
  border-bottom: 1px solid #2b3039;
  color: #c3c9d2;
  font-size: 12px;
}
.term-tools input,
.term-tools button {
  min-height: 30px;
  background: #191c23;
  color: #e6e9ef;
  border: 1px solid #3b4351;
  border-radius: 5px;
  font: inherit;
  padding: 3px 8px;
}
.term-tools button {
  cursor: pointer;
  white-space: normal;
  overflow-wrap: anywhere;
  min-width: 0;
}
.term-tools button:disabled {
  opacity: 0.4;
  cursor: default;
}
.term-tools button:focus-visible,
.term-tools input:focus-visible {
  outline: 2px solid #78a8ff;
  outline-offset: 2px;
}
.term-tools button[aria-pressed="true"] {
  border-color: #78a8ff;
  color: #9fc4ff;
}
.term-search {
  flex: 1 1 140px;
  min-width: 0;
}
.term-search input {
  width: 100%;
}
.term-hits {
  font-variant-numeric: tabular-nums;
}
.term-excerpt label {
  display: flex;
  align-items: center;
  gap: 5px;
}
.term-excerpt input {
  width: 75px;
  min-width: 0;
}
.term-copy-status {
  font-size: 11px;
  overflow-wrap: anywhere;
}
.term-pages {
  justify-content: space-between;
}
.term-manual {
  box-sizing: border-box;
  width: 100%;
  height: 100px;
  background: #111318;
  color: #eef1f6;
  font-family: var(--font-mono);
  padding: 10px;
  border: 1px solid #78a8ff;
}
.term-ln {
  background: transparent;
  border: 0;
  padding: 0;
  cursor: pointer;
  min-height: 20px;
}
.term-ln:hover {
  color: #9fc4ff;
}
.term-line--active {
  box-shadow: inset 3px 0 #78a8ff;
  background: #1b2b42;
}
.term-line--selected {
  background: #20354b;
}
.term-match {
  background: #dcc065;
  color: #151515;
  border-radius: 2px;
}
.term--wrap .term-lines {
  min-width: 0;
  width: 100%;
}
.term--wrap .term-line,
.term--wrap .term-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  min-width: 0;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
@media (max-width: 600px) {
  .term-tools input {
    font-size: 16px;
  }
  .term-tools button {
    min-height: 44px;
  }
  .term-excerpt label {
    font-size: 11px;
  }
  .term-line {
    padding-left: 8px;
    padding-right: 8px;
    gap: 8px;
  }
}

@container (max-width:300px) {
  .term-hits { flex: 0 0 auto; white-space: nowrap; }
  .term-action { padding-inline: 6px; }
  .term-excerpt {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 6px;
  }
  .term-excerpt label {
    flex-direction: column;
    align-items: stretch;
    gap: 4px;
    min-width: 0;
    font-size: 11px;
  }
  .term-excerpt input {
    box-sizing: border-box;
    width: 100%;
    min-height: 36px;
  }
  .term-excerpt button,
  .term-copy-status {
    grid-column: 1/-1;
  }
  .term-action {
    flex: 1 1 calc(50% - 6px);
  }
  .term-search {
    flex-basis: 100%;
  }
  .term-hits {
    flex: 1;
  }
}
.term-match--active {outline:2px solid #91bbff;outline-offset:1px;background:#ffcc58;}
.term-search-limit {margin:0;padding:6px 10px;background:#111318;color:#e4c87c;font-size:11px;}
</style>

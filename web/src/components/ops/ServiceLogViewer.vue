<!--
  ServiceLogViewer.vue — Story 6-2: 服务日志查看(FR-16,经 SSH 取目标服务器日志)

  一个面向单台已登记服务器的日志查看器:
    · source 选择(journald / file / docker)+ target 输入(unit 名 / 绝对路径 / 容器名)
    · 历史拉取(tail -n N):一次性取最近 N 行渲染到纯黑终端。
    · 实时 tail 开关:开启即订阅 SSE(`logline` 事件),逐行追加;关闭即断流,
      后端随之关 SSH session(不泄漏)。

  安全(AC-SEC-02):target 由后端严格白名单校验;非法 → 400 invalid_log_target,
  本组件原样呈现人读错误,绝不在前端拼任何 shell。SSH/命令失败 → 200 + error 字段,
  本组件渲染为终端内的错误状态,而非崩溃。

  终端风格参照 3-6 RunTerminal.vue(纯黑、等宽、自动滚底、reduced-motion 降级)。
-->
<script setup lang="ts">
import { ref, computed, shallowRef, onUnmounted, nextTick, watch } from 'vue'
import {
  getServerLogs,
  subscribeServerLogs,
  type LogSource,
} from '../../api/servers'
import { HttpError } from '../../api/http'

const props = defineProps<{
  serverId: string
  /** 展示用服务器名(终端标题)。 */
  serverName?: string
}>()

// ─── controls ─────────────────────────────────────────────────────────────────

const source = ref<LogSource>('file')
const targetInput = ref('')
const linesInput = ref(200)
const live = ref(false)

const sourcePlaceholder = computed(() => {
  switch (source.value) {
    case 'file':
      return '/var/log/app.log(绝对路径,无 .. 无 shell 元字符)'
    case 'journald':
      return 'nginx.service(unit 名)'
    case 'docker':
      return 'my-container(容器名)'
    default:
      return ''
  }
})

const targetValid = computed(() => targetInput.value.trim().length > 0)

// ─── log buffer ───────────────────────────────────────────────────────────────
// 每行一个递增序号(本地生成,仅用于稳定 key + 行号呈现;后端 ts 恒 null)。

interface ViewLine {
  seq: number
  text: string
}

let seqCounter = 0
const lines = shallowRef<ViewLine[]>([])

function resetLines(): void {
  seqCounter = 0
  lines.value = []
}

function appendLines(texts: string[]): void {
  if (texts.length === 0) return
  const next = lines.value.slice()
  for (const t of texts) {
    next.push({ seq: seqCounter++, text: t })
  }
  lines.value = next
  scheduleAutoScroll()
}

// ─── load / stream state ──────────────────────────────────────────────────────

type ViewState = 'idle' | 'loading' | 'history' | 'streaming' | 'error'
const viewState = ref<ViewState>('idle')
const errorMsg = ref('')

let cleanupSse: (() => void) | null = null

function stopStream(): void {
  if (cleanupSse) {
    cleanupSse()
    cleanupSse = null
  }
}

/** 历史拉取(一次性最近 N 行)。 */
async function fetchHistory(): Promise<void> {
  if (!targetValid.value) {
    errorMsg.value = '请填写日志目标(unit 名 / 绝对路径 / 容器名)'
    viewState.value = 'error'
    return
  }
  stopStream()
  live.value = false
  resetLines()
  viewState.value = 'loading'
  errorMsg.value = ''
  try {
    const res = await getServerLogs(props.serverId, {
      source: source.value,
      target: targetInput.value.trim(),
      lines: linesInput.value,
    })
    if (res.error) {
      // SSH/命令失败:后端 200 + 人读 error。呈现为终端内错误,不崩。
      errorMsg.value = res.error
      viewState.value = 'error'
      return
    }
    appendLines(res.lines.map((l) => l.text))
    viewState.value = 'history'
  } catch (err) {
    errorMsg.value = humanError(err)
    viewState.value = 'error'
  }
}

/** 切换实时 tail。 */
function toggleLive(): void {
  if (live.value) {
    // 关闭实时:断流(后端关 SSH session)。
    stopStream()
    live.value = false
    viewState.value = lines.value.length > 0 ? 'history' : 'idle'
    return
  }
  if (!targetValid.value) {
    errorMsg.value = '请填写日志目标后再开启实时'
    viewState.value = 'error'
    return
  }
  // 开启实时:清屏后订阅 SSE(后端先发最近 N 行,再实时追加)。
  resetLines()
  errorMsg.value = ''
  viewState.value = 'streaming'
  live.value = true
  cleanupSse = subscribeServerLogs(
    props.serverId,
    { source: source.value, target: targetInput.value.trim(), lines: linesInput.value },
    {
      onLine(text) {
        appendLines([text])
      },
      onError(message) {
        // 后端 SSE `error` 事件:SSH/命令失败的人读告知。
        errorMsg.value = message
        viewState.value = 'error'
        stopStream()
        live.value = false
      },
      onTransportError() {
        // 连接掉线:EventSource 会自动重连;此处仅提示。不视为致命。
        if (!live.value) return
        errorMsg.value = '实时连接中断,正在尝试重连…'
      },
    },
  )
}

function clearLogs(): void {
  resetLines()
  errorMsg.value = ''
  viewState.value = live.value ? 'streaming' : 'idle'
}

// 切 source/target 时若在实时中,断开旧流(避免取到旧目标的流)。
watch([source, targetInput], () => {
  if (live.value) {
    stopStream()
    live.value = false
    viewState.value = lines.value.length > 0 ? 'history' : 'idle'
  }
})

onUnmounted(stopStream)

function humanError(err: unknown): string {
  if (err instanceof HttpError) {
    if (err.status === 0) return '无法连接到后端,请检查服务是否运行'
    if (err.apiError?.code === 'invalid_log_target') {
      return err.apiError.message ?? '日志目标非法(已被安全校验拦截)'
    }
    if (err.apiError?.code === 'server_not_found') return '服务器不存在'
    if (err.apiError?.code === 'vault_unconfigured') {
      return '保险库未配置 master key,无法取 SSH 凭据'
    }
    return err.apiError?.message ?? `取日志失败(${err.status})`
  }
  return '取日志失败,请稍后重试'
}

// ─── auto-scroll(贴底,用户上滚则暂停) ──────────────────────────────────────

const scrollEl = ref<HTMLElement | null>(null)
const stickToBottom = ref(true)
const BOTTOM_EPS = 24

function onScroll(): void {
  const el = scrollEl.value
  if (!el) return
  const distance = el.scrollHeight - el.scrollTop - el.clientHeight
  stickToBottom.value = distance <= BOTTOM_EPS
}

let scrollQueued = false
function scheduleAutoScroll(): void {
  if (!stickToBottom.value || scrollQueued) return
  scrollQueued = true
  void nextTick(() => {
    scrollQueued = false
    const el = scrollEl.value
    if (el && stickToBottom.value) el.scrollTop = el.scrollHeight
  })
}

function jumpToBottom(): void {
  stickToBottom.value = true
  const el = scrollEl.value
  if (el) el.scrollTop = el.scrollHeight
}

const hasLines = computed(() => lines.value.length > 0)
const showFollowButton = computed(() => !stickToBottom.value && hasLines.value)
</script>

<template>
  <div class="logviewer">
    <!-- controls -->
    <div class="lv-controls">
      <label class="lv-field">
        <span class="lv-label">日志源</span>
        <select v-model="source" class="lv-input lv-select" aria-label="日志源">
          <option value="file">文件 (tail)</option>
          <option value="journald">journald (unit)</option>
          <option value="docker">docker 容器</option>
        </select>
      </label>

      <label class="lv-field lv-field--grow">
        <span class="lv-label">目标</span>
        <input
          v-model="targetInput"
          class="lv-input mono"
          type="text"
          :placeholder="sourcePlaceholder"
          autocomplete="off"
          spellcheck="false"
          @keyup.enter="fetchHistory"
        />
      </label>

      <label class="lv-field lv-field--lines">
        <span class="lv-label">行数</span>
        <input v-model.number="linesInput" class="lv-input" type="number" min="1" max="2000" />
      </label>

      <div class="lv-actions">
        <button
          class="lv-btn"
          type="button"
          :disabled="!targetValid || viewState === 'loading'"
          @click="fetchHistory"
        >
          {{ viewState === 'loading' ? '加载中…' : '查看历史' }}
        </button>
        <button
          class="lv-btn"
          :class="{ 'lv-btn--live': live }"
          type="button"
          :disabled="!targetValid"
          @click="toggleLive"
        >
          {{ live ? '■ 停止实时' : '▶ 实时 tail' }}
        </button>
        <button class="lv-btn lv-btn--ghost" type="button" :disabled="!hasLines" @click="clearLogs">
          清屏
        </button>
      </div>
    </div>

    <!-- terminal -->
    <div class="term" role="region" :aria-label="`${serverName ?? '服务器'} 日志终端`">
      <div class="term-bar">
        <span class="term-dots" aria-hidden="true">
          <span class="term-dot term-dot--r" />
          <span class="term-dot term-dot--y" />
          <span class="term-dot term-dot--g" />
        </span>
        <span class="term-label mono">{{ serverName ?? '日志' }}</span>
        <span v-if="live && viewState === 'streaming'" class="term-live" aria-label="实时">
          <span class="term-live-dot" aria-hidden="true" />
          LIVE
        </span>
        <span v-if="hasLines" class="term-count mono">{{ lines.length }} 行</span>
      </div>

      <div
        ref="scrollEl"
        class="term-scroll"
        tabindex="0"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        @scroll.passive="onScroll"
      >
        <div v-if="viewState === 'loading'" class="term-state mono">正在取日志…</div>

        <div v-else-if="viewState === 'error'" class="term-state term-state--err mono">
          {{ errorMsg || '取日志失败' }}
        </div>

        <div v-else-if="!hasLines" class="term-state mono">
          <template v-if="live">等待日志输出…</template>
          <template v-else>选择日志源 + 填写目标,点「查看历史」或「实时 tail」。</template>
        </div>

        <ol v-else class="term-lines" :aria-label="`共 ${lines.length} 行日志`">
          <li v-for="line in lines" :key="line.seq" class="term-line">
            <span class="term-ln mono" aria-hidden="true">{{ line.seq + 1 }}</span>
            <span class="term-text mono">{{ line.text }}</span>
          </li>
        </ol>
      </div>

      <button
        v-if="showFollowButton"
        class="term-follow"
        type="button"
        @click="jumpToBottom"
        aria-label="跳到最新日志"
      >
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M12 5v14M19 12l-7 7-7-7" />
        </svg>
        跳到底部
      </button>
    </div>
  </div>
</template>

<style scoped>
.logviewer {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* ─── controls ────────────────────────────────────────────────────────────── */
.lv-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 12px;
}

.lv-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.lv-field--grow {
  flex: 1 1 280px;
  min-width: 220px;
}

.lv-field--lines {
  width: 92px;
}

.lv-label {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--color-faint);
}

.lv-input {
  height: 38px;
  padding: 0 11px;
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-text);
  font-size: 0.84rem;
  transition: border-color var(--duration-fast);
}

.lv-input:focus {
  outline: none;
  border-color: var(--color-primary);
}

.lv-select {
  cursor: pointer;
}

.lv-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.lv-btn {
  height: 38px;
  padding: 0 14px;
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-text);
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: border-color var(--duration-fast), transform var(--duration-fast) var(--ease-out-expo);
}

.lv-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}

.lv-btn:active:not(:disabled) {
  transform: translateY(0);
}

.lv-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.lv-btn--live {
  color: var(--color-amber);
  border-color: var(--color-amber);
  background: var(--color-amber-soft);
}

.lv-btn--ghost {
  background: transparent;
}

.lv-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ─── terminal (参照 RunTerminal.vue) ─────────────────────────────────────── */
.term {
  position: relative;
  display: flex;
  flex-direction: column;
  background: var(--color-term);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg);
  overflow: hidden;
  box-shadow: var(--shadow-inner);
  min-height: 260px;
}

.term-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  background: oklch(11% 0.004 270);
  border-bottom: 1px solid var(--color-border);
  flex-shrink: 0;
}

.term-dots { display: inline-flex; gap: 6px; }
.term-dot { width: 10px; height: 10px; border-radius: var(--rounded-full); display: inline-block; }
.term-dot--r { background: oklch(63% 0.2 25); }
.term-dot--y { background: oklch(80% 0.15 80); }
.term-dot--g { background: oklch(72% 0.16 150); }

.term-label {
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

.term-scroll {
  flex: 1;
  overflow-y: auto;
  overflow-x: auto;
  padding: 8px 0 12px;
  max-height: 520px;
  scrollbar-width: thin;
  scrollbar-color: var(--color-border-strong) transparent;
}

.term-scroll:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.term-scroll::-webkit-scrollbar { width: 10px; height: 10px; }
.term-scroll::-webkit-scrollbar-thumb {
  background: var(--color-border-strong);
  border-radius: var(--rounded-full);
  border: 2px solid transparent;
  background-clip: padding-box;
}

.term-state {
  padding: 22px 16px;
  font-size: 0.78rem;
  color: var(--color-faint);
}

.term-state--err { color: var(--color-red); }

.term-lines {
  list-style: none;
  margin: 0;
  padding: 0;
  min-width: max-content;
}

.term-line {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 0 16px;
  line-height: 1.55;
  white-space: pre;
  animation: line-in 140ms ease-out both;
}

@keyframes line-in {
  from { opacity: 0; }
  to   { opacity: 1; }
}

@media (prefers-reduced-motion: reduce) {
  .term-line { animation: none; }
  .term-live-dot { animation: none; }
  .lv-btn:hover { transform: none; }
}

.term-line:hover { background: oklch(100% 0 0 / 0.035); }

.term-ln {
  flex-shrink: 0;
  width: 4ch;
  text-align: right;
  font-size: 0.72rem;
  color: var(--color-line-num);
  user-select: none;
  -webkit-user-select: none;
}

.term-text {
  flex: 1;
  font-size: 0.78rem;
  color: oklch(88% 0.006 270);
  white-space: pre;
  word-break: normal;
}

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
  transition: transform var(--duration-fast) var(--ease-out-expo), border-color var(--duration-fast);
}

.term-follow:hover {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}

.term-follow:active { transform: translateY(0); }
.term-follow:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }

@media (prefers-reduced-motion: reduce) {
  .term-follow { transition: border-color var(--duration-fast); }
  .term-follow:hover { transform: none; }
}
</style>

<!--
  ContainerTerminal.vue — Story 6-4: 容器内交互终端(FR-18,WS ↔ SSH → docker exec -it)

  面向单台已登记服务器:输入容器 ID(+ 可选 shell)→ 进入容器执行交互命令。
    · 传输:**WebSocket**(平台唯一 WS 用途;其余实时流走 SSE)。同源 cookie 自动携带。
    · 渲染:xterm.js(**动态 import**,不进主 bundle —— 性能预算)。
    · 输入转发:键盘 → ws.send;窗口尺寸 → addon-fit + resize 控制帧 → 后端 WindowChange。
    · 断线:后端关连接 / 远端退出 → 终端冻结 + 人读提示,可重连。

  安全(FR-18 / AC-SEC-02):containerId / shell 由后端严格白名单校验,本组件绝不在前端拼
  任何 shell;非法 → 400,呈现人读错误。鉴权由 /api 组会话校验 + 后端同源(Origin)校验把守。

  视觉(UX-DR6):纯黑终端 + 内阴影 + mac 三灯 + 等宽字。键盘可达,Esc 关闭由父级模态处理(UX-DR10)。
-->
<script setup lang="ts">
import { ref, shallowRef, onMounted, onBeforeUnmount, nextTick } from 'vue'
import {
  openContainerTerminal,
  type TerminalConnection,
  type TerminalShell,
} from '../../api/servers'

// xterm 类型(仅类型,不影响运行时 / bundle)。
import type { Terminal as XTerm } from '@xterm/xterm'
import type { FitAddon as XFitAddon } from '@xterm/addon-fit'

const props = defineProps<{
  serverId: string
  /** 展示用服务器名(终端标题)。 */
  serverName?: string
}>()

// ─── controls ─────────────────────────────────────────────────────────────────

const containerInput = ref('')
const shell = ref<TerminalShell>('/bin/sh')

const allowedShells: TerminalShell[] = ['/bin/sh', '/bin/bash', '/bin/ash', '/bin/zsh', 'sh', 'bash']

type ConnState = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'
const connState = ref<ConnState>('idle')
const statusMsg = ref('')

const containerValid = () => /^[\w][\w.-]*$/.test(containerInput.value.trim())

// ─── xterm + ws wiring ──────────────────────────────────────────────────────────

const termHost = ref<HTMLElement | null>(null)
const term = shallowRef<XTerm | null>(null)
const fitAddon = shallowRef<XFitAddon | null>(null)
const conn = shallowRef<TerminalConnection | null>(null)

let resizeObserver: ResizeObserver | null = null
const decoder = new TextDecoder()

/** 懒加载 xterm + addon-fit(动态 import,首帧不引入)。仅初始化一次。 */
async function ensureTerm(): Promise<XTerm> {
  if (term.value) return term.value
  const [{ Terminal }, { FitAddon }] = await Promise.all([
    import('@xterm/xterm'),
    import('@xterm/addon-fit'),
  ])
  await import('@xterm/xterm/css/xterm.css')

  const t = new Terminal({
    cursorBlink: true,
    fontFamily: 'var(--font-mono), "JetBrains Mono", ui-monospace, monospace',
    fontSize: 13,
    convertEol: false,
    theme: {
      background: '#0a0a0c',
      foreground: '#e0e0e4',
      cursor: '#e0e0e4',
    },
    scrollback: 5000,
  })
  const fit = new FitAddon()
  t.loadAddon(fit)
  await nextTick()
  if (termHost.value) {
    t.open(termHost.value)
    fit.fit()
  }
  // 键入 → 转发到容器 stdin。
  t.onData((data) => {
    conn.value?.send(data)
  })
  term.value = t
  fitAddon.value = fit
  return t
}

function refit(): void {
  const fit = fitAddon.value
  const t = term.value
  if (!fit || !t) return
  try {
    fit.fit()
    conn.value?.resize(t.cols, t.rows)
  } catch {
    // host not laid out yet — ignore
  }
}

async function connect(): Promise<void> {
  if (!containerValid()) {
    statusMsg.value = '请填写合法容器 ID(字母数字与 . _ -,不得以 - 开头)'
    connState.value = 'error'
    return
  }
  // 关掉旧连接(切容器重连)。
  disconnect()

  const t = await ensureTerm()
  t.clear()
  t.focus()
  connState.value = 'connecting'
  statusMsg.value = `正在连接容器 ${containerInput.value.trim()} …`

  conn.value = openContainerTerminal(
    props.serverId,
    containerInput.value.trim(),
    {
      onOpen() {
        connState.value = 'connected'
        statusMsg.value = ''
        refit()
        t.focus()
      },
      onData(chunk) {
        t.write(decoder.decode(chunk, { stream: true }))
      },
      onClose(reason) {
        if (connState.value === 'connecting') {
          // 从未连上(鉴权 / 同源 / 非法目标 / 连接失败)。
          connState.value = 'error'
          statusMsg.value = reason || '连接失败,请检查容器 ID、登录状态或服务器可达性'
        } else {
          connState.value = 'closed'
          statusMsg.value = reason || '终端会话已结束'
        }
        t.write('\r\n\x1b[2m── ' + (statusMsg.value || '会话结束') + ' ──\x1b[0m\r\n')
      },
    },
    shell.value,
  )

  // 窗口尺寸变化 → 重新 fit + 通知后端 WindowChange。
  if (!resizeObserver && termHost.value) {
    resizeObserver = new ResizeObserver(() => refit())
    resizeObserver.observe(termHost.value)
  }
}

function disconnect(): void {
  conn.value?.close()
  conn.value = null
  if (connState.value === 'connected' || connState.value === 'connecting') {
    connState.value = 'closed'
  }
}

onMounted(() => {
  // 预热 xterm(打开容器前就把终端铺好,减少首连等待)。
  void ensureTerm()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  disconnect()
  term.value?.dispose()
  term.value = null
})
</script>

<template>
  <div class="cterm">
    <!-- controls -->
    <div class="ct-controls">
      <label class="ct-field ct-field--grow">
        <span class="ct-label">容器 ID / 名</span>
        <input
          v-model="containerInput"
          class="ct-input mono"
          type="text"
          placeholder="my-app(容器名或 ID,字母数字与 . _ -)"
          autocomplete="off"
          spellcheck="false"
          @keyup.enter="connect"
        />
      </label>

      <label class="ct-field ct-field--shell">
        <span class="ct-label">Shell</span>
        <select v-model="shell" class="ct-input ct-select" aria-label="Shell">
          <option v-for="s in allowedShells" :key="s" :value="s">{{ s }}</option>
        </select>
      </label>

      <div class="ct-actions">
        <button
          class="ct-btn ct-btn--primary"
          type="button"
          :disabled="!containerValid() || connState === 'connecting'"
          @click="connect"
        >
          {{ connState === 'connecting' ? '连接中…' : connState === 'connected' ? '重连' : '进入终端' }}
        </button>
        <button
          class="ct-btn ct-btn--ghost"
          type="button"
          :disabled="connState !== 'connected'"
          @click="disconnect"
        >
          断开
        </button>
      </div>
    </div>

    <!-- terminal -->
    <div class="term" role="region" :aria-label="`${serverName ?? '服务器'} 容器终端`">
      <div class="term-bar">
        <span class="term-dots" aria-hidden="true">
          <span class="term-dot term-dot--r" />
          <span class="term-dot term-dot--y" />
          <span class="term-dot term-dot--g" />
        </span>
        <span class="term-label mono">
          {{ serverName ?? '终端' }}<template v-if="connState === 'connected'"> · {{ containerInput.trim() }}</template>
        </span>
        <span v-if="connState === 'connected'" class="term-live" aria-label="已连接">
          <span class="term-live-dot" aria-hidden="true" />
          LIVE
        </span>
        <span
          v-if="connState === 'error' || connState === 'closed'"
          class="term-status mono"
          :class="{ 'term-status--err': connState === 'error' }"
        >
          {{ connState === 'error' ? '连接失败' : '已断开' }}
        </span>
      </div>

      <!-- xterm mounts here -->
      <div ref="termHost" class="term-host" tabindex="0" aria-label="容器交互终端"></div>

      <div v-if="connState === 'idle'" class="term-hint mono">
        填写容器 ID,选择 shell,点「进入终端」开始交互。
      </div>
      <div v-else-if="statusMsg && connState !== 'connected'" class="term-hint term-hint--err mono">
        {{ statusMsg }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.cterm {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

/* ─── controls(参照 ServiceLogViewer.vue) ─────────────────────────────────── */
.ct-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 12px;
}

.ct-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.ct-field--grow {
  flex: 1 1 280px;
  min-width: 220px;
}

.ct-field--shell {
  width: 130px;
}

.ct-label {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--color-faint);
}

.ct-input {
  height: 38px;
  padding: 0 11px;
  background: var(--color-card-2);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-text);
  font-size: 0.84rem;
  transition: border-color var(--duration-fast);
}

.ct-input:focus {
  outline: none;
  border-color: var(--color-primary);
}

.ct-select {
  cursor: pointer;
}

.ct-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.ct-btn {
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

.ct-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  transform: translateY(-1px);
}

.ct-btn:active:not(:disabled) {
  transform: translateY(0);
}

.ct-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ct-btn--primary {
  color: var(--color-primary);
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

.ct-btn--ghost {
  background: transparent;
}

.ct-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* ─── terminal(纯黑 + 内阴影 + mac 三灯,UX-DR6) ─────────────────────────── */
.term {
  position: relative;
  display: flex;
  flex-direction: column;
  background: var(--color-term);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-lg);
  overflow: hidden;
  box-shadow: var(--shadow-inner);
  min-height: 360px;
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
.term-dot--r {
  background: oklch(63% 0.2 25);
}
.term-dot--y {
  background: oklch(80% 0.15 80);
}
.term-dot--g {
  background: oklch(72% 0.16 150);
}

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
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}

.term-status {
  margin-left: auto;
  font-size: 0.68rem;
  color: var(--color-line-num);
}
.term-status--err {
  color: var(--color-red);
}

.term-host {
  flex: 1;
  min-height: 320px;
  padding: 8px 10px;
  overflow: hidden;
}

.term-host:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}

.term-hint {
  position: absolute;
  left: 14px;
  bottom: 12px;
  right: 14px;
  font-size: 0.76rem;
  color: var(--color-faint);
  pointer-events: none;
}
.term-hint--err {
  color: var(--color-red);
}

@media (prefers-reduced-motion: reduce) {
  .term-live-dot {
    animation: none;
  }
  .ct-btn:hover {
    transform: none;
  }
}
</style>

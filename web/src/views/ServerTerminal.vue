<!--
  ServerTerminal.vue — AI 运维终端(独立全屏页;对标阿里云 Cloud Shell + 运维助手)。

  左终端 / 右 AI 助手双栏(shell 外全屏,需鉴权,进出经路由 /servers/:id/terminal?container=&shell=)。
  · 终端复用 ContainerTerminal 的 xterm + WebSocket(openContainerTerminal:WS ↔ SSH → docker exec)。
  · 补齐**完整终端交互**(原弹窗缺口):选中即复制、⌘/Ctrl+C(有选区复制 / 无选区 SIGINT)、
    ⌘/Ctrl+V·Ctrl+Shift+V 粘贴、右键菜单(复制 / 粘贴 / 全选 / 清屏)、选区高亮对齐主题。
  · 右栏 AI 助手:中文 → 命令卡;插入 = 写终端输入行,执行 = 发 PTY(danger 二次确认在面板内)。

  设计照 demos/ai-terminal-demo.html:Mission-control / 工程师驾驶舱,cyan=智能、JetBrains Mono 主角、
  氛围辉光 + 栅格、设计过的 hover/focus/active。安全上下文(localhost/https)才有剪贴板,失败优雅降级。
-->
<script setup lang="ts">
import { ref, shallowRef, computed, watch, onMounted, onBeforeUnmount, nextTick, reactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { setDocumentTitle } from '../router'
import {
  listServers,
  openServerTerminal,
  type Server,
  type TerminalConnection,
  type TerminalHandlers,
  type TerminalShell,
} from '../api/servers'
import AiOpsPanel from '../components/ops/AiOpsPanel.vue'
import { completeCommand } from '../api/aiOps'

import type { Terminal as XTerm } from '@xterm/xterm'
import type { FitAddon as XFitAddon } from '@xterm/addon-fit'

const route = useRoute()
const router = useRouter()

const serverId = computed(() => String(route.params.id ?? ''))
const allowedShells: TerminalShell[] = ['/bin/sh', '/bin/bash', '/bin/ash', '/bin/zsh', 'sh', 'bash']

// ─── session controls(顶栏会话段) ───────────────────────────────────────────────
// 终端目标 = 服务器**主机 shell**(SSH 直起登录 shell)。进容器留给用户在 shell 里自己
// `docker exec` 自由探索 —— 不把终端绑死到容器(很多服务器根本没 docker)。
const server = ref<Server | null>(null)
const shell = ref<TerminalShell>(
  allowedShells.includes(route.query.shell as TerminalShell) ? (route.query.shell as TerminalShell) : '/bin/sh',
)

type ConnState = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'
const connState = ref<ConnState>('idle')
const statusMsg = ref('')
const latencyMs = ref<number | null>(null)

const hostLabel = computed(() =>
  server.value ? `${server.value.user}@${server.value.host}:${server.value.port}` : serverId.value,
)
const serverName = computed(() => server.value?.name ?? '服务器')

// 标签页标题随服务器名 + 连接状态细化(终端常在新标签页打开:多开时一眼区分是哪台机,
// 且后台挂着的 tab 掉线时标题前缀能直接看出来,不用切过去)。router 的 afterEach 先兜底
// 成「运维终端」,这里在服务器名 / 连接态变化时覆写。
watch(
  [serverName, connState],
  ([name, st]) => {
    const prefix = st === 'connected' ? '' : st === 'connecting' ? '连接中 · ' : '⚠ 已断开 · '
    setDocumentTitle(`${prefix}${name} · 运维终端`)
  },
  { immediate: true },
)

const aiContext = computed(() => ({
  os: 'linux',
  shell: shell.value,
  container: '(宿主机)',
}))

// ─── AI 助手折叠态 ────────────────────────────────────────────────────────────────
// 终端是主角,AI 助手是辅助:默认收起让终端占满,点右侧「✦ AI 助手」一键滑出侧栏。
// 记忆上次开合(localStorage);切换后 refit 让 xterm 适配新宽度。
const AI_OPEN_KEY = 'pw-terminal-ai-open'
function readAiOpen(): boolean {
  try {
    return localStorage.getItem(AI_OPEN_KEY) === '1'
  } catch {
    return false
  }
}
const aiOpen = ref(readAiOpen())
function setAiOpen(open: boolean): void {
  aiOpen.value = open
  try {
    localStorage.setItem(AI_OPEN_KEY, open ? '1' : '0')
  } catch {
    /* 隐私模式 / 配额:退化为仅内存态 */
  }
  // 宽度变了,等布局稳定后让终端重新 fit。
  void nextTick(() => refit())
}
function toggleAi(): void {
  setAiOpen(!aiOpen.value)
}

// ─── P2 智能补全:输入上方建议条 + Tab 接受 ─────────────────────────────────────────
// 终端是裸 PTY,无现成「当前输入行」模型 —— 用 onData 跟踪用户键入(printable/退格/Enter/
// Ctrl-C 重置)近似维护 lineBuffer;停顿 debounce 后先本地常用命令字典即时兜底,再 AI 增强。
// 建议以「建议条」形式悬于输入上方(不动终端显示,稳健),Tab 接受发后缀到 PTY,Esc 忽略。
const COMPLETE_KEY = 'pw-terminal-complete'
function readComplete(): boolean {
  try {
    return localStorage.getItem(COMPLETE_KEY) !== '0' // 默认开
  } catch {
    return true
  }
}
const completeEnabled = ref(readComplete())
function toggleComplete(): void {
  completeEnabled.value = !completeEnabled.value
  try {
    localStorage.setItem(COMPLETE_KEY, completeEnabled.value ? '1' : '0')
  } catch {
    /* ignore */
  }
  if (!completeEnabled.value) clearSuggestion()
}

let lineBuffer = ''
const suggestion = ref('') // 补全后的完整命令(以 lineBuffer 开头);空=无建议
const suggestionSuffix = computed(() =>
  suggestion.value.startsWith(lineBuffer) ? suggestion.value.slice(lineBuffer.length) : '',
)
let completeTimer: number | null = null
let completeAbort: AbortController | null = null

// 光标像素坐标(把内联 ghost 叠层钉到光标格,像 IDE/zsh 接着打的字)。
const caret = ref<{ left: number; top: number; cellH: number } | null>(null)
function updateCaret(): void {
  const t = term.value
  const host = termHost.value
  if (!t || !host) {
    caret.value = null
    return
  }
  const screen = host.querySelector('.xterm-screen') as HTMLElement | null
  const rect = (screen ?? host).getBoundingClientRect()
  const cols = t.cols || 80
  const rows = t.rows || 24
  const cellW = rect.width / cols
  const cellH = rect.height / rows
  const buf = t.buffer.active
  caret.value = {
    left: rect.left + buf.cursorX * cellW,
    top: rect.top + buf.cursorY * cellH,
    cellH,
  }
}
function showSuggestion(full: string): void {
  if (!full.startsWith(lineBuffer) || full === lineBuffer) return
  suggestion.value = full
  updateCaret()
}

// 本地常用运维命令字典(即时兜底 + AI 未配时仍可用)。
const COMMON_COMMANDS = [
  'ls -la', 'ls -lah', 'cd ..', 'df -h', 'du -sh *', 'du -ah . | sort -rh | head -20',
  'ps aux', 'ps aux --sort=-%mem | head', 'top -b -n1 | head -20', 'free -m',
  'tail -f ', 'cat ', 'grep -rn ', 'find . -name ', 'mkdir -p ', 'rm -rf ',
  'docker ps', 'docker ps -a', 'docker logs -f ', 'docker exec -it ',
  'systemctl status ', 'systemctl restart ', 'journalctl -u ',
  'netstat -tlnp', 'ss -tlnp', 'lsof -i :', 'curl -sS ',
  'kubectl get pods', 'kubectl logs -f ', 'uname -a', 'whoami', 'env',
]
function localComplete(partial: string): string {
  return COMMON_COMMANDS.find((c) => c.startsWith(partial) && c !== partial) ?? ''
}

function clearSuggestion(): void {
  suggestion.value = ''
  caret.value = null
  completeAbort?.abort()
  completeAbort = null
}

// 全屏 TUI 程序(vim/top/less 等)会切到备用屏缓冲;此时不做命令补全(否则在 vim 里
// 弹 shell 命令灰字就错了)。仅在普通 shell(normal buffer)给补全。
function inAltScreen(): boolean {
  return term.value?.buffer.active.type === 'alternate'
}

function scheduleComplete(): void {
  if (completeTimer) window.clearTimeout(completeTimer)
  if (!completeEnabled.value) return
  if (inAltScreen()) {
    clearSuggestion()
    return
  }
  const snapshot = lineBuffer
  completeTimer = window.setTimeout(() => void fetchSuggestion(snapshot), 450)
}

async function fetchSuggestion(partial: string): Promise<void> {
  if (!completeEnabled.value || connState.value !== 'connected' || inAltScreen()) {
    clearSuggestion()
    return
  }
  if (partial !== lineBuffer || partial.trim().length < 2) {
    clearSuggestion()
    return
  }
  // 即时本地兜底。
  const local = localComplete(partial)
  if (local) showSuggestion(local)
  // AI 增强(可中断;未配则静默)。
  completeAbort?.abort()
  completeAbort = new AbortController()
  try {
    const res = await completeCommand(partial, aiContext.value, completeAbort.signal)
    if (
      partial === lineBuffer &&
      res.available &&
      res.completion &&
      res.completion.startsWith(partial) &&
      res.completion !== partial
    ) {
      showSuggestion(res.completion)
    }
  } catch {
    /* 中断 / 失败:保留本地兜底建议 */
  }
}

function acceptSuggestion(): void {
  const suffix = suggestionSuffix.value
  if (!suffix) return
  conn.value?.send(suffix)
  lineBuffer = suggestion.value
  clearSuggestion()
  term.value?.focus()
}

// onData 跟踪输入行 + 转发到 PTY。
function onTermData(data: string): void {
  conn.value?.send(data)
  if (data === '\r' || data === '\n') {
    lineBuffer = ''
    clearSuggestion()
  } else if (data === '\x7f' || data === '\b') {
    lineBuffer = lineBuffer.slice(0, -1)
    scheduleComplete()
  } else if (data === '\x03' || data === '\x15' || data === '\x1b' || data === '\t') {
    lineBuffer = ''
    clearSuggestion()
  } else if (data.charCodeAt(0) >= 0x20 && !data.startsWith('\x1b')) {
    lineBuffer += data
    scheduleComplete()
  } else {
    clearSuggestion()
  }
}

// ─── xterm + ws wiring(复用 ContainerTerminal 思路) ─────────────────────────────
const termHost = ref<HTMLElement | null>(null)
const term = shallowRef<XTerm | null>(null)
const fitAddon = shallowRef<XFitAddon | null>(null)
const conn = shallowRef<TerminalConnection | null>(null)
let resizeObserver: ResizeObserver | null = null
const decoder = new TextDecoder()

/** 剪贴板是否可用(安全上下文:localhost / https)。 */
const clipboardOK = typeof navigator !== 'undefined' && !!navigator.clipboard && window.isSecureContext

// 终端字体常量:xterm 渲染在 canvas 上,CSS var() 无法解析 → 必须用字面字体栈。
// 同一份值复用于 document.fonts.load(预热)与 .caret-ghost(行内补全)。
const TERM_FONT_SIZE = 13
const TERM_FONT_FAMILY = '"JetBrains Mono", ui-monospace, "SF Mono", Menlo, Consolas, monospace'

async function ensureTerm(): Promise<XTerm> {
  if (term.value) return term.value
  const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')])
  await import('@xterm/xterm/css/xterm.css')

  const t = new Terminal({
    cursorBlink: true,
    fontFamily: TERM_FONT_FAMILY,
    fontSize: TERM_FONT_SIZE,
    lineHeight: 1.15,
    letterSpacing: 0,
    convertEol: false,
    scrollback: 8000,
    theme: {
      background: '#08080b',
      foreground: '#e6e6ea',
      cursor: '#7fe3f0',
      // 选区高亮对齐主题(cyan,与命令卡 / 智能强调同源)。
      selectionBackground: 'rgba(120, 225, 240, 0.30)',
      selectionForeground: '#d6f6fb',
    },
  })
  const fit = new FitAddon()
  t.loadAddon(fit)

  // 键盘:⌘/Ctrl+C(有选区复制 / 无选区透传 SIGINT)、Ctrl+Shift+C 复制、
  // ⌘/Ctrl+V·Ctrl+Shift+V 粘贴。返回 false = 吞掉不发 PTY;true = 交终端处理。
  t.attachCustomKeyEventHandler((e: KeyboardEvent): boolean => {
    if (e.type !== 'keydown') return true
    // Tab:有 AI 建议时接受(发后缀,吞掉 Tab);无建议则透传给 shell 自带补全。
    // 必须 preventDefault,否则浏览器原生 Tab 会把焦点移走(终端失焦,光标变空心)。
    if (e.key === 'Tab' && !e.metaKey && !e.ctrlKey && !e.altKey && suggestionSuffix.value) {
      e.preventDefault()
      acceptSuggestion()
      return false
    }
    // Esc:有建议时仅忽略建议(吞掉);无建议则透传(vi 模式等)。
    if (e.key === 'Escape' && suggestion.value) {
      e.preventDefault()
      clearSuggestion()
      return false
    }
    const mod = e.metaKey || e.ctrlKey
    const key = e.key.toLowerCase()
    if (mod && key === 'c') {
      if (e.shiftKey) {
        void copySelection()
        return false
      }
      if (t.hasSelection()) {
        void copySelection()
        return false
      }
      // 无选区:仅 Ctrl(非 Cmd)透传 ^C(SIGINT);Cmd+C 无选区什么也不做。
      if (e.ctrlKey && !e.metaKey) {
        flashToast('sig', '已发送 SIGINT', '中断当前命令')
        return true
      }
      return false
    }
    if (mod && key === 'v') {
      void pasteFromClipboard()
      return false
    }
    return true
  })

  // xterm 在 canvas 上一次性测量单元格尺寸;JetBrains Mono 由 @fontsource 异步(font-display:
  // swap)加载,若 open() 时字体未就绪会按 fallback(偏窄/偏矮)测量、算出偏多的行数,字体到位
  // 后单元格变高 → 末行溢出到底部状态栏后面被挡。故先等该等宽字体真正加载完再 open + fit,
  // 保证首测就用正确字体度量。本地 woff2,通常已缓存、瞬时 resolve。
  if (typeof document !== 'undefined' && document.fonts?.load) {
    try {
      await document.fonts.load(`${TERM_FONT_SIZE}px "JetBrains Mono"`)
    } catch {
      /* 字体加载失败则退回 fallback 度量,不阻塞终端 */
    }
  }
  await nextTick()
  // term.value / fitAddon.value 先就位,好让 refit() 在 open 后立即可用。
  term.value = t
  fitAddon.value = fit
  if (termHost.value) {
    t.open(termHost.value)
    refit()
    // 兜底:字体即便已 load,canvas 字符图集仍可能缓存了首次 fallback 测量。改一下 fontSize
    // 触发 xterm 重新测量单元格,再 refit 校正行数(双写确保值变化被监听到)。
    if (typeof document !== 'undefined' && document.fonts?.ready) {
      void document.fonts.ready.then(() => {
        try {
          t.options.fontSize = TERM_FONT_SIZE + 1
          t.options.fontSize = TERM_FONT_SIZE
          refit()
        } catch {
          /* host 尚未布局完成,忽略 */
        }
      })
    }
  }
  // 键入 → 跟踪输入行(智能补全)+ 转发到容器 stdin。
  t.onData(onTermData)

  return t
}

/**
 * 适配终端尺寸。FitAddon 按「容器高 ÷ 单元格高」算行数,但单元格高的度量(canvas 字体测量 +
 * sub-pixel 取整)偶尔偏小,会多算半行~一行 —— 多出来的末行 .xterm-screen 溢出 .term-host 底边,
 * 落到下方状态栏(.term-status)后面被遮住一半。FitAddon 自身不感知这点。
 * 故 fit 之后再按真实渲染几何兜底:若 .xterm-screen 底边超出 host 内容区(host 底边 − padding-bottom),
 * 逐行 resize 收敛,直到终端完全容纳在自己的容器里、绝不与状态栏重叠。
 */
function refit(): void {
  const fit = fitAddon.value
  const t = term.value
  const host = termHost.value
  if (!fit || !t || !host) return
  try {
    fit.fit()
    const screen = host.querySelector('.xterm-screen') as HTMLElement | null
    if (screen) {
      const padBottom = parseFloat(getComputedStyle(host).paddingBottom) || 0
      let guard = 4
      while (guard-- > 0 && t.rows > 1) {
        const limit = host.getBoundingClientRect().bottom - padBottom
        if (screen.getBoundingClientRect().bottom <= limit + 0.5) break
        t.resize(t.cols, t.rows - 1)
      }
    }
    conn.value?.resize(t.cols, t.rows)
  } catch {
    /* host not laid out yet */
  }
}

/**
 * 连接成功后注入的提示符美化脚本(随 onOpen 发到 PTY)。
 * 远端 shell 的 PS1(默认 `sh-5.1#`)是原样字节,前端无法重新着色 —— 只能让 shell 自己发
 * 带 ANSI 的提示符。这里把 PS1 换成 `[user@host dir]$`:方括号包裹 + 主题青色(与光标/选区
 * 同源),末尾重置颜色,使随后键入的命令保持默认色,提示符与命令/输出一眼可分。
 *   · `\[ \]` 是 bash readline 的非打印标记(正确计算行宽),故仅在 bash 下设置;
 *     dash/zsh 等不支持会显示成乱码,因此用 $BASH_VERSION 守卫,非 bash 保持原样只 clear。
 *   · 颜色 38;2;127;227;240 = #7fe3f0,与终端 cursor / selection 的青色一致。
 */
function promptInitScript(): string {
  const ps1 = "\\[\\e[38;2;127;227;240m\\][\\u@\\h \\W]\\$\\[\\e[0m\\] "
  return `if [ -n "$BASH_VERSION" ]; then export PS1='${ps1}'; fi; clear\n`
}

async function connect(): Promise<void> {
  disconnect()

  const t = await ensureTerm()
  // reset() 而非 clear():clear 会保留当前 prompt 行,重连时新会话 prompt 接在旧
  // 「sh-5.1# 」后面叠成「sh-5.1# sh-5.1# …」;reset 彻底清空缓冲区+光标归位。
  t.reset()
  t.focus()
  connState.value = 'connecting'
  statusMsg.value = `正在连接主机 ${hostLabel.value} …`
  const startedAt = performance.now()

  const handlers: TerminalHandlers = {
      onOpen() {
        connState.value = 'connected'
        statusMsg.value = ''
        latencyMs.value = Math.round(performance.now() - startedAt)
        refit()
        t.focus()
        // 注入更易读的提示符:[user@host dir]$ 形式 + 主题青色,与命令/输出区分。
        conn.value?.send(promptInitScript())
      },
      onData(chunk) {
        t.write(decoder.decode(chunk, { stream: true }))
      },
      onClose(reason) {
        if (connState.value === 'connecting') {
          connState.value = 'error'
          statusMsg.value = reason || '连接失败,请检查登录状态、服务器可达性或容器是否存在'
        } else {
          connState.value = 'closed'
          statusMsg.value = reason || '终端会话已结束'
        }
        t.write('\r\n\x1b[2m── ' + (statusMsg.value || '会话结束') + ' ──\x1b[0m\r\n')
      },
  }

  conn.value = openServerTerminal(serverId.value, handlers, shell.value)

  if (!resizeObserver && termHost.value) {
    resizeObserver = new ResizeObserver(() => refit())
    resizeObserver.observe(termHost.value)
  }
}

function disconnect(): void {
  conn.value?.close()
  conn.value = null
  lineBuffer = ''
  clearSuggestion()
  if (connState.value === 'connected' || connState.value === 'connecting') {
    connState.value = 'closed'
  }
}

function closePage(): void {
  disconnect()
  router.push({ name: 'settings-servers' })
}

// ─── 剪贴板 / 选中复制 / 右键菜单 ─────────────────────────────────────────────────
async function copySelection(): Promise<void> {
  const t = term.value
  if (!t) return
  const sel = t.getSelection()
  if (!sel) return
  if (clipboardOK) {
    try {
      await navigator.clipboard.writeText(sel)
    } catch {
      /* 用户可能拒绝权限;静默降级 */
    }
  }
  flashToast('copy', '已复制', `${sel.length} 字符`)
}

async function pasteFromClipboard(): Promise<void> {
  if (!clipboardOK) {
    flashToast('paste', '无法读取剪贴板', '需 https / localhost 安全上下文')
    return
  }
  let text = ''
  try {
    text = await navigator.clipboard.readText()
  } catch {
    flashToast('paste', '剪贴板读取被拒', '请在浏览器允许剪贴板权限')
    return
  }
  if (!text) return
  conn.value?.send(text)
  if (!text.includes('\n')) lineBuffer += text
  clearSuggestion()
  flashToast('paste', '已粘贴', `${text.length} 字符`)
}

// copyOnSelect:在终端里选完(mouseup)即复制当前选区。
function onTermMouseUp(): void {
  const t = term.value
  if (t && t.hasSelection()) void copySelection()
}

// 右键菜单
const ctxMenu = reactive({ open: false, x: 0, y: 0, hasSelection: false })
function onTermContextMenu(e: MouseEvent): void {
  e.preventDefault()
  ctxMenu.hasSelection = !!term.value?.hasSelection()
  ctxMenu.x = Math.min(e.clientX, window.innerWidth - 200)
  ctxMenu.y = Math.min(e.clientY, window.innerHeight - 200)
  ctxMenu.open = true
}
function closeCtxMenu(): void {
  ctxMenu.open = false
}
function ctxCopy(): void {
  void copySelection()
  closeCtxMenu()
}
function ctxPaste(): void {
  void pasteFromClipboard()
  closeCtxMenu()
}
function ctxSelectAll(): void {
  term.value?.selectAll()
  closeCtxMenu()
}
function ctxClear(): void {
  term.value?.clear()
  closeCtxMenu()
}

// ─── AI 助手 → 终端 ──────────────────────────────────────────────────────────────
function onInsertCommand(cmd: string): void {
  // 写进终端输入行(不回车),让用户编辑后自行执行。
  conn.value?.send(cmd)
  lineBuffer += cmd // 同步输入行模型(补全)
  clearSuggestion()
  term.value?.focus()
}
function onExecuteCommand(cmd: string): void {
  // 执行:发 PTY + 回车(danger 已在面板内二次确认)。
  conn.value?.send(cmd + '\r')
  lineBuffer = ''
  clearSuggestion()
  term.value?.focus()
}

// ─── toasts(轻量反馈,照 demo) ──────────────────────────────────────────────────
type ToastKind = 'copy' | 'paste' | 'sig'
interface Toast {
  id: number
  kind: ToastKind
  msg: string
  sub: string
}
const toasts = ref<Toast[]>([])
let toastSeq = 0
const toastIcon: Record<ToastKind, string> = { copy: '⧉', paste: '⇲', sig: '^C' }
function flashToast(kind: ToastKind, msg: string, sub = ''): void {
  const id = ++toastSeq
  toasts.value.push({ id, kind, msg, sub })
  window.setTimeout(() => {
    const i = toasts.value.findIndex((x) => x.id === id)
    if (i >= 0) toasts.value.splice(i, 1)
  }, 1600)
}

// ─── lifecycle ───────────────────────────────────────────────────────────────────
onMounted(async () => {
  // 取服务器信息(顶栏 host 显示);失败不致命(host 退化为 id)。
  try {
    const list = await listServers()
    server.value = list.find((s) => s.id === serverId.value) ?? null
  } catch {
    /* 列表失败:host 退化为 id,终端仍可连 */
  }
  await ensureTerm()
  document.addEventListener('click', closeCtxMenu)
  // 主机 shell:打开即连。
  void connect()
})

onBeforeUnmount(() => {
  document.removeEventListener('click', closeCtxMenu)
  if (completeTimer) window.clearTimeout(completeTimer)
  resizeObserver?.disconnect()
  resizeObserver = null
  disconnect()
  term.value?.dispose()
  term.value = null
})
</script>

<template>
  <!-- data-theme="dark":运维终端是刻意的深色驾驶舱面,固定深色 token,不随全局浅/深主题翻转。 -->
  <div class="shell" data-theme="dark">
    <!-- 顶栏:驾驶舱状态条 -->
    <header class="top">
      <div class="brand"><span class="logo">p&gt;</span> Pipewright</div>
      <span class="crumb">运维终端 · <b>{{ serverName }}</b></span>
      <span class="grow" />

      <div class="seg">
        <div class="cell"><span class="k">主机</span><span class="v">{{ hostLabel }}</span></div>
        <div class="cell">
          <span class="k">Shell</span>
          <select v-model="shell" class="seg-select" aria-label="Shell">
            <option v-for="s in allowedShells" :key="s" :value="s">{{ s }}</option>
          </select>
        </div>
      </div>

      <span v-if="connState === 'connected'" class="live"><span class="dot" /> LIVE</span>
      <span v-else-if="connState === 'connecting'" class="conn-state">连接中…</span>
      <span v-else-if="connState === 'error'" class="conn-state err">连接失败</span>

      <button class="tbtn" type="button" :disabled="connState === 'connecting'" @click="connect">
        {{ connState === 'connected' ? '重连' : '连接' }}
      </button>
      <button class="tbtn danger" type="button" :disabled="connState !== 'connected'" @click="disconnect">断开</button>
      <button class="tbtn" type="button" @click="closePage">关闭</button>
    </header>

    <div class="main" :class="{ 'ai-open': aiOpen }">
      <!-- 终端 -->
      <section class="term-wrap">
        <div class="term-bar">
          <span class="tdot r" /><span class="tdot y" /><span class="tdot g" />
          <span class="name">{{ serverName }} · <b>主机 shell</b></span>
        </div>

        <div
          ref="termHost"
          class="term-host"
          tabindex="0"
          aria-label="服务器交互终端"
          @mouseup="onTermMouseUp"
          @contextmenu="onTermContextMenu"
        />

        <div v-if="connState === 'idle'" class="term-hint">点「连接」进入主机 shell。</div>
        <div v-else-if="statusMsg && connState !== 'connected'" class="term-hint err">{{ statusMsg }}</div>

        <!-- 终端状态行 -->
        <div class="term-status">
          <span class="s">⟢ <b>{{ serverName }}</b></span>
          <span v-if="latencyMs !== null && connState === 'connected'" class="s">延迟 <b>{{ latencyMs }}ms</b></span>
          <span class="s"><kbd>⌘C</kbd> 复制</span>
          <span class="s"><kbd>⌘V</kbd> 粘贴</span>
          <span class="s"><kbd>^C</kbd> 中断</span>
          <span class="s">右键菜单</span>
          <span v-if="!clipboardOK" class="s warn">剪贴板需 https/localhost</span>
          <button
            class="ai-toggle"
            :class="{ on: completeEnabled }"
            type="button"
            :title="completeEnabled ? '关闭 AI 补全' : '开启 AI 补全'"
            @click="toggleComplete"
          >
            <span class="d" aria-hidden="true" /> AI 补全 {{ completeEnabled ? '开' : '关' }}
          </button>
        </div>
      </section>

      <!-- AI 运维助手(可折叠侧栏;v-show 保留对话不丢) -->
      <AiOpsPanel
        v-show="aiOpen"
        :context="aiContext"
        @insert="onInsertCommand"
        @execute="onExecuteCommand"
        @collapse="setAiOpen(false)"
      />
    </div>

    <!-- 收起态:右侧边缘「✦ AI 助手」唤起 tab(终端占满,需要时一键滑出) -->
    <button v-if="!aiOpen" class="ai-launcher" type="button" title="展开 AI 运维助手" @click="toggleAi">
      <span class="ai-launcher-spark" aria-hidden="true">✦</span>
      <span class="ai-launcher-label">AI 助手</span>
    </button>

    <!-- 光标处内联补全 ghost(像 IDE / zsh:接着你打的字补灰字,Tab 接受) -->
    <div
      v-if="caret && suggestionSuffix && connState === 'connected'"
      class="caret-ghost"
      :style="{ left: caret.left + 'px', top: caret.top + 'px', height: caret.cellH + 'px', lineHeight: caret.cellH + 'px' }"
      @click="acceptSuggestion"
    >
      <span class="cg-text">{{ suggestionSuffix }}</span>
      <span class="cg-tab">Tab ⇥</span>
    </div>

    <!-- 右键菜单 -->
    <div
      v-if="ctxMenu.open"
      class="ctx"
      :style="{ left: ctxMenu.x + 'px', top: ctxMenu.y + 'px' }"
      @click.stop
    >
      <button type="button" :disabled="!ctxMenu.hasSelection" @click="ctxCopy">复制<span class="kbd">⌘C</span></button>
      <button type="button" @click="ctxPaste">粘贴<span class="kbd">⌘V</span></button>
      <button type="button" @click="ctxSelectAll">全选<span class="kbd">⌘A</span></button>
      <div class="div" />
      <button type="button" @click="ctxClear">清屏</button>
    </div>

    <!-- toasts -->
    <div class="toasts">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind">
        <span class="ic">{{ toastIcon[t.kind] }}</span>
        <span>{{ t.msg }}</span>
        <span v-if="t.sub" class="sub">{{ t.sub }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  position: fixed;
  inset: 0;
  z-index: 30;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: var(--color-text);
  background:
    radial-gradient(1200px 600px at 18% -10%, oklch(66% 0.155 258 / 0.1), transparent 60%),
    radial-gradient(900px 500px at 100% 8%, oklch(82% 0.1 205 / 0.07), transparent 55%),
    var(--color-canvas);
}

/* 顶栏 */
.top {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 0 20px;
  height: 58px;
  flex: none;
  position: relative;
  border-bottom: 1px solid var(--color-border);
  background: linear-gradient(180deg, oklch(17% 0.006 268), oklch(14% 0.004 270));
}
.top::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: -1px;
  height: 1px;
  background: linear-gradient(90deg, transparent, var(--color-primary), var(--color-cyan), transparent);
  opacity: 0.5;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 700;
  font-size: 1.02rem;
  letter-spacing: 0.01em;
}
.brand .logo {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  font-weight: 700;
  color: #fff;
  background: linear-gradient(145deg, oklch(72% 0.15 256), var(--color-primary));
  box-shadow: 0 4px 16px oklch(66% 0.155 258 / 0.5), inset 0 1px 0 oklch(100% 0 0 / 0.25);
}
.crumb {
  color: var(--color-faint);
  font-size: 0.82rem;
}
.crumb b {
  color: var(--color-dim);
  font-weight: 600;
}
.grow {
  flex: 1;
}

/* 会话信息:连体段控件 */
.seg {
  display: flex;
  align-items: stretch;
  background: oklch(12% 0.004 270);
  border: 1px solid var(--color-border-strong);
  border-radius: 11px;
  overflow: hidden;
  box-shadow: inset 0 1px 0 oklch(100% 0 0 / 0.04);
}
.seg .cell {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 13px;
  height: 38px;
  font-size: 0.8rem;
  border-right: 1px solid var(--color-border);
}
.seg .cell:last-child {
  border-right: none;
}
.seg .k {
  color: var(--color-faint);
  font-size: 0.68rem;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.seg .v {
  font-family: var(--font-mono);
  color: var(--color-text);
  font-size: 0.8rem;
}
.seg-input {
  width: 96px;
  background: none;
  border: none;
  color: oklch(90% 0.09 195);
  font: inherit;
  font-family: var(--font-mono);
  font-size: 0.8rem;
  outline: none;
}
.seg-input::placeholder {
  color: var(--color-faint);
}
.seg-select {
  background: none;
  border: none;
  color: var(--color-text);
  font: inherit;
  font-family: var(--font-mono);
  font-size: 0.8rem;
  outline: none;
  cursor: pointer;
}
.seg-select option {
  background: var(--color-card);
  color: var(--color-text);
}

.live {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--color-green);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.live .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-green);
  box-shadow: 0 0 8px var(--color-green);
  animation: pulse 2s infinite;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}
.conn-state {
  font-size: 0.76rem;
  color: var(--color-faint);
}
.conn-state.err {
  color: var(--color-red);
}

.tbtn {
  font: inherit;
  font-size: 0.8rem;
  font-weight: 600;
  border-radius: 9px;
  padding: 8px 14px;
  cursor: pointer;
  border: 1px solid var(--color-border-strong);
  background: oklch(100% 0 0 / 0.03);
  color: var(--color-dim);
  transition: border-color var(--duration-fast), color var(--duration-fast), background var(--duration-fast);
}
.tbtn:hover:not(:disabled) {
  border-color: var(--color-line-strong);
  color: var(--color-text);
  background: oklch(100% 0 0 / 0.06);
}
.tbtn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.tbtn.danger:hover:not(:disabled) {
  border-color: var(--color-red-line);
  color: var(--color-red);
  background: var(--color-red-soft);
}
.tbtn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

/* 主区。默认单列(终端占满,AI 收起);.ai-open 时滑出 AI 侧栏成双列。
   grid-template-rows: minmax(0,1fr) —— 把单行钉死到容器高度,防终端列 xterm 内容把行
   撑高、连带把 AI 列(stretch 等高)的底部(chips/compose)顶出 .shell 的 overflow:hidden 视口。 */
.main {
  flex: 1;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
  min-height: 0;
  transition: grid-template-columns var(--duration-normal, 300ms) var(--ease-out-expo);
}
.main.ai-open {
  grid-template-columns: minmax(0, 1.78fr) minmax(360px, 0.86fr);
}

/* 收起态唤起 tab:右侧边缘竖向 pill,cyan 辉光,点击滑出 AI 助手 */
.ai-launcher {
  position: fixed;
  top: 50%;
  right: 0;
  translate: 0 -50%;
  z-index: 40;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 9px;
  padding: 16px 9px;
  border: 1px solid var(--color-cyan-line);
  border-right: none;
  border-radius: 14px 0 0 14px;
  background: linear-gradient(180deg, var(--color-card), var(--color-canvas));
  color: var(--color-cyan);
  cursor: pointer;
  box-shadow: -8px 0 28px oklch(0% 0 0 / 0.35), inset 0 1px 0 oklch(100% 0 0 / 0.05);
  transition: transform var(--duration-fast) var(--ease-out-expo), box-shadow var(--duration-fast),
    background var(--duration-fast);
}
.ai-launcher:hover {
  transform: translateX(-2px);
  box-shadow: -10px 0 32px var(--color-cyan-line);
  background: linear-gradient(180deg, var(--color-card-2), var(--color-card));
}
.ai-launcher:focus-visible {
  outline: 2px solid var(--color-cyan);
  outline-offset: 2px;
}
.ai-launcher-spark {
  font-size: 1.1rem;
  filter: drop-shadow(0 0 6px var(--color-cyan-line));
}
.ai-launcher-label {
  writing-mode: vertical-rl;
  font-size: 0.74rem;
  font-weight: 600;
  letter-spacing: 0.12em;
}

/* 终端面板(最深层) */
.term-wrap {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  position: relative;
  border-right: 1px solid var(--color-border);
  background: radial-gradient(900px 420px at 30% -5%, oklch(66% 0.155 258 / 0.06), transparent 55%), #08080b;
}
.term-wrap::before {
  content: '';
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0.5;
  background-image: linear-gradient(oklch(100% 0 0 / 0.015) 1px, transparent 1px);
  background-size: 100% 28px;
}
.term-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  height: 40px;
  flex: none;
  border-bottom: 1px solid var(--color-border);
  background: oklch(10% 0.004 270);
  position: relative;
  z-index: 1;
}
.tdot {
  width: 11px;
  height: 11px;
  border-radius: 50%;
  box-shadow: inset 0 0 0 0.5px oklch(0% 0 0 / 0.3);
}
.tdot.r {
  background: #ff5f57;
}
.tdot.y {
  background: #febc2e;
}
.tdot.g {
  background: #28c840;
}
.term-bar .name {
  margin-left: 8px;
  color: var(--color-faint);
  font-family: var(--font-mono);
  font-size: 0.76rem;
}
.term-bar .name b {
  color: oklch(90% 0.09 195);
  font-weight: 500;
}

.term-host {
  flex: 1;
  min-height: 0;
  padding: 14px 16px 6px;
  overflow: hidden;
  position: relative;
  z-index: 1;
  cursor: text;
}
.term-host:focus-visible {
  outline: none;
}

.term-hint {
  position: absolute;
  left: 18px;
  bottom: 46px;
  right: 18px;
  font-size: 0.78rem;
  color: var(--color-faint);
  pointer-events: none;
  z-index: 1;
}
.term-hint.err {
  color: var(--color-red);
}

/* 终端状态行 */
.term-status {
  flex: none;
  display: flex;
  align-items: center;
  gap: 16px;
  height: 32px;
  padding: 0 16px;
  z-index: 1;
  border-top: 1px solid var(--color-border);
  background: oklch(10% 0.004 270);
  color: var(--color-faint);
  font-size: 0.72rem;
  font-family: var(--font-mono);
  overflow: hidden;
}
.term-status .s {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}
.term-status .s.warn {
  color: var(--color-amber);
}

/* AI 补全开关(状态行最右) */
.ai-toggle {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font: inherit;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-faint);
  background: none;
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 2px 8px;
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--duration-fast), border-color var(--duration-fast), background var(--duration-fast);
}
.ai-toggle .d {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-line-num);
}
.ai-toggle.on {
  color: var(--color-cyan);
}
.ai-toggle.on .d {
  background: var(--color-cyan);
  box-shadow: 0 0 6px var(--color-cyan);
}
.ai-toggle:hover {
  border-color: var(--color-border-strong);
  background: oklch(100% 0 0 / 0.04);
}

/* 光标处内联补全 ghost(像 IDE / zsh-autosuggestions:灰青 ghost 接着光标延展,Tab 接受) */
.caret-ghost {
  position: fixed;
  z-index: 45;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-family: "JetBrains Mono", ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  font-size: 13px; /* 与 xterm fontSize 对齐 */
  white-space: pre;
  cursor: pointer;
  user-select: none;
  animation: ghost-in 0.12s var(--ease-out-expo);
}
@keyframes ghost-in {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}
.cg-text {
  color: oklch(66% 0.05 215); /* 灰青 ghost,明显区别于已输入的亮字 */
}
.cg-tab {
  font-family: var(--font-sans);
  font-size: 0.58rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  border-radius: 5px;
  padding: 1px 6px;
  align-self: center;
}
.term-status .s b {
  color: var(--color-dim);
  font-weight: 500;
}
.term-status kbd {
  font-family: var(--font-mono);
  background: oklch(100% 0 0 / 0.06);
  border: 1px solid var(--color-border-strong);
  border-radius: 5px;
  padding: 1px 6px;
  color: var(--color-dim);
}

/* 右键菜单 */
.ctx {
  position: fixed;
  z-index: 50;
  min-width: 184px;
  padding: 6px;
  border-radius: 12px;
  background: oklch(18% 0.006 268 / 0.96);
  backdrop-filter: blur(12px);
  border: 1px solid var(--color-border-strong);
  box-shadow: 0 18px 48px oklch(0% 0 0 / 0.5), inset 0 1px 0 oklch(100% 0 0 / 0.05);
  animation: ctxin 0.12s ease-out;
}
@keyframes ctxin {
  from {
    opacity: 0;
    translate: 0 -4px;
  }
  to {
    opacity: 1;
    translate: 0 0;
  }
}
.ctx button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  font: inherit;
  font-size: 0.82rem;
  color: var(--color-dim);
  background: none;
  border: none;
  border-radius: 7px;
  padding: 8px 11px;
  cursor: pointer;
  text-align: left;
  transition: background var(--duration-fast), color var(--duration-fast);
}
.ctx button:hover:not(:disabled) {
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}
.ctx button:disabled {
  color: var(--color-faint);
  opacity: 0.5;
  cursor: default;
}
.ctx button .kbd {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: 0.68rem;
  color: var(--color-faint);
}
.ctx .div {
  height: 1px;
  background: var(--color-border);
  margin: 5px 6px;
}

/* toasts */
.toasts {
  position: fixed;
  bottom: 22px;
  left: 50%;
  translate: -50% 0;
  z-index: 60;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
}
.toast {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 0.82rem;
  font-weight: 500;
  color: var(--color-text);
  background: oklch(20% 0.006 268 / 0.96);
  backdrop-filter: blur(10px);
  border: 1px solid var(--color-border-strong);
  border-radius: 11px;
  padding: 9px 15px;
  box-shadow: 0 12px 36px oklch(0% 0 0 / 0.45);
  animation: tin 0.2s var(--ease-out-expo);
}
.toast .ic {
  width: 18px;
  height: 18px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  font-size: 0.7rem;
  font-weight: 700;
}
.toast.copy .ic {
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}
.toast.paste .ic {
  background: var(--color-primary-soft);
  color: oklch(72% 0.15 256);
}
.toast.sig .ic {
  background: var(--color-red-soft);
  color: var(--color-red);
}
.toast .sub {
  color: var(--color-faint);
  font-size: 0.74rem;
  font-weight: 400;
}
@keyframes tin {
  from {
    opacity: 0;
    translate: -50% 12px;
  }
  to {
    opacity: 1;
    translate: -50% 0;
  }
}

@media (max-width: 880px) {
  /* 窄屏:展开时上下堆叠(终端在上、AI 在下);收起时终端占满。 */
  .main.ai-open {
    grid-template-columns: 1fr;
    grid-template-rows: minmax(0, 1.4fr) minmax(0, 1fr);
  }
  .main.ai-open .term-wrap {
    border-right: none;
    border-bottom: 1px solid var(--color-border);
  }
  .seg .cell .v {
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
}

@media (prefers-reduced-motion: reduce) {
  .live .dot {
    animation: none;
  }
  .ctx,
  .toast {
    animation: none;
  }
}
</style>

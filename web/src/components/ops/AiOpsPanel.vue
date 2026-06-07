<!--
  AiOpsPanel.vue — AI 运维助手侧栏(运维终端 P1 · 对标阿里云 Cloud Shell 运维助手)。

  用中文直接说 → 生成命令卡(命令 + 解释 + 风险徽章 + 操作:解释 / 插入 / 执行)。
  · 风险等级由后端确定性复核(safe|write|danger);**danger 拦截直接执行,强制二次确认**(护城河)。
  · AI 只建议,不自动执行:插入 = 把命令写进终端输入行;执行 = 发 PTY(danger 必先二次确认)。
  · 未配 LLM:优雅降级 —— 提示「去设置配 AI」,终端 + 复制粘贴(P0)照常可用。

  设计照 demos/ai-terminal-demo.html:cyan=智能、命令卡左色条按风险、对话气泡、入场动画。
-->
<script setup lang="ts">
import { ref, computed, nextTick, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'
import {
  suggestCommand,
  explainCommand,
  type CommandContext,
  type CommandRisk,
} from '../../api/aiOps'

const props = defineProps<{
  /** 终端会话上下文(container/shell/os),随命令一起发给 AI。 */
  context: CommandContext
  /** 快捷提示词(默认通用运维;调用方可传领域专属,如容器管理)。 */
  chips?: string[]
}>()

const emit = defineEmits<{
  /** 把命令写进终端输入行(不回车),让用户编辑后自行执行。 */
  (e: 'insert', command: string): void
  /** 执行命令:发 PTY(safe/write 已在面板内二次确认 danger)。 */
  (e: 'execute', command: string): void
  /** 收起侧栏(回到终端占满)。 */
  (e: 'collapse'): void
}>()

const router = useRouter()

// ─── feed 模型 ──────────────────────────────────────────────────────────────────
type FeedItem =
  | { id: number; kind: 'user'; text: string }
  | { id: number; kind: 'typing' }
  | { id: number; kind: 'note'; text: string; tone: 'info' | 'error' | 'degraded' }
  | {
      id: number
      kind: 'card'
      reason: string
      command: string
      risk: CommandRisk
      explanation: string
      explaining: boolean
      confirming: boolean
    }

const feed = ref<FeedItem[]>([])
const input = ref('')
const sending = ref(false)
let seq = 0
const nextId = () => ++seq

const feedHost = useTemplateRef<HTMLElement>('feedHost')
async function scrollToEnd(): Promise<void> {
  await nextTick()
  const el = feedHost.value
  if (el) el.scrollTop = el.scrollHeight
}

const DEFAULT_CHIPS = [
  '磁盘占用 top 10',
  '8080 端口被谁占',
  '实时跟踪应用日志',
  '看 CPU 飙升原因',
  '清理 7 天前的日志',
]
const CHIPS = computed(() => (props.chips && props.chips.length ? props.chips : DEFAULT_CHIPS))

const riskLabel: Record<CommandRisk, string> = {
  safe: '● 只读 · 安全',
  write: '◆ 写操作 · 谨慎',
  danger: '⚠ 高危 · 破坏性',
}

// ─── 发送 → 命令卡 ────────────────────────────────────────────────────────────────
async function send(text?: string): Promise<void> {
  const nl = (text ?? input.value).trim()
  if (!nl || sending.value) return
  input.value = ''
  resizeInput()
  feed.value.push({ id: nextId(), kind: 'user', text: nl })
  const typingId = nextId()
  feed.value.push({ id: typingId, kind: 'typing' })
  sending.value = true
  await scrollToEnd()

  try {
    const res = await suggestCommand(nl, props.context)
    removeItem(typingId)
    if (!res.available) {
      feed.value.push({
        id: nextId(),
        kind: 'note',
        tone: 'degraded',
        text: res.reason || '尚未配置 AI 提供商,去「设置 · AI」配置后即可用中文生成命令。',
      })
    } else if (!res.command) {
      feed.value.push({ id: nextId(), kind: 'note', tone: 'error', text: res.reason || 'AI 未能给出命令,换个说法再试。' })
    } else {
      feed.value.push({
        id: nextId(),
        kind: 'card',
        reason: res.explanation || res.reason,
        command: res.command,
        risk: res.risk,
        explanation: '',
        explaining: false,
        confirming: false,
      })
    }
  } catch {
    removeItem(typingId)
    feed.value.push({ id: nextId(), kind: 'note', tone: 'error', text: '请求失败,请检查网络或登录状态后重试。' })
  } finally {
    sending.value = false
    await scrollToEnd()
  }
}

function removeItem(id: number): void {
  const i = feed.value.findIndex((f) => f.id === id)
  if (i >= 0) feed.value.splice(i, 1)
}

// ─── 命令卡操作 ──────────────────────────────────────────────────────────────────
function onInsert(card: Extract<FeedItem, { kind: 'card' }>): void {
  emit('insert', card.command)
}

function onExecute(card: Extract<FeedItem, { kind: 'card' }>): void {
  if (card.risk === 'danger') {
    // 危险命令护城河:拦截直接执行,要求二次确认。
    card.confirming = true
    return
  }
  emit('execute', card.command)
}

function confirmDanger(card: Extract<FeedItem, { kind: 'card' }>): void {
  card.confirming = false
  emit('execute', card.command)
}

function cancelDanger(card: Extract<FeedItem, { kind: 'card' }>): void {
  card.confirming = false
}

/** 改安全预览:把已知破坏性命令换成只读 / dry-run 形态,插入终端供用户先看清楚。 */
function safePreview(card: Extract<FeedItem, { kind: 'card' }>): void {
  emit('insert', toSafePreview(card.command))
}

function toSafePreview(cmd: string): string {
  if (/\s-delete\b/.test(cmd)) return cmd.replace(/\s-delete\b/, ' -print')
  if (/^\s*rm\s+/.test(cmd)) {
    // rm 的目标改成 ls -la 列出,先确认范围再删。
    return cmd.replace(/^\s*rm\s+(-[a-zA-Z]*\s+)*/, 'ls -la ')
  }
  // 未知破坏性命令:注释掉让用户审阅,不直接可执行。
  return `# 先确认再执行:${cmd}`
}

async function onExplain(card: Extract<FeedItem, { kind: 'card' }>): Promise<void> {
  if (card.explaining || card.explanation) return
  card.explaining = true
  try {
    const res = await explainCommand(card.command, props.context)
    card.explanation = res.available ? res.explanation : res.reason || '未能解释,请稍后再试。'
  } catch {
    card.explanation = '解释请求失败,请重试。'
  } finally {
    card.explaining = false
    await scrollToEnd()
  }
}

// ─── 输入框自适应高度 ─────────────────────────────────────────────────────────────
const inputEl = useTemplateRef<HTMLTextAreaElement>('inputEl')
function resizeInput(): void {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 120) + 'px'
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    void send()
  }
}

function goSettings(): void {
  void router.push({ name: 'settings-ai' })
}
</script>

<template>
  <aside class="ai">
    <header class="ai-head">
      <span class="spark" aria-hidden="true">✦</span>
      <div class="ai-head-text">
        <h2>AI 运维助手</h2>
        <p>用中文直接说 · 生成命令 · 解释 · 拦截危险操作</p>
      </div>
      <button class="ai-collapse" type="button" title="收起" aria-label="收起 AI 助手" @click="emit('collapse')">›</button>
    </header>

    <div ref="feedHost" class="feed">
      <div v-if="feed.length === 0" class="empty">
        <p class="empty-title">说说你想做什么</p>
        <p class="empty-sub">例如「看占内存最多的进程」「8080 端口被谁占」「清理 7 天前的日志」。AI 会生成命令,危险操作自动拦截二次确认。</p>
      </div>

      <template v-for="item in feed" :key="item.id">
        <!-- 用户气泡 -->
        <div v-if="item.kind === 'user'" class="msg u">
          <div class="ubub">{{ item.text }}</div>
        </div>

        <!-- 思考中 -->
        <div v-else-if="item.kind === 'typing'" class="msg a">
          <div class="typing" aria-label="AI 思考中"><i /><i /><i /></div>
        </div>

        <!-- 降级 / 错误提示 -->
        <div v-else-if="item.kind === 'note'" class="msg a">
          <div class="note" :class="`note--${item.tone}`">
            <span>{{ item.text }}</span>
            <button v-if="item.tone === 'degraded'" class="note-cta" type="button" @click="goSettings">去设置配 AI →</button>
          </div>
        </div>

        <!-- 命令卡 -->
        <div v-else class="msg a">
          <div v-if="item.reason" class="reason">{{ item.reason }}</div>
          <div class="cc" :class="item.risk">
            <pre class="code">{{ item.command }}</pre>
            <div v-if="item.explanation" class="explain">{{ item.explanation }}</div>
            <div class="foot">
              <span class="badge" :class="item.risk">{{ riskLabel[item.risk] }}</span>
              <span class="acts">
                <template v-if="item.confirming">
                  <button class="mini" type="button" @click="cancelDanger(item)">取消</button>
                  <button class="mini danger" type="button" @click="confirmDanger(item)">确认执行</button>
                </template>
                <template v-else>
                  <button class="mini" type="button" :disabled="item.explaining" @click="onExplain(item)">
                    {{ item.explaining ? '解释中…' : '解释' }}
                  </button>
                  <button v-if="item.risk === 'danger'" class="mini" type="button" @click="safePreview(item)">改安全预览</button>
                  <button class="mini" type="button" @click="onInsert(item)">插入</button>
                  <button class="mini run" :class="{ danger: item.risk === 'danger' }" type="button" @click="onExecute(item)">
                    执行
                  </button>
                </template>
              </span>
            </div>
          </div>
          <div v-if="item.risk === 'danger'" class="intercept">
            <span class="shield" aria-hidden="true">🛡</span> 已拦截直接执行,需二次确认 ——「危险命令护城河」
          </div>
        </div>
      </template>
    </div>

    <div class="chips" role="list">
      <button v-for="c in CHIPS" :key="c" class="chip" type="button" role="listitem" @click="send(c)">{{ c }}</button>
    </div>

    <div class="compose">
      <div class="box">
        <textarea
          ref="inputEl"
          v-model="input"
          rows="1"
          placeholder="用中文描述你想做的,如:重启服务 / 看磁盘占用 / 解释命令…"
          @input="resizeInput"
          @keydown="onKeydown"
        />
        <button class="send" type="button" title="发送（Enter）" :disabled="sending || !input.trim()" @click="send()">➤</button>
      </div>
    </div>
  </aside>
</template>

<style scoped>
.ai {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  height: 100%;
  overflow: hidden;
  position: relative;
  background: linear-gradient(180deg, var(--color-card), var(--color-canvas));
}
.ai::before {
  content: '';
  position: absolute;
  top: -40px;
  left: 50%;
  translate: -50% 0;
  width: 240px;
  height: 240px;
  background: radial-gradient(circle, var(--color-cyan-soft), transparent 70%);
  pointer-events: none;
  filter: blur(8px);
}

.ai-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 18px;
  flex: none;
  border-bottom: 1px solid var(--color-border);
  position: relative;
  z-index: 1;
}
.spark {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  display: grid;
  place-items: center;
  font-size: 1.05rem;
  color: oklch(18% 0.03 230);
  background: linear-gradient(145deg, oklch(90% 0.09 195), var(--color-cyan));
  box-shadow: 0 4px 18px var(--color-cyan-line), inset 0 1px 0 oklch(100% 0 0 / 0.4);
}
.ai-head-text {
  min-width: 0;
  flex: 1;
}
.ai-head h2 {
  font-size: 1.02rem;
  font-weight: 700;
  letter-spacing: 0.01em;
}
.ai-collapse {
  flex: none;
  width: 30px;
  height: 30px;
  border-radius: 9px;
  border: 1px solid var(--color-border-strong);
  background: oklch(100% 0 0 / 0.03);
  color: var(--color-dim);
  font-size: 1.25rem;
  line-height: 1;
  cursor: pointer;
  display: grid;
  place-items: center;
  transition: border-color var(--duration-fast), color var(--duration-fast), background var(--duration-fast);
}
.ai-collapse:hover {
  border-color: var(--color-cyan-line);
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
}
.ai-head p {
  font-size: 0.72rem;
  color: var(--color-faint);
  margin-top: 2px;
}

.feed {
  flex: 1;
  overflow: auto;
  padding: 18px 16px 8px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  position: relative;
  z-index: 1;
}

.empty {
  margin: auto 0;
  padding: 8px 6px;
  color: var(--color-faint);
}
.empty-title {
  font-size: 0.9rem;
  font-weight: 600;
  color: var(--color-dim);
  margin-bottom: 6px;
}
.empty-sub {
  font-size: 0.78rem;
  line-height: 1.6;
}

.msg {
  display: flex;
  flex-direction: column;
  gap: 8px;
  animation: rise 0.4s var(--ease-out-expo) both;
}
@keyframes rise {
  from {
    opacity: 0;
    translate: 0 10px;
  }
  to {
    opacity: 1;
    translate: 0 0;
  }
}
.msg.u {
  align-items: flex-end;
}
.ubub {
  font-size: 0.84rem;
  line-height: 1.5;
  padding: 9px 14px;
  border-radius: 14px 14px 4px 14px;
  max-width: 90%;
  background: linear-gradient(145deg, oklch(66% 0.155 258 / 0.26), oklch(66% 0.155 258 / 0.14));
  border: 1px solid oklch(66% 0.155 258 / 0.4);
  color: var(--color-text);
  box-shadow: 0 4px 14px oklch(66% 0.155 258 / 0.12);
}
.reason {
  font-size: 0.82rem;
  color: var(--color-dim);
  line-height: 1.65;
  padding-left: 2px;
}

.note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 0.8rem;
  line-height: 1.55;
  padding: 10px 13px;
  border-radius: 11px;
  border: 1px solid var(--color-border-strong);
  background: oklch(100% 0 0 / 0.03);
  color: var(--color-dim);
}
.note--error {
  border-color: var(--color-red-line);
  color: var(--color-red);
  background: var(--color-red-soft);
}
.note--degraded {
  border-color: var(--color-cyan-line);
  background: var(--color-cyan-soft);
}
.note-cta {
  font: inherit;
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-cyan);
  background: none;
  border: none;
  cursor: pointer;
  padding: 0;
}
.note-cta:hover {
  text-decoration: underline;
}

/* 命令卡:按风险用左色条分级 + 浮起 */
.cc {
  position: relative;
  border-radius: 13px;
  overflow: hidden;
  background: var(--color-term);
  border: 1px solid var(--color-border-strong);
  box-shadow: 0 10px 30px oklch(0% 0 0 / 0.35), inset 0 1px 0 oklch(100% 0 0 / 0.04);
}
.cc::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 3px;
  background: var(--color-cyan);
}
.cc.write::before {
  background: var(--color-amber);
}
.cc.danger::before {
  background: var(--color-red);
}
.cc .code {
  font-family: var(--font-mono);
  font-size: 13px;
  color: oklch(90% 0.09 195);
  padding: 13px 15px 13px 17px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
.cc.danger .code {
  color: oklch(88% 0.07 30);
}
.explain {
  font-size: 0.78rem;
  line-height: 1.6;
  color: var(--color-dim);
  padding: 0 15px 12px 17px;
  border-top: 1px dashed var(--color-border);
  padding-top: 10px;
  white-space: pre-wrap;
}
.foot {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 13px 9px 17px;
  border-top: 1px solid var(--color-border);
  background: oklch(9% 0.004 270 / 0.6);
}
.badge {
  font-size: 0.66rem;
  font-weight: 700;
  padding: 3px 9px;
  border-radius: 20px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  letter-spacing: 0.02em;
  white-space: nowrap;
}
.badge.safe {
  background: var(--color-green-soft);
  color: var(--color-green);
}
.badge.write {
  background: var(--color-amber-soft);
  color: var(--color-amber);
}
.badge.danger {
  background: var(--color-red-soft);
  color: var(--color-red);
}
.acts {
  margin-left: auto;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.mini {
  font: inherit;
  font-size: 0.73rem;
  font-weight: 600;
  border-radius: 8px;
  padding: 6px 12px;
  cursor: pointer;
  border: 1px solid var(--color-border-strong);
  background: oklch(100% 0 0 / 0.03);
  color: var(--color-dim);
  transition: border-color var(--duration-fast), color var(--duration-fast), transform var(--duration-fast) var(--ease-out-expo);
}
.mini:hover:not(:disabled) {
  border-color: var(--color-cyan-line);
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  transform: translateY(-1px);
}
.mini:disabled {
  opacity: 0.55;
  cursor: default;
}
.mini.run {
  background: linear-gradient(145deg, oklch(72% 0.15 256), var(--color-primary));
  border-color: transparent;
  color: #fff;
  box-shadow: 0 3px 12px oklch(66% 0.155 258 / 0.4);
}
.mini.run:hover:not(:disabled) {
  filter: brightness(1.08);
  color: #fff;
  background: linear-gradient(145deg, oklch(72% 0.15 256), var(--color-primary));
}
.mini.run.danger,
.mini.danger {
  background: linear-gradient(145deg, oklch(74% 0.2 22), var(--color-red));
  border-color: transparent;
  color: #fff;
  box-shadow: 0 3px 12px var(--color-red-line);
}
.mini.danger:hover:not(:disabled) {
  filter: brightness(1.08);
  color: #fff;
}
.intercept {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 0.73rem;
  color: var(--color-red);
  padding-left: 2px;
}
.shield {
  filter: drop-shadow(0 0 4px var(--color-red-line));
}

.typing {
  display: flex;
  gap: 5px;
  padding: 8px 2px;
}
.typing i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-faint);
  animation: bob 1s infinite;
}
.typing i:nth-child(2) {
  animation-delay: 0.16s;
}
.typing i:nth-child(3) {
  animation-delay: 0.32s;
}
@keyframes bob {
  0%,
  60%,
  100% {
    opacity: 0.3;
    translate: 0 0;
  }
  30% {
    opacity: 1;
    translate: 0 -5px;
  }
}

.chips {
  display: flex;
  gap: 8px;
  padding: 10px 16px;
  overflow-x: auto;
  flex: none;
  scrollbar-width: none;
}
.chips::-webkit-scrollbar {
  display: none;
}
.chip {
  flex: none;
  font: inherit;
  font-size: 0.74rem;
  color: var(--color-dim);
  background: oklch(100% 0 0 / 0.04);
  border: 1px solid var(--color-border-strong);
  border-radius: 20px;
  padding: 7px 13px;
  cursor: pointer;
  transition: border-color var(--duration-fast), color var(--duration-fast), transform var(--duration-fast) var(--ease-out-expo);
  white-space: nowrap;
}
.chip:hover {
  border-color: var(--color-cyan-line);
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  transform: translateY(-1px);
}

.compose {
  flex: none;
  padding: 12px 16px 16px;
  border-top: 1px solid var(--color-border);
  position: relative;
  z-index: 1;
}
.box {
  display: flex;
  gap: 9px;
  align-items: flex-end;
  background: var(--color-term);
  border: 1px solid var(--color-border-strong);
  border-radius: 14px;
  padding: 8px 8px 8px 14px;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.box:focus-within {
  border-color: var(--color-cyan-line);
  box-shadow: 0 0 0 3px var(--color-cyan-soft);
}
.compose textarea {
  flex: 1;
  background: none;
  border: none;
  color: var(--color-text);
  font: inherit;
  font-size: 0.84rem;
  resize: none;
  outline: none;
  min-height: 24px;
  max-height: 120px;
  padding: 6px 0;
  line-height: 1.5;
}
.compose textarea::placeholder {
  color: var(--color-faint);
}
.send {
  width: 38px;
  height: 38px;
  border-radius: 11px;
  border: none;
  cursor: pointer;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  color: oklch(16% 0.03 230);
  background: linear-gradient(145deg, oklch(90% 0.09 195), var(--color-cyan));
  box-shadow: 0 4px 14px var(--color-cyan-line);
  transition: filter var(--duration-fast), transform var(--duration-fast);
}
.send:hover:not(:disabled) {
  filter: brightness(1.08);
  transform: translateY(-1px);
}
.send:disabled {
  opacity: 0.5;
  cursor: default;
}

@media (prefers-reduced-motion: reduce) {
  .msg,
  .typing i {
    animation: none;
  }
  .mini:hover,
  .chip:hover,
  .send:hover {
    transform: none;
  }
}
</style>

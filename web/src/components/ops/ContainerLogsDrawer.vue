<script setup lang="ts">
/*
  ContainerLogsDrawer.vue — 容器日志抽屉(Portainer 式)。
  复用既有 /api/servers/:id/logs(source=docker)历史拉取 + SSE 实时 tail。
  右侧滑入,顶栏显示容器名/短 ID,可切 tail 行数、开/停实时跟随、复制、清屏。
*/
import { ref, watch, nextTick, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { getServerLogs, subscribeServerLogs } from '../../api/servers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  serverId: string
  containerName: string
  containerId?: string
}>()
const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const toast = useToast()

type ViewState = 'idle' | 'loading' | 'loaded' | 'streaming' | 'error'
const viewState = ref<ViewState>('idle')
const errorMsg = ref('')
const lines = ref<string[]>([])
const tailN = ref(200)
const TAIL_OPTIONS = [100, 200, 500, 1000]
const following = ref(false)
const bodyEl = ref<HTMLElement | null>(null)

let unsub: (() => void) | null = null

function stopStream(): void {
  if (unsub) {
    unsub()
    unsub = null
  }
  following.value = false
}

async function scrollToBottom(): Promise<void> {
  await nextTick()
  const el = bodyEl.value
  if (el) el.scrollTop = el.scrollHeight
}

async function loadHistory(): Promise<void> {
  stopStream()
  viewState.value = 'loading'
  errorMsg.value = ''
  try {
    const res = await getServerLogs(props.serverId, {
      source: 'docker',
      target: props.containerName,
      lines: tailN.value,
    })
    if (res.error) {
      viewState.value = 'error'
      errorMsg.value = res.error
      return
    }
    lines.value = res.lines.map((l) => l.text)
    viewState.value = 'loaded'
    void scrollToBottom()
  } catch (err) {
    viewState.value = 'error'
    errorMsg.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? t('opsContainer.logs.loadFailedStatus', { status: err.status }))
        : t('opsContainer.logs.loadFailed')
  }
}

function toggleFollow(): void {
  if (following.value) {
    stopStream()
    viewState.value = 'loaded'
    return
  }
  // 开实时:先保留已有历史,再订阅增量。
  following.value = true
  viewState.value = 'streaming'
  unsub = subscribeServerLogs(
    props.serverId,
    { source: 'docker', target: props.containerName, lines: tailN.value },
    {
      onLine(line) {
        lines.value.push(line)
        // 防爆:实时跟随时只保留最近 5000 行。
        if (lines.value.length > 5000) lines.value.splice(0, lines.value.length - 5000)
        void scrollToBottom()
      },
      onError(message) {
        errorMsg.value = message
        stopStream()
        viewState.value = 'error'
      },
      onTransportError() {
        stopStream()
        // 传输断开不当致命错误:停跟随,保留已拉到的内容。
        viewState.value = 'loaded'
      },
    },
  )
}

async function copyAll(): Promise<void> {
  try {
    await navigator.clipboard.writeText(lines.value.join('\n'))
    toast.success(t('opsContainer.logs.copied'), { detail: t('opsContainer.logs.lineCount', { n: lines.value.length }) })
  } catch {
    toast.error(t('opsContainer.copyFailed'), { detail: t('opsContainer.logs.copyFailedDetail') })
  }
}

function clearView(): void {
  lines.value = []
}

watch(tailN, () => void loadHistory())

// 打开即拉历史(组件随抽屉 v-if 挂载)。
void loadHistory()

onBeforeUnmount(stopStream)
</script>

<template>
  <div class="drawer-scrim" @click.self="emit('close')">
    <aside class="drawer" role="dialog" :aria-label="t('opsContainer.logs.dialogAria')">
      <header class="drawer__head">
        <div class="drawer__title">
          <span class="drawer__name">{{ containerName }}</span>
          <span v-if="containerId" class="drawer__cid mono">{{ containerId }}</span>
          <span class="drawer__src">docker logs</span>
        </div>
        <button class="drawer__close" :aria-label="t('opsContainer.logs.closeAria')" @click="emit('close')">✕</button>
      </header>

      <div class="drawer__toolbar">
        <label class="tool">
          <span class="tool__k">{{ t('opsContainer.logs.lines') }}</span>
          <select v-model.number="tailN" class="tool__sel" :aria-label="t('opsContainer.logs.linesAria')">
            <option v-for="n in TAIL_OPTIONS" :key="n" :value="n">{{ n }}</option>
          </select>
        </label>
        <button class="tool-btn" :disabled="viewState === 'loading'" @click="loadHistory">↻ {{ t('common.refresh') }}</button>
        <button class="tool-btn" :class="{ 'tool-btn--on': following }" @click="toggleFollow">
          {{ following ? `⏸ ${t('opsContainer.logs.stopLive')}` : `▶ ${t('opsContainer.logs.follow')}` }}
        </button>
        <span class="grow" />
        <button class="tool-btn" :disabled="lines.length === 0" @click="copyAll">{{ t('opsContainer.copy') }}</button>
        <button class="tool-btn" :disabled="lines.length === 0" @click="clearView">{{ t('opsContainer.logs.clear') }}</button>
      </div>

      <div ref="bodyEl" class="drawer__body" :class="{ 'is-live': following }">
        <div v-if="viewState === 'loading'" class="drawer__hint">{{ t('opsContainer.logs.fetching') }}</div>
        <div v-else-if="viewState === 'error'" class="drawer__hint drawer__hint--err">⚠ {{ errorMsg }}</div>
        <div v-else-if="lines.length === 0" class="drawer__hint">{{ t('opsContainer.logs.empty') }}</div>
        <pre v-else class="drawer__pre mono"><span v-for="(l, i) in lines" :key="i" class="logline">{{ l }}
</span></pre>
      </div>

      <footer class="drawer__foot">
        <span class="foot__s">{{ t('opsContainer.logs.lineCount', { n: lines.length }) }}</span>
        <span v-if="following" class="foot__live"><span class="foot__dot" /> {{ t('opsContainer.logs.live') }}</span>
        <span class="grow" />
        <span class="foot__hint">{{ t('opsContainer.logs.footHint') }}</span>
      </footer>
    </aside>
  </div>
</template>

<style scoped>
.drawer-scrim {
  position: fixed;
  inset: 0;
  z-index: 500;
  background: oklch(0% 0 0 / 0.42);
  display: flex;
  justify-content: flex-end;
  animation: scrimIn var(--duration-fast) var(--ease-out-expo);
}
@keyframes scrimIn {
  from { opacity: 0; }
  to { opacity: 1; }
}
.drawer {
  width: min(760px, 92vw);
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border-left: 1px solid var(--color-border-strong);
  box-shadow: var(--shadow-modal);
  animation: drawerIn var(--duration-normal) var(--ease-out-expo);
}
@keyframes drawerIn {
  from { transform: translateX(24px); opacity: 0.4; }
  to { transform: translateX(0); opacity: 1; }
}

.drawer__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
}
.drawer__title {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}
.drawer__name {
  font-size: var(--text-section);
  font-weight: 700;
  color: var(--color-text);
}
.drawer__cid {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.drawer__src {
  font-size: var(--text-micro);
  color: var(--color-cyan);
  background: var(--color-cyan-soft);
  border: 1px solid var(--color-cyan-line);
  padding: 1px 7px;
  border-radius: 999px;
}
.drawer__close {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out-expo);
}
.drawer__close:hover {
  color: var(--color-text);
  border-color: var(--color-text);
}

.drawer__toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-card-2);
}
.tool {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.tool__k {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.tool__sel {
  font-size: var(--text-label);
  padding: 3px 6px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
}
.tool-btn {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 5px 11px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out-expo);
}
.tool-btn:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-text);
}
.tool-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.tool-btn--on {
  color: var(--color-green);
  border-color: var(--color-green-line);
  background: var(--color-green-soft);
}
.grow {
  flex: 1;
}

.drawer__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  background: var(--color-term);
  padding: 12px 16px;
}
.drawer__pre {
  margin: 0;
  font-size: var(--text-mono);
  line-height: 1.5;
  color: oklch(88% 0.01 250);
  white-space: pre-wrap;
  word-break: break-word;
}
.logline {
  display: block;
}
.drawer__hint {
  color: var(--color-faint);
  font-size: var(--text-label);
  padding: 8px 2px;
}
.drawer__hint--err {
  color: var(--color-red);
}

.drawer__foot {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 18px;
  border-top: 1px solid var(--color-border);
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.foot__live {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--color-green);
}
.foot__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-green);
  animation: dotPulse 1.6s var(--ease-out-expo) infinite;
}
@keyframes dotPulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
</style>

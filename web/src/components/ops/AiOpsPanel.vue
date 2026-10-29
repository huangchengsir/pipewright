<script setup lang="ts">
import { computed, shallowRef, toRef, onBeforeUnmount, watch } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { History, Plus, X, Refresh } from '@vicons/tabler'
import { useOpsChat } from '../../composables/useOpsChat'
import { useOpsAutoScroll } from '../../composables/useOpsAutoScroll'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { copyText } from '../../utils/clipboard'
import { utf8Bytes } from '../../api/opsChat'
import { onOpsAuthReset, opsAuthEpoch } from '../../utils/opsAuth'
import CopyTextDialog from './CopyTextDialog.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import AiOpsServerPicker from './AiOpsServerPicker.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsConfirmation from './AiOpsConfirmation.vue'
const props = withDefaults(
  defineProps<{
    active?: boolean
    initialServerIds?: string[]
    context?: string
    terminalEpoch?: string
  }>(),
  { active: true, initialServerIds: () => [], context: 'containers' },
)
const emit = defineEmits<{ collapse: [] }>()
const { t } = useI18n(),
  router = useRouter(),
  confirm = useConfirm(),
  toast = useToast()
const chat = useOpsChat({
  active: toRef(props, 'active'),
  initialServerIds: () => props.initialServerIds,
  context: () => props.context,
})
const {
  state,
  capabilities,
  session,
  activeRun,
  draft,
  draftStatus,
  busy,
  loading,
  error,
  pending,
  servers,
  tools,
  confirmations,
  historyCursor,
} = chat
const {
  viewport: messagesViewport,
  content: messagesContent,
  scrollToBottom,
  preserveHistory,
} = useOpsAutoScroll({
  active: toRef(props, 'active'),
  sessionId: () => session.value?.id,
})
watch(
  () => {
    const entry = state.optimistic[state.optimistic.length - 1]
    return entry?.sessionId === session.value?.id ? entry?.requestId : undefined
  },
  (requestId) => {
    if (requestId) void scrollToBottom()
  },
  { flush: 'post' },
)
const historyOpen = shallowRef(false),
  manualCopy = shallowRef<string | null>(null)
let disposed = false
const terminalWaiters=new Set<()=>void>()
const unAuthCopy = onOpsAuthReset(() => {
  manualCopy.value = null
  terminalReference.value = null
  for(const cancel of terminalWaiters)cancel()
})
onBeforeUnmount(() => {
  disposed = true
  for(const cancel of terminalWaiters)cancel()
  manualCopy.value = null
  unAuthCopy()
})
const disabled = computed(
  () => busy.value || loading.value || !capabilities.value?.available,
)
const waiting = computed(() => {
  if (
    busy.value &&
    state.optimistic.some((e) => e.sessionId === session.value?.id)
  )
    return true
  const run = activeRun.value
  if (!run || (run.status !== 'planning' && run.phase !== 'analysis'))
    return false
  return !state.entries.some(
    (e) =>
      e.runId === run.id &&
      !!e.text &&
      (run.phase === 'analysis'
        ? e.kind === 'analysis'
        : e.kind === 'assistant'),
  )
})
const optimistic = computed(() =>
  state.optimistic.filter((e) => e.sessionId === session.value?.id),
)
async function leave(): Promise<boolean> {
  if (await chat.close()) return true
  if (
    !(await confirm.open({
      title: t('opsChat.unsaved'),
      body: t('opsChat.discardBody'),
      confirmLabel: t('opsChat.discard'),
      variant: 'danger',
    }))
  )
    return false
  chat.discard()
  return chat.close()
}
async function close(): Promise<void> {
  if (await leave()) emit('collapse')
}
async function remove(): Promise<void> {
  if (!session.value) return
  const id = session.value.id,
    runId = session.value.activeRunId,
    epoch = opsAuthEpoch()
  const selected = () =>
    !disposed &&
    epoch === opsAuthEpoch() &&
    session.value?.id === id &&
    session.value.activeRunId === runId
  if (
    !(await confirm.open({
      title: t('opsChat.delete'),
      body: t('opsChat.deleteBody'),
      confirmLabel: t('opsChat.delete'),
      variant: 'danger',
    }))
  )
    return
  if (!selected()) return
  if (runId) {
    if (
      !(await confirm.open({
        title: t('opsChat.stop'),
        body: t('opsChat.stopBody'),
        confirmLabel: t('opsChat.stop'),
        variant: 'danger',
      }))
    )
      return
    if (!selected() || !(await chat.stop(runId))) return
    // Delete never assumes cancellation has converged; backend and snapshot decide.
    await chat.refresh()
  }
  if (
    disposed ||
    epoch !== opsAuthEpoch() ||
    session.value?.id !== id ||
    session.value.activeRunId
  )
    return
  await chat.remove()
}
async function stop(): Promise<void> {
  const id = session.value?.id,
    runId = session.value?.activeRunId,
    epoch = opsAuthEpoch()
  if (!id || !runId) return
  if (
    await confirm.open({
      title: t('opsChat.stop'),
      body: t('opsChat.stopBody'),
      confirmLabel: t('opsChat.stop'),
      variant: 'danger',
    })
  )
    if (
      !disposed &&
      epoch === opsAuthEpoch() &&
      session.value?.id === id &&
      session.value.activeRunId === runId
    )
      await chat.stop(runId)
}
async function copy(text: string): Promise<void> {
  const epoch = opsAuthEpoch()
  const copied = await copyText(
    text,
    () => !disposed && epoch === opsAuthEpoch(),
  )
  if (disposed || epoch !== opsAuthEpoch()) return
  if (copied) toast.success(t('opsChat.copied'))
  else manualCopy.value = text
}
function terminal(id: string): void {
  window.open(
    router.resolve({ name: 'server-terminal', params: { id } }).href,
    '_blank',
    'noopener',
  )
}
onBeforeRouteLeave(async () => !props.active || (await leave()))
// Terminal references are deliberately excluded from the autosaved draft.
const terminalReference = shallowRef<{ text: string; binding: string } | null>(
  null,
)
function terminalTarget(): string | null {
  if (
    !props.terminalEpoch ||
    loading.value ||
    busy.value ||
    pending.value ||
    session.value?.activeRunId ||
    session.value?.serverIds?.length !== 1 ||
    session.value.serverIds[0] !== props.initialServerIds[0]
  )
    return null
  return JSON.stringify([
    props.terminalEpoch,
    session.value.id,
    session.value.serverIds,
    capabilities.value?.provider,
  ])
}
watch(
  () =>
    JSON.stringify([
      props.terminalEpoch,
      session.value?.id,
      session.value?.serverIds,
      capabilities.value?.provider,
    ]),
  () => {
    terminalReference.value = null
  },
  { flush: 'sync' },
)
function prepareTerminal(epoch: string, auth: number): Promise<string | null> {
  return new Promise((resolve) => {
    let stop = () => {},
      timer: ReturnType<typeof setTimeout> | undefined
    const finish = (target: string | null) => {
      stop()
      clearTimeout(timer)
      terminalWaiters.delete(cancel)
      resolve(target)
    }
    const cancel = () => finish(null)
    const check = () => {
      if (disposed || props.terminalEpoch !== epoch || auth !== opsAuthEpoch())
        return finish(null)
      const target = terminalTarget()
      if (target) finish(target)
      else if (!loading.value && error.value) finish(null)
    }
    terminalWaiters.add(cancel)
    stop = watch(
      () => [
        props.terminalEpoch,
        session.value,
        capabilities.value,
        loading.value,
        busy.value,
        pending.value,
        session.value?.activeRunId,
        terminalTarget(),
        error.value,
      ],
      check,
      { flush: 'post' },
    )
    timer = setTimeout(cancel, 5000)
    check()
  })
}
function attachTerminal(text: string, binding: string): boolean {
  if (
    binding !== terminalTarget() ||
    utf8Bytes([draft.value, text].filter(Boolean).join('\n\n')) > 8192
  )
    return false
  terminalReference.value = { text, binding }
  return true
}
async function send(): Promise<boolean> {
  const reference = terminalReference.value
  if (!reference) return chat.send()
  const valid = () =>
    !disposed &&
    reference === terminalReference.value &&
    reference.binding === terminalTarget()
  if (!valid()) {
    terminalReference.value = null
    return false
  }
  const accepted = await chat.send({ text: reference.text, valid })
  if (accepted || pending.value) terminalReference.value = null
  return accepted
}
defineExpose({ close, terminalTarget, prepareTerminal, attachTerminal })

</script>
<template>
  <aside class="ops-panel" data-testid="ops-panel">
    <header class="ops-section ops-row ops-header" data-testid="ops-header">
      <div class="ops-grow">
        <strong
          class="ops-wrap ops-heading"
          :title="session?.title || t('opsChat.title')"
          >{{ session?.title || t('opsChat.title') }}</strong
        >
        <div class="ops-muted">
          <template v-if="activeRun?.status !== 'planning'">
            {{
              activeRun
                ? t('opsChat.status.' + activeRun.status)
                : t('opsChat.idle')
            }}
          </template>
        </div>
      </div>
      <button
        class="ops-btn ops-icon"
        :title="t('opsChat.history')"
        :aria-label="t('opsChat.history')"
        :aria-expanded="historyOpen"
        @click="historyOpen = !historyOpen"
      >
        <History />
      </button>
      <button
        class="ops-btn ops-icon"
        :title="t('opsChat.newSession')"
        :aria-label="t('opsChat.newSession')"
        :disabled="disabled"
        @click="chat.create"
      >
        <Plus />
      </button>
      <button
        class="ops-btn ops-icon"
        :title="t('opsChat.close')"
        :aria-label="t('opsChat.close')"
        @click="close"
      >
        <X />
      </button>
    </header>
    <div v-if="error" class="ops-section ops-row ops-feedback" role="alert">
      <span class="ops-error ops-grow ops-wrap">{{
        t('opsChat.errors.' + error)
      }}</span
      ><button
        class="ops-btn ops-icon"
        :title="t('opsChat.refresh')"
        :aria-label="t('opsChat.refresh')"
        @click="session ? chat.recover() : chat.restore()"
      >
        <Refresh />
      </button>
    </div>
    <div
      class="ops-navigation"
      :class="{ 'ops-navigation--history': historyOpen }"
      data-testid="ops-navigation"
    >
      <AiOpsServerPicker
        :servers="servers"
        :selected="session?.serverIds ?? []"
        :limit="capabilities?.limits?.targets ?? 8"
        :disabled="disabled || !!session?.activeRunId || !!pending"
        @change="chat.targets"
      />
      <AiOpsSessionList
        v-if="historyOpen"
        :sessions="state.sessions"
        :current-id="state.currentId"
        :disabled="disabled"
        @select="chat.select"
        @rename="chat.rename"
        @remove="remove"
      />
    </div>
    <div
      ref="messagesViewport"
      class="ops-body"
      data-testid="ops-messages-scroll"
      tabindex="0"
      :aria-label="t('opsChat.title')"
    >
      <div ref="messagesContent" class="ops-scroll-content">
        <AiOpsMessageList
          :entries="state.entries"
          :calls="state.calls"
          :runs="state.snapshot?.runs ?? []"
          :optimistic="optimistic"
          :waiting="waiting"
          :disabled="disabled || !!session?.activeRunId || !!pending"
          :model-available="capabilities?.modelAvailable ?? false"
          :has-history="!!historyCursor"
          @copy="copy"
          @retry="(id) => chat.retry([id])"
          @analyze="chat.analyze"
          @analyze-run="(runId) => chat.analyze([], runId)"
          @terminal="terminal"
          @history="preserveHistory(chat.history)"
        />
        <AiOpsConfirmation
          v-for="confirmation in confirmations"
          :key="confirmation.runId"
          :confirmation="confirmation"
          :calls="state.calls"
          :disabled="disabled"
          @confirm="chat.confirmBatch"
          @cancel="chat.stop"
          @reissue="chat.reissue"
        />
      </div>
    </div>
    <div v-if="terminalReference" class="ops-section terminal-reference" data-testid="terminal-reference">
      <strong>{{t('serverTerminal.referenceAttached')}}</strong>
      <p>{{t('serverTerminal.referenceNotice')}}</p>
      <pre>{{terminalReference.text}}</pre>
      <button class="ops-btn" type="button" @click="terminalReference=null">{{t('serverTerminal.removeReference')}}</button>
    </div>
    <AiOpsComposer
      class="ops-composer"
      :draft="draft"
      :context-text="terminalReference?.text"
      :draft-status="draftStatus"
      :tools="tools"
      :disabled="disabled"
      :active="!!session?.activeRunId"
      :model-available="capabilities?.modelAvailable ?? false"
      :has-targets="!!session?.serverIds?.length"
      :pending="!!pending"
      @edit="chat.editDraft"
      @send="send"
      @tool="chat.tool"
      @stop="stop"
      @save="chat.flush"
      @remote="chat.resolveDraft(false)"
      @local="chat.resolveDraft(true)"
      @resend="chat.submitPending"
    />
    <CopyTextDialog
      v-if="manualCopy !== null"
      :text="manualCopy"
      @close="manualCopy = null"
    />
  </aside>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.terminal-reference{flex:0 1 auto;max-height:22%;overflow:auto;border-top:1px solid var(--color-border)}
.terminal-reference p{font-size:12px;line-height:1.5;color:var(--color-dim)}
.terminal-reference pre{font:12px/1.5 var(--font-mono);white-space:pre-wrap;overflow-wrap:anywhere}
.ops-panel {
  container: ops-panel / size;
  overflow: hidden;
}
.ops-header {
  flex: none;
  flex-wrap: nowrap;
}
.ops-heading {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  font-size:13px;
  line-height:1.5;
}
.ops-feedback {
  flex: 0 1 auto;
  max-height: 16%;
  min-height: 0;
  overflow: auto;
}
.ops-navigation {
  /* Reserve navigation chrome before sharing the panel's height between lists. */
  --ops-list-max-height: clamp(24px, calc(40cqh - 110px), 180px);
  display: flex;
  flex-direction: column;
  flex: none;
  max-height: 40%;
  min-height: 0;
  min-width: 0;
  overflow: auto;
  overscroll-behavior: contain;
}
.ops-navigation--history {
  --ops-list-max-height: clamp(24px, calc((40cqh - 180px) / 2), 180px);
}
.ops-body {
  flex: 1 0 48px;
  min-height: 48px;
  min-width: 0;
  overflow: auto;
  overscroll-behavior: contain;
  overflow-anchor: none;
  padding-inline:2px;
}
.ops-scroll-content {
  display: flow-root;
  min-width: 0;
}
.ops-panel > .ops-composer {
  flex: 0 1 auto;
  min-height: 0;
  max-height: 36%;
}
.ops-body:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: -2px;
}
@container ops-panel (max-height: 600px) {
  .ops-header,
  .ops-feedback {
    padding-block: 6px;
  }
  .ops-navigation--history {
    --ops-list-max-height: clamp(24px, calc(40cqh - 110px), 180px);
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    grid-template-rows: minmax(0, 1fr);
  }
}
</style>

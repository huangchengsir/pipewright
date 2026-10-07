<script setup lang="ts">
import { computed, shallowRef, toRef, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { History, Plus, X, Refresh } from '@vicons/tabler'
import { useOpsChat } from '../../composables/useOpsChat'
import { useConfirm } from '../../composables/useConfirm'
import { useToast } from '../../composables/useToast'
import { copyText } from '../../utils/clipboard'
import { onOpsAuthReset, opsAuthEpoch } from '../../utils/opsAuth'
import CopyTextDialog from './CopyTextDialog.vue'
import AiOpsSessionList from './AiOpsSessionList.vue'
import AiOpsServerPicker from './AiOpsServerPicker.vue'
import AiOpsMessageList from './AiOpsMessageList.vue'
import AiOpsComposer from './AiOpsComposer.vue'
import AiOpsConfirmation from './AiOpsConfirmation.vue'
import AiOpsAnalysisConsent from './AiOpsAnalysisConsent.vue'
const props = withDefaults(
  defineProps<{ active?: boolean; initialServerIds?: string[] }>(),
  { active: true, initialServerIds: () => [] },
)
const emit = defineEmits<{ collapse: [] }>()
const { t } = useI18n(),
  router = useRouter(),
  confirm = useConfirm(),
  toast = useToast()
const chat = useOpsChat({
  active: toRef(props, 'active'),
  initialServerIds: () => props.initialServerIds,
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
  preview,
  pending,
  servers,
  tools,
  confirmations,
  historyCursor,
} = chat
const historyOpen = shallowRef(false),
  manualCopy = shallowRef<string | null>(null)
let disposed = false
const unAuthCopy = onOpsAuthReset(() => {
  manualCopy.value = null
})
onBeforeUnmount(() => {
  disposed = true
  manualCopy.value = null
  unAuthCopy()
})
const disabled = computed(
  () => busy.value || loading.value || !capabilities.value?.available,
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
defineExpose({ close })
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
          {{
            activeRun
              ? t('opsChat.status.' + activeRun.status)
              : t('opsChat.idle')
          }}
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
      class="ops-body"
      data-testid="ops-messages-scroll"
      tabindex="0"
      :aria-label="t('opsChat.title')"
    >
      <AiOpsMessageList
        :entries="state.entries"
        :calls="state.calls"
        :disabled="disabled || !!session?.activeRunId || !!pending"
        :model-available="capabilities?.modelAvailable ?? false"
        :has-history="!!historyCursor"
        @copy="copy"
        @retry="(id) => chat.retry([id])"
        @analyze="(id) => chat.analyzePreview([id])"
        @terminal="terminal"
        @history="chat.history"
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
      <AiOpsAnalysisConsent
        v-if="preview"
        :preview="preview"
        :calls="state.calls"
        :disabled="disabled"
        @approve="chat.analyze"
        @cancel="preview = null"
      />
    </div>
    <AiOpsComposer
      class="ops-composer"
      :draft="draft"
      :draft-status="draftStatus"
      :tools="tools"
      :disabled="disabled"
      :active="!!session?.activeRunId"
      :model-available="capabilities?.modelAvailable ?? false"
      :has-targets="!!session?.serverIds?.length"
      :pending="!!pending"
      @edit="chat.editDraft"
      @send="chat.send"
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

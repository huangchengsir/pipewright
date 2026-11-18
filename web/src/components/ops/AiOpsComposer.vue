<script setup lang="ts">
import { computed, shallowRef, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { Send, PlayerStop, Refresh } from '@vicons/tabler'
import AppSelect from '../ui/AppSelect.vue'
import AiOpsExecArgs from './AiOpsExecArgs.vue'
import { isValidExecArgv, utf8Bytes, type Provider, type Tool, type ToolArgs } from '../../api/opsChat'
const props = withDefaults(defineProps<{
  canEdit?: boolean
  canSubmit?: boolean
  canCancel?: boolean
  draft: string
  contextText?: string
  draftStatus: string
  tools: Tool[]
  disabled: boolean
  active: boolean
  modelAvailable: boolean
  canConfigureModel?: boolean
  models?: Provider[]
  modelValue?: string
  hasTargets: boolean
  pending: boolean
}>(), { canEdit: true, canSubmit: true, canCancel: true })
const emit = defineEmits<{
  model: [value: string]
  edit: [value: string]
  send: []
  tool: [id: string, args: ToolArgs]
  stop: []
  save: []
  remote: []
  local: []
  resend: []
}>()
const { t } = useI18n()
const toolId = shallowRef('host_resources'),
  object = shallowRef(''),
  lines = shallowRef('200'),
  action = shallowRef('restart')
const execArgv = shallowRef<string[]>([''])
const exec = computed(() => toolId.value === 'docker_exec')
const needsContainer = computed(() =>
  ['docker_inspect', 'docker_logs', 'docker_action', 'docker_exec'].includes(toolId.value),
)
const needsUnit = computed(() => toolId.value.startsWith('systemd_'))
const needsLines = computed(() => toolId.value.endsWith('_logs'))
const selectedTool = computed(() => props.tools.find((tool) => tool.toolId === toolId.value))
const mutation = computed(() => selectedTool.value?.mutation ?? false)
const needsAction = computed(() => ['docker_action', 'systemd_action'].includes(toolId.value))
const formBusy = computed(() => props.disabled || !props.canSubmit || props.active || props.pending)
const modelOptions = computed(() => (props.models ?? []).map(model => ({
  value: model.configHash,
  label: model.model,
})))
const canSend = computed(() =>
  !formBusy.value && props.modelAvailable && props.hasTargets &&
  (!props.models?.length || modelOptions.value.some(option => option.value === props.modelValue)) &&
  !!(props.draft.trim() || props.contextText) &&
  utf8Bytes([props.draft, props.contextText].filter(Boolean).join('\n\n')) <= 8192,
)
const editor = useTemplateRef('editor')
function send(): void {
  if (!canSend.value) return
  editor.value?.focus({ preventScroll: true })
  emit('send')
}
function sendOnEnter(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.shiftKey || event.ctrlKey || event.altKey ||
      event.metaKey || event.isComposing || event.keyCode === 229 || !canSend.value) return
  event.preventDefault()
  if (!event.repeat) send()
}
const toolDisabled = computed(() =>
  formBusy.value || !props.hasTargets || !selectedTool.value ||
  ((needsContainer.value || needsUnit.value) && !object.value.trim()) ||
  (exec.value && !isValidExecArgv(execArgv.value)),
)
const catalogue = computed(() =>
  props.tools.map((tool) => ({
    value: tool.toolId,
    label: t('opsChat.tools.' + tool.toolId),
  })),
)
const actions = computed(() =>
  (toolId.value === 'docker_action'
    ? ['start', 'stop', 'restart', 'pause', 'unpause']
    : ['start', 'stop', 'restart']
  ).map((value) => ({ value, label: t('opsChat.actions.' + value) })),
)
function run(): void {
  if (toolDisabled.value) return
  const args: ToolArgs = {}
  if (needsContainer.value) args.container = object.value.trim()
  if (needsUnit.value) args.unit = object.value.trim()
  if (needsLines.value) args.lines = Number(lines.value)
  if (needsAction.value) args.action = action.value
  if (exec.value) args.argv = [...execArgv.value]
  emit('tool', toolId.value, args)
}
</script>
<template>
  <section class="composer">
    <details v-if="canSubmit" class="tool-picker">
      <summary>{{ t('opsChat.runTool') }}</summary>
      <div class="tool-fields">
        <AppSelect
          v-model="toolId"
          :options="catalogue"
          :aria-label="t('opsChat.runTool')"
          min-width="100%"
          height="36px"
          portal
          :disabled="disabled || !canEdit || active || pending"
        />
        <input
          v-if="needsContainer || needsUnit"
          v-model="object"
          class="ops-input"
          maxlength="256"
          :placeholder="t(needsUnit ? 'opsChat.unit' : 'opsChat.container')"
          :aria-label="t(needsUnit ? 'opsChat.unit' : 'opsChat.container')"
          :disabled="formBusy"
        />
        <AiOpsExecArgs
          v-if="exec"
          :argv="execArgv"
          :disabled="formBusy"
          @update:argv="execArgv = $event"
        />
        <p v-if="exec && !isValidExecArgv(execArgv)" class="ops-error" role="status">
          {{ t('opsChat.exec.invalidArgs') }}
        </p>
        <AppSelect
          v-if="needsAction"
          v-model="action"
          :options="actions"
          :aria-label="t('opsChat.action')"
          min-width="100%"
          height="36px"
          portal
          :disabled="formBusy"
        />
        <AppSelect
          v-if="needsLines"
          v-model="lines"
          :options="
            [100, 200, 500, 1000].map((n) => ({
              value: String(n),
              label: String(n),
            }))
          "
          :aria-label="t('opsChat.lines')"
          min-width="100%"
          height="36px"
          portal
        />
        <button
          class="ops-btn"
          :disabled="toolDisabled"
          @click="run"
        >
          {{ t(mutation ? 'opsChat.prepareBatch' : 'opsChat.collect') }}
        </button>
      </div>
    </details>
    <p v-if="!modelAvailable" class="ops-muted">
      {{ t('opsChat.modelUnavailable') }}
      <RouterLink v-if="canConfigureModel" :to="{ name: 'settings-ai' }">
        {{ t('settingsHub.navAi') }}
      </RouterLink>
    </p>
    <p v-if="!hasTargets" class="ops-muted" role="status">
      {{ t('opsChat.noTargets') }}
    </p>
    <div class="composer-input">
      <textarea
        ref="editor"
        class="ops-input draft"
        rows="3"
        :value="draft"
        :placeholder="t('opsChat.message')"
        :aria-label="t('opsChat.message')"
        :readonly="disabled || !canEdit"
        @input="!disabled && canEdit && emit('edit', ($event.target as HTMLTextAreaElement).value)"
        @keydown="sendOnEnter"
      />
      <div v-if="canSubmit || (active && canCancel)" class="composer-actions ops-row">
        <div v-if="canSubmit && modelOptions.length" class="composer-model">
          <span class="ops-muted">{{ t('settingsAI.modelLabel') }}</span>
          <AppSelect
            :model-value="modelValue ?? ''"
            :options="modelOptions"
            :placeholder="t('settingsAI.availableModels')"
            :aria-label="t('settingsAI.modelLabel')"
            :disabled="formBusy || (modelOptions.length === 1 && modelOptions[0]?.value === modelValue)"
            min-width="0"
            height="34px"
            portal
            @update:model-value="emit('model', $event)"
          />
        </div>
        <button
          v-if="active && canCancel"
          class="ops-btn ops-danger composer-send"
          :disabled="disabled"
          @click="canCancel && emit('stop')"
        >
          <PlayerStop />{{ t('opsChat.stop') }}
        </button>
        <button
          v-else-if="canSubmit"
          class="ops-btn ops-primary composer-send"
          :disabled="!canSend"
          :title="t(hasTargets ? 'opsChat.send' : 'opsChat.noTargets')"
          :aria-label="t('opsChat.send')"
          @click="send"
        >
          {{ t('opsChat.send') }} <Send />
        </button>
      </div>
    </div>
    <div class="ops-row">
      <span class="ops-muted ops-grow" role="status"
        >{{ t('opsChat.draft.' + draftStatus) }} · {{ utf8Bytes(draft) }}/8192
        B</span
      >
      <button
        v-if="draftStatus === 'failed' && canEdit"
        class="ops-btn"
        @click="canEdit && emit('save')"
      >
        {{ t('opsChat.save') }}
      </button>
    </div>
    <div v-if="draftStatus === 'conflict'" class="ops-row">
      <button class="ops-btn" @click="emit('remote')">
        {{ t('opsChat.useRemote') }}
      </button>
      <button v-if="canEdit" class="ops-btn" @click="canEdit && emit('local')">
        {{ t('opsChat.keepLocal') }}
      </button>
    </div>
    <div v-if="pending" class="ops-row">
      <span class="ops-muted ops-grow">{{ t('opsChat.ambiguous') }}</span>
      <button v-if="canSubmit" class="ops-btn" :disabled="disabled" @click="canSubmit && emit('resend')">
        <Refresh />{{ t('opsChat.retrySubmission') }}
      </button>
    </div>
  </section>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.composer {
  padding: 12px 16px;
  border-top: 1px solid var(--color-border);
  flex: none;
  max-height: 55%;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.tool-picker summary {
  cursor: pointer;
  min-height: 28px;
}
.tool-fields {
  display: grid;
  gap: 8px;
  padding: 6px 0;
}
.composer-input {
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: 12px;
  padding: 8px;
}
.composer-input:focus-within { border-color: var(--color-primary); }
.composer-actions { justify-content: space-between; margin-top: 6px; }
.composer-model { display: flex; align-items: center; flex: 1 1 136px; gap: 8px; min-width: 0; }
.composer-model > span { flex: none; }
.composer-model :deep(.app-select) { max-width: 220px; }
.composer-send { margin-inline-start: auto; }
:global([data-theme='dark'] .ops-composer .ops-primary) { color: #0c1421; }
.draft {
  display: block;
  resize: vertical;
  min-height: 54px;
  max-height: 140px;
  padding: 6px;
  border: 0;
  background: transparent;
  line-height: 1.55;
}
@media (max-width: 600px), (pointer: coarse) {
  .composer-send,
  .composer-model :deep(.app-select__trigger) { min-height: 44px; }
}
@media (max-width: 600px) {
  .composer input,
  .composer textarea {
    font-size: 16px;
  }
}
.composer p {
  margin: 0;
}
</style>

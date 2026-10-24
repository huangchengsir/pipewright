<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { Send, PlayerStop, Refresh } from '@vicons/tabler'
import AppSelect from '../ui/AppSelect.vue'
import AiOpsExecArgs from './AiOpsExecArgs.vue'
import { isValidExecArgv, utf8Bytes, type Tool, type ToolArgs } from '../../api/opsChat'
const props = defineProps<{
  draft: string
  contextText?: string
  draftStatus: string
  tools: Tool[]
  disabled: boolean
  active: boolean
  modelAvailable: boolean
  hasTargets: boolean
  pending: boolean
}>()
const emit = defineEmits<{
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
const formBusy = computed(() => props.disabled || props.active || props.pending)
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
    <details class="tool-picker">
      <summary>{{ t('opsChat.runTool') }}</summary>
      <div class="tool-fields">
        <AppSelect
          v-model="toolId"
          :options="catalogue"
          :aria-label="t('opsChat.runTool')"
          min-width="100%"
          height="36px"
          portal
          :disabled="disabled || active || pending"
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
    </p>
    <p v-if="!hasTargets" class="ops-muted" role="status">
      {{ t('opsChat.noTargets') }}
    </p>
    <div class="input-row">
      <textarea
        class="ops-input draft"
        rows="3"
        :value="draft"
        :placeholder="t('opsChat.message')"
        :aria-label="t('opsChat.message')"
        :disabled="disabled"
        @input="emit('edit', ($event.target as HTMLTextAreaElement).value)"
      />
      <button
        class="ops-btn ops-icon ops-primary"
        :disabled="
          disabled ||
          active ||
          pending ||
          !modelAvailable ||
          !hasTargets ||
          (!draft.trim() && !contextText) ||
          utf8Bytes([draft,contextText].filter(Boolean).join('\n\n')) > 8192
        "
        :title="t(hasTargets ? 'opsChat.send' : 'opsChat.noTargets')"
        :aria-label="t('opsChat.send')"
        @click="emit('send')"
      >
        <Send />
      </button>
    </div>
    <div class="ops-row">
      <span class="ops-muted ops-grow" role="status"
        >{{ t('opsChat.draft.' + draftStatus) }} · {{ utf8Bytes(draft) }}/8192
        B</span
      >
      <button
        v-if="draftStatus === 'failed'"
        class="ops-btn"
        @click="emit('save')"
      >
        {{ t('opsChat.save') }}
      </button>
      <button
        v-if="active"
        class="ops-btn ops-danger"
        :disabled="disabled"
        @click="emit('stop')"
      >
        <PlayerStop />{{ t('opsChat.stop') }}
      </button>
    </div>
    <div v-if="draftStatus === 'conflict'" class="ops-row">
      <button class="ops-btn" @click="emit('remote')">
        {{ t('opsChat.useRemote') }}
      </button>
      <button class="ops-btn" @click="emit('local')">
        {{ t('opsChat.keepLocal') }}
      </button>
    </div>
    <div v-if="pending" class="ops-row">
      <span class="ops-muted ops-grow">{{ t('opsChat.ambiguous') }}</span>
      <button class="ops-btn" :disabled="disabled" @click="emit('resend')">
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
.input-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  min-width: 0;
}
.draft {
  resize: vertical;
  min-height: 72px;
  max-height: 140px;
  flex: 1;
  line-height: 1.5;
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

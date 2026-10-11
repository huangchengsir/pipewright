<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { Plus, Trash } from '@vicons/tabler'
import { execArgvLimits } from '../../api/opsChat'

const props = defineProps<{ argv: string[]; disabled: boolean }>()
const emit = defineEmits<{ 'update:argv': [value: string[]] }>()
const { t } = useI18n()
const id = useId()

function update(index: number, value: string): void {
  if (props.disabled) return
  const argv = [...props.argv]
  argv[index] = value
  emit('update:argv', argv)
}
function add(): void {
  if (props.disabled || props.argv.length >= execArgvLimits.count) return
  emit('update:argv', [...props.argv, ''])
}
function remove(index: number): void {
  if (props.disabled || index < 1) return
  emit('update:argv', props.argv.filter((_, i) => i !== index))
}
</script>
<template>
  <div class="exec-args">
    <label class="ops-label" :for="`${id}-program`">
      {{ t('opsChat.exec.program') }}
      <input
        :id="`${id}-program`"
        class="ops-input exec-program"
        :value="argv[0] ?? ''"
        :disabled="disabled"
        autocomplete="off"
        spellcheck="false"
        @input="update(0, ($event.target as HTMLInputElement).value)"
      />
    </label>
    <div v-for="(_, index) in argv.slice(1)" :key="index" class="exec-token-row">
      <label class="ops-label exec-token" :for="`${id}-argument-${index + 1}`">
        {{ t('opsChat.exec.argument', { index: index + 1 }) }}
        <textarea
          :id="`${id}-argument-${index + 1}`"
          class="ops-input exec-argument"
          rows="2"
          :value="argv[index + 1]"
          :disabled="disabled"
          autocomplete="off"
          spellcheck="false"
          @input="update(index + 1, ($event.target as HTMLTextAreaElement).value)"
        />
      </label>
      <button
        type="button"
        class="ops-btn ops-icon"
        :title="t('opsChat.exec.removeArgument', { index: index + 1 })"
        :aria-label="t('opsChat.exec.removeArgument', { index: index + 1 })"
        :disabled="disabled"
        @click="remove(index + 1)"
      ><Trash /></button>
    </div>
    <button
      type="button"
      class="ops-btn ops-icon exec-add"
      :title="t('opsChat.exec.addArgument')"
      :aria-label="t('opsChat.exec.addArgument')"
      :disabled="disabled || argv.length >= execArgvLimits.count"
      @click="add"
    ><Plus /></button>
  </div>
</template>
<style scoped src="./opsChat.css"></style>
<style scoped>
.exec-args {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.exec-token-row {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  min-width: 0;
}
.exec-token {
  flex: 1;
  min-width: 0;
}
.exec-argument {
  resize: vertical;
  min-height: 52px;
}
.exec-add {
  justify-self: start;
}
@media (max-width: 600px) {
  .exec-program,
  .exec-argument {
    font-size: 16px;
  }
}
</style>

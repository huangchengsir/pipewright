<script setup lang="ts">
/**
 * StudioInstanceParams — 工作室节点实例的「提升参数短清单」类型化控件。
 *
 * 把一个工作室(templated)节点拖进流水线后,实例编辑只需面对作者「提升」出来的
 * 少数参数,而非整段 commandTemplate/params 原始文本(n8n promote 参数 / Node-RED
 * Subflow properties 范式)。据每个提升参数的 type 渲染控件:
 *   select → 下拉、toggle → 开关、number → 数字框、text → 输入框,以当前值预填。
 *
 * 数据契约(与「运行参数」TypedRunParams 区分):
 *   - 定义(key/label/type/options)由调用方经 `parsePromotedParams` 提供,**只读消费**,
 *     本组件绝不改它 —— 故 `__studio` 元信息不被实例编辑破坏。
 *   - 当前值经 v-model 以 Record<string,string> 回传;调用方用 `applyPromotedValues`
 *     写回 config.params 文本。本组件不直接碰 config。
 */
import { ref, watch } from 'vue'
import type { PromotedParam } from './studioCompile'

const props = defineProps<{
  /** 提升参数定义(只读):提供控件类型/标签/候选项与初始值。 */
  params: PromotedParam[]
  /** 当前实例值表(key → value);缺失的 key 由 param.default 兜底。 */
  modelValue: Record<string, string>
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: Record<string, string>): void
}>()

/** 以 default 兜底 + 已有 modelValue 覆盖,建立本地值表。 */
function seed(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const p of props.params) {
    const provided = props.modelValue[p.key]
    out[p.key] = provided != null && provided !== '' ? provided : p.default
  }
  return out
}

const values = ref<Record<string, string>>(seed())

// 切节点 / 参数定义变化时重新播种(不 commit:避免把回显当成一次编辑写回)。
watch(
  () => props.params,
  () => {
    values.value = seed()
  },
)

function setVal(key: string, v: string): void {
  values.value = { ...values.value, [key]: v }
  emit('update:modelValue', { ...values.value })
}

function toggleBool(key: string): void {
  setVal(key, values.value[key] === 'true' ? 'false' : 'true')
}
</script>

<template>
  <div class="studio-instance">
    <div v-for="p in params" :key="p.key" class="si-field">
      <label class="si-label" :for="`si-${p.key}`">
        {{ p.label || p.key }}
        <code class="si-key">{{ p.key }}</code>
      </label>

      <!-- 枚举 → 下拉 -->
      <select
        v-if="p.type === 'select'"
        :id="`si-${p.key}`"
        class="si-input"
        :value="values[p.key]"
        :aria-label="p.label || p.key"
        @change="setVal(p.key, ($event.target as HTMLSelectElement).value)"
      >
        <option v-for="opt in p.options ?? []" :key="opt" :value="opt">{{ opt }}</option>
      </select>

      <!-- 布尔 → 开关 -->
      <button
        v-else-if="p.type === 'toggle'"
        :id="`si-${p.key}`"
        type="button"
        class="si-toggle"
        :class="{ 'si-toggle--on': values[p.key] === 'true' }"
        role="switch"
        :aria-checked="values[p.key] === 'true'"
        :aria-label="p.label || p.key"
        @click="toggleBool(p.key)"
      >
        <span class="si-toggle-knob" />
        <span class="si-toggle-text">{{ values[p.key] === 'true' ? 'true' : 'false' }}</span>
      </button>

      <!-- 数字 / 文本 -->
      <input
        v-else
        :id="`si-${p.key}`"
        class="si-input"
        :type="p.type === 'number' ? 'number' : 'text'"
        :value="values[p.key]"
        :placeholder="p.default"
        :aria-label="p.label || p.key"
        autocomplete="off"
        @input="setVal(p.key, ($event.target as HTMLInputElement).value)"
      />
    </div>
  </div>
</template>

<style scoped>
.studio-instance { display: flex; flex-direction: column; gap: 12px; }
.si-field { display: flex; flex-direction: column; gap: 5px; }
.si-label {
  font-size: 0.8rem; font-weight: 600; color: var(--color-text);
  display: flex; align-items: center; gap: 6px;
}
.si-key {
  font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.7rem;
  color: var(--color-faint); background: var(--color-inset);
  padding: 0 4px; border-radius: 4px; font-weight: 400;
}
.si-input {
  height: 34px; padding: 0 10px;
  border: 1px solid var(--color-border-strong); border-radius: var(--rounded-md);
  background: var(--color-card); color: var(--color-text);
  font: inherit; font-size: 0.85rem; outline: none; width: 100%;
}
.si-input:focus { border-color: var(--color-primary); box-shadow: 0 0 0 2px var(--color-primary-soft); }
.si-toggle {
  display: inline-flex; align-items: center; gap: 9px;
  height: 34px; padding: 0 12px 0 4px; width: fit-content;
  border: 1px solid var(--color-border-strong); border-radius: var(--rounded-full, 999px);
  background: var(--color-inset); cursor: pointer; color: var(--color-dim);
  font: inherit; font-size: 0.8rem; transition: background-color var(--duration-fast);
}
.si-toggle--on { background: var(--color-primary-soft); color: var(--color-primary); border-color: var(--color-primary); }
.si-toggle-knob {
  width: 24px; height: 24px; border-radius: 50%;
  background: var(--color-card); border: 1px solid var(--color-border-strong);
  transition: transform var(--duration-fast); flex: none;
}
.si-toggle--on .si-toggle-knob { transform: translateX(2px); border-color: var(--color-primary); }
.si-toggle-text { font-family: var(--font-mono, ui-monospace, monospace); }
</style>

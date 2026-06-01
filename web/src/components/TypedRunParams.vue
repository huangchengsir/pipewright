<script setup lang="ts">
/**
 * TypedRunParams — 手动触发弹窗的「类型化参数」输入(P0 typed params)。
 *
 * 据项目参数定义渲染对应控件:枚举→下拉、布尔→开关、数字→数字框、文本→输入框,
 * 各以 default 预填。值经 v-model 以 Record<string,string> 回传(执行期注入容器环境)。
 * 当项目无参数定义时,调用方改用 RunParamsEditor(自由 KV);本组件只在有定义时渲染。
 */
import { ref, watch } from 'vue'
import type { ParamDef } from '../api/parameters'

const props = defineProps<{
  defs: ParamDef[]
  modelValue: Record<string, string>
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: Record<string, string>): void
}>()

/** 以 default 预填 + 已有 modelValue 覆盖,建立本地值表。 */
function seed(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const d of props.defs) {
    const provided = props.modelValue[d.key]
    out[d.key] = provided != null && provided !== '' ? provided : d.default
  }
  return out
}

const values = ref<Record<string, string>>(seed())

// defs 变化(切项目/重开弹窗)时重新播种。
watch(
  () => props.defs,
  () => {
    values.value = seed()
    commit()
  },
)

function commit(): void {
  emit('update:modelValue', { ...values.value })
}

function setVal(key: string, v: string): void {
  values.value = { ...values.value, [key]: v }
  commit()
}

function toggleBool(key: string): void {
  setVal(key, values.value[key] === 'true' ? 'false' : 'true')
}
</script>

<template>
  <div class="typed-params">
    <div v-for="d in defs" :key="d.key" class="tp-field">
      <label class="tp-label" :for="`tp-${d.key}`">
        {{ d.label }}
        <span v-if="d.required" class="tp-req" aria-label="必填">*</span>
        <code class="tp-key">{{ d.key }}</code>
      </label>

      <!-- 枚举 → 下拉 -->
      <select
        v-if="d.type === 'choice'"
        :id="`tp-${d.key}`"
        class="tp-input"
        :value="values[d.key]"
        @change="setVal(d.key, ($event.target as HTMLSelectElement).value)"
      >
        <option v-for="opt in d.options ?? []" :key="opt" :value="opt">{{ opt }}</option>
      </select>

      <!-- 布尔 → 开关 -->
      <button
        v-else-if="d.type === 'boolean'"
        :id="`tp-${d.key}`"
        type="button"
        class="tp-toggle"
        :class="{ 'tp-toggle--on': values[d.key] === 'true' }"
        role="switch"
        :aria-checked="values[d.key] === 'true'"
        @click="toggleBool(d.key)"
      >
        <span class="tp-toggle-knob" />
        <span class="tp-toggle-text">{{ values[d.key] === 'true' ? 'true' : 'false' }}</span>
      </button>

      <!-- 数字 / 文本 -->
      <input
        v-else
        :id="`tp-${d.key}`"
        class="tp-input"
        :type="d.type === 'number' ? 'number' : 'text'"
        :value="values[d.key]"
        :placeholder="d.default"
        autocomplete="off"
        @input="setVal(d.key, ($event.target as HTMLInputElement).value)"
      />
    </div>
  </div>
</template>

<style scoped>
.typed-params { display: flex; flex-direction: column; gap: 12px; }
.tp-field { display: flex; flex-direction: column; gap: 5px; }
.tp-label { font-size: 0.8rem; font-weight: 600; color: var(--color-text); display: flex; align-items: center; gap: 6px; }
.tp-req { color: var(--color-danger, #dc2626); }
.tp-key { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.7rem; color: var(--color-faint); background: var(--color-inset); padding: 0 4px; border-radius: 4px; font-weight: 400; }
.tp-input {
  height: 34px; padding: 0 10px;
  border: 1px solid var(--color-border-strong); border-radius: var(--rounded-md);
  background: var(--color-card); color: var(--color-text);
  font: inherit; font-size: 0.85rem; outline: none; width: 100%;
}
.tp-input:focus { border-color: var(--color-primary); box-shadow: 0 0 0 2px var(--color-primary-soft); }
.tp-toggle {
  display: inline-flex; align-items: center; gap: 9px;
  height: 34px; padding: 0 12px 0 4px; width: fit-content;
  border: 1px solid var(--color-border-strong); border-radius: var(--rounded-full, 999px);
  background: var(--color-inset); cursor: pointer; color: var(--color-dim);
  font: inherit; font-size: 0.8rem; transition: background-color var(--duration-fast);
}
.tp-toggle--on { background: var(--color-primary-soft); color: var(--color-primary); border-color: var(--color-primary); }
.tp-toggle-knob {
  width: 24px; height: 24px; border-radius: 50%;
  background: var(--color-card); border: 1px solid var(--color-border-strong);
  transition: transform var(--duration-fast); flex: none;
}
.tp-toggle--on .tp-toggle-knob { transform: translateX(2px); border-color: var(--color-primary); }
.tp-toggle-text { font-family: var(--font-mono, ui-monospace, monospace); }
</style>

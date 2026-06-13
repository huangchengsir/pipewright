<script setup lang="ts">
/**
 * RunParamsEditor — key=value rows for a parameterized manual run (Story 8-11).
 *
 * Self-contained row state; emits the normalized `Record<string,string>` via
 * v-model on every edit. Blank-key rows are dropped on normalize; later keys win
 * on collision. Remount (via :key) to reset — the editor seeds its rows from the
 * initial modelValue once on mount and owns them thereafter.
 *
 * Values are plain text injected as container env (PW_<KEY>); secrets should use
 * a vault reference, never a raw param — surfaced in the hint below.
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface Row {
  /** Stable client key for v-for (params have no natural id). */
  rid: number
  key: string
  value: string
}

const props = defineProps<{
  modelValue: Record<string, string>
  disabled?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: Record<string, string>): void
}>()

let ridSeq = 0
const rows = ref<Row[]>(
  Object.entries(props.modelValue).map(([key, value]) => ({ rid: ++ridSeq, key, value })),
)

/** Collapse rows into the wire record: trim keys, drop blanks, last key wins. */
function normalize(): Record<string, string> {
  const out: Record<string, string> = {}
  for (const r of rows.value) {
    const k = r.key.trim()
    if (k) out[k] = r.value
  }
  return out
}

function commit(): void {
  emit('update:modelValue', normalize())
}

function addRow(): void {
  rows.value.push({ rid: ++ridSeq, key: '', value: '' })
}

function removeRow(rid: number): void {
  rows.value = rows.value.filter((r) => r.rid !== rid)
  commit()
}
</script>

<template>
  <div class="params-editor">
    <div v-if="rows.length" class="params-rows">
      <div v-for="row in rows" :key="row.rid" class="params-row">
        <input
          v-model="row.key"
          class="params-input params-input--key"
          type="text"
          placeholder="KEY"
          autocomplete="off"
          spellcheck="false"
          :aria-label="t('projectPanels.runParams.keyAria')"
          :disabled="disabled"
          @input="commit"
        />
        <span class="params-eq" aria-hidden="true">=</span>
        <input
          v-model="row.value"
          class="params-input params-input--val"
          type="text"
          placeholder="value"
          autocomplete="off"
          :aria-label="t('projectPanels.runParams.valueAria')"
          :disabled="disabled"
          @input="commit"
        />
        <button
          type="button"
          class="params-del"
          :aria-label="t('projectPanels.runParams.deleteParamAria', { key: row.key || t('projectPanels.runParams.emptyKey') })"
          :disabled="disabled"
          @click="removeRow(row.rid)"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" />
          </svg>
        </button>
      </div>
    </div>
    <p v-else class="params-empty">{{ t('projectPanels.runParams.empty') }}</p>

    <button type="button" class="params-add" :disabled="disabled" @click="addRow">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
        <path d="M12 5v14M5 12h14" />
      </svg>
      {{ t('projectPanels.runParams.addParam') }}
    </button>
    <p class="params-hint">{{ t('projectPanels.runParams.hint', { var: 'PW_<KEY>' }) }}</p>
  </div>
</template>

<style scoped>
.params-editor {
  display: flex;
  flex-direction: column;
  gap: 7px;
}
.params-rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.params-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.params-input {
  height: 30px;
  padding: 0 9px;
  font: inherit;
  font-size: 0.8rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  box-sizing: border-box;
  transition: border-color var(--duration-fast);
}
.params-input:focus { outline: none; border-color: var(--color-primary); }
.params-input--key {
  flex: 0 0 38%;
  min-width: 0;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-weight: 600;
  text-transform: none;
}
.params-input--val { flex: 1; min-width: 0; }
.params-eq { color: var(--color-faint); font-weight: 700; }
.params-del {
  flex: none;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  background: none;
  border: 1px solid transparent;
  border-radius: var(--rounded-md);
  color: var(--color-faint);
  cursor: pointer;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}
.params-del:hover:not(:disabled) { color: var(--color-danger, #dc2626); background: var(--color-danger-soft, rgba(220, 38, 38, 0.1)); }
.params-del:disabled { opacity: 0.4; cursor: default; }
.params-empty { margin: 0; font-size: 0.78rem; color: var(--color-faint); font-style: italic; }
.params-add {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  align-self: flex-start;
  height: 28px;
  padding: 0 11px;
  background: none;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-dim);
  font: inherit;
  font-size: 0.76rem;
  font-weight: 500;
  cursor: pointer;
  transition: border-color var(--duration-fast), color var(--duration-fast);
}
.params-add:hover:not(:disabled) { border-color: var(--color-primary); color: var(--color-primary); }
.params-add:disabled { opacity: 0.5; cursor: default; }
.params-hint { margin: 0; font-size: 0.7rem; color: var(--color-faint); line-height: 1.45; }
.params-hint code {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.92em;
  padding: 0 3px;
  border-radius: 3px;
  background: var(--color-bg-subtle, rgba(0, 0, 0, 0.05));
}
</style>

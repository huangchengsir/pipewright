<script setup lang="ts">
/**
 * StagePostEditor — 阶段「后置步骤」(P1 · 对标 Jenkins post)的画布行编辑器。
 *
 * 每行一个 post 步骤:触发条件(总是/成功时/失败时)+ 镜像 + 多行命令(+ 可选工作目录)。
 * 结构化 v-model 回传 PipelinePostStep[](空数组回传 undefined)。
 */
import { computed } from 'vue'
import type { PipelinePostStep } from '../../api/pipeline'
import { POST_CONDITIONS, POST_CONDITION_LABELS, type PostCondition } from './stageSettings'

const props = defineProps<{
  steps: PipelinePostStep[] | undefined
  stageId: string
}>()

const emit = defineEmits<{
  (e: 'update', steps: PipelinePostStep[] | undefined): void
}>()

const rows = computed<PipelinePostStep[]>(() => props.steps ?? [])

function commit(next: PipelinePostStep[]): void {
  emit('update', next.length > 0 ? next : undefined)
}
function patch(i: number, p: Partial<PipelinePostStep>): void {
  commit(rows.value.map((s, idx) => (idx === i ? { ...s, ...p } : s)))
}
function addRow(): void {
  commit([...rows.value, { condition: 'always', image: '', commands: [] }])
}
function removeRow(i: number): void {
  commit(rows.value.filter((_, idx) => idx !== i))
}
function commandsText(s: PipelinePostStep): string {
  return (s.commands ?? []).join('\n')
}
function setCommands(i: number, text: string): void {
  patch(i, { commands: text.replace(/\r/g, '').split('\n') })
}
</script>

<template>
  <div class="post-editor">
    <div v-for="(step, i) in rows" :key="i" class="post-row">
      <div class="post-line">
        <select
          class="settings-input post-cond"
          :value="step.condition"
          :aria-label="`触发条件 ${i + 1}`"
          @change="patch(i, { condition: ($event.target as HTMLSelectElement).value as PostCondition })"
        >
          <option v-for="c in POST_CONDITIONS" :key="c" :value="c">{{ POST_CONDITION_LABELS[c] }}</option>
        </select>
        <input
          class="settings-input post-image"
          type="text"
          :value="step.image"
          placeholder="busybox"
          :aria-label="`后置步骤镜像 ${i + 1}`"
          @change="patch(i, { image: ($event.target as HTMLInputElement).value.trim() })"
        />
        <button class="post-del" :aria-label="`删除后置步骤 ${i + 1}`" @click="removeRow(i)">✕</button>
      </div>
      <textarea
        class="settings-input settings-textarea post-cmds"
        rows="2"
        :value="commandsText(step)"
        placeholder="清理/通知命令(每行一条)&#10;如 curl -fsS $WEBHOOK"
        :aria-label="`后置步骤命令 ${i + 1}`"
        @change="setCommands(i, ($event.target as HTMLTextAreaElement).value)"
      ></textarea>
    </div>
    <button class="settings-add-link" @click="addRow">+ 添加后置步骤</button>
  </div>
</template>

<style scoped>
.post-editor { display: flex; flex-direction: column; gap: 8px; }
.post-row {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 8px;
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-amber, #b8860b);
  border-radius: var(--rounded-md, 8px);
  background: var(--color-inset);
}
.post-line { display: flex; align-items: center; gap: 6px; }

/* Self-contained input/select/textarea styling(不依赖父组件 scoped class):
   box-sizing + min-width:0 防止溢出抽屉右缘。 */
.post-editor input,
.post-editor select,
.post-editor textarea {
  width: 100%;
  box-sizing: border-box;
  padding: 0 10px;
  font: inherit;
  font-size: 0.82rem;
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded, 8px);
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.post-editor input,
.post-editor select { height: 34px; }
.post-editor select { cursor: pointer; }
.post-editor input:focus,
.post-editor select:focus,
.post-editor textarea:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.post-cond { flex: 0 0 92px; min-width: 0; }
.post-image { flex: 1; min-width: 0; }
.post-cmds {
  width: 100%;
  padding: 8px 10px;
  line-height: 1.5;
  resize: vertical;
  font-family: var(--font-mono);
  font-size: 0.76rem;
}
.post-del {
  flex: none; width: 22px; height: 22px; border: none; background: none;
  color: var(--color-faint); cursor: pointer; border-radius: 5px;
}
.post-del:hover { color: var(--color-danger, #dc2626); background: var(--color-card); }
.settings-add-link {
  align-self: flex-start; background: none; border: none; padding: 0;
  color: var(--color-primary); font: inherit; font-size: 0.76rem; font-weight: 600; cursor: pointer;
}
.settings-add-link:hover { text-decoration: underline; }
</style>

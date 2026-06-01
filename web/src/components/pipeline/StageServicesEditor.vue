<script setup lang="ts">
/**
 * StageServicesEditor — 阶段「旁挂服务」(P1)的画布行编辑器。
 *
 * 每行一个服务:名称 + 镜像 + 可选环境变量(K=V 逗号分隔)。结构化 v-model 回传 PipelineServiceSpec[]
 * (空数组回传 undefined 表示无服务)。端口映射较少用 → 仍可经 .pipewright.yml 配,此处不暴露。
 */
import { computed } from 'vue'
import type { PipelineServiceSpec } from '../../api/pipeline'
import { envToText, parseEnvText } from './stageSettings'

const props = defineProps<{
  services: PipelineServiceSpec[] | undefined
  stageId: string
}>()

const emit = defineEmits<{
  (e: 'update', services: PipelineServiceSpec[] | undefined): void
}>()

const rows = computed<PipelineServiceSpec[]>(() => props.services ?? [])

function commit(next: PipelineServiceSpec[]): void {
  emit('update', next.length > 0 ? next : undefined)
}

function patch(i: number, p: Partial<PipelineServiceSpec>): void {
  commit(rows.value.map((s, idx) => (idx === i ? { ...s, ...p } : s)))
}
function addRow(): void {
  commit([...rows.value, { name: '', image: '' }])
}
function removeRow(i: number): void {
  commit(rows.value.filter((_, idx) => idx !== i))
}
function setEnv(i: number, text: string): void {
  patch(i, { env: parseEnvText(text) })
}
</script>

<template>
  <div class="svc-editor">
    <div v-for="(svc, i) in rows" :key="i" class="svc-row">
      <div class="svc-line">
        <input
          class="settings-input svc-name"
          type="text"
          :value="svc.name"
          placeholder="testdb"
          :aria-label="`服务名 ${i + 1}`"
          @change="patch(i, { name: ($event.target as HTMLInputElement).value.trim() })"
        />
        <span class="svc-colon">:</span>
        <input
          class="settings-input svc-image"
          type="text"
          :value="svc.image"
          placeholder="postgres:16"
          :aria-label="`服务镜像 ${i + 1}`"
          @change="patch(i, { image: ($event.target as HTMLInputElement).value.trim() })"
        />
        <button class="svc-del" :aria-label="`删除服务 ${i + 1}`" @click="removeRow(i)">✕</button>
      </div>
      <input
        class="settings-input svc-env"
        type="text"
        :value="envToText(svc.env)"
        placeholder="环境变量(可选):POSTGRES_PASSWORD=x, POSTGRES_DB=app"
        :aria-label="`服务环境变量 ${i + 1}`"
        @change="setEnv(i, ($event.target as HTMLInputElement).value)"
      />
    </div>
    <button class="settings-add-link" @click="addRow">+ 添加服务</button>
  </div>
</template>

<style scoped>
.svc-editor { display: flex; flex-direction: column; gap: 8px; }
.svc-row {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 8px;
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-cyan, #0891b2);
  border-radius: var(--rounded-md, 8px);
  background: var(--color-inset);
}
.svc-line { display: flex; align-items: center; gap: 6px; }
.svc-colon { color: var(--color-faint); font-family: var(--font-mono); flex: none; }

/* Self-contained input styling (不依赖父组件 scoped class):box-sizing + min-width:0
   保证 flex 内输入框能收缩、不溢出抽屉右缘。 */
.svc-editor input {
  width: 100%;
  box-sizing: border-box;
  height: 34px;
  padding: 0 10px;
  font: inherit;
  font-size: 0.82rem;
  color: var(--color-text);
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded, 8px);
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}
.svc-editor input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-soft);
}
.svc-name { flex: 0 0 34%; min-width: 0; }
.svc-image { flex: 1; min-width: 0; }
.svc-env { width: 100%; font-size: 0.76rem; }
.svc-del {
  flex: none; width: 22px; height: 22px; border: none; background: none;
  color: var(--color-faint); cursor: pointer; border-radius: 5px;
}
.svc-del:hover { color: var(--color-danger, #dc2626); background: var(--color-card); }
.settings-add-link {
  align-self: flex-start; background: none; border: none; padding: 0;
  color: var(--color-primary); font: inherit; font-size: 0.76rem; font-weight: 600; cursor: pointer;
}
.settings-add-link:hover { text-decoration: underline; }
</style>

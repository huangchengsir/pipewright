<script setup lang="ts">
/**
 * StageDrawer — 阶段级设置的右侧内联检视面板。
 *
 * 复用与 JobDrawer 完全相同的 `.job-drawer` / `.drawer-*` 卡片骨架(来自 pipeline.css):
 * 同一右侧槽位、同样的滚动容器与输入样式,与「点 job 弹出的配置卡」一致 —— 而非另起
 * 一个覆盖式弹层。承载阶段的 条件(when)/ 审批门(gate)/ 矩阵(matrix)/ 旁挂服务
 * (services)/ 后置步骤(post)五块。字段编辑逻辑(分支解析、事件开关、矩阵解析)在此,
 * 经 update-* 事件回传 PipelineCanvas → updateStage 落库。
 */
import { computed } from 'vue'
import type { PipelineStage, StageWhen, PipelinePostStep, PipelineServiceSpec } from '../../api/pipeline'
import StagePostEditor from './StagePostEditor.vue'
import StageServicesEditor from './StageServicesEditor.vue'
import {
  WHEN_EVENTS,
  WHEN_EVENT_LABELS,
  parseBranches,
  branchesToText,
  toggleWhenEvent,
  normalizeWhen,
  parseMatrix,
  matrixToText,
  matrixError,
  matrixSummary,
  type WhenEvent,
} from './stageSettings'
import './pipeline.css'

const props = defineProps<{
  stage: PipelineStage
  stageIndex: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update-when', when: StageWhen | undefined): void
  (e: 'update-gate', value: boolean): void
  (e: 'update-matrix', matrix: Record<string, string[]> | undefined): void
  (e: 'update-post', post: PipelinePostStep[] | undefined): void
  (e: 'update-services', services: PipelineServiceSpec[] | undefined): void
}>()

const branchText = computed<string>(() => branchesToText(props.stage.when?.branches))
const currentEvents = computed<string[]>(() => props.stage.when?.events ?? [])

const matrixText = computed<string>(() => matrixToText(props.stage.matrix))
const matrixWarn = computed(() => matrixError(props.stage.matrix))
const matrixChip = computed(() => matrixSummary(props.stage.matrix))

function commitBranches(text: string): void {
  emit('update-when', normalizeWhen(parseBranches(text), currentEvents.value))
}
function toggleEvent(ev: WhenEvent): void {
  const events = toggleWhenEvent(currentEvents.value, ev)
  emit('update-when', normalizeWhen(props.stage.when?.branches ?? [], events))
}
function commitMatrix(text: string): void {
  emit('update-matrix', parseMatrix(text))
}
</script>

<template>
  <aside class="job-drawer" aria-label="阶段设置">
    <!-- Head — 与 JobDrawer 同骨架 -->
    <div class="drawer-head">
      <span class="drawer-icon" aria-hidden="true">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </span>
      <div class="drawer-title">
        {{ stage.name }}
        <small class="drawer-subtitle">阶段设置 · 第 {{ stageIndex }} 阶段</small>
      </div>
      <button class="drawer-close" aria-label="关闭设置" @click="emit('close')">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M18 6 6 18M6 6l12 12"/>
        </svg>
      </button>
    </div>

    <!-- 条件执行(WHEN) -->
    <div class="drawer-section">
      <div class="drawer-section-label">条件执行 · when</div>
      <div class="drawer-field">
        <div class="drawer-field-label">分支(glob,空格/逗号分隔)</div>
        <input
          class="drawer-input"
          type="text"
          :value="branchText"
          placeholder="如 main release/*"
          aria-label="分支条件"
          @change="commitBranches(($event.target as HTMLInputElement).value)"
        />
      </div>
      <div class="drawer-field">
        <div class="drawer-field-label">触发事件</div>
        <div class="drawer-events">
          <label v-for="ev in WHEN_EVENTS" :key="ev" class="drawer-event">
            <input type="checkbox" :checked="currentEvents.includes(ev)" @change="toggleEvent(ev)" />
            <span>{{ WHEN_EVENT_LABELS[ev] }}</span>
          </label>
        </div>
      </div>
      <p class="drawer-hint">两者都留空 = 始终执行;不满足时本阶段及下游跳过(不计失败)</p>
    </div>

    <!-- 审批门(GATE) -->
    <div class="drawer-section">
      <div class="drawer-section-label">审批门 · gate</div>
      <label class="drawer-toggle">
        <input
          type="checkbox"
          :checked="stage.gate === true"
          @change="emit('update-gate', ($event.target as HTMLInputElement).checked)"
        />
        <span>进入本阶段前需人工审批</span>
      </label>
      <p class="drawer-hint">开启后运行将暂停在此,等待批准/拒绝</p>
    </div>

    <!-- 矩阵构建(MATRIX) -->
    <div class="drawer-section">
      <div class="drawer-section-head">
        <div class="drawer-section-label">矩阵构建 · matrix</div>
        <span v-if="matrixChip" class="drawer-section-badge">{{ matrixChip }}</span>
      </div>
      <div class="drawer-field">
        <div class="drawer-field-label">轴(每行一个:<code>名称: 值1, 值2</code>)</div>
        <textarea
          class="drawer-input drawer-textarea is-mono"
          rows="3"
          :value="matrixText"
          placeholder="go: 1.21, 1.22&#10;os: linux"
          aria-label="矩阵轴"
          @change="commitMatrix(($event.target as HTMLTextAreaElement).value)"
        ></textarea>
      </div>
      <p v-if="matrixWarn" class="drawer-warn" role="alert">{{ matrixWarn }}</p>
      <p class="drawer-hint">展开成并行 cell(笛卡尔积),各注入 <code>MATRIX_&lt;轴名&gt;</code> 环境变量;空 = 不展开</p>
    </div>

    <!-- 旁挂服务(SERVICES) -->
    <div class="drawer-section">
      <div class="drawer-section-label">旁挂服务 · services</div>
      <StageServicesEditor
        :services="stage.services"
        :stage-id="stage.id"
        @update="emit('update-services', $event)"
      />
      <p class="drawer-hint">测试旁挂 DB/redis:与脚本容器同网,脚本里按服务名互访(如 <code>psql -h testdb</code>)</p>
    </div>

    <!-- 后置步骤(POST) -->
    <div class="drawer-section">
      <div class="drawer-section-label">后置步骤 · post</div>
      <StagePostEditor
        :steps="stage.post"
        :stage-id="stage.id"
        @update="emit('update-post', $event)"
      />
      <p class="drawer-hint">阶段 job 跑完后按条件执行(同工作区),用于清理/通知/归档;空 = 无</p>
    </div>
  </aside>
</template>

<style scoped>
.drawer-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 9px;
}
.drawer-section-head .drawer-section-label { margin-bottom: 0; }
.drawer-section-badge {
  font-size: 0.66rem;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 100px;
  background: var(--color-violet-soft, rgba(124, 58, 237, 0.13));
  color: var(--color-violet, #6d28d9);
  white-space: nowrap;
}

.drawer-textarea {
  height: auto;
  min-height: 78px;
  padding: 9px 11px;
  line-height: 1.5;
  resize: vertical;
}
.is-mono { font-family: var(--font-mono, ui-monospace, monospace); font-size: 0.8rem; }

.drawer-events { display: flex; flex-wrap: wrap; gap: 8px 16px; }
.drawer-event {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.82rem;
  color: var(--color-text);
  cursor: pointer;
}
.drawer-event input { width: 15px; height: 15px; accent-color: var(--color-primary); cursor: pointer; }

.drawer-warn { margin: 7px 0 0; font-size: 0.72rem; color: var(--color-danger, #dc2626); line-height: 1.45; }

.drawer-hint { margin: 9px 0 0; font-size: 0.72rem; color: var(--color-faint); line-height: 1.5; }
.drawer-hint code,
.drawer-field-label code {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.92em;
  padding: 0 3px;
  border-radius: 3px;
  background: var(--color-inset);
  color: var(--color-dim);
}
</style>

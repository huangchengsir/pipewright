<script setup lang="ts">
import { ref, computed } from 'vue'
import type { PipelineStage, StageWhen, PipelinePostStep, PipelineServiceSpec } from '../../api/pipeline'
import JobCard from './JobCard.vue'
import StagePostEditor from './StagePostEditor.vue'
import StageServicesEditor from './StageServicesEditor.vue'
import { eligibleNeeds, toggleNeed } from './stageDeps'
import {
  WHEN_EVENTS,
  WHEN_EVENT_LABELS,
  parseBranches,
  branchesToText,
  toggleWhenEvent,
  normalizeWhen,
  hasWhen,
  whenSummary,
  parseMatrix,
  matrixToText,
  hasMatrix,
  matrixSummary,
  matrixError,
  hasPost,
  postSummary,
  hasServices,
  servicesSummary,
  type WhenEvent,
} from './stageSettings'

const props = defineProps<{
  stage: PipelineStage
  stageIndex: number
  selectedJobId: string | null
  /** All stages — used to compute cycle-safe dependency options. */
  allStages: PipelineStage[]
}>()

const emit = defineEmits<{
  (e: 'select-job', jobId: string): void
  (e: 'delete-job', jobId: string): void
  (e: 'add-job'): void
  (e: 'delete-stage'): void
  (e: 'reorder-job', payload: { from: number; to: number }): void
  (e: 'update-needs', needs: string[]): void
  (e: 'update-allow-failure', value: boolean): void
  (e: 'update-when', when: StageWhen | undefined): void
  (e: 'update-gate', value: boolean): void
  (e: 'update-matrix', matrix: Record<string, string[]> | undefined): void
  (e: 'update-post', post: PipelinePostStep[] | undefined): void
  (e: 'update-services', services: PipelineServiceSpec[] | undefined): void
}>()

function stageLabel(index: number): string {
  if (props.stage.kind === 'source') return '源'
  return String(index)
}

// ─── Stage dependencies (needs · DAG) ─────────────────────────────────────────

const depsOpen = ref(false)

const currentNeeds = computed<string[]>(() => props.stage.needs ?? [])

/** Stages that can be an upstream dependency without creating a cycle. */
const depChoices = computed(() => eligibleNeeds(props.allStages, props.stage.id))

/** Display labels for the current needs (name of each upstream stage). */
const needLabels = computed(() =>
  currentNeeds.value
    .map((id) => props.allStages.find((s) => s.id === id)?.name ?? id)
    .filter(Boolean),
)

function toggleDep(needId: string): void {
  emit('update-needs', toggleNeed(currentNeeds.value, needId))
}

// ─── Stage settings (when 条件 · 8-5 / gate 审批门 · 8-4) ──────────────────────

const settingsOpen = ref(false)

/** Editable branch-glob text (parsed → array only on commit). */
const branchText = computed<string>(() => branchesToText(props.stage.when?.branches))

const currentEvents = computed<string[]>(() => props.stage.when?.events ?? [])

/** Active when settings, gate, matrix, post, or services → highlight the settings button. */
const hasStageRules = computed(
  () =>
    hasWhen(props.stage.when) ||
    props.stage.gate === true ||
    hasMatrix(props.stage.matrix) ||
    hasPost(props.stage.post) ||
    hasServices(props.stage.services),
)

const whenChip = computed(() => whenSummary(props.stage.when))
const postChip = computed(() => postSummary(props.stage.post))
const servicesChip = computed(() => servicesSummary(props.stage.services))

// ─── Matrix build axes (P1) ───────────────────────────────────────────────────

/** Editable matrix text (one `axis: a, b` per line), parsed → map only on commit. */
const matrixText = computed<string>(() => matrixToText(props.stage.matrix))

/** Chip summary for the matrix (empty when none). */
const matrixChip = computed(() => matrixSummary(props.stage.matrix))

/** Client-side validation message for the current matrix (null = ok/empty). */
const matrixWarn = computed(() => matrixError(props.stage.matrix))

/** Commit edited matrix text → parsed axes map (or undefined when empty). */
function commitMatrix(text: string): void {
  emit('update-matrix', parseMatrix(text))
}

/** Commit a new branch-glob set, preserving the current events. */
function commitBranches(text: string): void {
  emit('update-when', normalizeWhen(parseBranches(text), currentEvents.value))
}

/** Toggle a trigger event, preserving the current branch globs. */
function toggleEvent(ev: WhenEvent): void {
  const events = toggleWhenEvent(currentEvents.value, ev)
  emit('update-when', normalizeWhen(props.stage.when?.branches ?? [], events))
}

// ─── Drag-to-reorder (within this stage) ──────────────────────────────────────

const dragFrom = ref<number | null>(null)
const dragOver = ref<number | null>(null)

function onDragStart(i: number): void {
  dragFrom.value = i
}

function onDragEnter(i: number): void {
  if (dragFrom.value !== null) dragOver.value = i
}

function onDrop(i: number): void {
  const from = dragFrom.value
  if (from !== null && from !== i) emit('reorder-job', { from, to: i })
  resetDrag()
}

function resetDrag(): void {
  dragFrom.value = null
  dragOver.value = null
}
</script>

<template>
  <div class="stage-col">
    <!-- Stage header -->
    <div class="stage-header">
      <span class="stage-index" aria-hidden="true">{{ stageLabel(stageIndex) }}</span>
      <span class="stage-name-text">{{ stage.name }}</span>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-deps-btn"
        :class="{ 'stage-deps-btn--active': depsOpen || currentNeeds.length > 0 }"
        :aria-label="`编辑阶段 ${stage.name} 的依赖`"
        :aria-expanded="depsOpen"
        title="编辑依赖(DAG)"
        @click="depsOpen = !depsOpen"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="6" cy="6" r="2.4"/><circle cx="6" cy="18" r="2.4"/><circle cx="18" cy="12" r="2.4"/>
          <path d="M8 6.6c5 0 3 5.4 8 5.4M8 17.4c5 0 3-5.4 8-5.4"/>
        </svg>
        依赖<span v-if="currentNeeds.length" class="stage-deps-count">{{ currentNeeds.length }}</span>
      </button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-deps-btn"
        :class="{ 'stage-deps-btn--active': settingsOpen || hasStageRules }"
        :aria-label="`编辑阶段 ${stage.name} 的条件与审批门`"
        :aria-expanded="settingsOpen"
        title="条件执行 / 审批门"
        @click="settingsOpen = !settingsOpen"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
        条件<span v-if="hasStageRules" class="stage-deps-count stage-deps-count--dot" aria-hidden="true"></span>
      </button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-add-job"
        :aria-label="`在阶段 ${stage.name} 中添加任务`"
        @click="emit('add-job')"
      >+ 任务</button>
      <button
        v-if="stage.kind !== 'source'"
        class="stage-del"
        :aria-label="`删除阶段 ${stage.name}`"
        title="删除此阶段"
        @click="emit('delete-stage')"
      >
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
          <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>
        </svg>
      </button>
    </div>

    <!-- Dependency chips (declared needs) -->
    <div v-if="currentNeeds.length" class="stage-needs-chips" aria-label="上游依赖">
      <span class="stage-needs-arrow" aria-hidden="true">⤳</span>
      <span v-for="label in needLabels" :key="label" class="stage-need-chip">{{ label }}</span>
    </div>

    <!-- Condition / gate chips (when · 8-5 / gate · 8-4) -->
    <div v-if="hasStageRules" class="stage-rule-chips" aria-label="阶段条件与审批门">
      <span v-if="whenChip" class="stage-rule-chip stage-rule-chip--when" title="仅满足条件时执行">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M20 6L9 17l-5-5"/></svg>
        {{ whenChip }}
      </span>
      <span v-if="stage.gate" class="stage-rule-chip stage-rule-chip--gate" title="进入前需人工审批">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
        审批门
      </span>
      <span v-if="matrixChip" class="stage-rule-chip stage-rule-chip--matrix" title="矩阵展开:并行多个 cell">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
        {{ matrixChip }}
      </span>
      <span v-if="servicesChip" class="stage-rule-chip stage-rule-chip--svc" title="旁挂服务(同网按服务名互访)">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg>
        {{ servicesChip }}
      </span>
      <span v-if="postChip" class="stage-rule-chip stage-rule-chip--post" title="后置步骤(无论成败按条件跑)">
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true"><path d="M21 12a9 9 0 1 1-6.2-8.5"/><path d="M21 3v6h-6"/></svg>
        {{ postChip }}
      </span>
    </div>

    <!-- Dependency editor popover -->
    <div v-if="depsOpen && stage.kind !== 'source'" class="deps-popover" role="group" aria-label="阶段依赖编辑">
      <div class="deps-popover-title">依赖的上游阶段</div>
      <p v-if="depChoices.length === 0" class="deps-empty">没有可选的上游阶段</p>
      <label v-for="opt in depChoices" :key="opt.id" class="deps-option">
        <input
          type="checkbox"
          :checked="currentNeeds.includes(opt.id)"
          @change="toggleDep(opt.id)"
        />
        <span>{{ opt.name }}</span>
      </label>
      <div class="deps-divider"></div>
      <label class="deps-option deps-option--toggle">
        <input
          type="checkbox"
          :checked="stage.allowFailure === true"
          @change="emit('update-allow-failure', ($event.target as HTMLInputElement).checked)"
        />
        <span>失败不阻断下游(allowFailure)</span>
      </label>
      <p class="deps-hint">留空 = 无显式依赖;全流水线无依赖时按从左到右线性执行</p>
    </div>

    <!-- Stage settings popover (when 条件 + gate 审批门) -->
    <div v-if="settingsOpen && stage.kind !== 'source'" class="deps-popover settings-popover" role="group" aria-label="阶段条件与审批门编辑">
      <div class="deps-popover-title">条件执行(when)</div>
      <label class="settings-field-label" :for="`branches-${stage.id}`">分支(glob,空格/逗号分隔)</label>
      <input
        :id="`branches-${stage.id}`"
        class="settings-input"
        type="text"
        :value="branchText"
        placeholder="如 main release/*"
        @change="commitBranches(($event.target as HTMLInputElement).value)"
      />
      <div class="settings-events-label">触发事件</div>
      <div class="settings-events">
        <label v-for="ev in WHEN_EVENTS" :key="ev" class="settings-event">
          <input
            type="checkbox"
            :checked="currentEvents.includes(ev)"
            @change="toggleEvent(ev)"
          />
          <span>{{ WHEN_EVENT_LABELS[ev] }}</span>
        </label>
      </div>
      <p class="deps-hint">两者都留空 = 始终执行;不满足时本阶段及下游跳过(不计失败)</p>

      <div class="deps-divider"></div>
      <div class="deps-popover-title">审批门(gate)</div>
      <label class="deps-option deps-option--toggle">
        <input
          type="checkbox"
          :checked="stage.gate === true"
          @change="emit('update-gate', ($event.target as HTMLInputElement).checked)"
        />
        <span>进入本阶段前需人工审批</span>
      </label>
      <p class="deps-hint">开启后运行将暂停在此,等待批准/拒绝</p>

      <div class="deps-divider"></div>
      <div class="deps-popover-title">矩阵构建(matrix)</div>
      <label class="settings-field-label" :for="`matrix-${stage.id}`">轴(每行一个:<code>名称: 值1, 值2</code>)</label>
      <textarea
        :id="`matrix-${stage.id}`"
        class="settings-input settings-textarea"
        rows="3"
        :value="matrixText"
        placeholder="go: 1.21, 1.22&#10;os: linux"
        @change="commitMatrix(($event.target as HTMLTextAreaElement).value)"
      ></textarea>
      <p v-if="matrixWarn" class="settings-warn" role="alert">{{ matrixWarn }}</p>
      <p class="deps-hint">展开成并行 cell(笛卡尔积),各注入 <code>MATRIX_&lt;轴名&gt;</code> 环境变量;空 = 不展开</p>

      <div class="deps-divider"></div>
      <div class="deps-popover-title">旁挂服务(services)</div>
      <StageServicesEditor
        :services="stage.services"
        :stage-id="stage.id"
        @update="emit('update-services', $event)"
      />
      <p class="deps-hint">测试旁挂 DB/redis:与脚本容器同网,脚本里按服务名互访(如 <code>psql -h testdb</code>)</p>

      <div class="deps-divider"></div>
      <div class="deps-popover-title">后置步骤(post)</div>
      <StagePostEditor
        :steps="stage.post"
        :stage-id="stage.id"
        @update="emit('update-post', $event)"
      />
      <p class="deps-hint">阶段 job 跑完后按条件执行(同工作区),用于清理/通知/归档;空 = 无</p>
    </div>

    <!-- Job cards -->
    <JobCard
      v-for="(job, i) in stage.jobs"
      :key="job.id"
      :job="job"
      :stage-kind="stage.kind"
      :selected="job.id === selectedJobId"
      :dragging="dragFrom === i"
      :drag-over="dragOver === i && dragFrom !== i"
      @select="emit('select-job', job.id)"
      @delete="emit('delete-job', job.id)"
      @dragstart="onDragStart(i)"
      @dragend="resetDrag"
      @dragenter="onDragEnter(i)"
      @drop="onDrop(i)"
    />

    <!-- Add job trigger (source stage has no add-job since it's preset) -->
    <button
      v-if="stage.kind !== 'source'"
      class="add-job-btn"
      :aria-label="`在阶段 ${stage.name} 末尾添加任务`"
      @click="emit('add-job')"
    >+ 添加任务</button>
  </div>
</template>

<style scoped>
.stage-col {
  position: relative;
}

.stage-name-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stage-deps-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  padding: 0 7px;
  background: none;
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-dim);
  font: inherit;
  font-size: 0.7rem;
  font-weight: 500;
  cursor: pointer;
  transition: border-color var(--duration-fast), color var(--duration-fast), background-color var(--duration-fast);
}
.stage-deps-btn:hover { border-color: var(--color-primary); color: var(--color-primary); }
.stage-deps-btn--active { border-color: var(--color-primary); color: var(--color-primary); background: var(--color-primary-soft); }
.stage-deps-btn:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }

.stage-deps-count {
  display: inline-grid;
  place-items: center;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 7px;
  background: var(--color-primary);
  color: #fff;
  font-size: 0.6rem;
  font-weight: 700;
}

.stage-needs-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  margin: 2px 0 8px;
}
.stage-needs-arrow { color: var(--color-faint); font-size: 0.8rem; }
.stage-need-chip {
  font-size: 0.66rem;
  font-weight: 600;
  padding: 1px 7px;
  border-radius: 8px;
  background: var(--color-cyan-soft);
  color: var(--color-cyan);
}

.deps-popover {
  position: absolute;
  top: 40px;
  right: 6px;
  z-index: 40;
  width: 210px;
  padding: 11px 12px;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.22);
}
.deps-popover-title {
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.02em;
  color: var(--color-faint);
  margin-bottom: 8px;
}
.deps-empty { font-size: 0.74rem; color: var(--color-faint); font-style: italic; margin: 0; }
.deps-option {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 3px 0;
  font-size: 0.78rem;
  color: var(--color-text);
  cursor: pointer;
}
.deps-option input { width: 14px; height: 14px; accent-color: var(--color-primary); cursor: pointer; }
.deps-option--toggle { color: var(--color-dim); }
.deps-divider { height: 1px; background: var(--color-border); margin: 8px 0; }
.deps-hint { margin: 7px 0 0; font-size: 0.68rem; color: var(--color-faint); line-height: 1.4; }

/* ─── Condition / gate chips ──────────────────────────────────────────────── */
.stage-deps-count--dot {
  min-width: 7px;
  width: 7px;
  height: 7px;
  padding: 0;
  border-radius: 50%;
}
.stage-rule-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: -4px 0 8px;
}
.stage-rule-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.66rem;
  font-weight: 600;
  padding: 1px 7px 1px 6px;
  border-radius: 8px;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.stage-rule-chip svg { flex: none; }
.stage-rule-chip--when { background: var(--color-primary-soft); color: var(--color-primary); }
.stage-rule-chip--gate { background: var(--color-amber-soft, rgba(217, 119, 6, 0.14)); color: var(--color-amber, #b45309); }
.stage-rule-chip--matrix { background: var(--color-violet-soft, rgba(124, 58, 237, 0.13)); color: var(--color-violet, #6d28d9); }
.stage-rule-chip--svc { background: var(--color-cyan-soft, rgba(8, 145, 178, 0.13)); color: var(--color-cyan, #0e7490); }
.stage-rule-chip--post { background: var(--color-amber-soft, rgba(217, 119, 6, 0.14)); color: var(--color-amber, #b45309); }

/* ─── Settings popover fields ─────────────────────────────────────────────── */
.settings-popover { width: 232px; }
.settings-field-label,
.settings-events-label {
  display: block;
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--color-dim);
  margin: 2px 0 5px;
}
.settings-events-label { margin-top: 10px; }
.settings-input {
  width: 100%;
  height: 28px;
  padding: 0 8px;
  font: inherit;
  font-size: 0.76rem;
  color: var(--color-text);
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  box-sizing: border-box;
  transition: border-color var(--duration-fast);
}
.settings-input:focus { outline: none; border-color: var(--color-primary); }
.settings-textarea { height: auto; padding: 6px 8px; line-height: 1.5; resize: vertical; font-family: var(--font-mono, ui-monospace, monospace); }
.settings-warn { margin: 5px 0 0; font-size: 0.68rem; color: var(--color-danger, #dc2626); line-height: 1.4; }
.settings-events { display: flex; flex-wrap: wrap; gap: 6px 12px; }
.settings-event {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 0.76rem;
  color: var(--color-text);
  cursor: pointer;
}
.settings-event input { width: 14px; height: 14px; accent-color: var(--color-primary); cursor: pointer; }
</style>

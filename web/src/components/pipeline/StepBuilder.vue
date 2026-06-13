<script setup lang="ts">
/**
 * StepBuilder — 节点内可视化步骤构建器(低代码 · Tier 3)。
 *
 * 用「加步骤」+ 拖拽/上下移排序拼出脚本块,实时编译成现有可执行 config 键
 * (commands 多行 / artifactPath 多行),经 emit('update') 落库。绝不新增执行语义:
 * 编译/反解析全在 stepCompile.ts 的纯函数里(见其文件头契约)。
 *
 * 父级(JobDrawer)负责持有 image/workDir 等节点级字段;本组件只管「步骤」那部分,
 * 把 commands + artifactPath 这两个键回传。
 */
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  type StepBlock,
  type StepKind,
  STEP_KIND_META,
  nextStepId,
  parseSteps,
  compileSteps,
} from './stepCompile'

const { t } = useI18n()

const props = defineProps<{
  /** 当前节点的完整 config(用于初始反解析) */
  config: Record<string, string>
}>()

const emit = defineEmits<{
  /** 步骤变更后回传编译出的 config 片段(只含 commands / artifactPath) */
  (e: 'update', patch: { commands: string; artifactPath: string }): void
}>()

const steps = ref<StepBlock[]>([])

/** 初次 / 切换节点时从 config 反解析步骤(允许有损,空则起手给一个空命令步骤)。 */
function hydrate(config: Record<string, string>): void {
  const parsed = parseSteps(config)
  steps.value = parsed.length > 0 ? parsed : [blank('command')]
}

/**
 * 只在「外部」变更时重建步骤(切换节点、原始视图改 commands 等)。我们自己 flush 回传后,
 * 父级会把同一份 config 透传回来 —— 这种「自激」更新不能重建,否则正在编辑的空步骤(空 env/空 cd,
 * 编译成空串)会被反解析丢掉。判据:incoming 的 commands/artifactPath 与当前步骤的编译结果一致 → 自激,跳过。
 */
let hydrated = false
watch(
  () => props.config,
  (next) => {
    if (hydrated) {
      const mine = compileSteps(steps.value)
      if ((next.commands ?? '') === mine.commands && (next.artifactPath ?? '') === mine.artifactPath) {
        return
      }
    }
    hydrated = true
    hydrate(next)
  },
  { immediate: true },
)

function blank(kind: StepKind): StepBlock {
  return { id: nextStepId(), kind }
}

/** 编译当前步骤并回传给父级落库。 */
function flush(): void {
  emit('update', compileSteps(steps.value))
}

// ─── 增删步骤 ──────────────────────────────────────────────────────────────────

const addMenuOpen = ref(false)

const addOptions = computed<ReadonlyArray<{ kind: StepKind; label: string; desc: string }>>(() => [
  { kind: 'command', label: STEP_KIND_META.command.label, desc: t('pipelineJob.sbAddCommandDesc') },
  { kind: 'env', label: STEP_KIND_META.env.label, desc: t('pipelineJob.sbAddEnvDesc') },
  { kind: 'workDir', label: STEP_KIND_META.workDir.label, desc: t('pipelineJob.sbAddWorkDirDesc') },
  { kind: 'condition', label: STEP_KIND_META.condition.label, desc: t('pipelineJob.sbAddConditionDesc') },
  { kind: 'artifact', label: STEP_KIND_META.artifact.label, desc: t('pipelineJob.sbAddArtifactDesc') },
])

function addStep(kind: StepKind): void {
  steps.value = [...steps.value, blank(kind)]
  addMenuOpen.value = false
  flush()
}

function removeStep(id: string): void {
  steps.value = steps.value.filter((s) => s.id !== id)
  flush()
}

function patchStep(id: string, patch: Partial<StepBlock>): void {
  steps.value = steps.value.map((s) => (s.id === id ? { ...s, ...patch } : s))
}

// ─── 排序:上下移按钮 + 原生 HTML5 拖拽 ─────────────────────────────────────────

function move(index: number, delta: number): void {
  const next = index + delta
  if (next < 0 || next >= steps.value.length) return
  const arr = [...steps.value]
  const [item] = arr.splice(index, 1)
  arr.splice(next, 0, item)
  steps.value = arr
  flush()
}

const dragIndex = ref<number | null>(null)
const overIndex = ref<number | null>(null)

function onDragStart(index: number): void {
  dragIndex.value = index
}

function onDragOver(index: number, e: DragEvent): void {
  e.preventDefault()
  overIndex.value = index
}

function onDrop(index: number): void {
  const from = dragIndex.value
  dragIndex.value = null
  overIndex.value = null
  if (from === null || from === index) return
  const arr = [...steps.value]
  const [item] = arr.splice(from, 1)
  arr.splice(index, 0, item)
  steps.value = arr
  flush()
}

function onDragEnd(): void {
  dragIndex.value = null
  overIndex.value = null
}

const stepCount = computed(() => steps.value.length)
</script>

<template>
  <div class="step-builder">
    <div class="sb-head">
      <span class="sb-head-label">{{ t('pipelineJob.sbVisualSteps') }}</span>
      <span class="sb-count" :aria-label="t('pipelineJob.sbStepCount')">{{ stepCount }}</span>
    </div>

    <ol class="sb-list">
      <li
        v-for="(step, index) in steps"
        :key="step.id"
        class="sb-step"
        :class="[
          `sb-step--${step.kind}`,
          { 'sb-step--dragging': dragIndex === index, 'sb-step--over': overIndex === index && dragIndex !== index },
        ]"
        draggable="true"
        @dragstart="onDragStart(index)"
        @dragover="onDragOver(index, $event)"
        @drop="onDrop(index)"
        @dragend="onDragEnd"
      >
        <div class="sb-step-bar">
          <span class="sb-grip" aria-hidden="true" :title="t('pipelineJob.sbDragSort')">
            <svg width="10" height="14" viewBox="0 0 10 14" fill="currentColor">
              <circle cx="2.5" cy="2" r="1.3" /><circle cx="7.5" cy="2" r="1.3" />
              <circle cx="2.5" cy="7" r="1.3" /><circle cx="7.5" cy="7" r="1.3" />
              <circle cx="2.5" cy="12" r="1.3" /><circle cx="7.5" cy="12" r="1.3" />
            </svg>
          </span>
          <span class="sb-kind">{{ STEP_KIND_META[step.kind].label }}</span>
          <span class="sb-order">{{ index + 1 }}</span>
          <span class="sb-step-actions">
            <button
              class="sb-move"
              :disabled="index === 0"
              :aria-label="t('pipelineJob.sbMoveUp', { n: index + 1 })"
              @click="move(index, -1)"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M18 15l-6-6-6 6" /></svg>
            </button>
            <button
              class="sb-move"
              :disabled="index === steps.length - 1"
              :aria-label="t('pipelineJob.sbMoveDown', { n: index + 1 })"
              @click="move(index, 1)"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
            </button>
            <button
              class="sb-del"
              :aria-label="t('pipelineJob.sbDelStep', { n: index + 1 })"
              @click="removeStep(step.id)"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12" /></svg>
            </button>
          </span>
        </div>

        <!-- 运行命令 -->
        <textarea
          v-if="step.kind === 'command'"
          :value="step.command ?? ''"
          class="sb-input sb-textarea is-mono"
          rows="2"
          placeholder="npm ci&#10;npm run build"
          :aria-label="t('pipelineJob.sbCommandAria', { n: index + 1 })"
          @input="patchStep(step.id, { command: ($event.target as HTMLTextAreaElement).value })"
          @blur="flush"
        ></textarea>

        <!-- 设环境变量 -->
        <div v-else-if="step.kind === 'env'" class="sb-env">
          <input
            :value="step.envKey ?? ''"
            class="sb-input is-mono"
            type="text"
            placeholder="KEY"
            :aria-label="t('pipelineJob.sbEnvKeyAria', { n: index + 1 })"
            @input="patchStep(step.id, { envKey: ($event.target as HTMLInputElement).value })"
            @blur="flush"
          />
          <span class="sb-eq" aria-hidden="true">=</span>
          <input
            :value="step.envValue ?? ''"
            class="sb-input is-mono"
            type="text"
            placeholder="value"
            :aria-label="t('pipelineJob.sbEnvValueAria', { n: index + 1 })"
            @input="patchStep(step.id, { envValue: ($event.target as HTMLInputElement).value })"
            @blur="flush"
          />
        </div>

        <!-- 切目录 -->
        <input
          v-else-if="step.kind === 'workDir'"
          :value="step.dir ?? ''"
          class="sb-input is-mono"
          type="text"
          placeholder="frontend"
          :aria-label="t('pipelineJob.sbDirAria', { n: index + 1 })"
          @input="patchStep(step.id, { dir: ($event.target as HTMLInputElement).value })"
          @blur="flush"
        />

        <!-- 条件守卫 -->
        <div v-else-if="step.kind === 'condition'" class="sb-cond">
          <input
            :value="step.condition ?? ''"
            class="sb-input is-mono"
            type="text"
            placeholder='[ "$BRANCH" = "main" ]'
            :aria-label="t('pipelineJob.sbCondAria', { n: index + 1 })"
            @input="patchStep(step.id, { condition: ($event.target as HTMLInputElement).value })"
            @blur="flush"
          />
          <p class="sb-cond-hint">
            {{ t('pipelineJob.sbCondHintPre') }}<strong>{{ t('pipelineJob.sbCondHintStrong') }}</strong>{{ t('pipelineJob.sbCondHintPost') }}
            <code>[ -f package.json ]</code>
          </p>
        </div>

        <!-- 上传产物 -->
        <input
          v-else
          :value="step.artifact ?? ''"
          class="sb-input is-mono"
          type="text"
          placeholder="frontend/dist"
          :aria-label="t('pipelineJob.sbArtifactAria', { n: index + 1 })"
          @input="patchStep(step.id, { artifact: ($event.target as HTMLInputElement).value })"
          @blur="flush"
        />
      </li>
    </ol>

    <div v-if="steps.length === 0" class="sb-empty">{{ t('pipelineJob.sbEmpty') }}</div>

    <div class="sb-add">
      <button
        class="sb-add-btn"
        :class="{ 'sb-add-btn--open': addMenuOpen }"
        :aria-expanded="addMenuOpen"
        @click="addMenuOpen = !addMenuOpen"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
        {{ t('pipelineJob.sbAddStep') }}
      </button>
      <div v-if="addMenuOpen" class="sb-add-menu" role="menu">
        <button
          v-for="opt in addOptions"
          :key="opt.kind"
          class="sb-add-item"
          :class="`sb-add-item--${opt.kind}`"
          role="menuitem"
          @click="addStep(opt.kind)"
        >
          <span class="sb-add-dot" aria-hidden="true"></span>
          <span class="sb-add-body">
            <span class="sb-add-name">{{ opt.label }}</span>
            <span class="sb-add-desc">{{ opt.desc }}</span>
          </span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.step-builder {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

.sb-head {
  display: flex;
  align-items: center;
  gap: 7px;
}

.sb-head-label {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.sb-count {
  display: inline-grid;
  place-items: center;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 0.66rem;
  font-weight: 700;
}

.sb-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* Each step is a left-accented card; the accent encodes the kind. */
.sb-step {
  position: relative;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--accent, var(--color-border-strong));
  border-radius: var(--rounded-md);
  padding: 8px 9px 9px;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast),
    opacity var(--duration-fast), transform var(--duration-fast);
}

.sb-step--command { --accent: var(--color-primary); }
.sb-step--env { --accent: var(--color-cyan); }
.sb-step--workDir { --accent: var(--color-amber); }
.sb-step--artifact { --accent: var(--color-green); }
.sb-step--condition { --accent: var(--color-red); }

.sb-step--dragging {
  opacity: 0.5;
}

.sb-step--over {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px var(--color-primary-soft);
  transform: translateY(1px);
}

.sb-step-bar {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 7px;
}

.sb-grip {
  color: var(--color-faint);
  cursor: grab;
  display: inline-flex;
}

.sb-grip:active {
  cursor: grabbing;
}

.sb-kind {
  font-size: 0.76rem;
  font-weight: 650;
  color: var(--accent, var(--color-text));
}

.sb-order {
  display: inline-grid;
  place-items: center;
  min-width: 15px;
  height: 15px;
  padding: 0 3px;
  border-radius: 7px;
  background: var(--color-border);
  color: var(--color-faint);
  font-size: 0.62rem;
  font-weight: 700;
  font-family: var(--font-mono);
}

.sb-step-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.sb-move,
.sb-del {
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  background: none;
  border: none;
  border-radius: 4px;
  color: var(--color-faint);
  cursor: pointer;
  padding: 0;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.sb-move:hover:not(:disabled) {
  color: var(--color-text);
  background: var(--color-border);
}

.sb-move:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.sb-del:hover {
  color: var(--color-red);
  background: var(--color-red-soft);
}

.sb-move:focus-visible,
.sb-del:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.sb-input {
  width: 100%;
  height: 30px;
  background: var(--color-bg, var(--color-inset));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  padding: 0 9px;
  color: var(--color-text);
  font: inherit;
  font-size: 0.8rem;
  transition: border-color var(--duration-fast), box-shadow var(--duration-fast);
}

.sb-textarea {
  height: auto;
  min-height: 52px;
  padding: 7px 9px;
  line-height: 1.5;
  resize: vertical;
}

.is-mono {
  font-family: var(--font-mono);
  font-size: 0.78rem;
}

.sb-input:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px var(--color-primary-soft);
}

.sb-env {
  display: grid;
  grid-template-columns: 1fr auto 1.4fr;
  align-items: center;
  gap: 6px;
}

.sb-eq {
  color: var(--color-faint);
  font-family: var(--font-mono);
  font-weight: 700;
}

.sb-cond {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.sb-cond-hint {
  margin: 0;
  font-size: 0.68rem;
  line-height: 1.45;
  color: var(--color-faint);
}

.sb-cond-hint strong {
  color: var(--color-red);
  font-weight: 600;
}

.sb-cond-hint code {
  font-family: var(--font-mono);
  font-size: 0.66rem;
  padding: 0 3px;
  border-radius: 3px;
  background: var(--color-border);
  color: var(--color-dim);
}

.sb-empty {
  font-size: 0.78rem;
  color: var(--color-faint);
  font-style: italic;
  padding: 4px 0;
}

.sb-add {
  position: relative;
}

.sb-add-btn {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 11px;
  background: none;
  border: 1px dashed var(--color-border-strong);
  border-radius: var(--rounded-md);
  color: var(--color-primary);
  font: inherit;
  font-size: 0.78rem;
  font-weight: 600;
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.sb-add-btn:hover,
.sb-add-btn--open {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

.sb-add-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.sb-add-menu {
  margin-top: 6px;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
  padding: 7px;
  background: var(--color-inset);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded);
}

.sb-add-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 9px;
  background: none;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  text-align: left;
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.sb-add-item:hover {
  border-color: var(--item-accent, var(--color-primary));
  background: var(--color-border);
}

.sb-add-item:focus-visible {
  outline: 2px solid var(--item-accent, var(--color-primary));
  outline-offset: 1px;
}

.sb-add-item--command { --item-accent: var(--color-primary); }
.sb-add-item--env { --item-accent: var(--color-cyan); }
.sb-add-item--workDir { --item-accent: var(--color-amber); }
.sb-add-item--condition { --item-accent: var(--color-red); }
.sb-add-item--artifact { --item-accent: var(--color-green); }

.sb-add-dot {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  margin-top: 4px;
  border-radius: 50%;
  background: var(--item-accent, var(--color-primary));
}

.sb-add-body {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.sb-add-name {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-text);
}

.sb-add-desc {
  font-size: 0.68rem;
  color: var(--color-faint);
  line-height: 1.3;
}
</style>

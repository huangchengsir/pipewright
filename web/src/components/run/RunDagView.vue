<!--
  RunDagView — DAG topology view for pipeline run details (FR-8-8).

  Data source: fetches GET /api/projects/{projectId}/pipeline to obtain the stage
  graph (stages with needs), then maps run.steps onto those stages by name.
  Falls back to linear chain when no stage declares needs, or when the pipeline
  spec is unavailable (network error, 404).

  Features:
  - SVG bezier edge overlay (same technique as PipelineCanvas.vue)
  - Per-stage status badge with animated running/failed states
  - Per-job/step expandable row with icon + duration
  - Approval gate badge for gate=true stages
  - SSE live-update: steps prop is reactive → DAG re-derives automatically
  - Reduced-motion: all animations disabled via media query
  - Keyboard accessible: expandable rows use button role
-->
<script setup lang="ts">
import {
  ref,
  computed,
  onMounted,
  onBeforeUnmount,
  watch,
  nextTick,
} from 'vue'
import type { RunStep, StepStatus } from '../../api/runs'
import type { PipelineStage } from '../../api/pipeline'
import { getPipeline } from '../../api/pipeline'
import { HttpError } from '../../api/http'
import {
  buildRunStages,
  buildDagEdges,
  topoSort,
  type RunStage,
} from './runDag'

// ─── Props ───────────────────────────────────────────────────────────────────

const props = defineProps<{
  /** Project ID to fetch the pipeline spec from. */
  projectId: string
  /** Steps from the live run (reactive — updates via SSE). */
  steps: RunStep[]
  /** Run status string (for edge color cues). */
  runStatus: string
}>()

// ─── Pipeline spec loading ────────────────────────────────────────────────────

const pipelineStages = ref<PipelineStage[]>([])
const specLoading = ref(true)
const specError = ref(false)

async function loadSpec(): Promise<void> {
  specLoading.value = true
  specError.value = false
  try {
    const dto = await getPipeline(props.projectId)
    pipelineStages.value = dto.stages
  } catch (err) {
    // Graceful fallback — we'll render a linear DAG from steps alone.
    // 404 = project has no pipeline spec yet; other errors treated same way.
    if (!(err instanceof HttpError) || err.status !== 404) {
      specError.value = true
    }
    pipelineStages.value = []
  } finally {
    specLoading.value = false
  }
}

onMounted(() => { void loadSpec() })

// ─── Derived DAG stages ───────────────────────────────────────────────────────

const dagStages = computed<RunStage[]>(() => {
  if (specLoading.value) return []
  return topoSort(buildRunStages(props.steps, pipelineStages.value))
})

const dagEdges = computed(() => buildDagEdges(dagStages.value))

// ─── Expandable step rows ─────────────────────────────────────────────────────

/** Set of stage IDs whose steps are expanded. */
const expanded = ref(new Set<string>())

function toggleExpand(stageId: string): void {
  const s = new Set(expanded.value)
  if (s.has(stageId)) s.delete(stageId)
  else s.add(stageId)
  expanded.value = s
}

function isExpanded(stageId: string): boolean {
  return expanded.value.has(stageId)
}

// Auto-expand stages with running or failed steps
watch(
  dagStages,
  (stages) => {
    const s = new Set(expanded.value)
    for (const stage of stages) {
      if (stage.status === 'running' || stage.status === 'failed') {
        s.add(stage.id)
      }
    }
    expanded.value = s
  },
  { immediate: true },
)

// ─── SVG edge overlay ─────────────────────────────────────────────────────────

const flowRef = ref<HTMLElement | null>(null)
const overlay = ref({ w: 0, h: 0 })
const edgePaths = ref<{ d: string; status: string }[]>([])

function edgeStatus(fromId: string): string {
  const stage = dagStages.value.find((s) => s.id === fromId)
  return stage?.status ?? 'pending'
}

const CONNECT_X_OFFSET = 14  // px from right edge of "from" stage card
const CONNECT_Y_CENTER = 28  // px from top of stage card to header center

function measureEdges(): void {
  const flow = flowRef.value
  if (!flow) return
  const cards = Array.from(flow.querySelectorAll<HTMLElement>('.dag-stage-card'))
  const byId = new Map<string, HTMLElement>()
  dagStages.value.forEach((s, i) => {
    if (cards[i]) byId.set(s.id, cards[i])
  })
  overlay.value = { w: flow.scrollWidth, h: flow.scrollHeight }
  const paths: { d: string; status: string }[] = []
  for (const e of dagEdges.value) {
    const a = byId.get(e.from)
    const b = byId.get(e.to)
    if (!a || !b) continue
    const x1 = a.offsetLeft + a.offsetWidth - CONNECT_X_OFFSET
    const y1 = a.offsetTop + CONNECT_Y_CENTER
    const x2 = b.offsetLeft + CONNECT_X_OFFSET
    const y2 = b.offsetTop + CONNECT_Y_CENTER
    const dx = Math.max(30, Math.abs(x2 - x1) * 0.5)
    paths.push({
      d: `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`,
      status: edgeStatus(e.from),
    })
  }
  edgePaths.value = paths
}

let ro: ResizeObserver | null = null
onMounted(() => {
  measureEdges()
  ro = new ResizeObserver(() => void nextTick(measureEdges))
  if (flowRef.value) ro.observe(flowRef.value)
})
onBeforeUnmount(() => ro?.disconnect())
watch(() => [dagStages.value, dagEdges.value], () => nextTick(measureEdges), { deep: true })
watch(specLoading, (v) => { if (!v) nextTick(measureEdges) })

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatDuration(ms: number | null): string {
  if (ms === null) return ''
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

function stageDuration(stage: RunStage): string {
  const total = stage.steps.reduce<number>((sum, s) => sum + (s.durationMs ?? 0), 0)
  return total > 0 ? formatDuration(total) : ''
}

// Status → visual config
interface StatusVis {
  dotColor: string
  label: string
  cardClass: string
  pulse: boolean
}

function stageVis(status: StepStatus): StatusVis {
  switch (status) {
    case 'success': return { dotColor: 'var(--color-green)', label: '成功',  cardClass: 'card--success', pulse: false }
    case 'running': return { dotColor: 'var(--color-amber)', label: '进行中', cardClass: 'card--running', pulse: true  }
    case 'failed':  return { dotColor: 'var(--color-red)',   label: '失败',  cardClass: 'card--failed',  pulse: false }
    case 'skipped': return { dotColor: 'var(--color-faint)', label: '跳过',  cardClass: 'card--skipped', pulse: false }
    default:        return { dotColor: 'var(--color-faint)', label: '等待',  cardClass: 'card--pending', pulse: false }
  }
}

function stepVis(status: StepStatus): { color: string; icon: 'check' | 'spinner' | 'x' | 'skip' | 'dot' } {
  switch (status) {
    case 'success': return { color: 'var(--color-green)', icon: 'check'   }
    case 'running': return { color: 'var(--color-amber)', icon: 'spinner' }
    case 'failed':  return { color: 'var(--color-red)',   icon: 'x'       }
    case 'skipped': return { color: 'var(--color-faint)', icon: 'skip'    }
    default:        return { color: 'var(--color-faint)', icon: 'dot'     }
  }
}

function edgeClass(status: string): string {
  switch (status) {
    case 'success': return 'edge--ok'
    case 'running': return 'edge--running'
    case 'failed':  return 'edge--bad'
    default:        return 'edge--pending'
  }
}
</script>

<template>
  <section class="dag-root" aria-label="流水线 DAG 拓扑视图">

    <!-- Loading: spec fetch in progress -->
    <div v-if="specLoading" class="dag-loading" aria-busy="true">
      <div class="dag-skel dag-skel--stage" />
      <div class="dag-skel dag-skel--stage" />
      <div class="dag-skel dag-skel--stage" />
    </div>

    <!-- Loaded -->
    <template v-else>

      <!-- Warning: pipeline spec fetch failed (non-404) — still renders linear DAG -->
      <div v-if="specError" class="dag-warn" role="status">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
          <path d="M12 9v4M12 17h.01"/>
        </svg>
        流水线配置加载失败,按步骤顺序展示
      </div>

      <!-- DAG canvas: horizontally scrollable -->
      <div class="dag-canvas" role="region" aria-label="阶段图">
        <!-- Edge overlay SVG -->
        <svg
          v-if="edgePaths.length > 0"
          class="dag-edge-overlay"
          :width="overlay.w"
          :height="overlay.h"
          :viewBox="`0 0 ${overlay.w} ${overlay.h}`"
          aria-hidden="true"
        >
          <!-- Base edge (status-colored) -->
          <path
            v-for="(ep, i) in edgePaths"
            :key="`b${i}`"
            :class="['dag-edge', edgeClass(ep.status)]"
            :d="ep.d"
          />
          <!-- Animated flow pulse on active edges -->
          <path
            v-for="(ep, i) in edgePaths"
            :key="`f${i}`"
            :class="['dag-edge-flow', edgeClass(ep.status)]"
            :d="ep.d"
          />
        </svg>

        <!-- Stage columns -->
        <div ref="flowRef" class="dag-flow" role="list" aria-label="流水线阶段列表">
          <article
            v-for="stage in dagStages"
            :key="stage.id"
            class="dag-stage-card"
            :class="stageVis(stage.status).cardClass"
            role="listitem"
            :aria-label="`阶段 ${stage.name}: ${stageVis(stage.status).label}`"
          >
            <!-- Stage header -->
            <header class="stage-head">
              <!-- Status dot -->
              <span
                class="stage-dot"
                :class="{ 'dot--pulse': stageVis(stage.status).pulse }"
                :style="{ background: stageVis(stage.status).dotColor }"
                aria-hidden="true"
              />

              <!-- Stage name -->
              <span class="stage-name">{{ stage.name }}</span>

              <!-- Gate badge -->
              <span v-if="stage.gate" class="stage-gate" aria-label="需要人工审批">
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                  <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                </svg>
                门控
              </span>

              <!-- Duration -->
              <span v-if="stageDuration(stage)" class="stage-dur mono">{{ stageDuration(stage) }}</span>

              <!-- Expand/collapse toggle -->
              <button
                v-if="stage.steps.length > 0"
                class="stage-toggle"
                :aria-expanded="isExpanded(stage.id)"
                :aria-controls="`steps-${stage.id}`"
                :aria-label="isExpanded(stage.id) ? `收起阶段 ${stage.name} 步骤` : `展开阶段 ${stage.name} 步骤`"
                @click="toggleExpand(stage.id)"
              >
                <svg
                  class="toggle-chevron"
                  :class="{ 'toggle-chevron--open': isExpanded(stage.id) }"
                  width="11" height="11" viewBox="0 0 24 24" fill="none"
                  stroke="currentColor" stroke-width="2.4" aria-hidden="true"
                >
                  <path d="M6 9l6 6 6-6"/>
                </svg>
              </button>
            </header>

            <!-- Step count summary (always visible) -->
            <div class="stage-summary">
              <span class="stage-step-count">
                {{ stage.steps.length }} 步骤
              </span>
              <span v-if="stage.steps.some(s => s.status === 'failed')" class="stage-fail-count">
                · {{ stage.steps.filter(s => s.status === 'failed').length }} 失败
              </span>
            </div>

            <!-- Expanded step list -->
            <ul
              v-if="isExpanded(stage.id)"
              :id="`steps-${stage.id}`"
              class="stage-steps"
              role="list"
            >
              <li
                v-for="step in stage.steps"
                :key="step.id"
                class="step-row"
                :class="`step-row--${step.status}`"
                role="listitem"
              >
                <!-- Step icon -->
                <span
                  class="step-icon"
                  :style="{ color: stepVis(step.status).color }"
                  :aria-label="step.status"
                >
                  <!-- success -->
                  <svg v-if="stepVis(step.status).icon === 'check'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" aria-hidden="true">
                    <path d="M20 6 9 17l-5-5"/>
                  </svg>
                  <!-- running spinner -->
                  <span v-else-if="stepVis(step.status).icon === 'spinner'" class="step-spinner" aria-hidden="true" />
                  <!-- failed -->
                  <svg v-else-if="stepVis(step.status).icon === 'x'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" aria-hidden="true">
                    <path d="m18 6-12 12M6 6l12 12"/>
                  </svg>
                  <!-- skipped -->
                  <svg v-else-if="stepVis(step.status).icon === 'skip'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path d="M13 17l5-5-5-5M6 17l5-5-5-5"/>
                  </svg>
                  <!-- pending dot -->
                  <svg v-else width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
                    <circle cx="12" cy="12" r="3.5"/>
                  </svg>
                </span>

                <!-- Step name -->
                <span class="step-name">{{ step.name }}</span>

                <!-- Step duration -->
                <span v-if="step.durationMs !== null" class="step-dur mono">{{ formatDuration(step.durationMs) }}</span>
              </li>
            </ul>

            <!-- Empty stage placeholder -->
            <div v-if="stage.steps.length === 0" class="stage-empty">
              尚无步骤
            </div>
          </article>
        </div>
      </div>

    </template>
  </section>
</template>

<style scoped>
/* ─── root ─────────────────────────────────────────────────────────────────── */
.dag-root {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* ─── loading skeleton ─────────────────────────────────────────────────────── */
.dag-loading {
  display: flex;
  gap: 16px;
  overflow: hidden;
  padding: 4px 0;
}

.dag-skel {
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.05) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  border-radius: var(--rounded);
  animation: dag-shimmer 1.4s ease-in-out infinite;
}

@keyframes dag-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .dag-skel { animation: none; background: var(--color-inset); }
}

.dag-skel--stage {
  width: 180px;
  height: 80px;
  flex-shrink: 0;
}

/* ─── warning banner ───────────────────────────────────────────────────────── */
.dag-warn {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 0.76rem;
  color: var(--color-amber);
  padding: 6px 10px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded-md);
}

/* ─── canvas: positions the flow + the SVG overlay together ───────────────── */
.dag-canvas {
  position: relative;
  overflow-x: auto;
  overflow-y: visible;
  /* Ensure overlay can grow taller than the initial viewport */
  min-height: 120px;
}

.dag-edge-overlay {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  overflow: visible;
}

/* ─── edges ────────────────────────────────────────────────────────────────── */
.dag-edge {
  fill: none;
  stroke-width: 1.8;
}

.dag-edge.edge--ok      { stroke: var(--color-green); opacity: 0.7; }
.dag-edge.edge--running { stroke: var(--color-amber); opacity: 0.8; }
.dag-edge.edge--bad     { stroke: var(--color-red);   opacity: 0.8; }
.dag-edge.edge--pending { stroke: var(--color-border-strong); opacity: 1; }

/* Animated flow pulse */
.dag-edge-flow {
  fill: none;
  stroke-width: 1.8;
  stroke-dasharray: 5 12;
  opacity: 0.5;
  animation: dag-flow 1.5s linear infinite;
}

.dag-edge-flow.edge--ok      { stroke: var(--color-green); opacity: 0.5; }
.dag-edge-flow.edge--running { stroke: var(--color-amber); opacity: 0.7; }
.dag-edge-flow.edge--bad     { stroke: var(--color-red);   opacity: 0.5; }
.dag-edge-flow.edge--pending { display: none; }

@keyframes dag-flow {
  to { stroke-dashoffset: -34; }
}

@media (prefers-reduced-motion: reduce) {
  .dag-edge-flow { animation: none; display: none; }
}

/* ─── flow row ─────────────────────────────────────────────────────────────── */
.dag-flow {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  gap: 56px;        /* gap for edges to thread through */
  min-width: max-content;
  padding: 8px 4px 16px;
  position: relative;
  z-index: 1;
}

/* ─── stage card ───────────────────────────────────────────────────────────── */
.dag-stage-card {
  width: 200px;
  flex-shrink: 0;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg);
  box-shadow: var(--shadow);
  overflow: hidden;
  transition:
    border-color var(--duration-fast),
    box-shadow var(--duration-fast);
}

/* Status-specific card accents */
.card--success {
  border-color: var(--color-green-line);
}
.card--running {
  border-color: var(--color-amber-line);
  box-shadow: 0 0 0 1px var(--color-amber-soft), var(--shadow);
  animation: card-running-glow 2s ease-in-out infinite alternate;
}
.card--failed {
  border-color: var(--color-red-line);
  box-shadow: 0 0 0 1px var(--color-red-soft), var(--shadow);
}
.card--skipped {
  opacity: 0.6;
}
.card--pending {
  border-color: var(--color-border);
  opacity: 0.75;
}

@keyframes card-running-glow {
  from { box-shadow: 0 0 0 1px var(--color-amber-soft), var(--shadow); }
  to   { box-shadow: 0 0 0 3px var(--color-amber-soft), 0 0 12px oklch(83% 0.13 82 / 0.2), var(--shadow); }
}

@media (prefers-reduced-motion: reduce) {
  .card--running { animation: none; }
}

/* ─── stage header ─────────────────────────────────────────────────────────── */
.stage-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px 8px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
}

.stage-dot {
  width: 7px;
  height: 7px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.dot--pulse {
  animation: dot-pulse 1.1s ease-in-out infinite;
}

@keyframes dot-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50%       { opacity: 0.5; transform: scale(0.75); }
}

@media (prefers-reduced-motion: reduce) {
  .dot--pulse { animation: none; }
}

.stage-name {
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-text);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.stage-gate {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 0.65rem;
  font-weight: 600;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded-sm);
  padding: 1px 5px;
  flex-shrink: 0;
  white-space: nowrap;
}

.stage-dur {
  font-size: 0.68rem;
  color: var(--color-faint);
  flex-shrink: 0;
}

.stage-toggle {
  width: 20px;
  height: 20px;
  display: grid;
  place-items: center;
  background: none;
  border: none;
  color: var(--color-faint);
  cursor: pointer;
  flex-shrink: 0;
  border-radius: var(--rounded-sm);
  padding: 0;
  transition: color var(--duration-fast), background-color var(--duration-fast);
}

.stage-toggle:hover {
  color: var(--color-text);
  background: var(--color-border);
}

.stage-toggle:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.toggle-chevron {
  transition: transform var(--duration-fast);
}

.toggle-chevron--open {
  transform: rotate(180deg);
}

@media (prefers-reduced-motion: reduce) {
  .toggle-chevron { transition: none; }
}

/* ─── stage summary row ────────────────────────────────────────────────────── */
.stage-summary {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 7px 12px 6px;
  font-size: 0.74rem;
  color: var(--color-faint);
}

.card--success .stage-summary { color: var(--color-green); opacity: 0.8; }
.card--running .stage-summary { color: var(--color-amber); opacity: 0.9; }
.card--failed  .stage-summary { color: var(--color-dim); }

.stage-fail-count {
  color: var(--color-red);
  font-weight: 600;
}

/* ─── step list ────────────────────────────────────────────────────────────── */
.stage-steps {
  list-style: none;
  padding: 0 8px 8px;
  display: flex;
  flex-direction: column;
  gap: 1px;
  animation: steps-reveal 0.18s var(--ease-out-expo) both;
}

@keyframes steps-reveal {
  from { opacity: 0; transform: translateY(-6px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .stage-steps { animation: none; }
}

.step-row {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 5px 6px;
  border-radius: var(--rounded-md);
  font-size: 0.77rem;
  transition: background-color var(--duration-fast);
}

.step-row--running { background: var(--color-amber-soft); }
.step-row--failed  { background: var(--color-red-soft); }
.step-row--success:hover { background: var(--color-inset); }

.step-icon {
  width: 14px;
  height: 14px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.step-spinner {
  display: inline-block;
  width: 9px;
  height: 9px;
  border: 1.5px solid oklch(83% 0.13 82 / 0.35);
  border-top-color: var(--color-amber);
  border-radius: var(--rounded-full);
  animation: spin 0.65s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .step-spinner { animation: none; border-top-color: currentColor; }
}

.step-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-dim);
}

.step-row--running .step-name { color: var(--color-text); }
.step-row--failed  .step-name { color: var(--color-text); }

.step-dur {
  font-size: 0.68rem;
  color: var(--color-faint);
  flex-shrink: 0;
}

.mono { font-family: var(--font-mono); }

/* ─── empty stage placeholder ──────────────────────────────────────────────── */
.stage-empty {
  padding: 10px 12px 12px;
  font-size: 0.74rem;
  color: var(--color-faint);
  text-align: center;
}
</style>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import type { PipelineStage, PipelineJob, StageKind } from '../../api/pipeline'
import type { Credential } from '../../api/credentials'
import type { Server } from '../../api/servers'
import StageColumn from './StageColumn.vue'
import JobDrawer from './JobDrawer.vue'
import JobTypePicker from './JobTypePicker.vue'
import { jobTypeLabel } from './jobConfigSchema'
import { hasAnyNeeds } from './stageDeps'
import './pipeline.css'

// ─── Props / emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  stages: PipelineStage[]
  yaml: string
  credentials?: Credential[]
  servers?: Server[]
}>()

const emit = defineEmits<{
  (e: 'update', stages: PipelineStage[]): void
}>()

// ─── Unique ID helpers ────────────────────────────────────────────────────────

function uid(): string {
  return Math.random().toString(36).slice(2, 10)
}

// ─── Selected job state ───────────────────────────────────────────────────────

const selectedJobId = ref<string | null>(null)

const selectedJob = computed<PipelineJob | null>(() => {
  if (!selectedJobId.value) return null
  for (const stage of props.stages) {
    const found = stage.jobs.find((j) => j.id === selectedJobId.value)
    if (found) return found
  }
  return null
})

const selectedStage = computed<PipelineStage | null>(() => {
  if (!selectedJobId.value) return null
  return props.stages.find((s) => s.jobs.some((j) => j.id === selectedJobId.value)) ?? null
})

function selectJob(jobId: string): void {
  selectedJobId.value = selectedJobId.value === jobId ? null : jobId
}

function closeDrawer(): void {
  selectedJobId.value = null
}

// ─── Mutation helpers ─────────────────────────────────────────────────────────

function updateJob(stageId: string, jobId: string, patch: Partial<PipelineJob>): void {
  const next = props.stages.map((s) => {
    if (s.id !== stageId) return s
    return {
      ...s,
      jobs: s.jobs.map((j) => (j.id === jobId ? { ...j, ...patch } : j)),
    }
  })
  emit('update', next)
}

function updateStage(stageId: string, patch: Partial<PipelineStage>): void {
  const next = props.stages.map((s) => (s.id === stageId ? { ...s, ...patch } : s))
  emit('update', next)
}

function deleteJob(stageId: string, jobId: string): void {
  if (selectedJobId.value === jobId) selectedJobId.value = null
  const next = props.stages.map((s) => {
    if (s.id !== stageId) return s
    return { ...s, jobs: s.jobs.filter((j) => j.id !== jobId) }
  })
  emit('update', next)
}

// ─── Type picker (add new job / change existing job's type) ───────────────────

interface PickerState {
  open: boolean
  mode: 'add' | 'change'
  stageId: string
  jobId: string
  current: string
}

const picker = ref<PickerState>({ open: false, mode: 'add', stageId: '', jobId: '', current: '' })

/** Open the type picker to add a new job to a stage. */
function requestAddJob(stageId: string): void {
  picker.value = { open: true, mode: 'add', stageId, jobId: '', current: '' }
}

/** Open the type picker to change the selected job's type (from the drawer). */
function requestChangeType(): void {
  if (!selectedJob.value || !selectedStage.value) return
  picker.value = {
    open: true,
    mode: 'change',
    stageId: selectedStage.value.id,
    jobId: selectedJob.value.id,
    current: selectedJob.value.type,
  }
}

function closePicker(): void {
  picker.value = { ...picker.value, open: false }
}

function onPickerSelect(type: string): void {
  const p = picker.value
  if (p.mode === 'add') {
    const stage = props.stages.find((s) => s.id === p.stageId)
    if (!stage) return closePicker()
    const newJob: PipelineJob = {
      id:      `job_${uid()}`,
      name:    jobTypeLabel(type),
      type,
      summary: '',
      config:  {},
    }
    const next = props.stages.map((s) =>
      s.id === p.stageId ? { ...s, jobs: [...s.jobs, newJob] } : s,
    )
    emit('update', next)
    selectedJobId.value = newJob.id
  } else {
    updateJob(p.stageId, p.jobId, { type })
  }
  closePicker()
}

function reorderJob(stageId: string, from: number, to: number): void {
  const next = props.stages.map((s) => {
    if (s.id !== stageId) return s
    const jobs = [...s.jobs]
    if (from < 0 || from >= jobs.length || to < 0 || to >= jobs.length) return s
    const [moved] = jobs.splice(from, 1)
    jobs.splice(to, 0, moved)
    return { ...s, jobs }
  })
  emit('update', next)
}

function deleteStage(stageId: string): void {
  const idx = props.stages.findIndex((s) => s.id === stageId)
  if (idx < 0) return
  // Deselect if selected job was in this stage
  if (selectedStage.value?.id === stageId) selectedJobId.value = null
  // Drop the deleted stage and strip any dangling needs referencing it (else save 422s).
  const next = props.stages
    .filter((s) => s.id !== stageId)
    .map((s) =>
      s.needs?.includes(stageId) ? { ...s, needs: s.needs.filter((n) => n !== stageId) } : s,
    )
  emit('update', next)
}

function addStage(): void {
  const KIND_SEQ: StageKind[] = ['build', 'deploy', 'notify', 'custom']
  const existing = props.stages.map((s) => s.kind)
  const nextKind: StageKind = KIND_SEQ.find((k) => !existing.includes(k)) ?? 'custom'
  const nextNum = props.stages.filter((s) => s.kind !== 'source').length + 1
  const KIND_LABELS: Partial<Record<StageKind, string>> = {
    build: '构建', deploy: '部署', notify: '通知',
  }
  const newStage: PipelineStage = {
    id:   `stg_${uid()}`,
    name: nextKind === 'custom' ? `自定义 ${nextNum}` : (KIND_LABELS[nextKind] ?? `阶段 ${nextNum}`),
    kind: nextKind,
    jobs: [],
  }
  emit('update', [...props.stages, newStage])
}

// ─── DAG edge overlay (Story 8-7) ─────────────────────────────────────────────
// Draw connectors from each stage's declared needs (upstream → downstream). When no
// stage declares needs, fall back to a linear chain (mirrors backend BuildGraph).

const flowRef = ref<HTMLElement | null>(null)
const overlay = ref({ w: 0, h: 0 })
const edgePaths = ref<string[]>([])

interface Edge { from: string; to: string }

const edges = computed<Edge[]>(() => {
  if (hasAnyNeeds(props.stages)) {
    const out: Edge[] = []
    for (const s of props.stages) for (const n of s.needs ?? []) out.push({ from: n, to: s.id })
    return out
  }
  const out: Edge[] = []
  for (let i = 1; i < props.stages.length; i++) {
    out.push({ from: props.stages[i - 1].id, to: props.stages[i].id })
  }
  return out
})

const HEADER_Y = 13 // connect edges at stage-header vertical center

function measureEdges(): void {
  const flow = flowRef.value
  if (!flow) return
  const cols = Array.from(flow.querySelectorAll<HTMLElement>('.stage-col'))
  const byId = new Map<string, HTMLElement>()
  props.stages.forEach((s, i) => { if (cols[i]) byId.set(s.id, cols[i]) })
  overlay.value = { w: flow.scrollWidth, h: flow.scrollHeight }
  const paths: string[] = []
  for (const e of edges.value) {
    const a = byId.get(e.from)
    const b = byId.get(e.to)
    if (!a || !b) continue
    const x1 = a.offsetLeft + a.offsetWidth
    const y1 = a.offsetTop + HEADER_Y
    const x2 = b.offsetLeft
    const y2 = b.offsetTop + HEADER_Y
    const dx = Math.max(26, Math.abs(x2 - x1) * 0.5)
    paths.push(`M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`)
  }
  edgePaths.value = paths
}

let ro: ResizeObserver | null = null
onMounted(() => {
  measureEdges()
  ro = new ResizeObserver(() => measureEdges())
  if (flowRef.value) ro.observe(flowRef.value)
})
onBeforeUnmount(() => ro?.disconnect())
watch(() => props.stages, () => nextTick(measureEdges), { deep: true })

// ─── YAML preview toggle ──────────────────────────────────────────────────────

const yamlOpen = ref(false)

function handleDrawerUpdate(patch: Partial<PipelineJob>): void {
  if (!selectedJob.value || !selectedStage.value) return
  updateJob(selectedStage.value.id, selectedJob.value.id, patch)
}
</script>

<template>
  <div class="canvas-body">
    <!-- ─── Scrollable canvas ────────────────────────────────────────────── -->
    <div class="pipeline-canvas">
      <div ref="flowRef" class="pipeline-flow pipeline-flow--dag" role="list" aria-label="流水线阶段">

        <!-- DAG edge overlay (drawn from declared needs; decorative) -->
        <svg
          v-if="edgePaths.length"
          class="dag-overlay"
          :width="overlay.w"
          :height="overlay.h"
          :viewBox="`0 0 ${overlay.w} ${overlay.h}`"
          aria-hidden="true"
        >
          <path v-for="(d, i) in edgePaths" :key="i" class="dag-edge" :d="d" />
          <path v-for="(d, i) in edgePaths" :key="`f${i}`" class="dag-edge-flow" :d="d" />
        </svg>

        <template v-for="(stage, idx) in stages" :key="stage.id">
          <!-- Stage column -->
          <StageColumn
            :stage="stage"
            :stage-index="idx"
            :selected-job-id="selectedJobId"
            :all-stages="stages"
            role="listitem"
            @select-job="selectJob"
            @delete-job="(jobId) => deleteJob(stage.id, jobId)"
            @add-job="requestAddJob(stage.id)"
            @delete-stage="deleteStage(stage.id)"
            @reorder-job="(p) => reorderJob(stage.id, p.from, p.to)"
            @update-needs="(needs) => updateStage(stage.id, { needs })"
            @update-allow-failure="(v) => updateStage(stage.id, { allowFailure: v })"
          />
        </template>

        <!-- Add stage button (dashed) -->
        <button
          class="add-stage-btn"
          aria-label="添加新阶段"
          @click="addStage"
        >+ 添加阶段</button>
      </div>

      <!-- YAML preview (collapsible, read-only) -->
      <template v-if="yaml">
        <button
          class="yaml-toggle"
          :class="{ 'yaml-toggle--open': yamlOpen }"
          :aria-expanded="yamlOpen"
          aria-controls="yaml-block"
          @click="yamlOpen = !yamlOpen"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <path d="M9 18l6-6-6-6"/>
          </svg>
          查看 YAML 源码
        </button>
        <div v-if="yamlOpen" id="yaml-block" class="yaml-block">
          <pre class="yaml-code">{{ yaml }}</pre>
        </div>
      </template>
    </div>

    <!-- ─── Right-side drawer (selected job) ──────────────────────────── -->
    <JobDrawer
      v-if="selectedJob && selectedStage"
      :job="selectedJob"
      :stage="selectedStage"
      :credentials="props.credentials"
      :servers="props.servers"
      @close="closeDrawer"
      @update="handleDrawerUpdate"
      @change-type="requestChangeType"
    />

    <!-- Type picker modal (add new job / change type) -->
    <JobTypePicker
      :open="picker.open"
      :current="picker.mode === 'change' ? picker.current : ''"
      :title="picker.mode === 'change' ? '更换任务类型' : '添加任务'"
      @select="onPickerSelect"
      @close="closePicker"
    />
  </div>
</template>

<style scoped>
.canvas-body {
  flex: 1;
  display: flex;
  min-height: 0;
  overflow: hidden;
}

/* DAG layout: position the flow so the overlay anchors to it; give columns a gap for edges. */
.pipeline-flow--dag {
  position: relative;
  gap: 60px;
}

.dag-overlay {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  overflow: visible;
}

.dag-edge {
  fill: none;
  stroke: var(--color-border-strong);
  stroke-width: 2;
}

/* animated flow pulse along the edge */
.dag-edge-flow {
  fill: none;
  stroke: var(--color-primary);
  stroke-width: 2;
  stroke-dasharray: 5 12;
  opacity: 0.75;
  animation: dag-flow 1.4s linear infinite;
}

@keyframes dag-flow {
  to {
    stroke-dashoffset: -34;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dag-edge-flow {
    animation: none;
  }
}
/* The overlay (z-index 0, first child) paints below the stage columns (later siblings),
   so edges show only in the gaps between columns. */
</style>

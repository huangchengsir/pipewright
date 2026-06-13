<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PipelineStage, PipelineJob, StageKind } from '../../api/pipeline'
import type { Credential } from '../../api/credentials'
import type { Server } from '../../api/servers'
import type { NotificationChannel } from '../../api/notifications'
import StageColumn from './StageColumn.vue'
import JobDrawer from './JobDrawer.vue'
import StageDrawer from './StageDrawer.vue'
import JobTypePicker from './JobTypePicker.vue'
import type { CustomNode } from '../../api/customNodes'
import { jobTypeLabel, getJobTypeSpec } from './jobConfigSchema'
import { hasAnyNeeds } from './stageDeps'
import './pipeline.css'

// ─── Props / emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  stages: PipelineStage[]
  yaml: string
  credentials?: Credential[]
  servers?: Server[]
  channels?: NotificationChannel[]
}>()

const emit = defineEmits<{
  (e: 'update', stages: PipelineStage[]): void
}>()

const { t } = useI18n()

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
  // Job drawer + stage-settings drawer share the one right slot — mutually exclusive.
  if (selectedJobId.value) selectedStageSettingsId.value = null
}

function closeDrawer(): void {
  selectedJobId.value = null
}

// ─── Selected stage settings (shares the right drawer slot with the job drawer) ─

const selectedStageSettingsId = ref<string | null>(null)

const selectedSettingsStage = computed<PipelineStage | null>(() =>
  selectedStageSettingsId.value
    ? props.stages.find((s) => s.id === selectedStageSettingsId.value) ?? null
    : null,
)

const selectedSettingsIndex = computed<number>(() =>
  selectedStageSettingsId.value
    ? props.stages.findIndex((s) => s.id === selectedStageSettingsId.value)
    : -1,
)

function openStageSettings(stageId: string): void {
  selectedStageSettingsId.value = selectedStageSettingsId.value === stageId ? null : stageId
  if (selectedStageSettingsId.value) selectedJobId.value = null
}

function closeStageSettings(): void {
  selectedStageSettingsId.value = null
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
    // Drop the job and strip any sibling job needs referencing it (else save 422s).
    return {
      ...s,
      jobs: s.jobs
        .filter((j) => j.id !== jobId)
        .map((j) =>
          j.needs?.includes(jobId) ? { ...j, needs: j.needs.filter((n) => n !== jobId) } : j,
        ),
    }
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
  /** Intra-stage deps to seed on the new job (serial/parallel add); empty = orphan. */
  needs: string[]
}

const picker = ref<PickerState>({ open: false, mode: 'add', stageId: '', jobId: '', current: '', needs: [] })

/** Open the type picker to add a new job to a stage. `needs` seeds intra-stage deps. */
function requestAddJob(stageId: string, needs: string[] = []): void {
  picker.value = { open: true, mode: 'add', stageId, jobId: '', current: '', needs }
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
    needs: [],
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
      // 模板节点带预填配置(深拷贝避免共享引用);普通节点空配置。
      config:  { ...(getJobTypeSpec(type)?.defaultConfig ?? {}) },
      ...(p.needs.length ? { needs: [...p.needs] } : {}),
    }
    addJobToStage(p.stageId, newJob)
  } else {
    updateJob(p.stageId, p.jobId, { type })
  }
  closePicker()
}

/**
 * Insert a saved custom node (复用库 Tier 2): a new Job pre-filled with the saved
 * type + summary + config snapshot. On "change" mode we overwrite type & config in place.
 */
function onPickerSelectCustom(node: CustomNode): void {
  const p = picker.value
  // config 快照转字符串 KV(Job.config 为 string KV;后端以 any 存,值实为字符串)。
  const config: Record<string, string> = {}
  for (const [k, v] of Object.entries(node.config ?? {})) {
    config[k] = typeof v === 'string' ? v : JSON.stringify(v)
  }
  if (p.mode === 'add') {
    const stage = props.stages.find((s) => s.id === p.stageId)
    if (!stage) return closePicker()
    const newJob: PipelineJob = {
      id:      `job_${uid()}`,
      name:    node.name || jobTypeLabel(node.nodeType),
      type:    node.nodeType,
      summary: node.summary ?? '',
      config,
      ...(p.needs.length ? { needs: [...p.needs] } : {}),
    }
    addJobToStage(p.stageId, newJob)
  } else {
    updateJob(p.stageId, p.jobId, { type: node.nodeType, summary: node.summary ?? '', config })
  }
  closePicker()
}

function addJobToStage(stageId: string, newJob: PipelineJob): void {
  const next = props.stages.map((s) =>
    s.id === stageId ? { ...s, jobs: [...s.jobs, newJob] } : s,
  )
  emit('update', next)
  selectedJobId.value = newJob.id
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
  if (selectedStageSettingsId.value === stageId) selectedStageSettingsId.value = null
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
    build: t('pipelineCanvas.stageBuild'),
    deploy: t('pipelineCanvas.stageDeploy'),
    notify: t('pipelineCanvas.stageNotify'),
  }
  const newStage: PipelineStage = {
    id:   `stg_${uid()}`,
    name: nextKind === 'custom'
      ? t('pipelineCanvas.stageCustomN', { n: nextNum })
      : (KIND_LABELS[nextKind] ?? t('pipelineCanvas.stageDefaultN', { n: nextNum })),
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
      <div ref="flowRef" class="pipeline-flow pipeline-flow--dag" role="list" :aria-label="t('pipelineCanvas.flowAriaLabel')">

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
            @add-job="(needs) => requestAddJob(stage.id, needs)"
            @delete-stage="deleteStage(stage.id)"
            @reorder-job="(p) => reorderJob(stage.id, p.from, p.to)"
            :settings-active="selectedStageSettingsId === stage.id"
            @update-needs="(needs) => updateStage(stage.id, { needs })"
            @update-allow-failure="(v) => updateStage(stage.id, { allowFailure: v })"
            @update-job-needs="(p) => updateJob(stage.id, p.jobId, { needs: p.needs })"
            @open-settings="openStageSettings(stage.id)"
          />
        </template>

        <!-- Add stage button (dashed) -->
        <button
          class="add-stage-btn"
          :aria-label="t('pipelineCanvas.addStageAria')"
          @click="addStage"
        >{{ t('pipelineCanvas.addStage') }}</button>
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
          {{ t('pipelineCanvas.viewYaml') }}
        </button>
        <div v-if="yamlOpen" id="yaml-block" class="yaml-block">
          <pre class="yaml-code">{{ yaml }}</pre>
        </div>
      </template>
    </div>

    <!-- ─── Right-side drawer (selected job OR stage settings — one shared slot) ─ -->
    <JobDrawer
      v-if="selectedJob && selectedStage"
      :job="selectedJob"
      :stage="selectedStage"
      :credentials="props.credentials"
      :servers="props.servers"
      :channels="props.channels"
      @close="closeDrawer"
      @update="handleDrawerUpdate"
      @change-type="requestChangeType"
    />

    <StageDrawer
      v-else-if="selectedSettingsStage"
      :stage="selectedSettingsStage"
      :stage-index="selectedSettingsIndex"
      @close="closeStageSettings"
      @update-when="(when) => updateStage(selectedSettingsStage!.id, { when })"
      @update-gate="(v) => updateStage(selectedSettingsStage!.id, { gate: v })"
      @update-matrix="(matrix) => updateStage(selectedSettingsStage!.id, { matrix })"
      @update-post="(post) => updateStage(selectedSettingsStage!.id, { post })"
      @update-services="(services) => updateStage(selectedSettingsStage!.id, { services })"
    />

    <!-- Type picker modal (add new job / change type) -->
    <JobTypePicker
      :open="picker.open"
      :current="picker.mode === 'change' ? picker.current : ''"
      :title="picker.mode === 'change' ? t('pipelineCanvas.pickerTitleChange') : t('pipelineCanvas.pickerTitleAdd')"
      @select="onPickerSelect"
      @select-custom="onPickerSelectCustom"
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

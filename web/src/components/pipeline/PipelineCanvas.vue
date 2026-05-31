<script setup lang="ts">
import { ref, computed } from 'vue'
import type { PipelineStage, PipelineJob, StageKind } from '../../api/pipeline'
import type { Credential } from '../../api/credentials'
import type { Server } from '../../api/servers'
import StageColumn from './StageColumn.vue'
import JobDrawer from './JobDrawer.vue'
import JobTypePicker from './JobTypePicker.vue'
import { jobTypeLabel } from './jobConfigSchema'
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
  emit('update', props.stages.filter((s) => s.id !== stageId))
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
      <div class="pipeline-flow" role="list" aria-label="流水线阶段">

        <template v-for="(stage, idx) in stages" :key="stage.id">
          <!-- Stage column -->
          <StageColumn
            :stage="stage"
            :stage-index="idx"
            :selected-job-id="selectedJobId"
            role="listitem"
            @select-job="selectJob"
            @delete-job="(jobId) => deleteJob(stage.id, jobId)"
            @add-job="requestAddJob(stage.id)"
            @delete-stage="deleteStage(stage.id)"
            @reorder-job="(p) => reorderJob(stage.id, p.from, p.to)"
          />

          <!-- SVG curved connector between stages -->
          <div
            v-if="idx < stages.length - 1"
            class="stage-connector"
            aria-hidden="true"
          >
            <svg viewBox="0 0 74 30">
              <path class="conn-edge" d="M5,15 C28,3 46,27 69,15"/>
              <path class="conn-flow" d="M5,15 C28,3 46,27 69,15"/>
              <circle class="conn-port" cx="5"  cy="15" r="3.2"/>
              <circle class="conn-port" cx="69" cy="15" r="3.2"/>
            </svg>
          </div>
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
</style>

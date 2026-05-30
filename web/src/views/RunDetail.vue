<!--
  RunDetail.vue — Story 3.1: 运行详情页 5 态骨架
  骨架所有权规则:本文件定义骨架结构;Epic 4/7 只往具名 slot 填内容,不改骨架形状。

  5 态由 run.status 驱动(同一 URL,运行生命周期内自然演进):
  · running       → SkeletonRunning  (穿珠时间线 + SSE + 日志占位)
  · success       → SkeletonSuccess  (全绿时间线 + 零停机流程图 slot)
  · failed        → SkeletonFailed   (失败节点 + AI 诊断 slot)
  · partial_failed → SkeletonPartial (targets 扇出 slot)
  · queued / rolled_back → SkeletonTerminal

  SSE 订阅:连线 /api/runs/:id/events;断线自动降级轮询,不白屏。
  reduced-motion:跳过脉冲/入场/流光;所有动效均有媒体查询降级。
-->
<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  getRun,
  cancelRun,
  subscribeRunEvents,
  type RunDetail,
  type RunStatus,
  type StepStatus,
} from '../api/runs'
import { HttpError } from '../api/http'

// ─── route ────────────────────────────────────────────────────────────────────

const route  = useRoute()
const router = useRouter()
const runId  = computed(() => route.params.id as string)

// ─── load state ──────────────────────────────────────────────────────────────

type LoadState = 'loading' | 'idle' | 'error'

const loadState = ref<LoadState>('loading')
const loadError = ref('')
const run = ref<RunDetail | null>(null)

// ─── cancel state ─────────────────────────────────────────────────────────────

const cancelling = ref(false)
const cancelError = ref('')

async function handleCancel(): Promise<void> {
  if (cancelling.value) return
  cancelling.value = true
  cancelError.value = ''
  try {
    const updated = await cancelRun(runId.value)
    run.value = updated
  } catch (err) {
    if (err instanceof HttpError) {
      cancelError.value = err.apiError?.message ?? `取消失败(${err.status})`
    } else {
      cancelError.value = '取消请求失败,请稍后重试'
    }
  } finally {
    cancelling.value = false
  }
}

// ─── data loading ─────────────────────────────────────────────────────────────

async function loadRun(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    run.value = await getRun(runId.value)
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value = err.status === 404
        ? `运行 ${runId.value} 不存在`
        : (err.apiError?.message ?? `加载失败(${err.status})`)
    } else {
      loadError.value = '加载运行详情失败,请稍后重试'
    }
    loadState.value = 'error'
  }
}

// ─── SSE subscription ─────────────────────────────────────────────────────────
// Subscribes when run is in-progress (running/queued).
// Cleans up on terminal states or component unmount.

let cleanupSse: (() => void) | null = null

function startSse(): void {
  if (cleanupSse) return
  cleanupSse = subscribeRunEvents(runId.value, {
    onStatus({ status }) {
      if (!run.value) return
      run.value = { ...run.value, status }
      // Stop SSE on terminal state
      if (isTerminal(status)) stopSse()
    },
    onStep({ step }) {
      if (!run.value) return
      const steps = run.value.steps.map((s) => (s.id === step.id ? { ...s, ...step } : s))
      run.value = { ...run.value, steps }
    },
    // onError is intentionally omitted — subscribeRunEvents falls back to polling
  })
}

function stopSse(): void {
  if (cleanupSse) {
    cleanupSse()
    cleanupSse = null
  }
}

function isTerminal(status: RunStatus): boolean {
  return status === 'success' || status === 'failed' || status === 'partial_failed' || status === 'rolled_back'
}

// Manage SSE lifecycle as run status changes
watch(
  () => run.value?.status,
  (status) => {
    if (!status) return
    if ((status === 'running' || status === 'queued') && !cleanupSse) {
      startSse()
    } else if (isTerminal(status)) {
      stopSse()
    }
  },
)

onMounted(async () => {
  await loadRun()
  if (run.value && (run.value.status === 'running' || run.value.status === 'queued')) {
    startSse()
  }
})

onUnmounted(() => { stopSse() })

// ─── Status config (fixed six-word set) ──────────────────────────────────────

interface StatusConfig {
  label: string
  dot: string
  bg: string
  border: string
  text: string
  pulse: boolean
}

const STATUS_CONFIG: Record<RunStatus, StatusConfig> = {
  queued:        { label: '排队中',   dot: 'var(--color-faint)',  bg: 'var(--color-card-2)',     border: 'var(--color-border-strong)', text: 'var(--color-dim)',   pulse: false },
  running:       { label: '进行中',   dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)', border: 'transparent',               text: 'var(--color-amber)', pulse: true  },
  success:       { label: '成功',     dot: 'var(--color-green)',  bg: 'var(--color-green-soft)', border: 'transparent',               text: 'var(--color-green)', pulse: false },
  failed:        { label: '失败',     dot: 'var(--color-red)',    bg: 'var(--color-red-soft)',   border: 'var(--color-red-line)',     text: 'var(--color-red)',   pulse: false },
  partial_failed:{ label: '部分失败', dot: 'var(--color-red)',    bg: 'var(--color-red-soft)',   border: 'var(--color-red-line)',     text: 'var(--color-red)',   pulse: false },
  rolled_back:   { label: '已回滚',   dot: 'var(--color-amber)',  bg: 'var(--color-amber-soft)', border: 'var(--color-amber-line)',   text: 'var(--color-amber)', pulse: false },
}

// ─── Step config ─────────────────────────────────────────────────────────────

interface StepConfig { icon: 'check' | 'spinner' | 'dot' | 'x' | 'skip'; color: string }

function stepConfig(status: StepStatus): StepConfig {
  switch (status) {
    case 'success': return { icon: 'check',   color: 'var(--color-green)' }
    case 'running': return { icon: 'spinner', color: 'var(--color-amber)' }
    case 'failed':  return { icon: 'x',       color: 'var(--color-red)'   }
    case 'skipped': return { icon: 'skip',    color: 'var(--color-faint)' }
    default:        return { icon: 'dot',     color: 'var(--color-faint)' }
  }
}

// ─── helpers ──────────────────────────────────────────────────────────────────

function shortId(id: string): string { return id.slice(0, 8) }

function shortCommit(commit: string): string {
  return commit.length > 7 ? commit.slice(0, 7) : commit
}

function formatDuration(ms: number | null): string {
  if (ms === null) return '—'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

function formatDateTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleString('zh-CN', {
    month: 'numeric', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

function goBack(): void {
  void router.push({ name: 'runs' })
}

// ─── Computed step status for skewer node style ───────────────────────────────
function nodeClass(status: StepStatus): string {
  switch (status) {
    case 'success': return 'node--ok'
    case 'running': return 'node--running'
    case 'failed':  return 'node--bad'
    case 'skipped': return 'node--skip'
    default:        return 'node--pending'
  }
}
</script>

<template>
  <div class="run-detail-root">

    <!-- ─── Back link + breadcrumb ──────────────────────────────────────── -->
    <div class="breadcrumb">
      <button class="back-btn" @click="goBack" aria-label="返回运行列表">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M15 18l-6-6 6-6"/>
        </svg>
        运行列表
      </button>
      <span class="breadcrumb-sep" aria-hidden="true">/</span>
      <span class="breadcrumb-cur">
        运行详情
        <span v-if="run" class="breadcrumb-id mono">· #{{ shortId(run.id) }}</span>
      </span>
    </div>

    <!-- ─── Loading skeleton ─────────────────────────────────────────────── -->
    <template v-if="loadState === 'loading'">
      <div class="detail-loading" aria-busy="true" aria-label="加载运行详情中">
        <div class="skel-head" />
        <div class="skel-body">
          <div class="skel skel--title" />
          <div class="skel skel--meta" />
          <div class="skel skel--timeline" />
          <div class="skel skel--steps" />
        </div>
      </div>
    </template>

    <!-- ─── Error state ──────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'error'">
      <div class="error-state" role="alert">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
        </svg>
        <p class="error-message">{{ loadError }}</p>
        <button class="btn-secondary" @click="loadRun">↻ 重试</button>
      </div>
    </template>

    <!-- ─── Run detail ───────────────────────────────────────────────────── -->
    <template v-else-if="loadState === 'idle' && run">

      <!-- Detail card wrapper -->
      <div class="detail-card">

        <!-- ── Card header: status + meta ─────────────────────────────── -->
        <div class="detail-head">
          <div class="detail-head-left">
            <!-- Status pill -->
            <span
              class="status-pill"
              :style="{
                background: STATUS_CONFIG[run.status].bg,
                border: `1px solid ${STATUS_CONFIG[run.status].border}`,
                color: STATUS_CONFIG[run.status].text,
              }"
              :aria-label="`运行状态:${STATUS_CONFIG[run.status].label}`"
              aria-live="polite"
            >
              <span
                class="status-dot"
                :class="{ 'status-dot--pulse': STATUS_CONFIG[run.status].pulse }"
                :style="{ background: STATUS_CONFIG[run.status].dot }"
                aria-hidden="true"
              />
              {{ STATUS_CONFIG[run.status].label }}
            </span>

            <h1 class="detail-title">
              {{ run.projectName }}
              <span class="detail-id mono">#{{ shortId(run.id) }}</span>
            </h1>
          </div>

          <!-- Cancel button (only when running/queued) -->
          <button
            v-if="run.status === 'running' || run.status === 'queued'"
            class="btn-danger-ghost"
            :disabled="cancelling"
            :aria-busy="cancelling"
            @click="handleCancel"
          >
            <span v-if="cancelling" class="spinner spinner--red" aria-hidden="true" />
            <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9"/><path d="m15 9-6 6M9 9l6 6"/>
            </svg>
            {{ cancelling ? '取消中…' : '取消运行' }}
          </button>
        </div>

        <!-- Cancel error banner -->
        <div v-if="cancelError" class="banner banner--error inline-banner" role="alert">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ cancelError }}
        </div>

        <!-- ── Meta strip: trigger / branch / commit / duration ──────── -->
        <div class="meta-strip">
          <div class="meta-item">
            <span class="meta-key">项目</span>
            <span class="meta-val">{{ run.projectName }}</span>
          </div>
          <div class="meta-item">
            <span class="meta-key">分支</span>
            <span class="meta-val mono">{{ run.trigger.branch }}</span>
          </div>
          <div class="meta-item">
            <span class="meta-key">Commit</span>
            <span class="meta-val mono">{{ shortCommit(run.trigger.commit) }}</span>
          </div>
          <div class="meta-item">
            <span class="meta-key">触发</span>
            <span class="meta-val">{{ run.trigger.type === 'webhook' ? 'Webhook' : '手动' }} · {{ run.trigger.actor }}</span>
          </div>
          <div class="meta-item">
            <span class="meta-key">开始</span>
            <span class="meta-val">{{ formatDateTime(run.startedAt) }}</span>
          </div>
          <div class="meta-item">
            <span class="meta-key">耗时</span>
            <span class="meta-val mono">{{ formatDuration(run.durationMs) }}</span>
          </div>
        </div>

        <!-- ══════════════════════════════════════════════════════════════
             5-STATE SECTION — driven by run.status
             骨架所有权:下方每个 v-if 分支是独立的具名骨架区块。
             Epic 4/7 只往标注了"slot"的区块填内容;不改本组件结构。
        ═══════════════════════════════════════════════════════════════ -->

        <!-- ── STATE: running ────────────────────────────────────────── -->
        <template v-if="run.status === 'running' || run.status === 'queued'">
          <div class="state-section">

            <!-- Skewer timeline: 穿珠串线 -->
            <div class="skewer-section">
              <h2 class="section-title">流水线进度</h2>
              <div
                class="skewer"
                role="list"
                :aria-label="`流水线步骤,共 ${run.steps.length} 步`"
              >
                <div
                  v-for="(step, idx) in run.steps"
                  :key="step.id"
                  class="skewer-item"
                  role="listitem"
                >
                  <!-- Connector line before node (except first) -->
                  <div
                    v-if="idx > 0"
                    class="skewer-connector"
                    :class="{
                      'connector--ok':      run.steps[idx - 1].status === 'success',
                      'connector--bad':     run.steps[idx - 1].status === 'failed',
                      'connector--running': run.steps[idx - 1].status === 'running',
                    }"
                    aria-hidden="true"
                  />
                  <!-- Node -->
                  <div
                    class="skewer-node"
                    :class="nodeClass(step.status)"
                    :title="step.name"
                    :aria-label="`步骤 ${step.name}: ${step.status}`"
                  >
                    <!-- Running pulse ring -->
                    <span
                      v-if="step.status === 'running'"
                      class="node-pulse-ring"
                      aria-hidden="true"
                    />
                  </div>
                  <!-- Step label below node -->
                  <div class="skewer-label" aria-hidden="true">{{ step.name }}</div>
                </div>
              </div>
            </div>

            <!-- Two-column layout: step list + log placeholder -->
            <div class="running-body">

              <!-- Step list -->
              <div class="step-list">
                <h3 class="subsection-title">步骤详情</h3>
                <ul class="steps" role="list">
                  <li
                    v-for="step in run.steps"
                    :key="step.id"
                    class="step-row"
                    :class="`step-row--${step.status}`"
                  >
                    <div class="step-icon" :aria-label="step.status">
                      <!-- success -->
                      <svg v-if="step.status === 'success'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                        <path d="M20 6 9 17l-5-5"/>
                      </svg>
                      <!-- running: spinner -->
                      <span v-else-if="step.status === 'running'" class="spinner spinner--amber" aria-hidden="true" />
                      <!-- failed -->
                      <svg v-else-if="step.status === 'failed'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                        <path d="m18 6-12 12M6 6l12 12"/>
                      </svg>
                      <!-- skipped -->
                      <svg v-else-if="step.status === 'skipped'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                        <path d="M13 17l5-5-5-5M6 17l5-5-5-5"/>
                      </svg>
                      <!-- pending -->
                      <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                        <circle cx="12" cy="12" r="3"/>
                      </svg>
                    </div>
                    <div class="step-info">
                      <span class="step-name">{{ step.name }}</span>
                      <span v-if="step.durationMs !== null" class="step-dur mono">{{ formatDuration(step.durationMs) }}</span>
                    </div>
                  </li>
                </ul>
              </div>

              <!--
                ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                SLOT: 实时日志终端 (Story 3-6 接入)
                骨架所有权: 本区块归 Story 3.1;
                Story 3-6 在此区块内填入真实日志终端组件,不改骨架结构。
                ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              -->
              <div class="slot-log" role="region" aria-label="实时日志(尚未接入)">
                <div class="slot-header">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                    <rect x="2" y="3" width="20" height="18" rx="3"/>
                    <path d="M7 8l4 4-4 4M13 16h4"/>
                  </svg>
                  <span class="slot-title">实时日志终端</span>
                  <span class="slot-badge">Story 3-6</span>
                </div>
                <div class="slot-body slot-body--term">
                  <span class="slot-placeholder-text">实时日志将在 Story 3-6 接入</span>
                  <span class="slot-placeholder-hint">SSE 状态/步骤推送已就绪;日志流内容尚未连接</span>
                </div>
              </div>
              <!-- END SLOT: 实时日志终端 -->

            </div>
          </div>
        </template>

        <!-- ── STATE: success ────────────────────────────────────────── -->
        <template v-else-if="run.status === 'success'">
          <div class="state-section">

            <!-- Full-green skewer -->
            <div class="skewer-section">
              <h2 class="section-title">流水线完成</h2>
              <div class="skewer" role="list" :aria-label="`流水线步骤,共 ${run.steps.length} 步,全部成功`">
                <div
                  v-for="(step, idx) in run.steps"
                  :key="step.id"
                  class="skewer-item"
                  role="listitem"
                >
                  <div v-if="idx > 0" class="skewer-connector connector--ok" aria-hidden="true" />
                  <div class="skewer-node node--ok" :title="step.name" :aria-label="`步骤 ${step.name}: 成功`" />
                  <div class="skewer-label" aria-hidden="true">{{ step.name }}</div>
                </div>
              </div>
            </div>

            <!-- Duration summary -->
            <div class="success-summary">
              <div class="summary-item">
                <span class="summary-key">完成时间</span>
                <span class="summary-val">{{ formatDateTime(run.finishedAt) }}</span>
              </div>
              <div class="summary-item">
                <span class="summary-key">总耗时</span>
                <span class="summary-val mono">{{ formatDuration(run.durationMs) }}</span>
              </div>
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: 零停机切换流程图 (Epic 4 接入)
              骨架所有权: 本区块归 Story 3.1;
              Epic 4 在此区块内填入零停机切换 4 步流程图(起新实例→健康门控→流量切换→旧实例下线),
              不改骨架结构。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <div class="slot-panel slot-panel--success" role="region" aria-label="零停机切换流程图(尚未接入)">
              <div class="slot-header">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83"/>
                </svg>
                <span class="slot-title">零停机切换流程图</span>
                <span class="slot-badge slot-badge--epic">Epic 4</span>
              </div>
              <div class="slot-body">
                <span class="slot-placeholder-text">零停机切换详情将在 Epic 4 接入</span>
                <span class="slot-placeholder-hint">起新实例 → 健康门控 → 流量切换 → 旧实例下线</span>
              </div>
            </div>
            <!-- END SLOT: 零停机切换流程图 -->

          </div>
        </template>

        <!-- ── STATE: failed ─────────────────────────────────────────── -->
        <template v-else-if="run.status === 'failed'">
          <div class="state-section">

            <!-- Skewer with failure node -->
            <div class="skewer-section">
              <h2 class="section-title">流水线失败</h2>
              <div class="skewer" role="list" :aria-label="`流水线步骤,含失败节点`">
                <div
                  v-for="(step, idx) in run.steps"
                  :key="step.id"
                  class="skewer-item"
                  role="listitem"
                >
                  <div
                    v-if="idx > 0"
                    class="skewer-connector"
                    :class="{
                      'connector--ok':  run.steps[idx - 1].status === 'success',
                      'connector--bad': run.steps[idx - 1].status === 'failed',
                    }"
                    aria-hidden="true"
                  />
                  <div
                    class="skewer-node"
                    :class="nodeClass(step.status)"
                    :title="step.name"
                    :aria-label="`步骤 ${step.name}: ${step.status}`"
                  />
                  <div class="skewer-label" aria-hidden="true">{{ step.name }}</div>
                </div>
              </div>
            </div>

            <!-- Failed step summary -->
            <div class="failed-summary">
              <div
                v-for="step in run.steps.filter(s => s.status === 'failed')"
                :key="step.id"
                class="failed-step-banner"
                role="alert"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
                </svg>
                <span class="failed-step-name">{{ step.name }}</span>
                <span class="failed-step-dur mono" v-if="step.durationMs !== null">{{ formatDuration(step.durationMs) }}</span>
              </div>
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: AI 失败诊断 (Epic 7 接入)
              骨架所有权: 本区块归 Story 3.1;
              Epic 7 在此区块内填入 AI 诊断卡(根因假说+证据+置信度+反馈闭环),
              diagnosis 字段已在 run-detail DTO 前向声明为可选块,
              不改骨架结构。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <div class="slot-panel slot-panel--ai" role="region" aria-label="AI 失败诊断(尚未接入)">
              <div class="slot-header">
                <!-- Activity-pulse line icon (no star-sparkle per DESIGN.md) -->
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <polyline points="2 12 6 12 8 4 10 20 12 10 14 15 16 12 22 12"/>
                </svg>
                <span class="slot-title slot-title--ai">AI 失败诊断</span>
                <span class="slot-badge slot-badge--epic">Epic 7</span>
              </div>
              <div class="slot-body">
                <span class="slot-placeholder-text">AI 诊断将在 Epic 7 接入</span>
                <span class="slot-placeholder-hint">根因假说 · 证据日志 · 置信度 · 👍/👎 反馈闭环</span>
                <!-- run.diagnosis is null in this story — Epic 7 fills it -->
              </div>
            </div>
            <!-- END SLOT: AI 失败诊断 -->

          </div>
        </template>

        <!-- ── STATE: partial_failed ────────────────────────────────── -->
        <template v-else-if="run.status === 'partial_failed'">
          <div class="state-section">

            <div class="skewer-section">
              <h2 class="section-title">部分失败</h2>
              <div class="skewer" role="list">
                <div
                  v-for="(step, idx) in run.steps"
                  :key="step.id"
                  class="skewer-item"
                  role="listitem"
                >
                  <div
                    v-if="idx > 0"
                    class="skewer-connector"
                    :class="{
                      'connector--ok':  run.steps[idx - 1].status === 'success',
                      'connector--bad': run.steps[idx - 1].status === 'failed',
                    }"
                    aria-hidden="true"
                  />
                  <div
                    class="skewer-node"
                    :class="nodeClass(step.status)"
                    :title="step.name"
                    :aria-label="`步骤 ${step.name}: ${step.status}`"
                  />
                  <div class="skewer-label" aria-hidden="true">{{ step.name }}</div>
                </div>
              </div>
            </div>

            <div class="partial-info" role="status">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
                <path d="M12 9v4M12 17h.01"/>
              </svg>
              部分目标失败,失败台已独立回滚;其余台继续运行,互不连累。
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: 多机目标扇出聚合 (Epic 4 接入)
              骨架所有权: 本区块归 Story 3.1;
              Epic 4 在此区块内填入多机扇出卡(各台独立状态卡+步骤+耗时+重试入口),
              targets 字段已在 run-detail DTO 前向声明为可选块,
              不改骨架结构。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <div class="slot-panel slot-panel--multi" role="region" aria-label="多机目标状态(尚未接入)">
              <div class="slot-header">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <rect x="2" y="14" width="6" height="6" rx="1"/>
                  <rect x="16" y="14" width="6" height="6" rx="1"/>
                  <rect x="9" y="2" width="6" height="6" rx="1"/>
                  <path d="M5 14v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3M12 7v4"/>
                </svg>
                <span class="slot-title">多机目标扇出</span>
                <span class="slot-badge slot-badge--epic">Epic 4</span>
              </div>
              <div class="slot-body">
                <span class="slot-placeholder-text">多机并行部署详情将在 Epic 4 接入</span>
                <span class="slot-placeholder-hint">各台独立状态 · 失败台回滚 · 仅重试失败目标</span>
                <!-- run.targets is null in this story — Epic 4 fills it -->
              </div>
            </div>
            <!-- END SLOT: 多机目标扇出 -->

          </div>
        </template>

        <!-- ── STATE: rolled_back / terminal ─────────────────────────── -->
        <template v-else>
          <!-- queued (stale) or rolled_back -->
          <div class="state-section">

            <div class="terminal-state">
              <div
                class="terminal-badge"
                :style="{
                  background: STATUS_CONFIG[run.status].bg,
                  border: `1px solid ${STATUS_CONFIG[run.status].border}`,
                  color: STATUS_CONFIG[run.status].text,
                }"
                role="status"
                :aria-label="`运行${STATUS_CONFIG[run.status].label}`"
              >
                <span
                  class="status-dot"
                  :style="{ background: STATUS_CONFIG[run.status].dot }"
                  aria-hidden="true"
                />
                {{ STATUS_CONFIG[run.status].label }}
              </div>

              <div class="terminal-meta">
                <div v-if="run.finishedAt" class="terminal-meta-item">
                  <span class="meta-key">结束时间</span>
                  <span class="meta-val">{{ formatDateTime(run.finishedAt) }}</span>
                </div>
                <div v-if="run.durationMs !== null" class="terminal-meta-item">
                  <span class="meta-key">总耗时</span>
                  <span class="meta-val mono">{{ formatDuration(run.durationMs) }}</span>
                </div>
              </div>
            </div>

            <!-- Steps summary for terminal states -->
            <div class="step-list" v-if="run.steps.length > 0">
              <h3 class="subsection-title">步骤记录</h3>
              <ul class="steps" role="list">
                <li
                  v-for="step in run.steps"
                  :key="step.id"
                  class="step-row"
                  :class="`step-row--${step.status}`"
                >
                  <div class="step-icon">
                    <svg v-if="step.status === 'success'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                      <path d="M20 6 9 17l-5-5"/>
                    </svg>
                    <svg v-else-if="step.status === 'failed'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                      <path d="m18 6-12 12M6 6l12 12"/>
                    </svg>
                    <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <circle cx="12" cy="12" r="3"/>
                    </svg>
                  </div>
                  <div class="step-info">
                    <span class="step-name">{{ step.name }}</span>
                    <span v-if="step.durationMs !== null" class="step-dur mono">{{ formatDuration(step.durationMs) }}</span>
                  </div>
                </li>
              </ul>
            </div>

          </div>
        </template>
        <!-- ══ END 5-STATE SECTION ══ -->

      </div>
      <!-- END detail-card -->

    </template>
    <!-- END run detail -->

  </div>
</template>

<style scoped>
/* ─── root ───────────────────────────────────────────────────────────────── */
.run-detail-root {
  display: flex;
  flex-direction: column;
  gap: var(--card-gap);
}

/* ─── breadcrumb ─────────────────────────────────────────────────────────── */
.breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.82rem;
  color: var(--color-faint);
}

.back-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: none;
  border: none;
  color: var(--color-dim);
  font-family: var(--font-sans);
  font-size: 0.82rem;
  cursor: pointer;
  padding: 3px 0;
  transition: color var(--duration-fast);
}

.back-btn:hover {
  color: var(--color-primary);
}

.back-btn:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  border-radius: var(--rounded-sm);
}

.breadcrumb-sep {
  color: var(--color-faint);
}

.breadcrumb-cur {
  color: var(--color-dim);
}

.breadcrumb-id {
  color: var(--color-faint);
  font-size: 0.78rem;
}

/* ─── detail card ────────────────────────────────────────────────────────── */
.detail-card {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  box-shadow: var(--shadow);
  overflow: hidden;
  animation: card-in 0.35s var(--ease-out-expo) both;
}

@keyframes card-in {
  from { opacity: 0; transform: translateY(10px); }
  to   { opacity: 1; transform: none; }
}

@media (prefers-reduced-motion: reduce) {
  .detail-card { animation: none; }
}

/* ─── detail head ────────────────────────────────────────────────────────── */
.detail-head {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px 24px 16px;
  border-bottom: 1px solid var(--color-border);
  flex-wrap: wrap;
}

.detail-head-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  min-width: 0;
}

.detail-title {
  font-size: 1.16rem;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: -0.01em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.detail-id {
  font-size: 0.9rem;
  color: var(--color-faint);
  font-weight: 400;
}

/* ─── status pill ────────────────────────────────────────────────────────── */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 10px;
  border-radius: var(--rounded-md);
  font-size: 0.78rem;
  font-weight: 600;
  white-space: nowrap;
  flex-shrink: 0;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.status-dot--pulse {
  animation: dot-pulse 1.1s ease-in-out infinite;
}

@keyframes dot-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50%       { opacity: 0.5; transform: scale(0.8); }
}

@media (prefers-reduced-motion: reduce) {
  .status-dot--pulse { animation: none; }
}

/* ─── meta strip ─────────────────────────────────────────────────────────── */
.meta-strip {
  display: flex;
  flex-wrap: wrap;
  gap: 0;
  padding: 14px 24px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
}

.meta-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 4px 20px 4px 0;
  min-width: 120px;
}

.meta-key {
  font-size: 0.71rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.meta-val {
  font-size: 0.83rem;
  color: var(--color-dim);
}

.mono { font-family: var(--font-mono); }

/* ─── state section wrapper ──────────────────────────────────────────────── */
.state-section {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.section-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 16px;
  letter-spacing: -0.01em;
}

.subsection-title {
  font-size: 0.78rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-faint);
  margin-bottom: 10px;
}

/* ─── skewer (穿珠时间线) ────────────────────────────────────────────────── */
.skewer-section {
  padding: 16px 20px;
  background: var(--color-inset);
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
}

.skewer {
  display: flex;
  align-items: flex-start;
  gap: 0;
  overflow-x: auto;
  padding-bottom: 4px;
}

.skewer-item {
  display: flex;
  align-items: center;
  flex-direction: row;
  position: relative;
}

/* Each node is wrapped with its label below */
.skewer-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

/* Connector: horizontal line between nodes */
.skewer-connector {
  width: 36px;
  height: 2px;
  background: var(--color-border-strong);
  margin-bottom: 20px; /* offset for label */
  align-self: center;
  flex-shrink: 0;
  position: relative;
  top: -12px; /* vertically center with node */
}

/* Override: actually position connector and node in a row */
.skewer {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
}

.skewer-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

/* Place connector to the left of the node — use separate element */
.skewer-connector {
  width: 36px;
  height: 2px;
  flex-shrink: 0;
  margin-top: 5px; /* align to center of 13px node */
  align-self: flex-start;
}

.connector--ok      { background: var(--color-green); }
.connector--bad     { background: linear-gradient(90deg, var(--color-green), var(--color-red)); }
.connector--running { background: linear-gradient(90deg, var(--color-green) 40%, var(--color-amber)); }

/* Connector as default border */
.skewer-connector:not(.connector--ok):not(.connector--bad):not(.connector--running) {
  background: var(--color-border-strong);
}

/* ─── skewer node ────────────────────────────────────────────────────────── */
.skewer-node {
  width: 13px;
  height: 13px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
  position: relative;
  transition: box-shadow var(--duration-fast);
}

.node--pending {
  background: var(--color-inset);
  border: 2px solid var(--color-border-strong);
}

.node--ok {
  background: var(--color-green);
  box-shadow: 0 0 0 4px var(--color-green-soft);
}

.node--running {
  background: var(--color-amber);
  box-shadow: 0 0 0 4px var(--color-amber-soft);
}

.node--bad {
  background: var(--color-red);
  box-shadow: 0 0 0 4px var(--color-red-soft), 0 0 8px var(--color-red-soft);
  animation: node-bad-pulse 1.2s ease-in-out infinite;
}

@keyframes node-bad-pulse {
  0%, 100% { box-shadow: 0 0 0 4px var(--color-red-soft), 0 0 8px var(--color-red-soft); }
  50%       { box-shadow: 0 0 0 6px var(--color-red-soft), 0 0 14px oklch(62% 0.18 22 / 0.3); }
}

@media (prefers-reduced-motion: reduce) {
  .node--bad { animation: none; }
}

.node--skip {
  background: var(--color-inset);
  border: 2px dashed var(--color-border-strong);
}

/* Running pulse ring */
.node-pulse-ring {
  position: absolute;
  inset: -4px;
  border-radius: var(--rounded-full);
  border: 2px solid var(--color-amber);
  animation: ring-pulse 1.1s ease-out infinite;
  opacity: 0;
}

@keyframes ring-pulse {
  0%   { opacity: 0.8; transform: scale(0.8); }
  100% { opacity: 0;   transform: scale(1.6); }
}

@media (prefers-reduced-motion: reduce) {
  .node-pulse-ring { animation: none; display: none; }
}

/* Node label */
.skewer-label {
  font-size: 0.68rem;
  color: var(--color-faint);
  white-space: nowrap;
  max-width: 72px;
  overflow: hidden;
  text-overflow: ellipsis;
  text-align: center;
}

/* ─── running body: two-column (step list + log slot) ───────────────────── */
.running-body {
  display: grid;
  grid-template-columns: 280px 1fr;
  gap: 16px;
  align-items: start;
}

@media (max-width: 820px) {
  .running-body { grid-template-columns: 1fr; }
}

/* ─── step list ──────────────────────────────────────────────────────────── */
.step-list {
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  padding: 14px 16px;
}

.steps {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.step-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 8px;
  border-radius: var(--rounded-md);
  transition: background-color var(--duration-fast);
}

.step-row--running {
  background: var(--color-amber-soft);
}

.step-row--failed {
  background: var(--color-red-soft);
}

.step-icon {
  width: 18px;
  height: 18px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.step-row--success .step-icon { color: var(--color-green); }
.step-row--running .step-icon { color: var(--color-amber); }
.step-row--failed  .step-icon { color: var(--color-red); }
.step-row--skipped .step-icon { color: var(--color-faint); }
.step-row--pending .step-icon { color: var(--color-faint); }

.step-info {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex: 1;
  min-width: 0;
}

.step-name {
  font-size: 0.83rem;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.step-dur {
  font-size: 0.72rem;
  color: var(--color-faint);
  margin-left: auto;
  flex-shrink: 0;
}

/* ─── slot panels ────────────────────────────────────────────────────────── */
/* Shared slot panel: clear ownership marker for future Epic implementors */
.slot-panel {
  border-radius: var(--rounded);
  border: 1.5px dashed var(--color-border-strong);
  overflow: hidden;
}

.slot-panel--success { border-color: var(--color-green-line); }
.slot-panel--ai      { border-color: var(--color-cyan-line); }
.slot-panel--multi   { border-color: var(--color-amber-line); }

.slot-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
}

.slot-panel--success .slot-header { color: var(--color-green); background: var(--color-green-soft); }
.slot-panel--ai      .slot-header { color: var(--color-cyan);  background: var(--color-cyan-soft);  }
.slot-panel--multi   .slot-header { color: var(--color-amber); background: var(--color-amber-soft); }

.slot-title {
  font-size: 0.82rem;
  font-weight: 600;
  color: inherit;
  flex: 1;
}

.slot-title--ai { color: var(--color-cyan); }

.slot-badge {
  font-size: 0.68rem;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: var(--rounded-sm);
  background: var(--color-border-strong);
  color: var(--color-dim);
  white-space: nowrap;
}

.slot-badge--epic {
  background: var(--color-primary-soft);
  color: var(--color-primary);
}

.slot-body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 20px 18px;
  text-align: center;
  align-items: center;
}

.slot-body--term {
  background: var(--color-term);
}

.slot-placeholder-text {
  font-size: 0.83rem;
  color: var(--color-dim);
  font-weight: 500;
}

.slot-body--term .slot-placeholder-text {
  color: var(--color-faint);
  font-family: var(--font-mono);
}

.slot-placeholder-hint {
  font-size: 0.75rem;
  color: var(--color-faint);
  line-height: 1.5;
}

/* Log slot */
.slot-log {
  border-radius: var(--rounded-lg);
  border: 1px solid var(--color-border);
  overflow: hidden;
}

.slot-log .slot-header {
  background: oklch(12% 0.004 270);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-dim);
}

/* ─── success summary ────────────────────────────────────────────────────── */
.success-summary {
  display: flex;
  gap: 24px;
  flex-wrap: wrap;
  padding: 12px 16px;
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  border-radius: var(--rounded);
}

.summary-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.summary-key {
  font-size: 0.71rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-green);
  opacity: 0.7;
}

.summary-val {
  font-size: 0.86rem;
  color: var(--color-green);
  font-weight: 500;
}

/* ─── failed summary ─────────────────────────────────────────────────────── */
.failed-summary {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.failed-step-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  border-radius: var(--rounded);
  color: var(--color-red);
  font-size: 0.83rem;
}

.failed-step-name { flex: 1; font-weight: 500; }

.failed-step-dur { color: var(--color-faint); font-size: 0.75rem; }

/* ─── partial info banner ────────────────────────────────────────────────── */
.partial-info {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 14px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded);
  color: var(--color-amber);
  font-size: 0.83rem;
  line-height: 1.5;
}

/* ─── terminal state (rolled_back / queued stale) ────────────────────────── */
.terminal-state {
  display: flex;
  align-items: flex-start;
  gap: 20px;
  flex-wrap: wrap;
}

.terminal-badge {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 6px 14px;
  border-radius: var(--rounded);
  font-size: 0.9rem;
  font-weight: 600;
}

.terminal-meta {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
}

.terminal-meta-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

/* ─── banner (cancel error) ──────────────────────────────────────────────── */
.banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 0.83rem;
  line-height: 1.5;
  border-radius: var(--rounded);
  padding: 10px 14px;
}

.banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}

.inline-banner {
  margin: 0 24px;
}

/* ─── error state ────────────────────────────────────────────────────────── */
.error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 72px 32px;
  text-align: center;
  background: var(--color-card);
  border: 1.5px dashed var(--color-red-line);
  border-radius: var(--rounded-card);
  color: var(--color-red);
}

.error-message {
  font-size: 0.86rem;
  color: var(--color-dim);
  max-width: 40ch;
  line-height: 1.6;
}

/* ─── loading skeleton ───────────────────────────────────────────────────── */
.detail-loading {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-card);
  overflow: hidden;
}

.skel-head {
  height: 64px;
  background: var(--color-card-2);
  border-bottom: 1px solid var(--color-border);
}

.skel-body {
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.skel {
  display: block;
  background: linear-gradient(
    90deg,
    var(--color-inset) 0%,
    oklch(100% 0 0 / 0.06) 50%,
    var(--color-inset) 100%
  );
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: shimmer 1.4s ease-in-out infinite;
}

@keyframes shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}

@media (prefers-reduced-motion: reduce) {
  .skel { animation: none; background: var(--color-inset); }
}

.skel--title    { height: 22px; width: 45%; }
.skel--meta     { height: 40px; }
.skel--timeline { height: 80px; }
.skel--steps    { height: 160px; }

/* ─── buttons ─────────────────────────────────────────────────────────────── */
.btn-secondary {
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 0 15px;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  font-family: var(--font-sans);
  font-size: 0.83rem;
  font-weight: 500;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.btn-secondary:hover { border-color: var(--color-faint); }

.btn-secondary:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.btn-danger-ghost {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 13px;
  background: var(--color-red-soft);
  color: var(--color-red);
  border: 1px solid var(--color-red-line);
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  flex-shrink: 0;
  transition: background-color var(--duration-fast);
}

.btn-danger-ghost:hover:not(:disabled) {
  background: oklch(62% 0.18 22 / 0.25);
}

.btn-danger-ghost:focus-visible {
  outline: 2px solid var(--color-red);
  outline-offset: 2px;
}

.btn-danger-ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ─── spinners ────────────────────────────────────────────────────────────── */
.spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: spin 0.7s linear infinite;
  flex-shrink: 0;
}

.spinner--red {
  border-color: oklch(69% 0.17 22 / 0.3);
  border-top-color: var(--color-red);
}

.spinner--amber {
  border-color: oklch(83% 0.13 82 / 0.3);
  border-top-color: var(--color-amber);
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spinner { animation: none; border-top-color: currentColor; }
}
</style>

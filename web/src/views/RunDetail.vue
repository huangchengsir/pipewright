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
  diagnoseRun,
  deployRun,
  retryFailedDeploy,
  continueDeploy,
  abortDeploy,
  listApprovals,
  approveStage,
  rejectStage,
  type RunDetail,
  type RunStatus,
  type StepStatus,
  type DiagnosisDTO,
  type HealthCheckType,
  type HealthCheckInput,
  type DeployStrategy,
  type ApprovalRecord,
} from '../api/runs'
import { listServers, type Server } from '../api/servers'
import { HttpError } from '../api/http'
import DiagnosisPanel from '../components/run/DiagnosisPanel.vue'
import SuccessFailDiff from '../components/run/SuccessFailDiff.vue'
import RunTerminal from '../components/run/RunTerminal.vue'
import RunStepList from '../components/run/RunStepList.vue'
import ArtifactList from '../components/run/ArtifactList.vue'
import TestReportPanel from '../components/run/TestReportPanel.vue'
import DeployTargets from '../components/run/DeployTargets.vue'
import RunDagView from '../components/run/RunDagView.vue'
import PromotionPanel from '../components/run/PromotionPanel.vue'

// ─── route ────────────────────────────────────────────────────────────────────

const route  = useRoute()
const router = useRouter()
const runId  = computed(() => route.params.id as string)

// ─── load state ──────────────────────────────────────────────────────────────

type LoadState = 'loading' | 'idle' | 'error'

const loadState = ref<LoadState>('loading')
const loadError = ref('')
const run = ref<RunDetail | null>(null)

// 选中的步骤序号(点击步骤详情切换「单步日志」过滤);null = 全部步骤。
const selectedStepOrdinal = ref<number | null>(null)

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

// ─── approval gate (Story 8-4) ──────────────────────────────────────────────────
// 运行阻塞在审批门(status=waiting_approval)时,展示待批阶段 + 批准/拒绝按钮。

const pendingApproval = ref<ApprovalRecord | null>(null)
const approving = ref(false)
const approvalError = ref('')

async function loadApprovals(): Promise<void> {
  try {
    const { items } = await listApprovals(runId.value)
    pendingApproval.value = items.find((a) => a.status === 'pending') ?? null
  } catch {
    pendingApproval.value = null
  }
}

async function decideApproval(approve: boolean): Promise<void> {
  const stage = pendingApproval.value
  if (!stage || approving.value) return
  approving.value = true
  approvalError.value = ''
  try {
    if (approve) await approveStage(runId.value, stage.stageId)
    else await rejectStage(runId.value, stage.stageId)
    pendingApproval.value = null
    // status flips via SSE (waiting_approval → running → terminal); refresh run + approvals.
    run.value = await getRun(runId.value)
    await loadApprovals()
  } catch (err) {
    approvalError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? `操作失败(${err.status})`)
        : '审批请求失败,请稍后重试'
  } finally {
    approving.value = false
  }
}

// ─── deploy state (Story 4-2: SSH 部署执行 / FR-10) ─────────────────────────────
// 成功态可把某个产物部署到所选目标服务器;同步执行,回填 run.targets slot。

const deployServers = ref<Server[]>([])
const deployServersLoaded = ref(false)
const selectedArtifactId = ref('')
const selectedServerIds = ref<string[]>([])
const deploying = ref(false)
const deployError = ref('')
const showDeployPanel = ref(false)

// ─── deploy-time health gate (Story 4-3 / FR-12) ─────────────────────────────
// 部署后健康门控:type none(默认,跳过)/ http(curl 探测)/ command(命令探测)。
// 字段值仅作表单状态;提交时按 type 收敛为 HealthCheckInput(none → 不传)。
const hcType = ref<HealthCheckType>('none')
const hcUrl = ref('')
const hcCommand = ref('') // 以空格分词为 command array(逐个 array 元素,经后端不拼 shell)
const hcRetries = ref(3)
const hcIntervalSeconds = ref(3)
const hcTimeoutSeconds = ref(5)

// ─── 零停机切换高级选项(Story 4-4 / FR-11)──────────────────────────────────
// dist/jar 走「releases/<runId> + current 软链原子切换」;以下为可选高级覆盖,默认隐藏。
// releaseBase:发布根目录 <base>(空 → 后端从 path 推导);keepReleases:额外保留旧发布份数。
// 经既有 deployConfig map 透传,不改部署端点形状。
const showAdvanced = ref(false)
const releaseBase = ref('')
const keepReleases = ref(1)

// ─── 部署策略(Story 8-8 / FR-8-8)──────────────────────────────────────────
// rolling(默认)= 全机并行各自成败;canary = 先发金丝雀批次、健康通过才铺其余;
// blue_green = 全机先就绪、统一切换、失败机群回滚(release 类产物 dist/jar)。
// 金丝雀批量经 deployConfig.canaryCount 透传。
const deployStrategy = ref<DeployStrategy>('rolling')
const canaryCount = ref(1)

const deployStrategyOptions: ReadonlyArray<{ value: DeployStrategy; label: string; desc: string }> = [
  { value: 'rolling', label: '滚动', desc: '全机并行,各自成败' },
  { value: 'canary', label: '金丝雀', desc: '先发小批,通过再铺' },
  { value: 'blue_green', label: '蓝绿', desc: '统一切换,失败全退' },
  { value: 'interactive', label: '交互式分批', desc: '先发首批,暂停等人确认' },
]

// buildDeployConfig 据高级选项收敛 deployConfig;空字段不传(后端取默认)。
function buildDeployConfig(): Record<string, string> | undefined {
  const cfg: Record<string, string> = {}
  const base = releaseBase.value.trim()
  if (base) cfg.releaseBase = base
  const keep = clampInt(keepReleases.value, 1, 50, 1)
  // 仅在非默认(1)时下发,避免无谓字段。
  if (keep !== 1) cfg.keepReleases = String(keep)
  // 首批量:canary / interactive 策略且 >1 时下发(默认 1 台)。
  if (deployStrategy.value === 'canary' || deployStrategy.value === 'interactive') {
    const n = clampInt(canaryCount.value, 1, 100, 1)
    if (n > 1) cfg.canaryCount = String(n)
  }
  return Object.keys(cfg).length > 0 ? cfg : undefined
}

// buildHealthCheck 据表单 type 收敛 HealthCheckInput;none → undefined(等同 4-2 不做健康检查)。
function buildHealthCheck(): HealthCheckInput | undefined {
  if (hcType.value === 'none') return undefined
  const base = {
    retries: clampInt(hcRetries.value, 1, 20, 3),
    intervalSeconds: clampInt(hcIntervalSeconds.value, 0, 3600, 3),
    timeoutSeconds: clampInt(hcTimeoutSeconds.value, 1, 60, 5),
  }
  if (hcType.value === 'http') {
    return { type: 'http', url: hcUrl.value.trim(), ...base }
  }
  // command:按空白分词为 array(各元素独立,AC-SEC-02 不拼 shell)。
  const command = hcCommand.value.trim().split(/\s+/).filter(Boolean)
  return { type: 'command', command, ...base }
}

function clampInt(v: number, lo: number, hi: number, fallback: number): number {
  const n = Math.trunc(Number(v))
  if (!Number.isFinite(n)) return fallback
  return Math.min(hi, Math.max(lo, n))
}

// 提交前的健康检查表单是否有效(http 需 url;command 需至少一个 token;none 永远有效)。
const healthCheckValid = computed(() => {
  if (hcType.value === 'http') return hcUrl.value.trim().length > 0
  if (hcType.value === 'command') return hcCommand.value.trim().length > 0
  return true
})

// 是否已部署过(run.targets 非空 → 已有部署结果)。
const hasDeployed = computed(() => !!run.value?.targets && run.value.targets.length > 0)

async function ensureDeployServers(): Promise<void> {
  if (deployServersLoaded.value) return
  try {
    deployServers.value = await listServers()
  } catch {
    deployServers.value = []
  } finally {
    deployServersLoaded.value = true
  }
}

async function openDeployPanel(): Promise<void> {
  deployError.value = ''
  showDeployPanel.value = true
  await ensureDeployServers()
  // 默认选中首个产物(若存在);服务器需用户显式勾选。
  if (!selectedArtifactId.value && run.value?.artifacts.length) {
    selectedArtifactId.value = run.value.artifacts[0].id
  }
}

function toggleServer(id: string): void {
  const idx = selectedServerIds.value.indexOf(id)
  if (idx >= 0) selectedServerIds.value.splice(idx, 1)
  else selectedServerIds.value.push(id)
}

const canDeploy = computed(
  () =>
    !!selectedArtifactId.value &&
    selectedServerIds.value.length > 0 &&
    healthCheckValid.value &&
    !deploying.value,
)

async function handleDeploy(): Promise<void> {
  if (!run.value || !canDeploy.value) return
  deploying.value = true
  deployError.value = ''
  try {
    const res = await deployRun(run.value.id, {
      artifactId: selectedArtifactId.value,
      serverIds: [...selectedServerIds.value],
      deployConfig: buildDeployConfig(),
      healthCheck: buildHealthCheck(),
      strategy: deployStrategy.value,
    })
    // 同步执行:用返回的 targets 直接填 slot(与 run-detail 同源形状)。
    run.value = { ...run.value, targets: res.targets, status: deriveStatus(res.targets) }
    showDeployPanel.value = false
    selectedServerIds.value = []
  } catch (err) {
    if (err instanceof HttpError) {
      deployError.value = err.apiError?.message ?? `部署失败(${err.status})`
    } else {
      deployError.value = '部署请求失败,请稍后重试'
    }
  } finally {
    deploying.value = false
  }
}

// deriveStatus 据每机结果推 run 终态(与后端 SetDeployTerminal 同语义),让状态徽标即时更新。
function deriveStatus(targets: { status: string }[]): RunStatus {
  const anyOk = targets.some((t) => t.status === 'success')
  const anyBad = targets.some((t) => t.status !== 'success')
  if (anyBad && anyOk) return 'partial_failed'
  if (anyBad) return 'failed'
  return run.value?.status ?? 'success'
}

// ─── retry only failed targets (Story 4-5 / FR-13) ───────────────────────────
// 有失败/回滚目标时,DeployTargets 显「重试失败目标」→ emit retry → 这里复用上次部署的
// 产物(selectedArtifactId,回退首个产物)+ 配置 + 健康检查,只对失败台重跑;成功台不动。
// 后端逐目标 upsert + 重算终态,返回全量最新 targets;直接回填 slot。

const retrying = ref(false)
const retryError = ref('')

async function handleRetryFailed(): Promise<void> {
  if (!run.value || retrying.value) return
  // 复用产物:优先上次部署选中的;否则回退首个产物(刷新页面后表单态丢失的兜底)。
  const artifactId = selectedArtifactId.value || run.value.artifacts[0]?.id || ''
  if (!artifactId) {
    retryError.value = '无可用产物,无法重试'
    return
  }
  retrying.value = true
  retryError.value = ''
  try {
    const res = await retryFailedDeploy(run.value.id, {
      artifactId,
      // serverIds 省略 → 后端重试该 run 当前所有失败/回滚目标。
      deployConfig: buildDeployConfig(),
      healthCheck: buildHealthCheck(),
    })
    run.value = { ...run.value, targets: res.targets, status: deriveStatus(res.targets) }
  } catch (err) {
    if (err instanceof HttpError) {
      retryError.value = err.apiError?.message ?? `重试失败(${err.status})`
    } else {
      retryError.value = '重试请求失败,请稍后重试'
    }
  } finally {
    retrying.value = false
  }
}

// ─── 交互式分批部署:续发 / 中止(P0)─────────────────────────────────────────
const batchBusy = ref(false)
const batchError = ref('')

// 是否有暂停待确认的批次(pending 目标存在)。
const hasPendingTargets = computed(() =>
  (run.value?.targets ?? []).some((t) => t.status === 'pending'),
)
const pendingCount = computed(() => (run.value?.targets ?? []).filter((t) => t.status === 'pending').length)

async function handleContinueDeploy(): Promise<void> {
  if (!run.value || batchBusy.value) return
  const artifactId = selectedArtifactId.value || run.value.artifacts[0]?.id || ''
  if (!artifactId) {
    batchError.value = '无可用产物,无法续发'
    return
  }
  batchBusy.value = true
  batchError.value = ''
  try {
    const res = await continueDeploy(run.value.id, {
      artifactId,
      deployConfig: buildDeployConfig(),
      healthCheck: buildHealthCheck(),
    })
    run.value = { ...run.value, targets: res.targets, status: deriveStatus(res.targets) }
  } catch (err) {
    batchError.value = err instanceof HttpError ? (err.apiError?.message ?? `续发失败(${err.status})`) : '续发请求失败,请稍后重试'
  } finally {
    batchBusy.value = false
  }
}

async function handleAbortDeploy(): Promise<void> {
  if (!run.value || batchBusy.value) return
  batchBusy.value = true
  batchError.value = ''
  try {
    const res = await abortDeploy(run.value.id)
    run.value = { ...run.value, targets: res.targets, status: deriveStatus(res.targets) }
  } catch (err) {
    batchError.value = err instanceof HttpError ? (err.apiError?.message ?? `中止失败(${err.status})`) : '中止请求失败,请稍后重试'
  } finally {
    batchBusy.value = false
  }
}

// ─── data loading ─────────────────────────────────────────────────────────────

async function loadRun(): Promise<void> {
  loadState.value = 'loading'
  loadError.value = ''
  try {
    run.value = await getRun(runId.value)
    loadState.value = 'idle'
    if (run.value.status === 'waiting_approval') void loadApprovals()
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

// Silent full-snapshot refresh — re-fetch RunDetail WITHOUT toggling loadState
// (no spinner flicker). The SSE `step` event only carries flat status metadata
// and never the resolved commit, so while a run is in-progress the commit field
// and per-stage step grouping would otherwise stay stale until the terminal
// reload. A periodic silent refresh keeps commit + steps (with stage) + status
// current during execution; SSE still drives live log tailing.
async function silentRefresh(): Promise<void> {
  try {
    const fresh = await getRun(runId.value)
    run.value = fresh
    if (fresh.status === 'waiting_approval') void loadApprovals()
  } catch {
    // Transient fetch failure — next tick retries; never surfaced to the UI.
  }
}

// ─── SSE subscription ─────────────────────────────────────────────────────────
// Subscribes when run is in-progress (running/queued).
// Cleans up on terminal states or component unmount.

let cleanupSse: (() => void) | null = null
// Periodic in-progress snapshot refresh; lives exactly as long as the SSE
// subscription (started/stopped alongside it).
let refreshTimer: ReturnType<typeof setInterval> | null = null

function startSse(): void {
  if (cleanupSse) return
  cleanupSse = subscribeRunEvents(runId.value, {
    onStatus({ status }) {
      if (!run.value) return
      run.value = { ...run.value, status }
      // Approval gate (8-4): load pending stage when blocked; clear once resumed.
      if (status === 'waiting_approval') void loadApprovals()
      else pendingApproval.value = null
      // Stop SSE on terminal state + 重拉完整 run:终态由后端补齐的 commit / 构建产物 / 时长
      // 经 SSE 只推了 status,需重新 GET 才能拿到 → 否则要手动刷新才出来。
      if (isTerminal(status)) {
        stopSse()
        void loadRun()
      }
    },
    onStep({ step }) {
      if (!run.value) return
      const steps = run.value.steps.map((s) => (s.id === step.id ? { ...s, ...step } : s))
      run.value = { ...run.value, steps }
    },
    // onError is intentionally omitted — subscribeRunEvents falls back to polling
  })
  if (refreshTimer === null) {
    refreshTimer = setInterval(() => { void silentRefresh() }, 4000)
  }
}

function stopSse(): void {
  if (cleanupSse) {
    cleanupSse()
    cleanupSse = null
  }
  if (refreshTimer !== null) {
    clearInterval(refreshTimer)
    refreshTimer = null
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
    if ((status === 'running' || status === 'queued' || status === 'waiting_approval') && !cleanupSse) {
      startSse()
    } else if (isTerminal(status)) {
      stopSse()
    }
  },
)

onMounted(async () => {
  await loadRun()
  const s = run.value?.status
  if (s === 'running' || s === 'queued' || s === 'waiting_approval') {
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
  waiting_approval:{ label: '等待审批', dot: 'var(--color-amber)', bg: 'var(--color-amber-soft)', border: 'var(--color-amber-line)',   text: 'var(--color-amber)', pulse: true  },
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

// Commit 区显示文案:有 commit 取短 hash;空时——运行已失败(典型源码克隆失败,
// 还没解析到 commit 就挂了)给失败语义「未取到」而非纯空白(误导成还在跑),
// 其余(进行中尚未解析)用占位符 —。
function commitDisplay(commit: string, status: RunStatus): string {
  if (commit) return shortCommit(commit)
  if (status === 'failed' || status === 'partial_failed' || status === 'rolled_back') return '未取到'
  return '—'
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

        <!-- ── Approval gate (Story 8-4): waiting for manual approval ── -->
        <div
          v-if="run.status === 'waiting_approval' && pendingApproval"
          class="approval-gate"
          role="region"
          aria-label="等待人工审批"
        >
          <div class="approval-gate-icon" aria-hidden="true">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
          </div>
          <div class="approval-gate-body">
            <div class="approval-gate-title">阶段「{{ pendingApproval.stageName }}」等待人工审批</div>
            <div class="approval-gate-sub">批准后继续执行,拒绝则该阶段失败、运行终止。</div>
            <div v-if="approvalError" class="approval-gate-error" role="alert">{{ approvalError }}</div>
          </div>
          <div class="approval-gate-actions">
            <button class="approval-btn approval-btn--reject" :disabled="approving" @click="decideApproval(false)">
              拒绝
            </button>
            <button class="approval-btn approval-btn--approve" :disabled="approving" @click="decideApproval(true)">
              {{ approving ? '处理中…' : '批准' }}
            </button>
          </div>
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
            <span
              class="meta-val mono"
              :class="{ 'meta-val--failed': !run.trigger.commit && (run.status === 'failed' || run.status === 'partial_failed' || run.status === 'rolled_back') }"
            >{{ commitDisplay(run.trigger.commit, run.status) }}</span>
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

            <!-- DAG topology: stage graph with steps, replaces linear skewer -->
            <div class="dag-section">
              <h2 class="section-title">流水线进度</h2>
              <RunDagView
                :project-id="run.projectId"
                :steps="run.steps"
                :run-status="run.status"
              />
            </div>

            <!-- Two-column layout: 步骤详情(点击单步看其日志) + 实时日志终端 -->
            <div class="running-body">
              <RunStepList :steps="run.steps" :selected="selectedStepOrdinal" @select="selectedStepOrdinal = $event" />
              <div class="slot-log-live" role="region" aria-label="实时日志终端">
                <RunTerminal :run-id="run.id" :live="true" :filter-ordinal="selectedStepOrdinal" />
              </div>
            </div>
          </div>
        </template>

        <!-- ── STATE: success ────────────────────────────────────────── -->
        <template v-else-if="run.status === 'success'">
          <div class="state-section">

            <!-- DAG topology: full-green stage graph -->
            <div class="dag-section">
              <h2 class="section-title">流水线完成</h2>
              <RunDagView
                :project-id="run.projectId"
                :steps="run.steps"
                :run-status="run.status"
              />
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
              构建产物列表 (Story 3-4 / FR-6 实现)
              成功态填 run-detail 冻结 artifacts slot:type 徽标 + name + reference(可复制)+ 大小。
              产物契约是 Epic 3 一等交付物;Epic 4 部署按 (type, reference) 消费。
              不扰终端(3-6)/零停机(Epic 4)/诊断(7-2)slot。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <ArtifactList :artifacts="run.artifacts" :run-id="run.id" />

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              测试报告 + 质量门禁 (Story 8-6 / FR-8-6 实现)
              script 步骤声明 testReport=junit + reportPath → 后端解析 JUnit XML(可选 Cobertura
              覆盖率)→ 通过/失败/跳过计数 + 覆盖率条 + 门禁裁决。门禁不过则阻断下游部署。
              自取数据(不在冻结 run-detail DTO 内);无报告 → 不渲染。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <TestReportPanel :run-id="run.id" />

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: 环境晋级 (Story 8-7 / FR-8-7 实现)
              成功态填 run-detail 晋级 slot:晋级按钮 + gated 审批门联动 + 晋级历史。
              自取数据(promotion API);不扰其他 slot。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <PromotionPanel
              :run-id="run.id"
              :project-id="run.projectId"
              :run-status="run.status"
              @promotion-pending="loadApprovals"
            />
            <!-- END SLOT: 环境晋级 -->

            <!-- 步骤详情(点击单步看其日志)+ 历史日志回放(2 列;步骤完成后仍可见) -->
            <div class="running-body">
              <RunStepList :steps="run.steps" :selected="selectedStepOrdinal" @select="selectedStepOrdinal = $event" />
              <div class="log-history" role="region" aria-label="历史运行日志">
                <RunTerminal :run-id="run.id" :live="false" :filter-ordinal="selectedStepOrdinal" />
              </div>
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: SSH 部署执行 (Story 4-2 / FR-10 实现)
              成功态填 run-detail 冻结 targets slot:部署入口(选产物 + 选服务器 + 触发)
              + 每机部署结果卡。命令 array 化经 SSH 执行(AC-SEC-02);message 绝无明文密钥。
              零停机切换/回滚 = 4-4;多机扇出 = 4-5,不在本期。
              不扰终端(3-6)/诊断(7-2)/产物(3-4)slot。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->

            <!-- 已部署 → 渲染每机结果卡(填 targets slot);有失败目标时可仅重试失败台(4-5) -->
            <DeployTargets
              v-if="hasDeployed && run.targets"
              :targets="run.targets"
              :retrying="retrying"
              :retry-error="retryError"
              @retry="handleRetryFailed"
            />

            <!-- 交互式分批部署:暂停态(有 pending 目标)→ 继续 / 中止(P0)-->
            <div v-if="hasPendingTargets" class="batch-pause" role="status">
              <div class="batch-pause-head">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <rect x="6" y="5" width="4" height="14" rx="1" /><rect x="14" y="5" width="4" height="14" rx="1" />
                </svg>
                <span>分批部署已暂停:首批已发布,其余 <strong>{{ pendingCount }}</strong> 台待确认</span>
              </div>
              <p v-if="batchError" class="batch-pause-err" role="alert">{{ batchError }}</p>
              <div class="batch-pause-actions">
                <button class="batch-btn batch-btn--go" :disabled="batchBusy" @click="handleContinueDeploy">
                  {{ batchBusy ? '处理中…' : '继续部署其余' }}
                </button>
                <button class="batch-btn batch-btn--abort" :disabled="batchBusy" @click="handleAbortDeploy">
                  中止(保留旧版本)
                </button>
              </div>
            </div>

            <!-- 部署入口:仅在有产物时可用 -->
            <div v-if="run.artifacts.length > 0" class="deploy-entry">
              <button
                v-if="!showDeployPanel"
                class="btn-deploy"
                @click="openDeployPanel"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
                </svg>
                {{ hasDeployed ? '再次部署' : '部署到目标服务器' }}
              </button>

              <!-- 部署配置面板:选产物 + 勾选服务器 + 触发 -->
              <div v-else class="deploy-panel" role="region" aria-label="部署配置">
                <div class="deploy-panel-head">
                  <span class="deploy-panel-title">部署到目标服务器</span>
                  <button class="deploy-close" aria-label="收起部署面板" @click="showDeployPanel = false">✕</button>
                </div>

                <!-- 选择产物 -->
                <div class="deploy-field">
                  <label class="deploy-label" for="deploy-artifact">部署产物</label>
                  <select id="deploy-artifact" v-model="selectedArtifactId" class="deploy-select">
                    <option v-for="a in run.artifacts" :key="a.id" :value="a.id">
                      {{ a.name }} · {{ a.type }} · {{ a.reference }}
                    </option>
                  </select>
                </div>

                <!-- 选择服务器 -->
                <div class="deploy-field">
                  <span class="deploy-label">目标服务器</span>
                  <p v-if="deployServersLoaded && deployServers.length === 0" class="deploy-empty">
                    暂无已登记的服务器,请先在「服务器」页登记。
                  </p>
                  <div v-else class="deploy-servers">
                    <label
                      v-for="s in deployServers"
                      :key="s.id"
                      class="deploy-server-opt"
                      :class="{ 'deploy-server-opt--on': selectedServerIds.includes(s.id) }"
                    >
                      <input
                        type="checkbox"
                        :checked="selectedServerIds.includes(s.id)"
                        @change="toggleServer(s.id)"
                      />
                      <span class="deploy-server-name">{{ s.name }}</span>
                      <span class="deploy-server-host mono">{{ s.user }}@{{ s.host }}:{{ s.port }}</span>
                    </label>
                  </div>
                </div>

                <!-- 健康检查(Story 4-3 / FR-12):部署后门控,通过才算该机成功 -->
                <div class="deploy-field">
                  <label class="deploy-label" for="deploy-hc-type">健康检查</label>
                  <select id="deploy-hc-type" v-model="hcType" class="deploy-select">
                    <option value="none">不检查(命令成功即视为部署成功)</option>
                    <option value="http">HTTP 探测(curl)</option>
                    <option value="command">命令探测</option>
                  </select>

                  <!-- http:探测 url -->
                  <input
                    v-if="hcType === 'http'"
                    v-model="hcUrl"
                    class="deploy-select hc-input"
                    type="url"
                    inputmode="url"
                    placeholder="http://localhost:8080/healthz"
                    aria-label="健康检查 URL"
                  />

                  <!-- command:探测命令(空格分词为 array,不拼 shell) -->
                  <input
                    v-if="hcType === 'command'"
                    v-model="hcCommand"
                    class="deploy-select hc-input mono"
                    type="text"
                    placeholder="例:systemctl is-active shop"
                    aria-label="健康检查命令"
                  />

                  <!-- 重试 / 间隔 / 超时(http + command 通用) -->
                  <div v-if="hcType !== 'none'" class="hc-params">
                    <label class="hc-param">
                      <span class="hc-param-key">重试次数</span>
                      <input v-model.number="hcRetries" class="hc-num" type="number" min="1" max="20" />
                    </label>
                    <label class="hc-param">
                      <span class="hc-param-key">间隔(秒)</span>
                      <input v-model.number="hcIntervalSeconds" class="hc-num" type="number" min="0" max="3600" />
                    </label>
                    <label class="hc-param">
                      <span class="hc-param-key">超时(秒)</span>
                      <input v-model.number="hcTimeoutSeconds" class="hc-num" type="number" min="1" max="60" />
                    </label>
                  </div>
                  <p v-if="hcType !== 'none' && !healthCheckValid" class="deploy-empty">
                    {{ hcType === 'http' ? '请填写探测 URL。' : '请填写探测命令。' }}
                  </p>
                </div>

                <!-- 部署策略(Story 8-8 / FR-8-8):rolling | canary | blue_green -->
                <div class="deploy-field">
                  <span class="deploy-label">发布策略</span>
                  <div class="strat-seg" role="radiogroup" aria-label="发布策略">
                    <button
                      v-for="opt in deployStrategyOptions"
                      :key="opt.value"
                      type="button"
                      class="strat-opt"
                      :class="{ 'strat-opt--active': deployStrategy === opt.value }"
                      role="radio"
                      :aria-checked="deployStrategy === opt.value"
                      @click="deployStrategy = opt.value"
                    >
                      <span class="strat-opt-name">{{ opt.label }}</span>
                      <span class="strat-opt-desc">{{ opt.desc }}</span>
                    </button>
                  </div>
                  <label v-if="deployStrategy === 'canary'" class="adv-row strat-canary">
                    <span class="adv-key">金丝雀台数</span>
                    <input v-model.number="canaryCount" class="hc-num" type="number" min="1" max="100" aria-label="金丝雀台数" />
                    <span class="adv-hint strat-canary-hint">先发这么多台并健康门控,通过后再铺其余</span>
                  </label>
                  <p v-else-if="deployStrategy === 'blue_green'" class="adv-hint">
                    全机先就绪发布目录、再统一原子切换;任一机切换失败则整个机群回滚到上一发布(dist / jar)。
                  </p>
                </div>

                <!-- 零停机切换高级选项(Story 4-4):默认隐藏;dist/jar 走 releases + current 软链 -->
                <div class="deploy-field">
                  <button
                    type="button"
                    class="adv-toggle"
                    :aria-expanded="showAdvanced"
                    @click="showAdvanced = !showAdvanced"
                  >
                    <svg
                      class="adv-chevron"
                      :class="{ 'adv-chevron--open': showAdvanced }"
                      width="12" height="12" viewBox="0 0 24 24" fill="none"
                      stroke="currentColor" stroke-width="2.4" aria-hidden="true"
                    >
                      <path d="M9 18l6-6-6-6" />
                    </svg>
                    高级:零停机发布选项
                  </button>

                  <div v-if="showAdvanced" class="adv-body">
                    <label class="adv-row">
                      <span class="adv-key">发布根目录</span>
                      <input
                        v-model="releaseBase"
                        class="deploy-select mono"
                        type="text"
                        placeholder="留空 → 后端从部署路径推导(<base>/releases/<runId> + <base>/current)"
                        aria-label="发布根目录"
                      />
                    </label>
                    <label class="adv-row">
                      <span class="adv-key">保留旧发布份数</span>
                      <input v-model.number="keepReleases" class="hc-num" type="number" min="1" max="50" />
                    </label>
                    <p class="adv-hint">
                      dist / jar 部署到版本化发布目录并原子切换 current 软链;健康检查失败时自动回滚到上一发布。
                    </p>
                  </div>
                </div>

                <!-- 部署错误 -->
                <div v-if="deployError" class="banner banner--error" role="alert">
                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
                  </svg>
                  {{ deployError }}
                </div>

                <!-- 触发 -->
                <div class="deploy-actions">
                  <button class="btn-deploy" :disabled="!canDeploy" :aria-busy="deploying" @click="handleDeploy">
                    <span v-if="deploying" class="spinner" aria-hidden="true" />
                    {{ deploying ? '部署中…' : '开始部署' }}
                  </button>
                  <span v-if="selectedServerIds.length > 0" class="deploy-hint">
                    已选 {{ selectedServerIds.length }} 台
                  </span>
                </div>
              </div>
            </div>
            <!-- END SLOT: SSH 部署执行 -->

          </div>
        </template>

        <!-- ── STATE: failed ─────────────────────────────────────────── -->
        <template v-else-if="run.status === 'failed'">
          <div class="state-section">

            <!-- DAG topology: failed stage highlighted -->
            <div class="dag-section">
              <h2 class="section-title">流水线失败</h2>
              <RunDagView
                :project-id="run.projectId"
                :steps="run.steps"
                :run-status="run.status"
              />
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

            <!-- 失败日志证据(只读历史回放,Story 3-6)。在 AI 诊断面板之上;
                 不属于 7-2 的 DiagnosisPanel slot,二者共存。 -->
            <div class="log-history" role="region" aria-label="失败运行日志">
              <RunTerminal :run-id="run.id" :live="false" />
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: AI 失败诊断 (Story 7-2 实现)
              骨架所有权: 本区块归 Story 3.1;
              Story 7-2 在此区块内填入 DiagnosisPanel 组件.
              仅修改此区块内容;不改骨架结构。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <DiagnosisPanel
              :diagnosis="run.diagnosis"
              :run-id="run.id"
              @diagnosed="(dto: DiagnosisDTO) => { if (run) run = { ...run, diagnosis: dto } }"
            />
            <!-- END SLOT: AI 失败诊断 -->

            <!-- 成功/失败差异对比(Story 7-3;FR-25)。作为诊断「证据」补充,挂在 AI 诊断面板之后;
                 独立区块,不属于 7-2 的 DiagnosisPanel slot,二者共存。无 baseline/克隆失败时组件自身降级。 -->
            <div class="diff-section" role="region" aria-label="成功失败代码差异对比">
              <SuccessFailDiff :run-id="run.id" />
            </div>

          </div>
        </template>

        <!-- ── STATE: partial_failed ────────────────────────────────── -->
        <template v-else-if="run.status === 'partial_failed'">
          <div class="state-section">

            <!-- DAG topology: partial failure view -->
            <div class="dag-section">
              <h2 class="section-title">部分失败</h2>
              <RunDagView
                :project-id="run.projectId"
                :steps="run.steps"
                :run-status="run.status"
              />
            </div>

            <div class="partial-info" role="status">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
                <path d="M12 9v4M12 17h.01"/>
              </svg>
              部分目标失败,失败台已独立回滚;其余台继续运行,互不连累。
            </div>

            <!-- 历史日志回放(只读,Story 3-6) -->
            <div class="log-history" role="region" aria-label="历史运行日志">
              <RunTerminal :run-id="run.id" :live="false" />
            </div>

            <!--
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
              SLOT: 多机目标扇出聚合 (Story 4-5 / FR-13 实现)
              partial_failed 态填 run-detail 冻结 targets slot:各台独立状态卡(success/failed/
              rolled_back)+ 人读 message + 耗时,有失败台时显「仅重试失败目标」入口(成功台不动)。
              并行扇出有界(后端信号量);命令 array 化(AC-SEC-02);message 绝无明文密钥。
              ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
            -->
            <DeployTargets
              v-if="hasDeployed && run.targets"
              :targets="run.targets"
              :retrying="retrying"
              :retry-error="retryError"
              @retry="handleRetryFailed"
            />
            <!-- 兜底:partial_failed 但无 targets(理论上不该出现)— 占位提示,不白屏 -->
            <div v-else class="slot-panel slot-panel--multi" role="region" aria-label="多机目标状态">
              <div class="slot-header">
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                  <rect x="2" y="14" width="6" height="6" rx="1"/>
                  <rect x="16" y="14" width="6" height="6" rx="1"/>
                  <rect x="9" y="2" width="6" height="6" rx="1"/>
                  <path d="M5 14v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3M12 7v4"/>
                </svg>
                <span class="slot-title">多机目标扇出</span>
              </div>
              <div class="slot-body">
                <span class="slot-placeholder-text">暂无多机部署结果</span>
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

            <!-- DAG topology: terminal state step record -->
            <div v-if="run.steps.length > 0" class="dag-section">
              <h3 class="subsection-title">步骤记录</h3>
              <RunDagView
                :project-id="run.projectId"
                :steps="run.steps"
                :run-status="run.status"
              />
            </div>

            <!-- 历史日志回放(只读,Story 3-6) -->
            <div class="log-history" role="region" aria-label="历史运行日志">
              <RunTerminal :run-id="run.id" :live="false" />
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
/* ─── approval gate (Story 8-4) ──────────────────────────────────────────── */
.approval-gate {
  display: flex;
  align-items: flex-start;
  gap: 13px;
  margin: 14px 0;
  padding: 14px 16px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber);
  border-radius: var(--rounded);
}
.approval-gate-icon {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: var(--rounded-md);
  background: var(--color-amber);
  color: #fff;
}
.approval-gate-body { flex: 1; min-width: 0; }
.approval-gate-title { font-size: 0.92rem; font-weight: 650; color: var(--color-text); }
.approval-gate-sub { margin-top: 2px; font-size: 0.8rem; color: var(--color-dim); }
.approval-gate-error { margin-top: 6px; font-size: 0.78rem; color: var(--color-red); }
.approval-gate-actions { display: flex; gap: 8px; align-items: center; flex-shrink: 0; }
.approval-btn {
  height: 34px;
  padding: 0 16px;
  border-radius: var(--rounded-md);
  font: inherit;
  font-size: 0.84rem;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid transparent;
  transition: filter var(--duration-fast), background-color var(--duration-fast);
}
.approval-btn:disabled { opacity: 0.6; cursor: default; }
.approval-btn--approve { background: var(--color-green); color: #fff; }
.approval-btn--approve:not(:disabled):hover { filter: brightness(1.06); }
.approval-btn--reject {
  background: transparent;
  border-color: var(--color-border-strong);
  color: var(--color-dim);
}
.approval-btn--reject:not(:disabled):hover { border-color: var(--color-red); color: var(--color-red); }

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
/* 失败且未取到 commit:用失败色,不留纯空白(误导成还在跑)。 */
.meta-val--failed {
  color: var(--color-red);
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

/* ─── DAG topology section (FR-8-8) ──────────────────────────────────────── */
.dag-section {
  padding: 16px 20px;
  background: var(--color-inset);
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
}

.dag-section .section-title {
  margin-bottom: 14px;
}

.dag-section .subsection-title {
  margin-bottom: 12px;
}

/* ─── skewer (穿珠时间线) — kept for reference; not actively rendered ─────── */
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
  /* minmax(0,1fr):防止终端超长日志行把右列撑爆、溢出整页(自适应窗口宽度)。 */
  grid-template-columns: 280px minmax(0, 1fr);
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

.slot-placeholder-text {
  font-size: 0.83rem;
  color: var(--color-dim);
  font-weight: 500;
}

.slot-placeholder-hint {
  font-size: 0.75rem;
  color: var(--color-faint);
  line-height: 1.5;
}

/* Live log terminal slot (Story 3-6) — sits in running-body's second column */
.slot-log-live {
  min-width: 0;
}

/* Historical log replay (terminal states) — full width below summaries */
.log-history {
  min-width: 0;
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

/* ─── deploy entry / panel (Story 4-2) ───────────────────────────────────── */
.deploy-entry {
  display: flex;
  flex-direction: column;
}

.btn-deploy {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  align-self: flex-start;
  height: 36px;
  padding: 0 16px;
  background: var(--color-primary);
  color: #fff;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.84rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  transition: filter var(--duration-fast), opacity var(--duration-fast);
}

.btn-deploy:hover:not(:disabled) { filter: brightness(1.08); }

.btn-deploy:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.btn-deploy:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.deploy-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 18px;
  background: var(--color-inset);
  border: 1px solid var(--color-primary-soft);
  border-radius: var(--rounded);
}

.deploy-panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.deploy-panel-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}

.deploy-close {
  background: none;
  border: none;
  color: var(--color-faint);
  font-size: 0.9rem;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: var(--rounded-sm);
  transition: color var(--duration-fast);
}

.deploy-close:hover { color: var(--color-text); }

.deploy-field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.deploy-label {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.deploy-select {
  height: 36px;
  padding: 0 10px;
  background: var(--color-card);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  font-family: var(--font-sans);
  font-size: 0.82rem;
}

.deploy-select:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
}

.deploy-empty {
  font-size: 0.8rem;
  color: var(--color-faint);
  line-height: 1.5;
}

.deploy-servers {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.deploy-server-opt {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}

.deploy-server-opt:hover { border-color: var(--color-faint); }

.deploy-server-opt--on {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}

.deploy-server-name {
  font-size: 0.84rem;
  font-weight: 500;
  color: var(--color-text);
}

.deploy-server-host {
  font-size: 0.74rem;
  color: var(--color-faint);
  margin-left: auto;
}

.deploy-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.deploy-hint {
  font-size: 0.76rem;
  color: var(--color-dim);
}

/* ─── deploy-time health gate (Story 4-3 / FR-12) ─────────────────────────── */
.hc-input {
  margin-top: 2px;
}

.hc-params {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 2px;
}

.hc-param {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1 1 90px;
  min-width: 90px;
}

.hc-param-key {
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.03em;
  color: var(--color-faint);
}

.hc-num {
  height: 32px;
  padding: 0 8px;
  background: var(--color-card);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  font-family: var(--font-mono);
  font-size: 0.82rem;
  width: 100%;
}

.hc-num:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 1px;
  border-color: var(--color-primary);
}

/* ── 零停机发布高级选项(Story 4-4)──────────────────────────────────────── */
.adv-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: none;
  padding: 2px 0;
  cursor: pointer;
  color: var(--color-dim);
  font-size: 0.78rem;
  font-weight: 600;
}

.adv-toggle:hover { color: var(--color-text); }

.adv-chevron {
  transition: transform var(--duration-fast);
}

.adv-chevron--open { transform: rotate(90deg); }

@media (prefers-reduced-motion: reduce) {
  .adv-chevron { transition: none; }
}

.adv-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 10px;
  padding: 12px;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
}

.adv-row {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.adv-key {
  font-size: 0.72rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--color-faint);
}

.adv-row .hc-num { width: 120px; }

.adv-hint {
  font-size: 0.74rem;
  line-height: 1.5;
  color: var(--color-faint);
}

/* ─── 部署策略选择器(Story 8-8)──────────────────────────────────────────── */
.strat-seg {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-top: 6px;
}
.strat-opt {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 9px 10px;
  text-align: left;
  background: var(--color-bg-subtle, var(--color-card));
  border: 1px solid var(--color-border-strong);
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: border-color var(--duration-fast), background-color var(--duration-fast);
}
.strat-opt:hover { border-color: var(--color-primary); }
.strat-opt--active {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
}
.strat-opt:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px; }
.strat-opt-name { font-size: 0.82rem; font-weight: 650; color: var(--color-text); }
.strat-opt--active .strat-opt-name { color: var(--color-primary); }
.strat-opt-desc { font-size: 0.68rem; color: var(--color-faint); line-height: 1.35; }
.strat-canary { margin-top: 10px; }
.strat-canary-hint { margin-left: 8px; }

/* ─── 交互式分批部署:暂停态 banner(P0)─────────────────────────────────── */
.batch-pause {
  margin-top: 12px;
  border: 1px solid var(--color-amber, #b8860b);
  background: var(--color-amber-soft, #fbf3df);
  border-radius: var(--rounded-lg, 12px);
  padding: 13px 15px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.batch-pause-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.85rem;
  color: var(--color-text);
}
.batch-pause-head svg { color: var(--color-amber, #b8860b); flex: none; }
.batch-pause-head strong { color: var(--color-amber, #b8860b); }
.batch-pause-err { margin: 0; font-size: 0.78rem; color: var(--color-danger, #dc2626); }
.batch-pause-actions { display: flex; gap: 9px; }
.batch-btn {
  height: 32px; padding: 0 14px; border-radius: var(--rounded-md);
  font: inherit; font-size: 0.8rem; font-weight: 600; cursor: pointer;
  border: 1px solid var(--color-border-strong); background: var(--color-card); color: var(--color-text);
  transition: background-color var(--duration-fast), transform var(--duration-fast);
}
.batch-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.batch-btn--go { background: var(--color-primary); border-color: var(--color-primary); color: #fff; }
.batch-btn--go:hover:not(:disabled) { background: var(--color-primary-press, var(--color-primary)); transform: translateY(-1px); }
.batch-btn--abort:hover:not(:disabled) { border-color: var(--color-danger, #dc2626); color: var(--color-danger, #dc2626); }
</style>

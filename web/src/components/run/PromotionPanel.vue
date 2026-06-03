<script setup lang="ts">
/**
 * PromotionPanel — environment promotion action + history for a run detail page.
 * (Epic 8 · Story 8-7 / FR-8-7)
 *
 * Rendered inside RunDetail.vue on the success state.
 * Responsibilities:
 *   - Fetch the run's promotion history on mount
 *   - Show a "晋级到下一环境" button (or confirmation for gated envs)
 *   - Surface the gated-promotion waiting-approval banner (reuses approval
 *     endpoints existing in RunDetail; emits 'promotion-pending' for the parent
 *     to load its approval gate)
 *   - Show per-run promotion history timeline
 *
 * Props:
 *   runId       — the run to promote / show history for
 *   projectId   — used to fetch the environment chain
 *   runStatus   — current run status; promotion only when 'success'
 *
 * Emits:
 *   promotion-pending — when a gated promotion is submitted (409 status=pending)
 */
import { ref, computed, onMounted, watch } from 'vue'
import {
  getEnvironments,
  promoteRun,
  listRunPromotions,
  type EnvStage,
  type PromotionDTO,
  type PromotionStatus,
} from '../../api/promotion'
import { promotionStatusConfig, formatPromotionDate } from '../../api/promotion.helpers'
import { approveStage, rejectStage } from '../../api/runs'
import { HttpError } from '../../api/http'

// ─── Props / emits ────────────────────────────────────────────────────────────

const props = defineProps<{
  runId: string
  projectId: string
  runStatus: string
}>()

const emit = defineEmits<{
  /** The parent should refresh its approval gate list. */
  (e: 'promotion-pending'): void
}>()

// ─── Environment chain ────────────────────────────────────────────────────────

const chain = ref<EnvStage[]>([])
const chainLoaded = ref(false)

async function loadChain(): Promise<void> {
  try {
    const res = await getEnvironments(props.projectId)
    chain.value = res.environments
  } catch {
    chain.value = []
  } finally {
    chainLoaded.value = true
  }
}

// ─── Promotion history ────────────────────────────────────────────────────────

const promotions = ref<PromotionDTO[]>([])
const historyLoading = ref(false)
const historyError = ref('')

async function loadHistory(): Promise<void> {
  historyLoading.value = true
  historyError.value = ''
  try {
    const res = await listRunPromotions(props.runId)
    promotions.value = res.items
  } catch (err) {
    if (err instanceof HttpError && err.status !== 404) {
      historyError.value = err.apiError?.message ?? `加载历史失败(${err.status})`
    }
    promotions.value = []
  } finally {
    historyLoading.value = false
  }
}

// ─── Compute next environment ─────────────────────────────────────────────────

/**
 * Determine the next environment to promote to, based on chain + history.
 * Returns null if: chain empty, already at top, not configured.
 */
const nextEnv = computed<EnvStage | null>(() => {
  if (!chainLoaded.value || chain.value.length === 0) return null
  if (promotions.value.length === 0) {
    // Never promoted — target is chain[0]
    return chain.value[0]
  }
  // Find the highest-index successfully promoted or pending environment
  let maxIdx = -1
  for (const p of promotions.value) {
    if (p.status === 'promoted' || p.status === 'pending') {
      const idx = chain.value.findIndex((e) => e.name === p.targetEnvironment)
      if (idx > maxIdx) maxIdx = idx
    }
  }
  if (maxIdx < 0) {
    // No successful/pending promotions — back to chain[0]
    return chain.value[0]
  }
  if (maxIdx >= chain.value.length - 1) {
    // Already at top
    return null
  }
  return chain.value[maxIdx + 1]
})

const isNextGated = computed<boolean>(() => nextEnv.value?.gated ?? false)

// ─── Pending gated promotion (审批门:批准/拒绝就在本面板) ──────────────────────
// gated 晋级提交后状态为 pending,等待人工审批。审批复用运行审批门端点
// (/runs/{id}/approve|reject,stageId="promote:<env>")。此前面板只提示「请在审批门中批准」
// 却无入口、运行级审批 UI 又不覆盖成功运行上的晋级门 → 晋级审批在 UI 里点不了。这里补齐。
const pendingPromotion = computed<PromotionDTO | null>(
  () => promotions.value.find((p) => p.status === 'pending') ?? null,
)

const deciding = ref(false)

/** 批准(approve=true)/拒绝 当前待审晋级;复用审批门端点,决定后刷新历史。 */
async function decidePromotion(approve: boolean): Promise<void> {
  const target = pendingPromotion.value?.targetEnvironment
  if (!target || deciding.value) return
  deciding.value = true
  promotionError.value = ''
  try {
    const stageId = `promote:${target}`
    if (approve) await approveStage(props.runId, stageId)
    else await rejectStage(props.runId, stageId)
    // 审批端点只是给阻塞的 promote 协程投递决定,最终状态(promoted/rejected)由该协程写库,
    // 与 approve 响应之间有微小窗口 → 轮询几次直到 pending 落定,确保面板及时切走待审块。
    await loadHistory()
    for (let i = 0; i < 5 && pendingPromotion.value; i++) {
      await new Promise((resolve) => setTimeout(resolve, 200))
      await loadHistory()
    }
  } catch (err) {
    promotionError.value =
      err instanceof HttpError
        ? (err.apiError?.message ?? `审批失败(${err.status})`)
        : '审批失败,请稍后重试'
  } finally {
    deciding.value = false
  }
}

// ─── Promotion action ─────────────────────────────────────────────────────────

const promoting = ref(false)
const promotionError = ref('')
const promoted = ref(false)
const showConfirm = ref(false)

function requestPromote(): void {
  if (isNextGated.value) {
    showConfirm.value = true
  } else {
    void doPromote()
  }
}

function cancelConfirm(): void {
  showConfirm.value = false
}

/** 把晋级请求错误映射为展示文案;gate_rejected 是审批拒绝/超时的正常终态,返回空串(不作错误横幅)。 */
function mapPromoteError(err: unknown): string {
  if (!(err instanceof HttpError)) return '晋级请求失败,请稍后重试。'
  const code = err.apiError?.code ?? ''
  switch (code) {
    case 'chain_not_configured':
      return '项目尚未配置环境链,请在「触发设置」中添加环境链后再晋级。'
    case 'run_not_successful':
      return '仅成功完成的运行可晋级。'
    case 'already_promoted':
      return '该运行已晋级到此环境。'
    case 'already_at_top':
      return '已到达链尾,无更高环境可晋级。'
    case 'skip_env':
      return '只能按顺序逐级晋级,不可跳级。'
    case 'gate_rejected':
      return '' // 审批拒绝/超时是正常终态,历史已反映 rejected,不弹错误横幅
    default:
      return err.apiError?.message ?? `晋级失败(${err.status})`
  }
}

async function doPromote(): Promise<void> {
  showConfirm.value = false
  if (promoting.value || !nextEnv.value) return
  const target = nextEnv.value.name
  const gated = isNextGated.value
  promoting.value = true
  promotionError.value = ''
  promoted.value = false

  if (gated) {
    // gated 晋级:POST /promote 在服务端会阻塞,挂起直到 /approve|/reject 投递决定
    // (promotion/coordinator.go Promote → Gate.Await)。因此**绝不能 await** 这个请求——
    // 否则待审晋级的批准/拒绝按钮永远渲染不出来(此前的 bug)。pending 记录在阻塞前已写库,
    // 故发起请求后立即拉历史把它显示出来;请求最终(审批决定后)settle 时再刷新一次历史。
    const inflight = promoteRun(props.runId, { targetEnvironment: target })
    inflight.then(
      () => loadHistory(), // 批准 → promoted
      (err) => {
        const msg = mapPromoteError(err)
        if (msg) promotionError.value = msg
        return loadHistory() // 拒绝/超时 → rejected(或快速校验错误)
      },
    )
    emit('promotion-pending')
    try {
      await loadHistory()
      // 偶发竞态(记录刚写库)用一次轻量重试兜底,确保 pending 块即时出现。
      if (!pendingPromotion.value) {
        await new Promise((resolve) => setTimeout(resolve, 300))
        await loadHistory()
      }
    } finally {
      promoting.value = false
    }
    return
  }

  // 非 gated:同步晋级,直接 await 即可拿到 promoted 记录。
  try {
    const dto = await promoteRun(props.runId, { targetEnvironment: target })
    promotions.value = [dto, ...promotions.value.filter((p) => p.id !== dto.id)]
    if (dto.status === 'promoted') promoted.value = true
  } catch (err) {
    promotionError.value = mapPromoteError(err)
  } finally {
    promoting.value = false
  }
}

// ─── Promotion status display ─────────────────────────────────────────────────

function statusCfg(status: PromotionStatus) {
  return promotionStatusConfig(status)
}

function fmtDate(iso: string) {
  return formatPromotionDate(iso)
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

async function init(): Promise<void> {
  await Promise.all([loadChain(), loadHistory()])
}

watch(() => props.runId, init)
onMounted(init)
</script>

<template>
  <div class="promotion-panel" role="region" aria-labelledby="promo-heading">

    <!-- ─── Promote action section ──────────────────────────────────────── -->
    <div class="promo-action-card">
      <div class="promo-action-head">
        <div class="promo-icon" aria-hidden="true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
          </svg>
        </div>
        <h3 id="promo-heading" class="promo-title">环境晋级</h3>
        <span v-if="chain.length > 0" class="promo-chain-badge" aria-label="环境链">
          {{ chain.map(e => e.name).join(' → ') }}
        </span>
      </div>

      <!-- Chain not loaded / empty -->
      <div v-if="chainLoaded && chain.length === 0" class="promo-empty">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
        </svg>
        <span>尚未配置环境链。请前往「触发设置 → 环境链」配置后再晋级。</span>
      </div>

      <!-- Pending gated promotion: 审批门批准/拒绝就在这里(优先于「已到链尾」,因 pending 也算到顶) -->
      <div v-else-if="pendingPromotion" class="promo-action-body">
        <div class="promo-banner promo-banner--pending" role="status" aria-live="polite">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          晋级到「{{ pendingPromotion.targetEnvironment }}」等待人工审批
        </div>
        <div v-if="promotionError" class="promo-banner promo-banner--error" role="alert" aria-live="assertive">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ promotionError }}
        </div>
        <div class="promo-btn-row">
          <button
            class="promo-btn promo-btn--gate"
            :disabled="deciding"
            :aria-busy="deciding"
            @click="decidePromotion(true)"
          >
            <span v-if="deciding" class="promo-spinner" aria-hidden="true" />
            {{ deciding ? '处理中…' : `批准晋级到「${pendingPromotion.targetEnvironment}」` }}
          </button>
          <button class="promo-btn promo-btn--cancel" :disabled="deciding" @click="decidePromotion(false)">
            拒绝
          </button>
        </div>
      </div>

      <!-- Already at top -->
      <div
        v-else-if="chainLoaded && nextEnv === null && promotions.length > 0"
        class="promo-empty promo-empty--top"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path d="M20 6 9 17l-5-5"/>
        </svg>
        <span>已晋级到链尾环境「{{ chain[chain.length - 1]?.name }}」,无更高级别。</span>
      </div>

      <!-- Promote button -->
      <div v-else-if="chainLoaded && nextEnv !== null && props.runStatus === 'success'" class="promo-action-body">
        <!-- Success flash -->
        <div v-if="promoted" class="promo-banner promo-banner--success" role="status" aria-live="polite">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
            <path d="M20 6 9 17l-5-5"/>
          </svg>
          已成功晋级到「{{ promotions[0]?.targetEnvironment }}」
        </div>

        <!-- Pending gate banner -->
        <div
          v-if="promotions[0]?.status === 'pending'"
          class="promo-banner promo-banner--pending"
          role="status"
          aria-live="polite"
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          晋级到「{{ promotions[0]?.targetEnvironment }}」等待审批 · 请在审批门中批准
        </div>

        <!-- Error banner -->
        <div v-if="promotionError" class="promo-banner promo-banner--error" role="alert" aria-live="assertive">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/>
          </svg>
          {{ promotionError }}
        </div>

        <!-- Gated confirm dialog -->
        <div v-if="showConfirm" class="promo-confirm" role="alertdialog" aria-modal="true" aria-labelledby="promo-confirm-title">
          <div class="promo-confirm-icon" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
              <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
          </div>
          <div class="promo-confirm-body">
            <p id="promo-confirm-title" class="promo-confirm-title">
              晋级到「{{ nextEnv?.name }}」需要人工审批
            </p>
            <p class="promo-confirm-sub">
              提交后晋级进入等待状态,须由有权限的用户批准后方可继续。
            </p>
          </div>
          <div class="promo-confirm-actions">
            <button class="promo-btn promo-btn--cancel" @click="cancelConfirm">取消</button>
            <button class="promo-btn promo-btn--gate" :disabled="promoting" :aria-busy="promoting" @click="doPromote">
              <span v-if="promoting" class="promo-spinner" aria-hidden="true" />
              {{ promoting ? '提交中…' : '提交晋级申请' }}
            </button>
          </div>
        </div>

        <!-- Main promote button -->
        <div v-else class="promo-btn-row">
          <button
            class="promo-btn promo-btn--promote"
            :disabled="promoting || promotions[0]?.status === 'pending'"
            :aria-busy="promoting"
            @click="requestPromote"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 2 2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
            </svg>
            <span v-if="promoting" class="promo-spinner" aria-hidden="true" />
            {{
              promoting
                ? '晋级中…'
                : isNextGated
                  ? `申请晋级到「${nextEnv?.name}」(需审批)`
                  : `晋级到「${nextEnv?.name}」`
            }}
          </button>
          <span v-if="isNextGated" class="promo-gated-hint">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <rect x="3" y="11" width="18" height="10" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
            审批门 · 需人工批准
          </span>
        </div>
      </div>

      <!-- Run not successful -->
      <div v-else-if="props.runStatus !== 'success'" class="promo-empty">
        <span>仅成功完成的运行可晋级</span>
      </div>

      <!-- Loading -->
      <div v-else-if="!chainLoaded" class="promo-loading" aria-busy="true" aria-label="加载环境链">
        <div class="promo-skel promo-skel--btn" aria-hidden="true" />
      </div>
    </div>

    <!-- ─── Promotion history ────────────────────────────────────────────── -->
    <div class="promo-history" aria-labelledby="promo-hist-heading">
      <div class="promo-history-head">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9"/><path d="M12 6v6l4 2"/>
        </svg>
        <h4 id="promo-hist-heading" class="promo-history-title">晋级历史</h4>
        <span class="promo-hist-count">{{ promotions.length }}</span>
      </div>

      <div v-if="historyLoading" class="promo-hist-body promo-hist-loading" aria-busy="true" aria-label="加载历史">
        <div class="promo-skel promo-skel--row" aria-hidden="true" />
        <div class="promo-skel promo-skel--row" aria-hidden="true" />
      </div>

      <div v-else-if="historyError" class="promo-hist-body promo-hist-error" role="alert">
        {{ historyError }}
      </div>

      <div v-else-if="promotions.length === 0" class="promo-hist-body promo-hist-empty">
        <span>暂无晋级记录</span>
      </div>

      <ol v-else class="promo-hist-list" aria-label="晋级历史列表">
        <li
          v-for="p in promotions"
          :key="p.id"
          class="promo-hist-item"
          :aria-label="`晋级到 ${p.targetEnvironment}・${statusCfg(p.status).label}・${fmtDate(p.createdAt)}`"
        >
          <!-- Status dot -->
          <span
            class="hist-dot"
            :style="{ background: statusCfg(p.status).color }"
            aria-hidden="true"
          />

          <!-- Arrow indicator -->
          <div class="hist-route">
            <span v-if="p.fromEnvironment" class="hist-from mono">{{ p.fromEnvironment }}</span>
            <svg v-if="p.fromEnvironment" width="12" height="8" viewBox="0 0 12 8" fill="none" aria-hidden="true">
              <path d="M0 4h10M7 1l3 3-3 3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="hist-to mono">{{ p.targetEnvironment }}</span>
          </div>

          <!-- Status badge -->
          <span
            class="hist-status"
            :style="{
              color: statusCfg(p.status).color,
              background: statusCfg(p.status).bg,
              borderColor: statusCfg(p.status).border,
            }"
          >{{ statusCfg(p.status).label }}</span>

          <!-- Meta -->
          <div class="hist-meta">
            <span class="hist-by">{{ p.promotedBy }}</span>
            <span class="hist-sep" aria-hidden="true">·</span>
            <time :datetime="p.createdAt" class="hist-time">{{ fmtDate(p.createdAt) }}</time>
          </div>
        </li>
      </ol>
    </div>

  </div>
</template>

<style scoped>
.promotion-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  border-radius: var(--rounded);
  border: 1px solid var(--color-border);
  overflow: hidden;
  background: var(--color-card);
}

/* ─── Action card ─────────────────────────────────── */
.promo-action-card {
  padding: 0;
}

.promo-action-head {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 13px 16px 11px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
}

.promo-icon {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  background: var(--color-primary-soft);
  color: var(--color-primary);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.promo-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
}

.promo-chain-badge {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: 0.72rem;
  color: var(--color-faint);
  background: var(--color-card-2);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-md);
  padding: 2px 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 300px;
}

.promo-action-body {
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.promo-empty {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 16px;
  font-size: 0.81rem;
  color: var(--color-faint);
}

.promo-empty--top {
  color: var(--color-green);
}

.promo-loading {
  padding: 14px 16px;
}

/* ─── Banners ─────────────────────────────────────── */
.promo-banner {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 13px;
  border-radius: var(--rounded-md);
  font-size: 0.82rem;
  line-height: 1.5;
}

.promo-banner--success {
  background: var(--color-green-soft);
  border: 1px solid var(--color-green-line);
  color: var(--color-green);
}

.promo-banner--pending {
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  color: var(--color-amber);
}

.promo-banner--error {
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
  color: var(--color-red);
}

/* ─── Confirm dialog (inline) ─────────────────────── */
.promo-confirm {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px;
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded);
}

.promo-confirm-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: var(--rounded-md);
  background: var(--color-amber);
  color: #fff;
  display: grid;
  place-items: center;
}

.promo-confirm-body {
  flex: 1;
  min-width: 0;
}

.promo-confirm-title {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
  line-height: 1.4;
}

.promo-confirm-sub {
  margin-top: 3px;
  font-size: 0.78rem;
  color: var(--color-dim);
  line-height: 1.5;
}

.promo-confirm-actions {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-shrink: 0;
}

/* ─── Buttons ─────────────────────────────────────── */
.promo-btn-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.promo-btn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 36px;
  padding: 0 16px;
  border: none;
  font-family: var(--font-sans);
  font-size: 0.84rem;
  font-weight: 600;
  border-radius: var(--rounded);
  cursor: pointer;
  white-space: nowrap;
  transition: filter var(--duration-fast), background-color var(--duration-fast), opacity var(--duration-fast);
}

.promo-btn--promote {
  background: var(--color-primary);
  color: #fff;
  box-shadow: 0 4px 14px var(--color-primary-soft);
}
.promo-btn--promote:hover:not(:disabled) { filter: brightness(1.08); }
.promo-btn--promote:disabled { opacity: 0.45; cursor: not-allowed; box-shadow: none; }

.promo-btn--gate {
  background: var(--color-amber);
  color: #fff;
}
.promo-btn--gate:hover:not(:disabled) { filter: brightness(1.06); }
.promo-btn--gate:disabled { opacity: 0.5; cursor: not-allowed; }

.promo-btn--cancel {
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
}
.promo-btn--cancel:hover { border-color: var(--color-faint); }

.promo-gated-hint {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.75rem;
  color: var(--color-amber);
}

.promo-spinner {
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: #fff;
  border-radius: var(--rounded-full);
  animation: promo-spin 0.7s linear infinite;
  flex-shrink: 0;
}
@keyframes promo-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .promo-spinner { animation: none; border-top-color: currentColor; } }

/* ─── Skeleton ────────────────────────────────────── */
.promo-skel {
  display: block;
  background: linear-gradient(90deg, var(--color-inset) 0%, oklch(100% 0 0 / 0.06) 50%, var(--color-inset) 100%);
  background-size: 200% 100%;
  border-radius: var(--rounded-md);
  animation: promo-shimmer 1.4s ease-in-out infinite;
}
@keyframes promo-shimmer {
  0%   { background-position: 200% center; }
  100% { background-position: -200% center; }
}
@media (prefers-reduced-motion: reduce) { .promo-skel { animation: none; background: var(--color-inset); } }
.promo-skel--btn { height: 36px; width: 220px; border-radius: var(--rounded); }
.promo-skel--row { height: 32px; width: 100%; margin-bottom: 6px; }

/* ─── History section ─────────────────────────────── */
.promo-history {
  border-top: 1px solid var(--color-border);
}

.promo-history-head {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
  color: var(--color-dim);
}

.promo-history-title {
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--color-text);
  flex: 1;
}

.promo-hist-count {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 1px 7px;
  border-radius: var(--rounded-full);
  background: var(--color-border-strong);
  color: var(--color-dim);
}

.promo-hist-body {
  padding: 14px 16px;
}

.promo-hist-loading {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.promo-hist-error {
  font-size: 0.8rem;
  color: var(--color-red);
}

.promo-hist-empty {
  font-size: 0.8rem;
  color: var(--color-faint);
  font-style: italic;
}

/* History list */
.promo-hist-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 0;
}

.promo-hist-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--color-border);
  transition: background-color var(--duration-fast);
}

.promo-hist-item:last-child { border-bottom: none; }
.promo-hist-item:hover { background: var(--color-inset); }

.hist-dot {
  width: 7px;
  height: 7px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.hist-route {
  display: flex;
  align-items: center;
  gap: 5px;
  flex: 1;
  min-width: 0;
}

.hist-from {
  font-size: 0.78rem;
  color: var(--color-faint);
}

.hist-to {
  font-size: 0.82rem;
  font-weight: 500;
  color: var(--color-text);
}

.hist-status {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: var(--rounded-md);
  border: 1px solid transparent;
  font-size: 0.72rem;
  font-weight: 600;
  white-space: nowrap;
  letter-spacing: 0.02em;
  flex-shrink: 0;
}

.hist-meta {
  display: flex;
  align-items: center;
  gap: 5px;
  flex-shrink: 0;
  font-size: 0.74rem;
  color: var(--color-faint);
}

.hist-sep { color: var(--color-border-strong); }
.hist-by { white-space: nowrap; max-width: 90px; overflow: hidden; text-overflow: ellipsis; }
.hist-time { white-space: nowrap; }

.mono { font-family: var(--font-mono); }
</style>

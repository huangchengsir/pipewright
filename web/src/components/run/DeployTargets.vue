<!--
  DeployTargets.vue — Story 4-2: SSH 部署执行结果(FR-10)

  渲染一次部署在各台目标机上的结果:每机一张结果卡(status 徽标 + serverName +
  人读 message + 起止时间 / 耗时)。填 run-detail 冻结 targets slot(部署过 → 数组)。
  status 枚举冻结:pending | deploying | success | failed | rolled_back。
  message 由后端保证人读且绝无明文密钥;前端只渲染、不再处理。

  纯展示组件(container/presentational split):仅接收 targets prop,不自取数据。
  RunDetail 成功态据 run.targets 渲染;不扰终端(3-6)/诊断(7-2)/产物(3-4)slot。
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { DeployTarget, TargetStatus } from '../../api/runs'

const props = withDefaults(
  defineProps<{
    targets: DeployTarget[]
    // 重试进行中(由父组件 RunDetail 持有 artifactId/config 后发起 API);禁用按钮 + 转圈。
    retrying?: boolean
    // 重试错误(人读);非空时在面板内展示。
    retryError?: string
  }>(),
  { retrying: false, retryError: '' },
)

// 「仅重试失败目标」事件:本组件纯展示,不自取 artifactId/config;
// 由父组件(RunDetail,持有上次部署表单)发起 retryFailedDeploy 并刷新 targets。
const emit = defineEmits<{ retry: [] }>()

// 当前有几台 failed/rolled_back 目标可重试(rolled_back 亦计入:健康失败已回滚,可再试)。
const failedCount = computed(
  () => props.targets.filter((t) => t.status === 'failed' || t.status === 'rolled_back').length,
)

// ─── Status badge config (semantic color per fixed five-word set) ────────────

interface StatusConfig {
  label: string
  fg: string
  bg: string
  line: string
  icon: 'check' | 'x' | 'spinner' | 'dot' | 'undo'
  pulse: boolean
}

const STATUS_CONFIG: Record<TargetStatus, StatusConfig> = {
  pending:     { label: '待部署', fg: 'var(--color-faint)', bg: 'var(--color-card-2)',     line: 'var(--color-border-strong)', icon: 'dot',     pulse: false },
  deploying:   { label: '部署中', fg: 'var(--color-amber)', bg: 'var(--color-amber-soft)', line: 'var(--color-amber-line)',    icon: 'spinner', pulse: true  },
  success:     { label: '成功',   fg: 'var(--color-green)', bg: 'var(--color-green-soft)', line: 'var(--color-green-line)',    icon: 'check',   pulse: false },
  failed:      { label: '失败',   fg: 'var(--color-red)',   bg: 'var(--color-red-soft)',   line: 'var(--color-red-line)',      icon: 'x',       pulse: false },
  rolled_back: { label: '已回滚', fg: 'var(--color-amber)', bg: 'var(--color-amber-soft)', line: 'var(--color-amber-line)',    icon: 'undo',    pulse: false },
}

function statusConfig(status: string): StatusConfig {
  return STATUS_CONFIG[status as TargetStatus] ?? {
    label: status, fg: 'var(--color-dim)', bg: 'var(--color-card-2)', line: 'var(--color-border-strong)', icon: 'dot', pulse: false,
  }
}

// ─── duration / time helpers ──────────────────────────────────────────────────

function durationText(t: DeployTarget): string {
  if (!t.finishedAt) return '进行中'
  const ms = new Date(t.finishedAt).getTime() - new Date(t.startedAt).getTime()
  if (!Number.isFinite(ms) || ms < 0) return '—'
  const s = Math.round(ms / 1000)
  if (s < 1) return '<1s'
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const rem = s % 60
  return rem > 0 ? `${m}m ${rem}s` : `${m}m`
}

function formatTime(iso: string | null): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', {
    month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

// ─── summary counts (for the header) ──────────────────────────────────────────

function counts(targets: DeployTarget[]): { ok: number; bad: number; rolledBack: number } {
  let ok = 0
  let bad = 0
  let rolledBack = 0
  for (const t of targets) {
    if (t.status === 'success') ok++
    else if (t.status === 'rolled_back') rolledBack++
    else if (t.status === 'failed') bad++
  }
  return { ok, bad, rolledBack }
}
</script>

<template>
  <section
    v-if="targets.length > 0"
    class="deploy-targets"
    role="region"
    aria-label="部署目标结果"
  >
    <header class="dt-head">
      <div class="dt-head-title">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <rect x="2" y="3" width="20" height="6" rx="1" />
          <rect x="2" y="15" width="20" height="6" rx="1" />
          <path d="M6 6h.01M6 18h.01" />
        </svg>
        <span class="dt-title">部署目标</span>
        <span class="dt-count">{{ targets.length }} 台</span>
      </div>
      <div class="dt-head-stats">
        <span v-if="counts(targets).ok > 0" class="dt-stat dt-stat--ok">{{ counts(targets).ok }} 成功</span>
        <span v-if="counts(targets).rolledBack > 0" class="dt-stat dt-stat--rollback">{{ counts(targets).rolledBack }} 已回滚</span>
        <span v-if="counts(targets).bad > 0" class="dt-stat dt-stat--bad">{{ counts(targets).bad }} 失败</span>
      </div>
    </header>

    <ul class="dt-list" role="list">
      <li
        v-for="t in targets"
        :key="t.serverId + t.startedAt"
        class="dt-card"
        :style="{ borderColor: statusConfig(t.status).line }"
        role="listitem"
      >
        <div class="dt-card-top">
          <span
            class="dt-badge"
            :style="{ background: statusConfig(t.status).bg, color: statusConfig(t.status).fg }"
            :aria-label="`部署状态:${statusConfig(t.status).label}`"
          >
            <span
              class="dt-badge-dot"
              :class="{ 'dt-badge-dot--pulse': statusConfig(t.status).pulse }"
              :style="{ background: statusConfig(t.status).fg }"
              aria-hidden="true"
            />
            {{ statusConfig(t.status).label }}
          </span>
          <span class="dt-server" :title="t.serverName">{{ t.serverName }}</span>
          <span class="dt-duration mono" :aria-label="`耗时 ${durationText(t)}`">{{ durationText(t) }}</span>
        </div>

        <p
          v-if="t.message"
          class="dt-message"
          :class="{ 'dt-message--rollback': t.status === 'rolled_back' }"
        >
          <svg
            v-if="t.status === 'rolled_back'"
            class="dt-message-icon"
            width="13" height="13" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2" aria-hidden="true"
          >
            <path d="M3 7v6h6" />
            <path d="M3 13a9 9 0 1 0 3-7.7L3 8" />
          </svg>
          <span>{{ t.message }}</span>
        </p>

        <div class="dt-times">
          <span class="dt-time">
            <span class="dt-time-key">开始</span>
            <span class="dt-time-val mono">{{ formatTime(t.startedAt) }}</span>
          </span>
          <span class="dt-time">
            <span class="dt-time-key">结束</span>
            <span class="dt-time-val mono">{{ formatTime(t.finishedAt) }}</span>
          </span>
        </div>
      </li>
    </ul>

    <!--
      仅重试失败目标(Story 4-5 / FR-13):有 failed/rolled_back 目标时显「重试失败目标」。
      点击 → 父组件 RunDetail(持上次部署产物/配置)发起 retryFailedDeploy → 刷新 targets;
      成功台不动,只重跑失败台。本组件纯展示,只 emit 事件。
    -->
    <footer v-if="failedCount > 0" class="dt-foot">
      <div v-if="retryError" class="dt-retry-error" role="alert">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="12" r="9" /><path d="M12 8v4M12 16h.01" />
        </svg>
        <span>{{ retryError }}</span>
      </div>
      <button
        class="dt-retry-btn"
        :disabled="retrying"
        :aria-busy="retrying"
        @click="emit('retry')"
      >
        <span v-if="retrying" class="dt-retry-spinner" aria-hidden="true" />
        <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <path d="M3 7v6h6" />
          <path d="M3 13a9 9 0 1 0 3-7.7L3 8" />
        </svg>
        {{ retrying ? '重试中…' : `重试失败目标（${failedCount}）` }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.deploy-targets {
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  overflow: hidden;
  background: var(--color-card);
}

.dt-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 16px;
  background: var(--color-inset);
  border-bottom: 1px solid var(--color-border);
}

.dt-head-title {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--color-text);
}

.dt-title {
  font-size: 0.84rem;
  font-weight: 600;
  letter-spacing: -0.01em;
}

.dt-count {
  font-size: 0.74rem;
  color: var(--color-faint);
  font-weight: 500;
}

.dt-head-stats {
  display: flex;
  gap: 8px;
}

.dt-stat {
  font-size: 0.72rem;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: var(--rounded-sm);
}

.dt-stat--ok       { color: var(--color-green); background: var(--color-green-soft); }
.dt-stat--bad      { color: var(--color-red);   background: var(--color-red-soft);   }
.dt-stat--rollback { color: var(--color-amber); background: var(--color-amber-soft); }

.dt-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
}

.dt-card {
  border: 1px solid var(--color-border);
  border-left-width: 3px;
  border-radius: var(--rounded-md);
  padding: 11px 14px;
  background: var(--color-inset);
  display: flex;
  flex-direction: column;
  gap: 8px;
  transition: box-shadow var(--duration-fast), transform var(--duration-fast);
}

.dt-card:hover {
  box-shadow: var(--shadow);
}

.dt-card-top {
  display: flex;
  align-items: center;
  gap: 10px;
}

.dt-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.74rem;
  font-weight: 600;
  padding: 2px 9px;
  border-radius: var(--rounded-md);
  white-space: nowrap;
  flex-shrink: 0;
}

.dt-badge-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--rounded-full);
  flex-shrink: 0;
}

.dt-badge-dot--pulse {
  animation: dt-pulse 1.1s ease-in-out infinite;
}

@keyframes dt-pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50%      { opacity: 0.5; transform: scale(0.8); }
}

@media (prefers-reduced-motion: reduce) {
  .dt-badge-dot--pulse { animation: none; }
}

.dt-server {
  font-size: 0.86rem;
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex: 1;
  min-width: 0;
}

.dt-duration {
  font-size: 0.74rem;
  color: var(--color-faint);
  flex-shrink: 0;
}

.dt-message {
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.5;
  word-break: break-word;
}

/* rolled_back:回滚信息醒目展示(amber 强调 + 撤回图标),区别于普通 message。 */
.dt-message--rollback {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-radius: var(--rounded-sm);
  padding: 7px 10px;
  font-weight: 500;
}

.dt-message-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.dt-times {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
}

.dt-time {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.dt-time-key {
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

.dt-time-val {
  font-size: 0.76rem;
  color: var(--color-dim);
}

.mono { font-family: var(--font-mono); }

/* ─── retry-failed footer (Story 4-5 / FR-13) ─────────────────────────────── */
.dt-foot {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  flex-wrap: wrap;
  padding: 12px 16px;
  border-top: 1px solid var(--color-border);
  background: var(--color-inset);
}

.dt-retry-error {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-right: auto;
  font-size: 0.78rem;
  color: var(--color-red);
  background: var(--color-red-soft);
  padding: 5px 10px;
  border-radius: var(--rounded-sm);
}

.dt-retry-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 14px;
  font-family: var(--font-sans);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border: 1px solid var(--color-amber-line);
  border-radius: var(--rounded-md);
  cursor: pointer;
  transition: background-color var(--duration-fast), transform var(--duration-fast), box-shadow var(--duration-fast);
}

.dt-retry-btn:hover:not(:disabled) {
  background: var(--color-amber);
  color: var(--color-card);
  box-shadow: var(--shadow);
}

.dt-retry-btn:active:not(:disabled) {
  transform: translateY(1px);
}

.dt-retry-btn:focus-visible {
  outline: 2px solid var(--color-amber);
  outline-offset: 2px;
}

.dt-retry-btn:disabled {
  opacity: 0.6;
  cursor: progress;
}

.dt-retry-spinner {
  width: 13px;
  height: 13px;
  border: 2px solid currentColor;
  border-top-color: transparent;
  border-radius: var(--rounded-full);
  animation: dt-spin 0.7s linear infinite;
}

@keyframes dt-spin {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .dt-retry-spinner { animation-duration: 1.6s; }
  .dt-retry-btn { transition: none; }
}
</style>

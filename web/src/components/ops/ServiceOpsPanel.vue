<!--
  ServiceOpsPanel.vue — Story 6-3: 服务操作(重启/停止/启动)(FR-17,经 SSH 跑 systemctl/docker)

  面向单台已登记服务器的服务操作面板:
    · type 选择(systemd / docker)+ target 输入(unit 名 / 容器名)
    · restart / stop / start 三个动作按钮
    · 危险动作(restart / stop)走二次确认弹层后才真正提交
    · 结果展示:ok / 失败人读 error / 截断 output

  安全(AC-SEC-02):target 由后端严格白名单校验(首字符非 `-` 防 flag 注入、无 shell 元字符
  防命令注入);非法 → 400 invalid_service_target,本面板原样呈现人读错误,绝不在前端拼任何
  shell。SSH/命令失败 → 200 + ok:false + error 字段,本面板渲染为失败状态而非崩溃。
  写操作经后端审计(成功投递即留痕)。
-->
<script setup lang="ts">
import { ref, computed } from 'vue'
import {
  serviceAction,
  type ServiceType,
  type ServiceAction,
  type ServiceActionResult,
} from '../../api/servers'
import { HttpError } from '../../api/http'

const props = defineProps<{
  serverId: string
  /** 展示用服务器名(确认文案)。 */
  serverName?: string
}>()

// ─── controls ─────────────────────────────────────────────────────────────────

const type = ref<ServiceType>('systemd')
const targetInput = ref('')

const targetPlaceholder = computed(() =>
  type.value === 'systemd' ? 'nginx.service(unit 名)' : 'my-container(容器名)',
)

const targetValid = computed(() => targetInput.value.trim().length > 0)

// ─── second-confirm for destructive actions ───────────────────────────────────
// restart / stop 视为危险(影响线上服务可用性),提交前弹二次确认;start 直接执行。

const pendingAction = ref<ServiceAction | null>(null)
const submitting = ref(false)

// 本面板仅提供 restart/stop/start 三个动作(systemd/docker 通用);ServiceAction 联合类型
// 另含容器专用的 pause/unpause/kill/rm(由「容器」页驱动),此处用 Partial 只列用到的键。
const actionLabel: Partial<Record<ServiceAction, string>> = {
  restart: '重启',
  stop: '停止',
  start: '启动',
}

function isDestructive(a: ServiceAction): boolean {
  return a === 'restart' || a === 'stop'
}

/** 点动作按钮:危险动作进入二次确认;否则直接执行。 */
function requestAction(a: ServiceAction): void {
  if (!targetValid.value || submitting.value) return
  if (isDestructive(a)) {
    pendingAction.value = a
  } else {
    void runAction(a)
  }
}

function cancelConfirm(): void {
  pendingAction.value = null
}

function confirmPending(): void {
  const a = pendingAction.value
  pendingAction.value = null
  if (a) void runAction(a)
}

// ─── result ───────────────────────────────────────────────────────────────────

const result = ref<ServiceActionResult | null>(null)
/** 校验/客户端错误(如 400 invalid_service_target)的人读文案。 */
const banner = ref('')

async function runAction(a: ServiceAction): Promise<void> {
  banner.value = ''
  result.value = null
  submitting.value = true
  try {
    result.value = await serviceAction(props.serverId, {
      type: type.value,
      target: targetInput.value.trim(),
      action: a,
    })
  } catch (e) {
    if (e instanceof HttpError) {
      // 400 invalid_service_target / 404 / 503 等:呈现后端人读 message,绝不拼 shell。
      banner.value = e.message || '服务操作请求失败'
    } else {
      banner.value = '服务操作请求失败'
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="ops-panel">
    <div class="ops-controls">
      <label class="ops-field">
        <span class="ops-field-label">类型</span>
        <select v-model="type" class="ops-input" :disabled="submitting">
          <option value="systemd">systemd</option>
          <option value="docker">docker</option>
        </select>
      </label>

      <label class="ops-field ops-field-grow">
        <span class="ops-field-label">目标</span>
        <input
          v-model="targetInput"
          class="ops-input mono"
          type="text"
          :placeholder="targetPlaceholder"
          autocomplete="off"
          spellcheck="false"
          :disabled="submitting"
          @keydown.enter.prevent
        />
      </label>
    </div>

    <p class="ops-hint">
      目标名由服务端严格白名单校验(首字符非 <code>-</code>、无 shell 元字符);非法将被拒绝。
    </p>

    <div class="ops-actions">
      <button
        type="button"
        class="ops-btn ops-btn--warn"
        :disabled="!targetValid || submitting"
        @click="requestAction('restart')"
      >
        重启
      </button>
      <button
        type="button"
        class="ops-btn ops-btn--danger"
        :disabled="!targetValid || submitting"
        @click="requestAction('stop')"
      >
        停止
      </button>
      <button
        type="button"
        class="ops-btn ops-btn--ok"
        :disabled="!targetValid || submitting"
        @click="requestAction('start')"
      >
        启动
      </button>
      <span v-if="submitting" class="ops-busy">执行中…</span>
    </div>

    <!-- 校验/请求层错误(如 400 invalid_service_target) -->
    <div v-if="banner" class="ops-banner ops-banner--error" role="alert">{{ banner }}</div>

    <!-- 执行结果(ok / ok:false + 人读 error) -->
    <div
      v-if="result"
      class="ops-result"
      :class="result.ok ? 'ops-result--ok' : 'ops-result--fail'"
      role="status"
    >
      <div class="ops-result-head">
        <span class="ops-result-badge">{{ result.ok ? '成功' : '失败' }}</span>
        <span class="mono"
          >{{ result.type }} · {{ actionLabel[result.action] }} · {{ result.target }}</span
        >
      </div>
      <pre v-if="result.output" class="ops-result-output mono">{{ result.output }}</pre>
      <p v-if="!result.ok && result.error" class="ops-result-error mono">{{ result.error }}</p>
    </div>

    <!-- ─── 危险动作二次确认 ─────────────────────────────────────────────────── -->
    <div v-if="pendingAction" class="ops-confirm-backdrop" @click.self="cancelConfirm">
      <div
        class="ops-confirm"
        role="dialog"
        aria-modal="true"
        aria-labelledby="ops-confirm-title"
      >
        <h4 id="ops-confirm-title" class="ops-confirm-title">确认{{ actionLabel[pendingAction] }}服务?</h4>
        <p class="ops-confirm-body">
          即将对
          <strong v-if="serverName">{{ serverName }}</strong>
          上的 <code class="mono">{{ type }}</code> 目标
          <code class="mono">{{ targetInput.trim() }}</code>
          执行<strong>{{ actionLabel[pendingAction] }}</strong>。此操作会影响线上服务可用性。
        </p>
        <div class="ops-confirm-actions">
          <button type="button" class="ops-btn" @click="cancelConfirm">取消</button>
          <button
            type="button"
            class="ops-btn"
            :class="pendingAction === 'stop' ? 'ops-btn--danger' : 'ops-btn--warn'"
            @click="confirmPending"
          >
            确认{{ actionLabel[pendingAction] }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ops-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ops-controls {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}

.ops-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.ops-field-grow {
  flex: 1;
}

.ops-field-label {
  font-size: 12px;
  color: var(--color-text-muted, #6b7280);
}

.ops-input {
  padding: 8px 10px;
  border: 1px solid var(--color-border, #d1d5db);
  border-radius: 6px;
  background: var(--color-surface, #fff);
  color: var(--color-text, #111827);
  font-size: 14px;
}

.ops-input:focus-visible {
  outline: 2px solid var(--color-accent, #4f46e5);
  outline-offset: 1px;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.ops-hint {
  margin: 0;
  font-size: 12px;
  color: var(--color-text-muted, #6b7280);
}

.ops-hint code,
.ops-result code,
.ops-confirm code {
  padding: 0 4px;
  border-radius: 4px;
  background: var(--color-code-bg, #f3f4f6);
}

.ops-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ops-btn {
  padding: 8px 14px;
  border: 1px solid var(--color-border, #d1d5db);
  border-radius: 6px;
  background: var(--color-surface, #fff);
  color: var(--color-text, #111827);
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: background 120ms ease, border-color 120ms ease, transform 120ms ease;
}

.ops-btn:hover:not(:disabled) {
  background: var(--color-surface-hover, #f9fafb);
}

.ops-btn:active:not(:disabled) {
  transform: translateY(1px);
}

.ops-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ops-btn--ok {
  border-color: #16a34a;
  color: #15803d;
}

.ops-btn--warn {
  border-color: #d97706;
  color: #b45309;
}

.ops-btn--danger {
  border-color: #dc2626;
  color: #b91c1c;
}

.ops-busy {
  font-size: 13px;
  color: var(--color-text-muted, #6b7280);
}

.ops-banner {
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 13px;
}

.ops-banner--error {
  background: #fef2f2;
  color: #991b1b;
  border: 1px solid #fecaca;
}

.ops-result {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border-radius: 8px;
  border: 1px solid var(--color-border, #d1d5db);
}

.ops-result--ok {
  background: #f0fdf4;
  border-color: #bbf7d0;
}

.ops-result--fail {
  background: #fef2f2;
  border-color: #fecaca;
}

.ops-result-head {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
}

.ops-result-badge {
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
}

.ops-result--ok .ops-result-badge {
  background: #16a34a;
  color: #fff;
}

.ops-result--fail .ops-result-badge {
  background: #dc2626;
  color: #fff;
}

.ops-result-output,
.ops-result-error {
  margin: 0;
  padding: 8px;
  border-radius: 6px;
  background: #0b0f17;
  color: #e5e7eb;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 220px;
  overflow: auto;
}

.ops-result-error {
  color: #fca5a5;
}

/* ─── second-confirm overlay ─────────────────────────────────────────────── */
.ops-confirm-backdrop {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 23, 42, 0.45);
  z-index: 50;
  padding: 16px;
}

.ops-confirm {
  width: min(420px, 100%);
  background: var(--color-surface, #fff);
  border-radius: 12px;
  padding: 20px;
  box-shadow: 0 20px 50px rgba(15, 23, 42, 0.3);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ops-confirm-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--color-text, #111827);
}

.ops-confirm-body {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
  color: var(--color-text, #374151);
}

.ops-confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

@media (prefers-reduced-motion: reduce) {
  .ops-btn {
    transition: none;
  }
}
</style>

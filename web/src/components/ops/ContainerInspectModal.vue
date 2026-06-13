<!--
  ContainerInspectModal.vue — 容器详情(inspect)弹窗

  挂载即拉 GET /api/servers/{id}/containers/{name}/inspect,分区展示容器精选元信息:
    · 基本信息(镜像 / 命令 / 状态 / 重启策略 / 创建时间)
    · 环境变量(等宽字体列表 —— 值可能含密钥,仅展示给已登录管理员,不复制到剪贴板按钮)
    · 挂载 / 网络 / 端口 / 标签

  优雅降级:加载中骨架;请求层失败(404/503 等)→ 错误条;后端 reachable:false(容器不存在/
  连接失败)→ 人读 error 条而非崩溃。scrim z-index 500(盖住主题切换按钮,与现有弹窗一致)。
-->
<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { getContainerInspect, type ContainerInspect } from '../../api/containers'
import { HttpError } from '../../api/http'

const props = defineProps<{
  serverId: string
  containerName: string
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const loading = ref(true)
/** 请求层错误(网络/404/503 等);与后端 reachable:false 区分。 */
const requestError = ref('')
const data = ref<ContainerInspect | null>(null)

async function load(): Promise<void> {
  loading.value = true
  requestError.value = ''
  data.value = null
  try {
    data.value = await getContainerInspect(props.serverId, props.containerName)
  } catch (e) {
    requestError.value =
      e instanceof HttpError
        ? e.message || t('opsContainer.inspect.loadFailed')
        : t('opsContainer.inspect.loadFailed')
  } finally {
    loading.value = false
  }
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') emit('close')
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void load()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
})

/** 端口的可读表示:发布 → host:port → container;未发布 → 仅容器端口。 */
function portLabel(p: ContainerInspect['ports'][number]): string {
  if (p.hostPort) {
    const host = p.hostIp ? `${p.hostIp}:${p.hostPort}` : p.hostPort
    return `${host} → ${p.containerPort}`
  }
  return `${p.containerPort}${t('opsContainer.inspect.unpublished')}`
}
</script>

<template>
  <div class="ci-backdrop" @click.self="emit('close')">
    <div class="ci-modal" role="dialog" aria-modal="true" aria-labelledby="ci-title">
      <header class="ci-head">
        <div class="ci-head-text">
          <h3 id="ci-title" class="ci-title">{{ t('opsContainer.inspect.title') }}</h3>
          <span class="ci-sub mono">{{ containerName }}</span>
        </div>
        <button type="button" class="ci-close" :aria-label="t('opsContainer.close')" @click="emit('close')">×</button>
      </header>

      <div class="ci-body">
        <!-- 加载中 -->
        <div v-if="loading" class="ci-loading">
          <span class="ci-spinner" aria-hidden="true"></span>
          {{ t('opsContainer.inspect.loading') }}
        </div>

        <!-- 请求层错误 -->
        <div v-else-if="requestError" class="ci-banner ci-banner--error" role="alert">
          {{ requestError }}
        </div>

        <!-- 后端可达但 inspect 失败 / 容器不存在 -->
        <div
          v-else-if="data && !data.reachable"
          class="ci-banner ci-banner--warn"
          role="alert"
        >
          {{ data.error || t('opsContainer.inspect.fetchFailed') }}
        </div>

        <!-- 正常详情 -->
        <template v-else-if="data">
          <!-- 基本信息 -->
          <section class="ci-section">
            <h4 class="ci-section-title">{{ t('opsContainer.inspect.basicInfo') }}</h4>
            <dl class="ci-kv">
              <dt>{{ t('opsContainer.inspect.image') }}</dt>
              <dd class="mono">{{ data.image || '—' }}</dd>
              <dt>{{ t('opsContainer.inspect.command') }}</dt>
              <dd class="mono">{{ data.command || '—' }}</dd>
              <dt>{{ t('opsContainer.inspect.status') }}</dt>
              <dd>
                <span class="ci-state" :class="`ci-state--${data.state || 'unknown'}`">{{
                  data.state || t('opsContainer.inspect.unknown')
                }}</span>
              </dd>
              <dt>{{ t('opsContainer.inspect.restartPolicy') }}</dt>
              <dd class="mono">{{ data.restartPolicy || '—' }}</dd>
              <dt>{{ t('opsContainer.inspect.createdAt') }}</dt>
              <dd class="mono">{{ data.createdAt || '—' }}</dd>
            </dl>
          </section>

          <!-- 环境变量 -->
          <section class="ci-section">
            <h4 class="ci-section-title">
              {{ t('opsContainer.inspect.env') }}
              <span class="ci-count">{{ data.env.length }}</span>
            </h4>
            <ul v-if="data.env.length" class="ci-list mono">
              <li v-for="(e, i) in data.env" :key="i" class="ci-list-item">{{ e }}</li>
            </ul>
            <p v-else class="ci-empty">{{ t('opsContainer.inspect.noEnv') }}</p>
          </section>

          <!-- 挂载 -->
          <section class="ci-section">
            <h4 class="ci-section-title">
              {{ t('opsContainer.inspect.mounts') }}
              <span class="ci-count">{{ data.mounts.length }}</span>
            </h4>
            <ul v-if="data.mounts.length" class="ci-list mono">
              <li v-for="(m, i) in data.mounts" :key="i" class="ci-list-item ci-mount">
                <span class="ci-mount-path">{{ m.source }} → {{ m.destination }}</span>
                <span class="ci-tag">{{ m.rw ? 'rw' : 'ro' }}{{ m.mode ? `,${m.mode}` : '' }}</span>
              </li>
            </ul>
            <p v-else class="ci-empty">{{ t('opsContainer.inspect.noMounts') }}</p>
          </section>

          <!-- 网络 -->
          <section class="ci-section">
            <h4 class="ci-section-title">
              {{ t('opsContainer.inspect.networks') }}
              <span class="ci-count">{{ data.networks.length }}</span>
            </h4>
            <ul v-if="data.networks.length" class="ci-list mono">
              <li v-for="(n, i) in data.networks" :key="i" class="ci-list-item ci-mount">
                <span class="ci-mount-path">{{ n.name }}</span>
                <span class="ci-tag">{{ n.ipAddress || t('opsContainer.inspect.noIp') }}</span>
              </li>
            </ul>
            <p v-else class="ci-empty">{{ t('opsContainer.inspect.noNetworks') }}</p>
          </section>

          <!-- 端口 -->
          <section class="ci-section">
            <h4 class="ci-section-title">
              {{ t('opsContainer.inspect.ports') }}
              <span class="ci-count">{{ data.ports.length }}</span>
            </h4>
            <ul v-if="data.ports.length" class="ci-list mono">
              <li v-for="(p, i) in data.ports" :key="i" class="ci-list-item">{{ portLabel(p) }}</li>
            </ul>
            <p v-else class="ci-empty">{{ t('opsContainer.inspect.noPorts') }}</p>
          </section>

          <!-- 标签 -->
          <section class="ci-section">
            <h4 class="ci-section-title">
              {{ t('opsContainer.inspect.labels') }}
              <span class="ci-count">{{ Object.keys(data.labels).length }}</span>
            </h4>
            <ul v-if="Object.keys(data.labels).length" class="ci-list mono">
              <li
                v-for="(val, key) in data.labels"
                :key="key"
                class="ci-list-item ci-mount"
              >
                <span class="ci-mount-path">{{ key }}</span>
                <span class="ci-tag ci-tag--label">{{ val }}</span>
              </li>
            </ul>
            <p v-else class="ci-empty">{{ t('opsContainer.inspect.noLabels') }}</p>
          </section>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ci-backdrop {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(15, 23, 42, 0.55);
  z-index: 500;
  padding: 24px;
}

.ci-modal {
  width: min(680px, 100%);
  max-height: min(82vh, 880px);
  display: flex;
  flex-direction: column;
  background: var(--color-surface, #fff);
  color: var(--color-text, #111827);
  border: 1px solid var(--color-border, #d1d5db);
  border-radius: 14px;
  box-shadow: 0 24px 60px rgba(15, 23, 42, 0.35);
  overflow: hidden;
}

.ci-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 18px 20px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
}

.ci-head-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.ci-title {
  margin: 0;
  font-size: 17px;
  font-weight: 650;
}

.ci-sub {
  font-size: 13px;
  color: var(--color-text-muted, #6b7280);
  word-break: break-all;
}

.ci-close {
  flex: none;
  width: 30px;
  height: 30px;
  border: 1px solid transparent;
  border-radius: 8px;
  background: transparent;
  color: var(--color-text-muted, #6b7280);
  font-size: 22px;
  line-height: 1;
  cursor: pointer;
  transition: background 120ms ease, color 120ms ease;
}

.ci-close:hover {
  background: var(--color-inset, #f3f4f6);
  color: var(--color-text, #111827);
}

.ci-close:focus-visible {
  outline: 2px solid var(--color-accent, #4f46e5);
  outline-offset: 1px;
}

.ci-body {
  padding: 16px 20px 20px;
  overflow-y: auto;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

/* ─── 加载 / 错误 ──────────────────────────────────────────────────────────── */
.ci-loading {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 24px 4px;
  font-size: 14px;
  color: var(--color-text-muted, #6b7280);
}

.ci-spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--color-border, #d1d5db);
  border-top-color: var(--color-accent, #4f46e5);
  border-radius: 50%;
  animation: ci-spin 700ms linear infinite;
}

@keyframes ci-spin {
  to {
    transform: rotate(360deg);
  }
}

.ci-banner {
  padding: 12px 14px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
}

.ci-banner--error {
  background: var(--color-danger-soft, #fef2f2);
  color: var(--color-danger, #991b1b);
  border: 1px solid var(--color-red-line, #fecaca);
}

.ci-banner--warn {
  background: var(--color-amber-soft, #fffbeb);
  color: var(--color-warn, #92400e);
  border: 1px solid var(--color-amber-line, #fde68a);
}

/* ─── 分区 ────────────────────────────────────────────────────────────────── */
.ci-section + .ci-section {
  margin-top: 18px;
}

.ci-section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-text-muted, #6b7280);
}

.ci-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--color-inset, #f3f4f6);
  color: var(--color-text-muted, #6b7280);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0;
}

.ci-kv {
  display: grid;
  grid-template-columns: 88px 1fr;
  gap: 6px 14px;
  margin: 0;
  font-size: 13px;
}

.ci-kv dt {
  color: var(--color-text-muted, #6b7280);
}

.ci-kv dd {
  margin: 0;
  word-break: break-word;
}

.ci-state {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  background: var(--color-inset, #f3f4f6);
  color: var(--color-text, #374151);
}

.ci-state--running {
  background: var(--color-green-soft, #dcfce7);
  color: var(--color-green, #15803d);
}

.ci-state--exited,
.ci-state--dead {
  background: var(--color-red-soft, #fee2e2);
  color: var(--color-red, #b91c1c);
}

.ci-state--paused,
.ci-state--restarting {
  background: var(--color-amber-soft, #fef3c7);
  color: var(--color-warn, #b45309);
}

.ci-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0;
  padding: 8px 10px;
  list-style: none;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 8px;
  background: var(--color-code-bg, #f8fafc);
}

.ci-list-item {
  font-size: 12.5px;
  line-height: 1.5;
  word-break: break-all;
}

.ci-mount {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
}

.ci-mount-path {
  min-width: 0;
  word-break: break-all;
}

.ci-tag {
  flex: none;
  padding: 1px 6px;
  border-radius: 5px;
  font-size: 11px;
  background: var(--color-inset, #eef2f7);
  color: var(--color-text-muted, #6b7280);
}

.ci-tag--label {
  max-width: 55%;
  word-break: break-all;
}

.ci-empty {
  margin: 0;
  padding: 8px 2px;
  font-size: 13px;
  color: var(--color-text-muted, #9ca3af);
}

@media (prefers-reduced-motion: reduce) {
  .ci-spinner {
    animation-duration: 1600ms;
  }
  .ci-close {
    transition: none;
  }
}
</style>

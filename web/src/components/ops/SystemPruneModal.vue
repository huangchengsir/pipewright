<script setup lang="ts">
/**
 * SystemPruneModal — 容器管理一键清理(docker system df / prune)。
 *
 * 选一台主机,看它的 Docker 磁盘占用(镜像 / 容器 / 卷 / 构建缓存的大小 + 可回收量),
 * 一键清理停止的容器 / 悬空镜像 / 无用卷 / 构建缓存 / 全部。每个清理走二次确认(说明会删什么、
 * 全部不含数据卷),结果走 toast,清理后刷新占用。
 *
 * 安全:scope 走后端枚举白名单;命令服务端 array 化、不拼 shell;all 不加 --volumes(不误删卷)。
 */
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getSystemDf,
  systemPrune,
  type SystemDf,
  type PruneScope,
} from '../../api/containers'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import AppButton from '../ui/AppButton.vue'

const props = defineProps<{
  /** 可作用于的服务器(通常是 reachable + 装了 docker 的)。 */
  servers: { id: string; name: string }[]
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const toast = useToast()
const confirm = useConfirm()

const selectedId = ref(props.servers[0]?.id ?? '')
const df = ref<SystemDf | null>(null)
const loading = ref(false)
const loadError = ref('')
/** 正在清理的 scope(禁用所有按钮 + 给该按钮 loading)。 */
const busyScope = ref<PruneScope | ''>('')

const selectedName = computed(
  () => props.servers.find((s) => s.id === selectedId.value)?.name ?? selectedId.value,
)

/** 清理动作定义(顺序即展示顺序)。 */
interface PruneAction {
  scope: PruneScope
  label: string
  /** 二次确认正文:明确说明会删什么。 */
  confirmBody: string
  danger?: boolean
}

const actions = computed<PruneAction[]>(() => [
  {
    scope: 'containers',
    label: t('opsContainer.prune.containersLabel'),
    confirmBody: t('opsContainer.prune.containersBody'),
  },
  {
    scope: 'images',
    label: t('opsContainer.prune.imagesLabel'),
    confirmBody: t('opsContainer.prune.imagesBody'),
  },
  {
    scope: 'volumes',
    label: t('opsContainer.prune.volumesLabel'),
    confirmBody: t('opsContainer.prune.volumesBody'),
    danger: true,
  },
  {
    scope: 'builder',
    label: t('opsContainer.prune.builderLabel'),
    confirmBody: t('opsContainer.prune.builderBody'),
  },
  {
    scope: 'all',
    label: t('opsContainer.prune.allLabel'),
    confirmBody: t('opsContainer.prune.allBody'),
    danger: true,
  },
])

async function loadDf(): Promise<void> {
  if (!selectedId.value) return
  loading.value = true
  loadError.value = ''
  try {
    df.value = await getSystemDf(selectedId.value)
  } catch (err: unknown) {
    df.value = null
    loadError.value =
      err instanceof HttpError
        ? err.status === 0
          ? t('opsContainer.prune.errConnect')
          : err.apiError?.message ?? t('opsContainer.prune.dfFailedStatus', { status: err.status })
        : t('opsContainer.prune.dfFailedRetry')
  } finally {
    loading.value = false
  }
}

async function runPrune(action: PruneAction): Promise<void> {
  if (!selectedId.value || busyScope.value) return
  const ok = await confirm.open({
    title: t('opsContainer.prune.confirmTitle', { label: action.label, server: selectedName.value }),
    body: action.confirmBody,
    confirmLabel: t('opsContainer.prune.confirmLabel', { label: action.label }),
    variant: action.danger ? 'danger' : 'primary',
  })
  if (!ok) return

  busyScope.value = action.scope
  try {
    const res = await systemPrune(selectedId.value, action.scope)
    if (res.ok) {
      const summary = res.output.trim()
      toast.success(t('opsContainer.prune.cleaned', { label: action.label }), {
        detail: summary || t('opsContainer.prune.nothingToClean'),
      })
    } else {
      toast.error(t('opsContainer.prune.cleanFailed', { label: action.label }), { detail: res.error || t('opsContainer.prune.remoteCmdFailed') })
    }
  } catch (err: unknown) {
    const msg =
      err instanceof HttpError
        ? err.apiError?.message ?? t('opsContainer.prune.reqFailedStatus', { status: err.status })
        : t('opsContainer.prune.reqFailedRetry')
    toast.error(t('opsContainer.prune.cleanFailed', { label: action.label }), { detail: msg })
  } finally {
    busyScope.value = ''
    // 清理后刷新占用(无论成功失败,占用都可能变了)。
    void loadDf()
  }
}

watch(selectedId, () => void loadDf())
onMounted(() => void loadDf())
</script>

<template>
  <div class="scrim" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" :aria-label="t('opsContainer.prune.dialogAria')">
      <header class="head">
        <div>
          <h2 class="title">{{ t('opsContainer.prune.title') }}</h2>
          <p class="sub">{{ t('opsContainer.prune.subtitle') }}</p>
        </div>
        <button class="close" :aria-label="t('opsContainer.close')" @click="emit('close')">✕</button>
      </header>

      <!-- Server picker -->
      <div v-if="servers.length === 0" class="empty">
        {{ t('opsContainer.prune.empty') }}
      </div>
      <template v-else>
        <div class="field">
          <label class="label" for="prune-server">{{ t('opsContainer.prune.targetHost') }}</label>
          <select id="prune-server" v-model="selectedId" class="select">
            <option v-for="s in servers" :key="s.id" :value="s.id">{{ s.name }}</option>
          </select>
        </div>

        <!-- Disk usage table -->
        <section class="usage" :aria-label="t('opsContainer.prune.diskUsageAria')">
          <div class="usage-head">
            <span>{{ t('opsContainer.prune.diskUsage') }}</span>
            <button class="link" :disabled="loading" @click="loadDf">
              {{ loading ? t('opsContainer.prune.refreshing') : t('common.refresh') }}
            </button>
          </div>

          <p v-if="loadError" class="err">{{ loadError }}</p>
          <p v-else-if="loading && !df" class="hint">{{ t('opsContainer.prune.reading') }}</p>
          <p v-else-if="df && !df.reachable" class="err">{{ t('opsContainer.prune.hostUnreachable') }}{{ df.error }}</p>
          <p v-else-if="df && !df.dockerAvailable" class="err">{{ df.error }}</p>
          <table v-else-if="df && df.entries.length > 0" class="df">
            <thead>
              <tr>
                <th>{{ t('opsContainer.prune.colType') }}</th>
                <th class="num">{{ t('opsContainer.prune.colCount') }}</th>
                <th class="num">{{ t('opsContainer.prune.colActive') }}</th>
                <th class="num">{{ t('opsContainer.prune.colSize') }}</th>
                <th class="num">{{ t('opsContainer.prune.colReclaimable') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="e in df.entries" :key="e.type">
                <td>{{ e.type }}</td>
                <td class="num">{{ e.totalCount }}</td>
                <td class="num">{{ e.active }}</td>
                <td class="num">{{ e.size }}</td>
                <td class="num reclaim">{{ e.reclaimable }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="hint">{{ t('opsContainer.prune.noUsageData') }}</p>
        </section>

        <!-- Cleanup actions -->
        <section class="actions" :aria-label="t('opsContainer.prune.actionsAria')">
          <p class="actions-title">{{ t('opsContainer.prune.cleanup') }}</p>
          <div class="action-grid">
            <AppButton
              v-for="a in actions"
              :key="a.scope"
              :variant="a.danger ? 'danger' : 'default'"
              :disabled="busyScope !== '' || !df?.dockerAvailable"
              :loading="busyScope === a.scope"
              @click="runPrune(a)"
            >
              {{ a.label }}
            </AppButton>
          </div>
          <p class="note">{{ t('opsContainer.prune.noteHead') }}<strong>{{ t('opsContainer.prune.noteStrong') }}</strong>{{ t('opsContainer.prune.noteTail') }}</p>
        </section>
      </template>

      <footer class="foot">
        <button class="btn-secondary" @click="emit('close')">{{ t('opsContainer.close') }}</button>
      </footer>
    </div>
  </div>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  background: oklch(0% 0 0 / 0.55);
  display: grid;
  place-items: center;
  padding: 1rem;
  z-index: 500;
}
.modal {
  width: min(600px, 100%);
  max-height: 88vh;
  overflow-y: auto;
  background: var(--color-card);
  border: 1px solid var(--color-border-strong);
  border-radius: 18px;
  box-shadow: var(--shadow-modal);
  padding: 1.4rem;
}
.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1.1rem;
}
.title {
  margin: 0;
  font-size: 1.15rem;
  font-weight: 640;
  color: var(--color-text);
}
.sub {
  margin: 0.3rem 0 0;
  font-size: 0.85rem;
  color: var(--color-dim);
}
.close {
  border: none;
  background: none;
  color: var(--color-dim);
  font-size: 1rem;
  cursor: pointer;
  width: 2rem;
  height: 2rem;
  border-radius: 8px;
  flex-shrink: 0;
}
.close:hover {
  background: var(--color-inset);
  color: var(--color-text);
}

.empty {
  padding: 1rem;
  border: 1px dashed var(--color-border);
  border-radius: 11px;
  color: var(--color-dim);
  font-size: 0.88rem;
  line-height: 1.6;
}

.field {
  margin-bottom: 1.1rem;
}
.label {
  display: block;
  margin-bottom: 0.35rem;
  font-size: 0.83rem;
  font-weight: 560;
  color: var(--color-dim);
}
.select {
  width: 100%;
  padding: 0.55rem 0.75rem;
  background: var(--color-inset);
  border: 1px solid var(--color-border);
  border-radius: 9px;
  color: var(--color-text);
  font-size: 0.9rem;
}
.select:focus {
  outline: none;
  border-color: var(--color-primary);
}

.usage {
  margin-bottom: 1.2rem;
}
.usage-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 0.55rem;
  font-size: 0.83rem;
  font-weight: 560;
  color: var(--color-dim);
}
.link {
  border: none;
  background: none;
  color: var(--color-primary);
  font-size: 0.83rem;
  cursor: pointer;
  padding: 0;
}
.link:disabled {
  opacity: 0.55;
  cursor: default;
}

.df {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}
.df th,
.df td {
  padding: 0.45rem 0.6rem;
  border-bottom: 1px solid var(--color-border);
  text-align: left;
  color: var(--color-text);
}
.df th {
  color: var(--color-dim);
  font-weight: 560;
}
.df .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.df .reclaim {
  color: var(--color-green, #2e7d32);
}

.actions-title {
  margin: 0 0 0.55rem;
  font-size: 0.83rem;
  font-weight: 560;
  color: var(--color-dim);
}
.action-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.note {
  margin: 0.7rem 0 0;
  font-size: 0.8rem;
  color: var(--color-dim);
  line-height: 1.5;
}
.note strong {
  color: var(--color-text);
}

.err {
  margin: 0;
  padding: 0.6rem 0.85rem;
  border-radius: 9px;
  font-size: 0.85rem;
  color: var(--color-red);
  background: var(--color-red-soft);
  border: 1px solid var(--color-red-line);
}
.hint {
  margin: 0;
  color: var(--color-dim);
  font-size: 0.85rem;
}

.foot {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
  margin-top: 1.4rem;
}
.btn-secondary {
  padding: 0.55rem 1.1rem;
  background: var(--color-card-2);
  color: var(--color-text);
  border: 1px solid var(--color-border-strong);
  border-radius: 10px;
  font-weight: 560;
  font-size: 0.9rem;
  cursor: pointer;
}
.btn-secondary:hover {
  border-color: var(--color-primary);
}
</style>

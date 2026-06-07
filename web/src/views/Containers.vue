<script setup lang="ts">
/*
  Containers.vue — 容器总览(Portainer 式):聚合所有已登记服务器上的容器,逐台分组展示,
  行内可执行生命周期操作(起停重启 / 暂停恢复 / kill / 删除)+ 跳主机终端 docker exec。

  · 数据:GET /api/servers/containers(批量,逐台并行、单台失败不连累全局)+ listServers(取名/主机)。
  · 操作:复用 serviceAction(type=docker);破坏性操作(停止/重启/kill/删除)走 useConfirm。
  · 每 12s 自动刷新(stale-while-revalidate:刷新保留旧数据,仅首屏显骨架)。
*/
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { getAllContainers, type ServerContainers, type ContainerInfo, type ContainerState } from '../api/containers'
import { listServers, serviceAction, type Server, type ServiceAction } from '../api/servers'
import { HttpError } from '../api/http'
import { useToast } from '../composables/useToast'
import { useConfirm } from '../composables/useConfirm'
import { NIcon } from 'naive-ui'
import { BrandDocker, AlertTriangle } from '@vicons/tabler'
import AppButton from '../components/ui/AppButton.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import ErrorState from '../components/ui/ErrorState.vue'
import SkeletonBlock from '../components/ui/SkeletonBlock.vue'

type LoadState = 'idle' | 'loading' | 'error'

const router = useRouter()
const toast = useToast()
const confirm = useConfirm()

const loadState = ref<LoadState>('idle')
const loadError = ref('')
const groups = ref<ServerContainers[]>([])
const serverById = ref<Map<string, Server>>(new Map())
const refreshing = ref(false)
/** 正在执行操作的 `${serverId}:${containerId}:${action}`,用于禁用对应按钮并显示 loading。 */
const busy = ref<Set<string>>(new Set())

const POLL_MS = 12_000
let timer: ReturnType<typeof setInterval> | null = null

// ─── 聚合统计 ─────────────────────────────────────────────────────────────────
const totalContainers = computed(() => groups.value.reduce((n, g) => n + g.total, 0))
const runningContainers = computed(() => groups.value.reduce((n, g) => n + g.running, 0))
const stoppedContainers = computed(() => totalContainers.value - runningContainers.value)
const coveredServers = computed(() => groups.value.filter((g) => g.reachable && g.runtime).length)

// ─── 状态筛选 ─────────────────────────────────────────────────────────────────
// 把六种容器状态归并为三档「桶」,供筛选段与计数使用:
//   running   = running / restarting   （在跑)
//   paused    = paused                 （已暂停)
//   stopped   = exited / created / dead / unknown  （未在跑)
type StateBucket = 'running' | 'paused' | 'stopped'
type StateFilter = 'all' | StateBucket

function stateBucket(s: ContainerState): StateBucket {
  if (s === 'running' || s === 'restarting') return 'running'
  if (s === 'paused') return 'paused'
  return 'stopped'
}

const stateFilter = ref<StateFilter>('all')

/** 各档全局计数(跨所有服务器)。 */
const bucketCounts = computed(() => {
  let running = 0
  let paused = 0
  let stopped = 0
  for (const g of groups.value) {
    for (const c of g.containers) {
      const b = stateBucket(c.state)
      if (b === 'running') running++
      else if (b === 'paused') paused++
      else stopped++
    }
  }
  return { all: running + paused + stopped, running, paused, stopped }
})

interface FilterOption {
  key: StateFilter
  label: string
}
const FILTER_OPTIONS: FilterOption[] = [
  { key: 'all', label: '全部' },
  { key: 'running', label: '运行中' },
  { key: 'stopped', label: '已停止' },
  { key: 'paused', label: '已暂停' },
]
function filterCount(key: StateFilter): number {
  return bucketCounts.value[key]
}
const activeFilterLabel = computed(() => FILTER_OPTIONS.find((f) => f.key === stateFilter.value)?.label ?? '')

/** 应用当前筛选后该服务器可见的容器(all 时原样返回)。 */
function visibleContainers(g: ServerContainers): ContainerInfo[] {
  if (stateFilter.value === 'all') return g.containers
  return g.containers.filter((c) => stateBucket(c.state) === stateFilter.value)
}

function serverName(id: string): string {
  return serverById.value.get(id)?.name ?? id
}
function serverHost(id: string): string {
  const s = serverById.value.get(id)
  return s ? `${s.user}@${s.host}:${s.port}` : ''
}

/** 运行时显示名:首字母大写(docker → Docker),未知则原样。 */
function runtimeLabel(runtime: string): string {
  if (!runtime) return ''
  return runtime.charAt(0).toUpperCase() + runtime.slice(1)
}

// ─── 状态徽标 ─────────────────────────────────────────────────────────────────
interface StateMeta {
  label: string
  tone: 'green' | 'amber' | 'cyan' | 'red' | 'faint'
  pulse: boolean
}
const STATE_META: Record<ContainerState, StateMeta> = {
  running:    { label: '运行中', tone: 'green', pulse: true },
  paused:     { label: '已暂停', tone: 'amber', pulse: false },
  restarting: { label: '重启中', tone: 'amber', pulse: true },
  created:    { label: '已创建', tone: 'cyan', pulse: false },
  exited:     { label: '已停止', tone: 'faint', pulse: false },
  dead:       { label: '异常', tone: 'red', pulse: false },
  unknown:    { label: '未知', tone: 'faint', pulse: false },
}
function stateMeta(s: ContainerState): StateMeta {
  return STATE_META[s] ?? STATE_META.unknown
}
function shortId(id: string): string {
  return id.length > 12 ? id.slice(0, 12) : id
}

// ─── 行内操作 ─────────────────────────────────────────────────────────────────
interface ActionSpec {
  action: ServiceAction
  label: string
  variant: 'default' | 'ghost' | 'danger'
  danger?: boolean // 需二次确认
}

/** 据容器状态给出可用操作集(顺序即展示顺序)。 */
function actionsFor(state: ContainerState): ActionSpec[] {
  switch (state) {
    case 'running':
      return [
        { action: 'restart', label: '重启', variant: 'default', danger: true },
        { action: 'stop', label: '停止', variant: 'ghost', danger: true },
        { action: 'pause', label: '暂停', variant: 'ghost' },
        { action: 'kill', label: 'Kill', variant: 'ghost', danger: true },
      ]
    case 'paused':
      return [
        { action: 'unpause', label: '恢复', variant: 'default' },
        { action: 'stop', label: '停止', variant: 'ghost', danger: true },
      ]
    case 'restarting':
      return [{ action: 'stop', label: '停止', variant: 'ghost', danger: true }]
    default: // exited / created / dead / unknown
      return [
        { action: 'start', label: '启动', variant: 'default' },
        { action: 'rm', label: '删除', variant: 'ghost', danger: true },
      ]
  }
}

// 行内操作的 hover 提示(原生 title):点明各动作机制与区别,鼠标悬停即见。
const ACTION_HINTS: Record<ServiceAction, string> = {
  start: '启动已停止的容器(docker start),从头跑新进程。',
  restart: '重启:先优雅停止(SIGTERM,10 秒宽限)再启动(docker restart)。',
  stop: '优雅停止:发 SIGTERM,10 秒内未退再补 SIGKILL(docker stop)。日常停服务用这个,能让程序收尾、落盘。',
  pause: '暂停:cgroup 冻结容器内所有进程(docker pause),内存原样保留、CPU 不再分给它;点「恢复」从断点续跑。不释放内存。',
  unpause: '恢复:解冻已暂停的容器(docker unpause),同一进程从断点继续。',
  kill: '强制 Kill:直接发 SIGKILL 立即终止,不给清理机会(docker kill),可能丢未落盘数据。仅在「停止」卡住时用。',
  rm: '删除容器(docker rm),运行中的需先停止。容器配置移除,挂载的数据卷不受影响。',
}

const DANGER_COPY: Partial<Record<ServiceAction, { title: (n: string) => string; body: string; confirmLabel: string }>> = {
  restart: { title: (n) => `重启容器 ${n}?`, body: '容器将停止后重新启动,期间该服务短暂不可用。', confirmLabel: '确认重启' },
  stop:    { title: (n) => `停止容器 ${n}?`, body: '容器将被停止,其提供的服务会中断,直到再次启动。', confirmLabel: '确认停止' },
  kill:    { title: (n) => `强制 Kill 容器 ${n}?`, body: '将发送 SIGKILL 立即终止容器进程,可能丢失未落盘数据。', confirmLabel: '强制 Kill' },
  rm:      { title: (n) => `删除容器 ${n}?`, body: '将删除该容器(运行中的需先停止)。容器配置随之移除,数据卷不受影响。', confirmLabel: '确认删除' },
}

function busyKey(serverId: string, containerId: string, action: ServiceAction): string {
  return `${serverId}:${containerId}:${action}`
}
function isBusy(serverId: string, containerId: string, action: ServiceAction): boolean {
  return busy.value.has(busyKey(serverId, containerId, action))
}
function rowBusy(serverId: string, containerId: string): boolean {
  for (const k of busy.value) if (k.startsWith(`${serverId}:${containerId}:`)) return true
  return false
}

async function runAction(serverId: string, c: ContainerInfo, spec: ActionSpec): Promise<void> {
  if (spec.danger) {
    const copy = DANGER_COPY[spec.action]
    const ok = await confirm.open({
      title: copy?.title(c.names) ?? `对 ${c.names} 执行 ${spec.label}?`,
      body: copy?.body ?? '该操作会影响容器运行状态。',
      confirmLabel: copy?.confirmLabel ?? spec.label,
      variant: 'danger',
    })
    if (!ok) return
  }

  const key = busyKey(serverId, c.id, spec.action)
  busy.value = new Set(busy.value).add(key)
  try {
    const res = await serviceAction(serverId, { type: 'docker', target: c.names, action: spec.action })
    if (res.ok) {
      toast.success(`${spec.label}成功`, { detail: `${c.names} · ${serverName(serverId)}` })
    } else {
      toast.error(`${spec.label}失败`, { detail: res.error || '远端命令以非零状态退出' })
    }
  } catch (err) {
    toast.error(`${spec.label}失败`, { detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误' })
  } finally {
    const next = new Set(busy.value)
    next.delete(key)
    busy.value = next
    // 操作后刷新该台的容器状态(稍候让 docker 状态稳定)。
    setTimeout(() => void load(), 600)
  }
}

function openTerminal(serverId: string, c: ContainerInfo): void {
  // 跳终端全屏页(新标签),带 ?container= → 直接 `docker exec -it <容器> sh` 进该容器。
  const url = router.resolve({
    name: 'server-terminal',
    params: { id: serverId },
    query: { container: c.names, cid: shortId(c.id) },
  }).href
  window.open(url, '_blank', 'noopener')
}

// ─── 加载 ─────────────────────────────────────────────────────────────────────
async function load(): Promise<void> {
  const firstLoad = groups.value.length === 0
  loadState.value = 'loading'
  loadError.value = ''
  if (!firstLoad) refreshing.value = true
  try {
    const [servers, items] = await Promise.all([listServers(), getAllContainers()])
    const m = new Map<string, Server>()
    for (const s of servers) m.set(s.id, s)
    serverById.value = m
    groups.value = items
    loadState.value = 'idle'
  } catch (err) {
    if (err instanceof HttpError) {
      loadError.value =
        err.status === 0
          ? '无法连接到服务器,请检查后端是否运行后重试'
          : (err.apiError?.message ?? `加载容器列表失败(${err.status})`)
    } else {
      loadError.value = '加载容器列表失败,请稍后重试'
    }
    loadState.value = 'error'
  } finally {
    refreshing.value = false
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => void load(), POLL_MS)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="containers-view">
    <header class="view-header">
      <div class="view-header__text">
        <h1 class="view-title">容器</h1>
        <p class="view-sub">
          跨所有已登记服务器的容器总览
          <span v-if="totalContainers > 0" class="view-sub__count">
            · 共 {{ totalContainers }} 个 · {{ runningContainers }} 运行中
          </span>
          <span class="view-sub__count">· 每 12 秒自动刷新</span>
        </p>
      </div>
      <AppButton variant="default" :loading="loadState === 'loading' && groups.length === 0" @click="load">
        刷新
      </AppButton>
    </header>

    <!-- 首屏骨架 -->
    <div v-if="loadState === 'loading' && groups.length === 0" class="skeletons" aria-busy="true" aria-label="正在加载容器列表">
      <div v-for="n in 2" :key="n" class="skeleton-panel">
        <SkeletonBlock :height="20" width="40%" />
        <SkeletonBlock :height="48" width="100%" />
        <SkeletonBlock :height="48" width="100%" />
      </div>
    </div>

    <!-- 整页加载失败 -->
    <ErrorState
      v-else-if="loadState === 'error' && groups.length === 0"
      title="加载容器列表失败"
      :description="loadError"
      @retry="load"
    />

    <!-- 无已登记服务器 -->
    <EmptyState
      v-else-if="groups.length === 0"
      title="尚无已登记服务器"
      description="先在「设置 › 服务器」登记目标服务器,这里就会汇总它们上面的容器。"
    />

    <template v-else>
      <!-- 状态筛选段 -->
      <div class="filter-bar" role="group" aria-label="按状态筛选容器">
        <button
          v-for="f in FILTER_OPTIONS"
          :key="f.key"
          class="filter-chip"
          :class="{ 'filter-chip--active': stateFilter === f.key }"
          :aria-pressed="stateFilter === f.key"
          @click="stateFilter = f.key"
        >
          {{ f.label }}
          <span class="filter-chip__count">{{ filterCount(f.key) }}</span>
        </button>
      </div>

      <!-- 聚合 KPI 条 -->
      <section class="kpi-strip" aria-label="容器聚合统计">
        <div class="kpi kpi--total">
          <span class="kpi__num">{{ totalContainers }}</span>
          <span class="kpi__label">容器总数</span>
        </div>
        <div class="kpi kpi--running">
          <span class="kpi__num">{{ runningContainers }}</span>
          <span class="kpi__label">运行中</span>
        </div>
        <div class="kpi kpi--stopped">
          <span class="kpi__num">{{ stoppedContainers }}</span>
          <span class="kpi__label">已停止</span>
        </div>
        <div class="kpi kpi--hosts">
          <span class="kpi__num">{{ coveredServers }}<span class="kpi__den">/{{ groups.length }}</span></span>
          <span class="kpi__label">有容器的服务器</span>
        </div>
      </section>

      <!-- 逐台分组 -->
      <section class="groups" :aria-busy="refreshing || undefined">
        <article v-for="g in groups" :key="g.serverId" class="panel">
          <header class="panel__head">
            <div class="panel__id">
              <h2 class="panel__name">{{ serverName(g.serverId) }}</h2>
              <span class="panel__host mono">{{ serverHost(g.serverId) }}</span>
            </div>
            <div class="panel__meta">
              <span
                class="rt-badge"
                :class="{
                  'rt-badge--ok': g.reachable && g.runtime,
                  'rt-badge--warn': g.reachable && !g.runtime,
                  'rt-badge--down': !g.reachable,
                }"
              >
                <NIcon :size="15" class="rt-badge__icon">
                  <BrandDocker v-if="g.reachable && g.runtime" />
                  <AlertTriangle v-else />
                </NIcon>
                {{ !g.reachable ? '不可达' : g.runtime ? runtimeLabel(g.runtime) : '无容器运行时' }}
              </span>
              <span v-if="g.reachable && g.runtime" class="panel__count mono">
                {{ g.running }}/{{ g.total }} 运行
              </span>
            </div>
          </header>

          <!-- 不可达 / 无运行时:行内提示,不连累全局 -->
          <p v-if="!g.reachable" class="panel__hint panel__hint--down">⚠ {{ g.error }}</p>
          <p v-else-if="!g.runtime" class="panel__hint">{{ g.error }}</p>
          <p v-else-if="g.total === 0" class="panel__hint">该服务器暂无容器。</p>
          <p v-else-if="visibleContainers(g).length === 0" class="panel__hint">
            无「{{ activeFilterLabel }}」状态的容器(该服务器共 {{ g.total }} 个)。
          </p>

          <!-- 容器表 -->
          <ul v-else class="clist" role="list">
            <li v-for="c in visibleContainers(g)" :key="c.id" class="crow" :class="{ 'crow--busy': rowBusy(g.serverId, c.id) }">
              <div class="crow__state">
                <span
                  class="dot"
                  :class="[`dot--${stateMeta(c.state).tone}`, { 'dot--pulse': stateMeta(c.state).pulse }]"
                  aria-hidden="true"
                />
                <span class="crow__statelabel" :class="`txt--${stateMeta(c.state).tone}`">{{ stateMeta(c.state).label }}</span>
              </div>
              <div class="crow__main">
                <div class="crow__name" :title="c.names">{{ c.names }}</div>
                <div class="crow__sub mono" :title="`${c.image} · ${c.id}`">{{ c.image }} · {{ shortId(c.id) }}</div>
              </div>
              <div class="crow__status">
                <div class="crow__statustxt" :title="c.status">{{ c.status }}</div>
                <div v-if="c.ports" class="crow__ports mono" :title="c.ports">{{ c.ports }}</div>
              </div>
              <div class="crow__actions">
                <button
                  v-for="a in actionsFor(c.state)"
                  :key="a.action"
                  class="op"
                  :class="[`op--${a.variant}`, { 'op--busy': isBusy(g.serverId, c.id, a.action) }]"
                  :disabled="rowBusy(g.serverId, c.id)"
                  :title="ACTION_HINTS[a.action]"
                  @click="runAction(g.serverId, c, a)"
                >
                  {{ a.label }}
                </button>
                <button class="op op--ghost" :disabled="rowBusy(g.serverId, c.id)" title="进入容器交互终端(docker exec -it)" @click="openTerminal(g.serverId, c)">
                  终端
                </button>
              </div>
            </li>
          </ul>
        </article>
      </section>
    </template>
  </div>
</template>

<style scoped>
.containers-view {
  display: flex;
  flex-direction: column;
  gap: 22px;
}

/* header — 与 server-status 一致 */
.view-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.view-header__text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.view-title {
  margin: 0;
  font-size: var(--text-h1);
  font-weight: 700;
  color: var(--color-text);
}
.view-sub {
  margin: 0;
  font-size: var(--text-label);
  color: var(--color-dim);
}
.view-sub__count {
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}

/* ── 状态筛选段 ── */
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 6px 13px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-dim);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 999px;
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--ease-out-expo),
    border-color var(--duration-fast) var(--ease-out-expo),
    background var(--duration-fast) var(--ease-out-expo);
}
.filter-chip:hover {
  color: var(--color-text);
  border-color: var(--color-border-strong);
}
.filter-chip--active {
  color: #fff;
  background: var(--color-primary);
  border-color: var(--color-primary);
}
.filter-chip__count {
  font-size: var(--text-micro);
  font-variant-numeric: tabular-nums;
  padding: 1px 7px;
  border-radius: 999px;
  background: var(--color-inset);
  color: var(--color-faint);
}
.filter-chip--active .filter-chip__count {
  background: oklch(100% 0 0 / 0.22);
  color: #fff;
}

/* ── 聚合 KPI 条:bento 式,语义左描边 ── */
.kpi-strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
}
.kpi {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 16px 18px;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded);
  overflow: hidden;
}
.kpi::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--color-faint);
}
.kpi--running::before { background: var(--color-green); }
.kpi--stopped::before { background: var(--color-faint); }
.kpi--total::before { background: var(--color-primary); }
.kpi--hosts::before { background: var(--color-cyan); }
.kpi__num {
  font-size: var(--text-kpi);
  font-weight: 700;
  line-height: 1;
  color: var(--color-text);
  font-variant-numeric: tabular-nums;
}
.kpi__den {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-faint);
}
.kpi__label {
  font-size: var(--text-caps);
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--color-faint);
}

/* ── 逐台分组面板 ── */
.groups {
  display: flex;
  flex-direction: column;
  gap: 16px;
  transition: opacity var(--duration-fast) var(--ease-out-expo);
}
.groups[aria-busy='true'] {
  opacity: 0.7;
}
.panel {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-card-2);
}
.panel__id {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}
.panel__name {
  margin: 0;
  font-size: var(--text-section);
  font-weight: 700;
  color: var(--color-text);
}
.panel__host {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.panel__meta {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.panel__count {
  font-size: var(--text-micro);
  color: var(--color-dim);
  font-variant-numeric: tabular-nums;
}

.rt-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: var(--text-label);
  font-weight: 700;
  letter-spacing: 0.01em;
  padding: 4px 11px 4px 9px;
  border-radius: 999px;
  border: 1px solid var(--color-border-strong);
  color: var(--color-dim);
  white-space: nowrap;
}
.rt-badge__icon {
  display: inline-flex;
}
.rt-badge--ok {
  color: var(--color-green);
  background: var(--color-green-soft);
  border-color: var(--color-green-line);
}
.rt-badge--warn {
  color: var(--color-amber);
  background: var(--color-amber-soft);
  border-color: var(--color-amber-line);
}
.rt-badge--down {
  color: var(--color-red);
  background: var(--color-red-soft);
  border-color: var(--color-red-line);
}

.panel__hint {
  margin: 0;
  padding: 16px 18px;
  font-size: var(--text-label);
  color: var(--color-faint);
}
.panel__hint--down {
  color: var(--color-red);
}

/* ── 容器行 ── */
.clist {
  list-style: none;
  margin: 0;
  padding: 0;
}
.crow {
  display: grid;
  grid-template-columns: 92px minmax(0, 1.4fr) minmax(0, 1.3fr) auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast) var(--ease-out-expo);
}
.crow:last-child {
  border-bottom: none;
}
.crow:hover {
  background: var(--color-card-2);
}
.crow--busy {
  opacity: 0.55;
}
.crow__state {
  display: flex;
  align-items: center;
  gap: 7px;
}
.crow__statelabel {
  font-size: var(--text-micro);
  font-weight: 600;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--color-faint);
}
.dot--green { background: var(--color-green); }
.dot--amber { background: var(--color-amber); }
.dot--cyan { background: var(--color-cyan); }
.dot--red { background: var(--color-red); }
.dot--faint { background: var(--color-faint); }
.dot--pulse {
  animation: dotPulse 1.8s var(--ease-out-expo) infinite;
}
@keyframes dotPulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.45; transform: scale(0.8); }
}
.txt--green { color: var(--color-green); }
.txt--amber { color: var(--color-amber); }
.txt--cyan { color: var(--color-cyan); }
.txt--red { color: var(--color-red); }
.txt--faint { color: var(--color-faint); }

.crow__main {
  min-width: 0;
}
.crow__name {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__sub {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__status {
  min-width: 0;
}
.crow__statustxt {
  font-size: var(--text-label);
  color: var(--color-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__ports {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crow__actions {
  display: flex;
  align-items: center;
  gap: 6px;
  justify-content: flex-end;
  flex-wrap: wrap;
}

/* 行内迷你操作按钮(比 AppButton 更紧凑,适配密集表格) */
.op {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 4px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition:
    background var(--duration-fast) var(--ease-out-expo),
    color var(--duration-fast) var(--ease-out-expo),
    border-color var(--duration-fast) var(--ease-out-expo);
}
.op:hover:not(:disabled) {
  color: var(--color-text);
  border-color: var(--color-text);
}
.op--default {
  color: var(--color-primary);
  border-color: var(--color-primary-soft);
}
.op--default:hover:not(:disabled) {
  color: #fff;
  background: var(--color-primary);
  border-color: var(--color-primary);
}
.op:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.op--busy {
  opacity: 0.6;
  pointer-events: none;
}

/* 骨架 */
.skeletons {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.skeleton-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--rounded-lg, 12px);
  background: var(--color-card);
}

@media (max-width: 720px) {
  .crow {
    grid-template-columns: 1fr;
    gap: 8px;
  }
  .crow__actions {
    justify-content: flex-start;
  }
}
</style>

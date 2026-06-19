<script setup lang="ts">
/*
  ServerCard.vue — 单台服务器的容器管理卡片。卡片内部按「容器 / 镜像」分 tab,
  tab 内容只针对这一台(Portainer 式按主机管理)。容器 tab:生命周期操作 + 日志 + 终端;
  镜像 tab:列表 + 拉取 + 删除。生命周期/镜像写操作完成后 emit('changed') 让父刷新聚合。
*/
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getServerContainerStats,
  getServerImages,
  pullImage,
  removeImage,
  getServerStacks,
  getStackDetail,
  saveStackCompose,
  deployStack,
  stackAction,
  getServerVolumes,
  createVolume,
  removeVolume,
  getServerNetworks,
  createNetwork,
  removeNetwork,
  getNetworkContainers,
  connectNetwork,
  disconnectNetwork,
  type ServerContainers,
  type ContainerInfo,
  type ContainerStat,
  type ImageInfo,
  type StackInfo,
  type StackAction,
  type StackDetail,
  type VolumeInfo,
  type NetworkInfo,
  type NetworkContainer,
} from '../../api/containers'
import { serviceAction } from '../../api/servers'
import { generateCompose } from '../../api/aiOps'
import ReverseProxyPanel from './ReverseProxyPanel.vue'
import { HttpError } from '../../api/http'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'
import { NIcon } from 'naive-ui'
import { BrandDocker, AlertTriangle } from '@vicons/tabler'
import {
  stateBucket,
  stateMeta,
  shortId,
  actionsFor,
  ACTION_HINTS,
  DANGER_COPY,
  type ActionSpec,
  type StateBucket,
} from '../../lib/containerState'

const props = defineProps<{
  group: ServerContainers
  name: string
  host: string
  /** 容器状态筛选(来自页面级筛选段);'all' = 不筛。 */
  stateFilter: 'all' | StateBucket
  /** 文本搜索(来自页面级搜索框);按 names / image 忽略大小写包含。 */
  search: string
  /** 批量模式:容器行左侧显示复选框。 */
  bulkMode: boolean
  /** 父持有的选择集;key = `${serverId}::${containerName}`。 */
  selectedSet: Set<string>
}>()
const emit = defineEmits<{
  (e: 'changed'): void
  (e: 'logs', c: ContainerInfo): void
  (e: 'terminal', c: ContainerInfo): void
  (e: 'diagnose', c: ContainerInfo): void
  (e: 'inspect', c: ContainerInfo): void
  (e: 'toggle-select', name: string): void
}>()

const toast = useToast()
const confirm = useConfirm()
const { t } = useI18n()

type TabKey = 'containers' | 'images' | 'stacks' | 'volumes' | 'networks' | 'domains'
const tab = ref<TabKey>('containers')

// 运行时显示名:首字母大写(docker → Docker)。
const runtimeLabel = computed(() =>
  props.group.runtime ? props.group.runtime.charAt(0).toUpperCase() + props.group.runtime.slice(1) : '',
)

// ─── 容器 ─────────────────────────────────────────────────────────────────────
const busy = ref<Set<string>>(new Set())
function rowBusy(cid: string): boolean {
  for (const k of busy.value) if (k.startsWith(cid + ':')) return true
  return false
}
function isBusy(cid: string, action: string): boolean {
  return busy.value.has(`${cid}:${action}`)
}

const visibleContainers = computed(() => {
  let list = props.group.containers
  if (props.stateFilter !== 'all') {
    list = list.filter((c) => stateBucket(c.state) === props.stateFilter)
  }
  const q = props.search.trim().toLowerCase()
  if (q) {
    list = list.filter(
      (c) => c.names.toLowerCase().includes(q) || c.image.toLowerCase().includes(q),
    )
  }
  return list
})

// 容器 tab 过滤后为空时的提示文案(区分搜索 / 状态筛选)。
const emptyContainersHint = computed(() => {
  const hasSearch = props.search.trim().length > 0
  const hasFilter = props.stateFilter !== 'all'
  const q = props.search.trim()
  const total = props.group.total
  if (hasSearch && hasFilter) return t('opsServer.card.emptySearchFilter', { q, total })
  if (hasSearch) return t('opsServer.card.emptySearch', { q, total })
  return t('opsServer.card.emptyFilter', { total })
})

// 批量:该容器是否已被选中(key 带本机 serverId)。
function isSelected(name: string): boolean {
  return props.selectedSet.has(`${props.group.serverId}::${name}`)
}

async function runAction(c: ContainerInfo, spec: ActionSpec): Promise<void> {
  if (spec.danger) {
    const copy = DANGER_COPY[spec.action]
    const ok = await confirm.open({
      title: copy?.title(c.names) ?? t('opsServer.card.actionConfirmTitle', { name: c.names, label: spec.label }),
      body: copy?.body ?? t('opsServer.card.actionConfirmBody'),
      confirmLabel: copy?.confirmLabel ?? spec.label,
      variant: 'danger',
    })
    if (!ok) return
  }
  const key = `${c.id}:${spec.action}`
  busy.value = new Set(busy.value).add(key)
  try {
    const res = await serviceAction(props.group.serverId, { type: 'docker', target: c.names, action: spec.action })
    if (res.ok) toast.success(t('opsServer.card.actionSuccess', { label: spec.label }), { detail: `${c.names} · ${props.name}` })
    else toast.error(t('opsServer.card.actionFail', { label: spec.label }), { detail: res.error || t('opsServer.card.errNonZeroExit') })
  } catch (err) {
    toast.error(t('opsServer.card.actionFail', { label: spec.label }), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    const next = new Set(busy.value)
    next.delete(key)
    busy.value = next
    setTimeout(() => emit('changed'), 600)
  }
}

// ─── 实时资源 stats(进容器 tab 拉一次 + 手动刷新) ──────────────────────────────
// docker stats --no-stream:仅 running 容器有样本,按容器名 merge 到行上。失败静默(只读
// 锦上添花,不打断容器管理);不可达/无运行时 → 空 Map,行上不显示。
const containerStats = ref<Map<string, ContainerStat>>(new Map())
const statsLoading = ref(false)

function statFor(name: string): ContainerStat | undefined {
  return containerStats.value.get(name)
}

// 挂载时(每卡片一次,不随 12s 轮询重跑):容器 tab 拉 stats;并后台并行预取
// 镜像/Stacks/卷/网络 的计数,让各 tab 上的数字一开始就有(不必点开)。仅对连得上且有
// 运行时的服务器预取,避免对不可达主机白跑 SSH。
onMounted(() => {
  if (tab.value === 'containers') void loadStats()
  if (props.group.reachable && props.group.runtime) {
    void loadImages()
    void loadStacks()
    void loadVolumes()
    void loadNetworks()
  }
})

async function loadStats(): Promise<void> {
  if (statsLoading.value) return
  statsLoading.value = true
  try {
    const res = await getServerContainerStats(props.group.serverId)
    const next = new Map<string, ContainerStat>()
    if (res.reachable && res.runtime) {
      for (const s of res.stats) next.set(s.name, s)
    }
    containerStats.value = next
  } catch {
    // 静默:stats 仅为容器行的附加信息,拉失败不影响主流程。
  } finally {
    statsLoading.value = false
  }
}

// ─── 镜像(懒加载:首次切到镜像 tab 才拉) ───────────────────────────────────────
const images = ref<ImageInfo[]>([])
const imgState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const imgError = ref('')
const pullRef = ref('')
const pulling = ref(false)
const busyImage = ref('')

function repoTag(img: ImageInfo): string {
  return img.repository === '<none>' ? `<none>:${img.tag}` : `${img.repository}:${img.tag}`
}

async function loadImages(): Promise<void> {
  imgState.value = 'loading'
  imgError.value = ''
  try {
    const res = await getServerImages(props.group.serverId)
    if (!res.reachable || !res.runtime) {
      imgState.value = 'error'
      imgError.value = res.error || t('opsServer.card.errUnreachableOrNoRuntime')
      return
    }
    images.value = res.images
    imgState.value = 'loaded'
  } catch (err) {
    imgState.value = 'error'
    imgError.value =
      err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errLoadImages', { status: err.status })) : t('opsServer.card.errLoadImagesPlain')
  }
}

// ─── 域名 / 反向代理(懒加载:首次切到 domains tab 才挂载面板) ─────────────────────
const domainsMounted = ref(false)
const domainCount = ref(-1) // -1 = 尚未加载(角标不显示)

function switchTab(tabKey: TabKey): void {
  tab.value = tabKey
  if (tabKey === 'containers') void loadStats()
  if (tabKey === 'images' && imgState.value === 'idle') void loadImages()
  if (tabKey === 'stacks' && stackState.value === 'idle') void loadStacks()
  if (tabKey === 'volumes' && volState.value === 'idle') void loadVolumes()
  if (tabKey === 'networks' && netState.value === 'idle') void loadNetworks()
  if (tabKey === 'domains') domainsMounted.value = true
}

// ─── 数据卷(懒加载) ──────────────────────────────────────────────────────────
const volumes = ref<VolumeInfo[]>([])
const volState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const volError = ref('')
const newVol = ref('')
const busyVol = ref('')

async function loadVolumes(): Promise<void> {
  volState.value = 'loading'
  volError.value = ''
  try {
    const res = await getServerVolumes(props.group.serverId)
    if (!res.reachable || !res.runtime) {
      volState.value = 'error'
      volError.value = res.error || t('opsServer.card.errUnreachableOrNoRuntime')
      return
    }
    volumes.value = res.volumes
    volState.value = 'loaded'
  } catch (err) {
    volState.value = 'error'
    volError.value = err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errLoadVol', { status: err.status })) : t('opsServer.card.errLoadVolPlain')
  }
}
async function doCreateVol(): Promise<void> {
  const name = newVol.value.trim()
  if (!name) return
  busyVol.value = '@create'
  try {
    const res = await createVolume(props.group.serverId, name)
    if (res.ok) {
      toast.success(t('opsServer.card.volCreated'), { detail: name })
      newVol.value = ''
      void loadVolumes()
    } else toast.error(t('opsServer.card.createFail'), { detail: res.error })
  } finally {
    busyVol.value = ''
  }
}
async function doRemoveVol(v: VolumeInfo): Promise<void> {
  const ok = await confirm.open({
    title: t('opsServer.card.removeVolTitle', { name: v.name }),
    body: t('opsServer.card.removeVolBody'),
    confirmLabel: t('opsServer.card.removeVolConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  busyVol.value = v.name
  try {
    const res = await removeVolume(props.group.serverId, v.name)
    if (res.ok) {
      toast.success(t('opsServer.card.volRemoved'), { detail: v.name })
      void loadVolumes()
    } else toast.error(t('opsServer.card.removeFail'), { detail: res.error || t('opsServer.card.volInUse') })
  } finally {
    busyVol.value = ''
  }
}

// ─── 网络(懒加载) ────────────────────────────────────────────────────────────
const networks = ref<NetworkInfo[]>([])
const netState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const netError = ref('')
const newNet = ref('')
const busyNet = ref('')

async function loadNetworks(): Promise<void> {
  netState.value = 'loading'
  netError.value = ''
  try {
    const res = await getServerNetworks(props.group.serverId)
    if (!res.reachable || !res.runtime) {
      netState.value = 'error'
      netError.value = res.error || t('opsServer.card.errUnreachableOrNoRuntime')
      return
    }
    networks.value = res.networks
    netState.value = 'loaded'
  } catch (err) {
    netState.value = 'error'
    netError.value = err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errLoadNet', { status: err.status })) : t('opsServer.card.errLoadNetPlain')
  }
}
async function doCreateNet(): Promise<void> {
  const name = newNet.value.trim()
  if (!name) return
  busyNet.value = '@create'
  try {
    const res = await createNetwork(props.group.serverId, name)
    if (res.ok) {
      toast.success(t('opsServer.card.netCreated'), { detail: name })
      newNet.value = ''
      void loadNetworks()
    } else toast.error(t('opsServer.card.createFail'), { detail: res.error })
  } finally {
    busyNet.value = ''
  }
}
async function doRemoveNet(n: NetworkInfo): Promise<void> {
  const ok = await confirm.open({
    title: t('opsServer.card.removeNetConfirmTitle', { name: n.name }),
    body: t('opsServer.card.removeNetBody'),
    confirmLabel: t('opsServer.card.removeNetConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  busyNet.value = n.id
  try {
    const res = await removeNetwork(props.group.serverId, n.name)
    if (res.ok) {
      toast.success(t('opsServer.card.netRemoved'), { detail: n.name })
      void loadNetworks()
    } else toast.error(t('opsServer.card.removeFail'), { detail: res.error || t('opsServer.card.netInUse') })
  } finally {
    busyNet.value = ''
  }
}

// 内置网络(bridge/host/none)不可删,删除按钮置灰。
const BUILTIN_NETWORKS = new Set(['bridge', 'host', 'none'])

// 网络详情展开:点网络行 → 显示连着的容器(可断开)。
const expandedNet = ref<string>('')
const netConns = ref<NetworkContainer[]>([])
const netConnState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const netConnError = ref('')
const busyConn = ref('')

async function toggleNetDetail(n: NetworkInfo): Promise<void> {
  if (expandedNet.value === n.name) {
    expandedNet.value = ''
    return
  }
  expandedNet.value = n.name
  netConnState.value = 'loading'
  netConnError.value = ''
  netConns.value = []
  try {
    const res = await getNetworkContainers(props.group.serverId, n.name)
    if (!res.reachable) {
      netConnState.value = 'error'
      netConnError.value = res.error || t('opsServer.card.errReadFail')
      return
    }
    netConns.value = res.containers
    netConnState.value = 'loaded'
  } catch (err) {
    netConnState.value = 'error'
    netConnError.value = err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReadFail')) : t('opsServer.card.errReadFail')
  }
}

// 连接容器到网络:从该机容器里选一个,docker network connect。
const connectName = ref('')
async function doConnect(network: string): Promise<void> {
  const name = connectName.value.trim()
  if (!name) return
  busyConn.value = '@connect'
  try {
    const res = await connectNetwork(props.group.serverId, network, name)
    if (res.ok) {
      toast.success(t('opsServer.card.connected'), { detail: `${name} ⇢ ${network}` })
      connectName.value = ''
      // 刷新该网络的连接列表。
      const r = await getNetworkContainers(props.group.serverId, network)
      if (r.reachable) netConns.value = r.containers
    } else toast.error(t('opsServer.card.connectFail'), { detail: res.error })
  } catch (err) {
    toast.error(t('opsServer.card.connectFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFailPlain')) : t('opsServer.card.errNetwork'),
    })
  } finally {
    busyConn.value = ''
  }
}

/** 尚未连接到该网络的本机容器(供「连接」下拉)。 */
function attachableContainers(): string[] {
  const connected = new Set(netConns.value.map((c) => c.name))
  return props.group.containers.map((c) => c.names).filter((n) => !connected.has(n))
}

async function doDisconnect(network: string, c: NetworkContainer): Promise<void> {
  const ok = await confirm.open({
    title: t('opsServer.card.disconnectConfirmTitle', { name: c.name, network }),
    body: t('opsServer.card.disconnectBody'),
    confirmLabel: t('opsServer.card.disconnectConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  busyConn.value = c.id
  try {
    const res = await disconnectNetwork(props.group.serverId, network, c.name)
    if (res.ok) {
      toast.success(t('opsServer.card.disconnected'), { detail: `${c.name} ⇠ ${network}` })
      netConns.value = netConns.value.filter((x) => x.id !== c.id)
    } else toast.error(t('opsServer.card.disconnectFail'), { detail: res.error })
  } finally {
    busyConn.value = ''
  }
}

// ─── Stacks / Compose(懒加载) ────────────────────────────────────────────────
const stacks = ref<StackInfo[]>([])
const stackState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const stackError = ref('')
const busyStack = ref('')
const showDeploy = ref(false)
const deployName = ref('')
const deployCompose = ref('')
const deploying = ref(false)

async function loadStacks(): Promise<void> {
  stackState.value = 'loading'
  stackError.value = ''
  try {
    const res = await getServerStacks(props.group.serverId)
    if (!res.reachable || !res.runtime) {
      stackState.value = 'error'
      stackError.value = res.error || t('opsServer.card.errUnreachableOrNoCompose')
      return
    }
    stacks.value = res.stacks
    stackState.value = 'loaded'
  } catch (err) {
    stackState.value = 'error'
    stackError.value =
      err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errLoadStacks', { status: err.status })) : t('opsServer.card.errLoadStacksPlain')
  }
}

const STACK_ACTIONS = computed<{ action: StackAction; label: string; danger?: boolean; title?: string }[]>(() => [
  { action: 'update', label: t('opsServer.card.actUpdate'), danger: true, title: t('opsServer.card.actUpdateTitle') },
  { action: 'start', label: t('opsServer.card.actStart') },
  { action: 'restart', label: t('opsServer.card.actRestart'), danger: true },
  { action: 'stop', label: t('opsServer.card.actStop'), danger: true },
  { action: 'down', label: t('opsServer.card.actDown'), danger: true },
])

async function doStackAction(s: StackInfo, action: StackAction, label: string, danger?: boolean): Promise<void> {
  // 「更新」不再据列表 configFiles 预拦:老 compose v1 标签恒空,后端会三级解析(受管目录/探测)
  // 兜底找 compose 文件,真定位不到才返回清晰错误。预拦会误伤可探测到文件的存量 stack。
  if (danger) {
    const ok = await confirm.open({
      title: t('opsServer.card.stackActionConfirmTitle', { name: s.name, label }),
      body:
        action === 'down'
          ? t('opsServer.card.stackDownBody')
          : action === 'update'
            ? t('opsServer.card.stackUpdateBody')
            : t('opsServer.card.stackGenericBody', { label }),
      confirmLabel: label,
      variant: 'danger',
    })
    if (!ok) return
  }
  busyStack.value = s.name
  try {
    const res = await stackAction(props.group.serverId, s.name, action, s.configFiles)
    if (res.ok) {
      toast.success(t('opsServer.card.stackActionSuccess', { label }), { detail: s.name })
      void loadStacks()
      emit('changed')
    } else {
      toast.error(t('opsServer.card.stackActionFail', { label }), { detail: res.error || t('opsServer.card.errNonZeroExit') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.stackActionFail', { label }), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    busyStack.value = ''
  }
}

// ─── Stack 详情(点「详情」展开:看服务/容器 + compose 文件,可编辑就地保存) ─────────
const detailName = ref('') // 当前展开的 stack(空 = 都收起)
const detailState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
const detailError = ref('')
const stackDetail = ref<StackDetail | null>(null)
const editCompose = ref('') // compose 编辑缓冲
const savingStack = ref(false)

async function toggleDetail(s: StackInfo): Promise<void> {
  if (detailName.value === s.name) {
    detailName.value = '' // 再点一次收起
    return
  }
  detailName.value = s.name
  stackDetail.value = null
  detailState.value = 'loading'
  detailError.value = ''
  try {
    const res = await getStackDetail(props.group.serverId, s.name)
    if (!res.reachable || !res.runtime) {
      detailState.value = 'error'
      detailError.value = res.error || t('opsServer.card.errUnreachable')
      return
    }
    stackDetail.value = res
    editCompose.value = res.compose
    detailState.value = 'loaded'
  } catch (err) {
    detailState.value = 'error'
    detailError.value =
      err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errLoadDetail', { status: err.status })) : t('opsServer.card.errLoadDetailPlain')
  }
}

async function doSaveStack(): Promise<void> {
  const d = stackDetail.value
  if (!d || !d.editable || savingStack.value) return
  const compose = editCompose.value.trim()
  if (!compose) {
    toast.error(t('opsServer.card.cannotSave'), { detail: t('opsServer.card.composeEmpty') })
    return
  }
  const ok = await confirm.open({
    title: t('opsServer.card.saveStackTitle', { name: d.name }),
    body: t('opsServer.card.saveStackBody', { path: d.configFiles }),
    confirmLabel: t('opsServer.card.saveStackConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  savingStack.value = true
  try {
    const res = await saveStackCompose(props.group.serverId, d.name, compose, d.configFiles)
    if (res.ok) {
      toast.success(t('opsServer.card.savedRedeployed'), { detail: d.name })
      void loadStacks()
      emit('changed')
    } else {
      toast.error(t('opsServer.card.saveFail'), { detail: res.error || t('opsServer.card.errNonZeroExit') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.saveFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    savingStack.value = false
  }
}

// AI 生成 compose:中文需求 → docker-compose.yml,填进部署表单。
const aiComposeNl = ref('')
const aiComposeBusy = ref(false)
async function aiGenCompose(): Promise<void> {
  const nl = aiComposeNl.value.trim()
  if (!nl || aiComposeBusy.value) return
  aiComposeBusy.value = true
  try {
    const res = await generateCompose(nl)
    if (!res.available) {
      toast.error(t('opsServer.card.aiNotConfigured'), { detail: t('opsServer.card.aiConfigHint') })
      return
    }
    if (res.yaml) {
      deployCompose.value = res.yaml
      if (!deployName.value.trim()) deployName.value = 'ai-stack'
      toast.success(t('opsServer.card.aiComposeGenerated'), { detail: t('opsServer.card.aiComposeVerify') })
    } else {
      toast.error(t('opsServer.card.genFail'), { detail: res.reason || t('opsServer.card.modelNoCompose') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.genFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFailPlain')) : t('opsServer.card.errNetwork'),
    })
  } finally {
    aiComposeBusy.value = false
  }
}

async function doDeploy(): Promise<void> {
  const name = deployName.value.trim()
  const compose = deployCompose.value.trim()
  if (!name || !compose || deploying.value) return
  deploying.value = true
  try {
    const res = await deployStack(props.group.serverId, name, compose)
    if (res.ok) {
      toast.success(t('opsServer.card.stackDeployed'), { detail: name })
      showDeploy.value = false
      deployName.value = ''
      deployCompose.value = ''
      void loadStacks()
      emit('changed')
    } else {
      toast.error(t('opsServer.card.deployFail'), { detail: res.error || t('opsServer.card.composeUpFail') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.deployFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    deploying.value = false
  }
}

async function doPull(): Promise<void> {
  const ref = pullRef.value.trim()
  if (!ref || pulling.value) return
  pulling.value = true
  try {
    const res = await pullImage(props.group.serverId, ref)
    if (res.ok) {
      toast.success(t('opsServer.card.imagePulled'), { detail: ref })
      pullRef.value = ''
      void loadImages()
    } else {
      toast.error(t('opsServer.card.pullFail'), { detail: res.error || t('opsServer.card.pullCheck') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.pullFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    pulling.value = false
  }
}

async function doRemoveImage(img: ImageInfo): Promise<void> {
  const tagStr = repoTag(img)
  const ok = await confirm.open({
    title: t('opsServer.card.removeImageConfirmTitle', { tag: tagStr }),
    body: t('opsServer.card.removeImageBody'),
    confirmLabel: t('opsServer.card.removeImageConfirm'),
    variant: 'danger',
  })
  if (!ok) return
  busyImage.value = img.id
  try {
    const res = await removeImage(props.group.serverId, img.id, false)
    if (res.ok) {
      toast.success(t('opsServer.card.imageRemoved'), { detail: tagStr })
      void loadImages()
    } else {
      toast.error(t('opsServer.card.removeFail'), { detail: res.error || t('opsServer.card.imageInUse') })
    }
  } catch (err) {
    toast.error(t('opsServer.card.removeFail'), {
      detail: err instanceof HttpError ? (err.apiError?.message ?? t('opsServer.card.errReqFail', { status: err.status })) : t('opsServer.card.errNetwork'),
    })
  } finally {
    busyImage.value = ''
  }
}
</script>

<template>
  <article class="panel">
    <header class="panel__head">
      <div class="panel__id">
        <h2 class="panel__name">{{ name }}</h2>
        <span class="panel__host mono">{{ host }}</span>
      </div>
      <div class="panel__meta">
        <span
          class="rt-badge"
          :class="{
            'rt-badge--ok': group.reachable && group.runtime,
            'rt-badge--warn': group.reachable && !group.runtime,
            'rt-badge--down': !group.reachable,
          }"
        >
          <NIcon :size="15" class="rt-badge__icon">
            <BrandDocker v-if="group.reachable && group.runtime" />
            <AlertTriangle v-else />
          </NIcon>
          {{ !group.reachable ? t('opsServer.card.unreachable') : group.runtime ? runtimeLabel : t('opsServer.card.noRuntime') }}
        </span>
        <span v-if="group.reachable && group.runtime" class="panel__count mono">
          {{ t('opsServer.card.runningCount', { running: group.running, total: group.total }) }}
        </span>
      </div>
    </header>

    <!-- 不可达 / 无运行时:行内提示 -->
    <p v-if="!group.reachable" class="panel__hint panel__hint--down">⚠ {{ group.error }}</p>
    <p v-else-if="!group.runtime" class="panel__hint">{{ group.error }}</p>

    <template v-else>
      <!-- 卡片内 tab:容器 / 镜像 -->
      <div class="card-tabs" role="tablist">
        <button class="ctab" :class="{ 'ctab--active': tab === 'containers' }" role="tab" @click="switchTab('containers')">
          {{ t('opsServer.card.tabContainers') }} <span class="ctab__n">{{ group.total }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'images' }" role="tab" @click="switchTab('images')">
          {{ t('opsServer.card.tabImages') }} <span v-if="imgState === 'loaded'" class="ctab__n">{{ images.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'stacks' }" role="tab" @click="switchTab('stacks')">
          {{ t('opsServer.card.tabStacks') }} <span v-if="stackState === 'loaded'" class="ctab__n">{{ stacks.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'volumes' }" role="tab" @click="switchTab('volumes')">
          {{ t('opsServer.card.tabVolumes') }} <span v-if="volState === 'loaded'" class="ctab__n">{{ volumes.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'networks' }" role="tab" @click="switchTab('networks')">
          {{ t('opsServer.card.tabNetworks') }} <span v-if="netState === 'loaded'" class="ctab__n">{{ networks.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'domains' }" role="tab" @click="switchTab('domains')">
          {{ t('opsServer.card.tabDomains') }} <span v-if="domainCount >= 0" class="ctab__n">{{ domainCount }}</span>
        </button>
      </div>

      <!-- 容器 tab -->
      <template v-if="tab === 'containers'">
        <div v-if="group.total > 0" class="cstats-bar">
          <span class="cstats-bar__label">{{ t('opsServer.card.realtimeStats') }}</span>
          <button class="op op--ghost" :disabled="statsLoading" :title="t('opsServer.card.refreshStatsTitle')" @click="loadStats">
            {{ statsLoading ? t('opsServer.card.sampling') : t('common.refresh') }}
          </button>
        </div>
        <p v-if="group.total === 0" class="panel__hint">{{ t('opsServer.card.noContainers') }}</p>
        <p v-else-if="visibleContainers.length === 0" class="panel__hint">{{ emptyContainersHint }}</p>
        <ul v-else class="clist" role="list">
          <li v-for="c in visibleContainers" :key="c.id" class="crow" :class="{ 'crow--busy': rowBusy(c.id), 'crow--bulk': bulkMode }">
            <label v-if="bulkMode" class="crow__check">
              <input
                type="checkbox"
                :checked="isSelected(c.names)"
                :aria-label="t('opsServer.card.selectContainerAria', { name: c.names })"
                @change="emit('toggle-select', c.names)"
              />
            </label>
            <div class="crow__state">
              <span class="dot" :class="[`dot--${stateMeta(c.state).tone}`, { 'dot--pulse': stateMeta(c.state).pulse }]" aria-hidden="true" />
              <span class="crow__statelabel" :class="`txt--${stateMeta(c.state).tone}`">{{ stateMeta(c.state).label }}</span>
            </div>
            <div class="crow__main">
              <div class="crow__name" :title="c.names">{{ c.names }}</div>
              <div class="crow__sub mono" :title="`${c.image} · ${c.id}`">{{ c.image }} · {{ shortId(c.id) }}</div>
            </div>
            <div class="crow__status">
              <div class="crow__statustxt" :title="c.status">{{ c.status }}</div>
              <div v-if="c.ports" class="crow__ports mono" :title="c.ports">{{ c.ports }}</div>
              <div
                v-if="c.state === 'running' && statFor(c.names)"
                class="crow__stats mono"
                :title="t('opsServer.card.statTitle', { net: statFor(c.names)!.netIO, block: statFor(c.names)!.blockIO })"
              >
                {{ t('opsServer.card.statLine', { cpu: statFor(c.names)!.cpuPerc, mem: statFor(c.names)!.memUsage, memPerc: statFor(c.names)!.memPerc }) }}
              </div>
            </div>
            <div class="crow__actions">
              <button
                v-for="a in actionsFor(c.state)"
                :key="a.action"
                class="op"
                :class="[`op--${a.variant}`, { 'op--busy': isBusy(c.id, a.action) }]"
                :disabled="rowBusy(c.id)"
                :title="ACTION_HINTS[a.action]"
                @click="runAction(c, a)"
              >
                {{ a.label }}
              </button>
              <button class="op op--ghost" :title="t('opsServer.card.inspectTitle')" @click="emit('inspect', c)">{{ t('opsServer.card.detail') }}</button>
              <button class="op op--ai" :title="t('opsServer.card.aiDiagnoseTitle')" @click="emit('diagnose', c)">{{ t('opsServer.card.ai') }}</button>
              <button class="op op--ghost" :title="t('opsServer.card.logsTitle')" @click="emit('logs', c)">{{ t('opsServer.card.logs') }}</button>
              <button class="op op--ghost" :disabled="rowBusy(c.id)" :title="t('opsServer.card.terminalTitle')" @click="emit('terminal', c)">{{ t('opsServer.card.terminal') }}</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 镜像 tab -->
      <template v-else-if="tab === 'images'">
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="pullRef" class="pull__in mono" :placeholder="t('opsServer.card.pullPlaceholder')" @keyup.enter="doPull" />
            <button class="pull__btn" :disabled="pulling || !pullRef.trim()" @click="doPull">
              {{ pulling ? t('opsServer.card.pulling') : t('opsServer.card.pull') }}
            </button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="imgState === 'loading'" @click="loadImages">{{ t('opsServer.card.refresh') }}</button>
        </div>
        <p v-if="imgState === 'error'" class="panel__hint panel__hint--down">⚠ {{ imgError }}</p>
        <p v-else-if="imgState === 'loading' && images.length === 0" class="panel__hint">{{ t('opsServer.card.loadingImages') }}</p>
        <p v-else-if="images.length === 0" class="panel__hint">{{ t('opsServer.card.noImages') }}</p>
        <ul v-else class="ilist" role="list">
          <li v-for="img in images" :key="img.id + repoTag(img)" class="irow" :class="{ 'irow--busy': busyImage === img.id }">
            <div class="irow__main">
              <div class="irow__tag" :title="repoTag(img)">{{ repoTag(img) }}</div>
              <div class="irow__id mono">{{ img.id }}</div>
            </div>
            <div class="irow__size mono">{{ img.size }}</div>
            <div class="irow__age">{{ img.createdSince }}</div>
            <div class="irow__act">
              <button class="op op--ghost" :disabled="busyImage === img.id" :title="t('opsServer.card.removeImageTitle')" @click="doRemoveImage(img)">{{ t('opsServer.card.remove') }}</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- Stacks tab -->
      <template v-else-if="tab === 'stacks'">
        <div class="img-toolbar">
          <button class="pull__btn" @click="showDeploy = !showDeploy">{{ showDeploy ? t('opsServer.card.collapseDeploy') : t('opsServer.card.deployStack') }}</button>
          <span class="grow" />
          <button class="refresh" :disabled="stackState === 'loading'" @click="loadStacks">{{ t('opsServer.card.refresh') }}</button>
        </div>

        <!-- 部署表单(贴 compose yaml) -->
        <div v-if="showDeploy" class="deploy">
          <div class="deploy__ai">
            <span class="deploy__spark">✦</span>
            <input
              v-model="aiComposeNl"
              class="deploy__ainl mono"
              :placeholder="t('opsServer.card.aiComposePlaceholder')"
              @keyup.enter="aiGenCompose"
            />
            <button class="pull__btn" :disabled="aiComposeBusy || !aiComposeNl.trim()" @click="aiGenCompose">
              {{ aiComposeBusy ? t('opsServer.card.aiGenerating') : t('opsServer.card.aiGenerate') }}
            </button>
          </div>
          <input v-model="deployName" class="deploy__name mono" :placeholder="t('opsServer.card.deployNamePlaceholder')" />
          <textarea
            v-model="deployCompose"
            class="deploy__yaml mono"
            rows="8"
            :placeholder="t('opsServer.card.deployYamlPlaceholder')"
          />
          <div class="deploy__act">
            <span class="deploy__hint">{{ t('opsServer.card.deployHint') }}</span>
            <button class="pull__btn" :disabled="deploying || !deployName.trim() || !deployCompose.trim()" @click="doDeploy">
              {{ deploying ? t('opsServer.card.deploying') : t('opsServer.card.deploy') }}
            </button>
          </div>
        </div>

        <p v-if="stackState === 'error'" class="panel__hint panel__hint--down">⚠ {{ stackError }}</p>
        <p v-else-if="stackState === 'loading' && stacks.length === 0" class="panel__hint">{{ t('opsServer.card.loadingStacks') }}</p>
        <p v-else-if="stacks.length === 0" class="panel__hint">{{ t('opsServer.card.noStacks') }}</p>
        <ul v-else class="ilist" role="list">
          <li
            v-for="s in stacks"
            :key="s.name"
            class="srow srow--stack"
            :class="{ 'irow--busy': busyStack === s.name, 'srow--open': detailName === s.name }"
          >
            <div class="srow__head">
              <button
                class="srow__toggle"
                :title="detailName === s.name ? t('opsServer.card.collapseDetailTitle') : t('opsServer.card.viewServicesTitle')"
                @click="toggleDetail(s)"
              >
                <span class="srow__caret" :class="{ 'srow__caret--open': detailName === s.name }">▸</span>
                <span class="srow__name">{{ s.name }}</span>
              </button>
              <div class="srow__status mono">{{ s.status }}</div>
              <div class="srow__act">
                <button
                  v-for="a in STACK_ACTIONS"
                  :key="a.action"
                  class="op"
                  :class="{ 'op--default': a.action === 'update' }"
                  :disabled="busyStack === s.name"
                  :title="a.title"
                  @click="doStackAction(s, a.action, a.label, a.danger)"
                >
                  {{ a.label }}
                </button>
              </div>
            </div>

            <!-- 展开:服务/容器 + compose -->
            <div v-if="detailName === s.name" class="sdetail">
              <p v-if="detailState === 'loading'" class="panel__hint">{{ t('opsServer.card.loadingDetail') }}</p>
              <p v-else-if="detailState === 'error'" class="panel__hint panel__hint--down">⚠ {{ detailError }}</p>
              <template v-else-if="detailState === 'loaded' && stackDetail">
                <div class="sdetail__sub">{{ t('opsServer.card.servicesSub', { running: stackDetail.running, total: stackDetail.total }) }}</div>
                <ul class="svc" role="list">
                  <li v-for="svc in stackDetail.services" :key="svc.name" class="svc__row">
                    <span class="svc__dot" :class="`svc__dot--${svc.state}`" :title="svc.state" />
                    <span class="svc__name mono">{{ svc.service || svc.name }}</span>
                    <span class="svc__img mono" :title="svc.image">{{ svc.image }}</span>
                    <span class="svc__ports mono" :title="svc.ports">{{ svc.ports || '—' }}</span>
                    <span class="svc__status mono">{{ svc.status }}</span>
                  </li>
                </ul>

                <div class="sdetail__sub sdetail__sub--gap">
                  {{ t('opsServer.card.composeFile') }}
                  <span v-if="stackDetail.composeSource === 'file'" class="sdetail__path mono" :title="stackDetail.configFiles">
                    · {{ stackDetail.configFiles }}
                  </span>
                </div>
                <template v-if="stackDetail.composeSource === 'file'">
                  <textarea v-model="editCompose" class="deploy__yaml mono" rows="12" spellcheck="false" />
                  <div class="deploy__act">
                    <span class="deploy__hint">
                      {{ stackDetail.editable ? t('opsServer.card.saveWritebackHint') : t('opsServer.card.multiFileReadonlyHint') }}
                    </span>
                    <button
                      v-if="stackDetail.editable"
                      class="pull__btn"
                      :disabled="savingStack || !editCompose.trim()"
                      @click="doSaveStack"
                    >
                      {{ savingStack ? t('opsServer.card.saving') : t('opsServer.card.saveRedeploy') }}
                    </button>
                  </div>
                </template>
                <p v-else class="panel__hint">
                  {{ t('opsServer.card.externalStackHint', { name: stackDetail.name }) }}
                </p>
              </template>
            </div>
          </li>
        </ul>
      </template>

      <!-- 卷 tab -->
      <template v-else-if="tab === 'volumes'">
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="newVol" class="pull__in mono" :placeholder="t('opsServer.card.newVolPlaceholder')" @keyup.enter="doCreateVol" />
            <button class="pull__btn" :disabled="busyVol === '@create' || !newVol.trim()" @click="doCreateVol">{{ t('opsServer.card.create') }}</button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="volState === 'loading'" @click="loadVolumes">{{ t('opsServer.card.refresh') }}</button>
        </div>
        <p v-if="volState === 'error'" class="panel__hint panel__hint--down">⚠ {{ volError }}</p>
        <p v-else-if="volState === 'loading' && volumes.length === 0" class="panel__hint">{{ t('opsServer.card.loadingVolumes') }}</p>
        <p v-else-if="volumes.length === 0" class="panel__hint">{{ t('opsServer.card.noVolumes') }}</p>
        <ul v-else class="ilist" role="list">
          <li v-for="v in volumes" :key="v.name" class="srow" :class="{ 'irow--busy': busyVol === v.name }">
            <div class="srow__main">
              <div class="srow__name">{{ v.name }}</div>
            </div>
            <div class="srow__status mono">{{ v.driver }}</div>
            <div class="srow__act">
              <button class="op op--ghost" :disabled="busyVol === v.name" @click="doRemoveVol(v)">{{ t('opsServer.card.remove') }}</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 网络 tab -->
      <template v-else-if="tab === 'networks'">
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="newNet" class="pull__in mono" :placeholder="t('opsServer.card.newNetPlaceholder')" @keyup.enter="doCreateNet" />
            <button class="pull__btn" :disabled="busyNet === '@create' || !newNet.trim()" @click="doCreateNet">{{ t('opsServer.card.create') }}</button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="netState === 'loading'" @click="loadNetworks">{{ t('opsServer.card.refresh') }}</button>
        </div>
        <p v-if="netState === 'error'" class="panel__hint panel__hint--down">⚠ {{ netError }}</p>
        <p v-else-if="netState === 'loading' && networks.length === 0" class="panel__hint">{{ t('opsServer.card.loadingNetworks') }}</p>
        <p v-else-if="networks.length === 0" class="panel__hint">{{ t('opsServer.card.noNetworks') }}</p>
        <ul v-else class="ilist" role="list">
          <li v-for="n in networks" :key="n.id">
            <div class="srow" :class="{ 'irow--busy': busyNet === n.id }">
              <button class="srow__main netexp" @click="toggleNetDetail(n)">
                <span class="netexp__caret" :class="{ 'netexp__caret--open': expandedNet === n.name }">▸</span>
                <span>
                  <span class="srow__name">{{ n.name }}</span>
                  <span class="srow__cfg mono">{{ n.id }} · {{ n.scope }}</span>
                </span>
              </button>
              <div class="srow__status mono">{{ n.driver }}</div>
              <div class="srow__act">
                <button
                  class="op op--ghost"
                  :disabled="busyNet === n.id || BUILTIN_NETWORKS.has(n.name)"
                  :title="BUILTIN_NETWORKS.has(n.name) ? t('opsServer.card.builtinNetUndeletable') : t('opsServer.card.removeNetTitle')"
                  @click="doRemoveNet(n)"
                >
                  {{ t('opsServer.card.remove') }}
                </button>
              </div>
            </div>
            <!-- 展开:该网络上连着的容器 -->
            <div v-if="expandedNet === n.name" class="netdetail">
              <p v-if="netConnState === 'loading'" class="netdetail__hint">{{ t('opsServer.card.loadingConns') }}</p>
              <p v-else-if="netConnState === 'error'" class="netdetail__hint netdetail__hint--err">⚠ {{ netConnError }}</p>
              <p v-else-if="netConns.length === 0" class="netdetail__hint">{{ t('opsServer.card.noConns') }}</p>
              <ul v-else class="connlist" role="list">
                <li v-for="c in netConns" :key="c.id" class="connrow" :class="{ 'irow--busy': busyConn === c.id }">
                  <span class="connrow__name">{{ c.name }}</span>
                  <span class="connrow__ip mono">{{ c.ipv4 || '—' }}</span>
                  <button class="op op--ghost" :disabled="busyConn === c.id" @click="doDisconnect(n.name, c)">{{ t('opsServer.card.disconnect') }}</button>
                </li>
              </ul>
              <!-- 连接容器到该网络 -->
              <div v-if="netConnState === 'loaded'" class="connadd">
                <select v-model="connectName" class="connadd__sel" :disabled="attachableContainers().length === 0">
                  <option value="">{{ attachableContainers().length ? t('opsServer.card.selectAttachable') : t('opsServer.card.noAttachable') }}</option>
                  <option v-for="name in attachableContainers()" :key="name" :value="name">{{ name }}</option>
                </select>
                <button class="op op--default" :disabled="!connectName || busyConn === '@connect'" @click="doConnect(n.name)">
                  {{ t('opsServer.card.connect') }}
                </button>
              </div>
            </div>
          </li>
        </ul>
      </template>

      <!-- 域名 / 反向代理 tab(R1 · 自动 HTTPS) -->
      <template v-else-if="tab === 'domains'">
        <ReverseProxyPanel
          v-if="domainsMounted"
          :server-id="group.serverId"
          :host="host"
          :containers="group.containers"
          @count="(n) => (domainCount = n)"
        />
      </template>
    </template>
  </article>
</template>

<style scoped>
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

/* 卡片内 tab */
.card-tabs {
  display: flex;
  gap: 2px;
  padding: 0 12px;
  border-bottom: 1px solid var(--color-border);
}
.ctab {
  padding: 9px 14px;
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-faint);
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-out-expo);
}
.ctab:hover {
  color: var(--color-dim);
}
.ctab--active {
  color: var(--color-text);
  border-bottom-color: var(--color-primary);
}
.ctab__n {
  font-size: var(--text-micro);
  color: var(--color-faint);
  font-variant-numeric: tabular-nums;
}

/* 容器行 */
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
.crow--bulk {
  grid-template-columns: 26px 92px minmax(0, 1.4fr) minmax(0, 1.3fr) auto;
}
.crow__check {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
}
.crow__check input {
  width: 16px;
  height: 16px;
  cursor: pointer;
  accent-color: var(--color-primary);
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
.dot--pulse { animation: dotPulse 1.8s var(--ease-out-expo) infinite; }
@keyframes dotPulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.45; transform: scale(0.8); }
}
.txt--green { color: var(--color-green); }
.txt--amber { color: var(--color-amber); }
.txt--cyan { color: var(--color-cyan); }
.txt--red { color: var(--color-red); }
.txt--faint { color: var(--color-faint); }
.crow__main { min-width: 0; }
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
.crow__status { min-width: 0; }
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
.crow__stats {
  margin-top: 2px;
  font-size: var(--text-micro);
  color: var(--color-accent);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cstats-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 18px;
  border-bottom: 1px solid var(--color-border);
}
.cstats-bar__label {
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.crow__actions {
  display: flex;
  align-items: center;
  gap: 6px;
  justify-content: flex-end;
  flex-wrap: wrap;
}

/* 镜像 tab */
.img-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  flex-wrap: wrap;
}
.pull {
  display: inline-flex;
  gap: 6px;
}
.pull__in {
  width: 260px;
  max-width: 50vw;
  font-size: var(--text-label);
  padding: 6px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-inset);
  color: var(--color-text);
}
.pull__in:focus {
  outline: none;
  border-color: var(--color-primary);
}
.pull__btn {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  cursor: pointer;
}
.pull__btn:hover:not(:disabled) { background: var(--color-primary-press); }
.refresh {
  font-size: var(--text-label);
  font-weight: 600;
  padding: 6px 14px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
}
.refresh:hover:not(:disabled) { color: var(--color-text); border-color: var(--color-text); }
.pull__btn:disabled, .refresh:disabled { opacity: 0.5; cursor: not-allowed; }
.grow { flex: 1; }

.ilist {
  list-style: none;
  margin: 0;
  padding: 0;
}
.irow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 100px 130px auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast) var(--ease-out-expo);
}
.irow:last-child { border-bottom: none; }
.irow:hover { background: var(--color-card-2); }
.irow--busy { opacity: 0.55; }
.irow__main { min-width: 0; }
.irow__tag {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.irow__id { font-size: var(--text-micro); color: var(--color-faint); }
.irow__size { font-size: var(--text-label); color: var(--color-dim); font-variant-numeric: tabular-nums; }
.irow__age { font-size: var(--text-label); color: var(--color-faint); }
.irow__act { display: flex; justify-content: flex-end; }

/* 部署表单 + stack 行 */
.deploy {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-inset);
}
.deploy__ai {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border: 1px solid var(--color-primary-soft);
  background: var(--color-primary-soft);
  border-radius: var(--rounded-sm);
}
.deploy__spark {
  color: var(--color-primary);
  font-weight: 700;
}
.deploy__ainl {
  flex: 1;
  font-size: var(--text-label);
  padding: 6px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
}
.deploy__ainl:focus {
  outline: none;
  border-color: var(--color-primary);
}
.deploy__name {
  font-size: var(--text-label);
  padding: 7px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
}
.deploy__yaml {
  width: 100%;
  box-sizing: border-box;
  font-size: var(--text-mono);
  line-height: 1.5;
  padding: 10px 12px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-term);
  color: oklch(88% 0.01 250);
  resize: vertical;
}
.deploy__name:focus,
.deploy__yaml:focus {
  outline: none;
  border-color: var(--color-primary);
}
.deploy__act {
  display: flex;
  align-items: center;
  gap: 12px;
}
.deploy__hint {
  flex: 1;
  font-size: var(--text-micro);
  color: var(--color-faint);
}

.srow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--color-border);
  transition: background var(--duration-fast) var(--ease-out-expo);
}
.srow:last-child { border-bottom: none; }
.srow:hover { background: var(--color-card-2); }
.srow__main { min-width: 0; }
.srow__name {
  font-size: var(--text-body);
  font-weight: 600;
  color: var(--color-text);
}
.srow__cfg {
  font-size: var(--text-micro);
  color: var(--color-faint);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.srow__status {
  font-size: var(--text-label);
  color: var(--color-dim);
}
.srow__act {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
  flex-wrap: wrap;
}

/* Stack 行:可展开,head 是原来的三列网格,展开内容垂直堆在下方 */
.srow--stack {
  display: block;
  padding: 0;
}
.srow--stack:hover { background: transparent; }
.srow--open { background: var(--color-card-2); }
.srow__head {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px auto;
  align-items: center;
  gap: 14px;
  padding: 12px 18px;
}
.srow__toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  text-align: left;
}
.srow__caret {
  color: var(--color-faint);
  font-size: 11px;
  transition: transform var(--duration-fast) var(--ease-out-expo);
}
.srow__caret--open { transform: rotate(90deg); color: var(--color-accent); }
.srow__toggle:hover .srow__name { color: var(--color-accent); }

.sdetail {
  padding: 4px 18px 16px 38px;
  border-top: 1px dashed var(--color-border);
}
.sdetail__sub {
  font-size: var(--text-micro);
  font-weight: 600;
  color: var(--color-dim);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  margin: 10px 0 8px;
}
.sdetail__sub--gap { margin-top: 18px; }
.sdetail__path {
  font-weight: 400;
  text-transform: none;
  letter-spacing: 0;
  color: var(--color-faint);
}
.svc { display: flex; flex-direction: column; gap: 2px; }
.svc__row {
  display: grid;
  grid-template-columns: 14px minmax(80px, 1.1fr) minmax(0, 1.4fr) minmax(0, 1.2fr) minmax(0, 1.3fr);
  align-items: center;
  gap: 10px;
  padding: 5px 8px;
  border-radius: 6px;
  font-size: var(--text-micro);
}
.svc__row:hover { background: var(--color-card); }
.svc__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-faint);
}
.svc__dot--running { background: #16a34a; }
.svc__dot--exited, .svc__dot--dead { background: #9ca3af; }
.svc__dot--paused { background: #d97706; }
.svc__dot--restarting { background: #2563eb; }
.svc__dot--created { background: #6366f1; }
.svc__name { font-weight: 600; color: var(--color-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.svc__img, .svc__ports, .svc__status {
  color: var(--color-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 网络展开:连接的容器 */
.netexp {
  display: flex;
  align-items: center;
  gap: 8px;
  background: transparent;
  border: none;
  padding: 0;
  cursor: pointer;
  text-align: left;
  min-width: 0;
}
.netexp__caret {
  color: var(--color-faint);
  font-size: var(--text-micro);
  transition: transform var(--duration-fast) var(--ease-out-expo);
}
.netexp__caret--open {
  transform: rotate(90deg);
}
.netdetail {
  padding: 4px 18px 12px 40px;
  background: var(--color-card-2);
  border-bottom: 1px solid var(--color-border);
}
.netdetail__hint {
  margin: 0;
  padding: 8px 0;
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.netdetail__hint--err {
  color: var(--color-red);
}
.connlist {
  list-style: none;
  margin: 0;
  padding: 0;
}
.connrow {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 0;
}
.connrow__name {
  font-size: var(--text-label);
  font-weight: 600;
  color: var(--color-text);
  min-width: 0;
}
.connrow__ip {
  flex: 1;
  font-size: var(--text-micro);
  color: var(--color-faint);
}
.connadd {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-top: 8px;
  margin-top: 4px;
  border-top: 1px dashed var(--color-border);
}
.connadd__sel {
  font-size: var(--text-micro);
  padding: 5px 8px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
  max-width: 240px;
}

/* 行内迷你操作按钮 */
.op {
  font-size: var(--text-micro);
  font-weight: 600;
  padding: 4px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-dim);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out-expo);
}
.op:hover:not(:disabled) { color: var(--color-text); border-color: var(--color-text); }
.op--default { color: var(--color-primary); border-color: var(--color-primary-soft); }
.op--default:hover:not(:disabled) { color: #fff; background: var(--color-primary); border-color: var(--color-primary); }
.op--ai { color: var(--color-primary); border-color: var(--color-primary-soft); }
.op--ai:hover:not(:disabled) { color: #fff; background: var(--color-primary); border-color: var(--color-primary); }
.op:disabled { opacity: 0.45; cursor: not-allowed; }
.op--busy { opacity: 0.6; pointer-events: none; }

@media (max-width: 720px) {
  .crow, .crow--bulk, .irow { grid-template-columns: 1fr; gap: 8px; }
  .crow__actions { justify-content: flex-start; }
}
</style>

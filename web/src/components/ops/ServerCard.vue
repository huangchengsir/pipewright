<script setup lang="ts">
/*
  ServerCard.vue — 单台服务器的容器管理卡片。卡片内部按「容器 / 镜像」分 tab,
  tab 内容只针对这一台(Portainer 式按主机管理)。容器 tab:生命周期操作 + 日志 + 终端;
  镜像 tab:列表 + 拉取 + 删除。生命周期/镜像写操作完成后 emit('changed') 让父刷新聚合。
*/
import { ref, computed } from 'vue'
import {
  getServerImages,
  pullImage,
  removeImage,
  getServerStacks,
  deployStack,
  stackAction,
  getServerVolumes,
  createVolume,
  removeVolume,
  getServerNetworks,
  createNetwork,
  removeNetwork,
  getNetworkContainers,
  disconnectNetwork,
  type ServerContainers,
  type ContainerInfo,
  type ImageInfo,
  type StackInfo,
  type StackAction,
  type VolumeInfo,
  type NetworkInfo,
  type NetworkContainer,
} from '../../api/containers'
import { serviceAction } from '../../api/servers'
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
}>()
const emit = defineEmits<{
  (e: 'changed'): void
  (e: 'logs', c: ContainerInfo): void
  (e: 'terminal', c: ContainerInfo): void
  (e: 'diagnose', c: ContainerInfo): void
}>()

const toast = useToast()
const confirm = useConfirm()

type TabKey = 'containers' | 'images' | 'stacks' | 'volumes' | 'networks'
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
  if (props.stateFilter === 'all') return props.group.containers
  return props.group.containers.filter((c) => stateBucket(c.state) === props.stateFilter)
})

async function runAction(c: ContainerInfo, spec: ActionSpec): Promise<void> {
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
  const key = `${c.id}:${spec.action}`
  busy.value = new Set(busy.value).add(key)
  try {
    const res = await serviceAction(props.group.serverId, { type: 'docker', target: c.names, action: spec.action })
    if (res.ok) toast.success(`${spec.label}成功`, { detail: `${c.names} · ${props.name}` })
    else toast.error(`${spec.label}失败`, { detail: res.error || '远端命令以非零状态退出' })
  } catch (err) {
    toast.error(`${spec.label}失败`, {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    const next = new Set(busy.value)
    next.delete(key)
    busy.value = next
    setTimeout(() => emit('changed'), 600)
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
      imgError.value = res.error || '该服务器不可达或无容器运行时'
      return
    }
    images.value = res.images
    imgState.value = 'loaded'
  } catch (err) {
    imgState.value = 'error'
    imgError.value =
      err instanceof HttpError ? (err.apiError?.message ?? `加载镜像失败(${err.status})`) : '加载镜像失败'
  }
}

function switchTab(t: TabKey): void {
  tab.value = t
  if (t === 'images' && imgState.value === 'idle') void loadImages()
  if (t === 'stacks' && stackState.value === 'idle') void loadStacks()
  if (t === 'volumes' && volState.value === 'idle') void loadVolumes()
  if (t === 'networks' && netState.value === 'idle') void loadNetworks()
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
      volError.value = res.error || '该服务器不可达或无容器运行时'
      return
    }
    volumes.value = res.volumes
    volState.value = 'loaded'
  } catch (err) {
    volState.value = 'error'
    volError.value = err instanceof HttpError ? (err.apiError?.message ?? `加载卷失败(${err.status})`) : '加载卷失败'
  }
}
async function doCreateVol(): Promise<void> {
  const name = newVol.value.trim()
  if (!name) return
  busyVol.value = '@create'
  try {
    const res = await createVolume(props.group.serverId, name)
    if (res.ok) {
      toast.success('数据卷已创建', { detail: name })
      newVol.value = ''
      void loadVolumes()
    } else toast.error('创建失败', { detail: res.error })
  } finally {
    busyVol.value = ''
  }
}
async function doRemoveVol(v: VolumeInfo): Promise<void> {
  const ok = await confirm.open({
    title: `删除数据卷 ${v.name}?`,
    body: '将执行 docker volume rm。若有容器在用会删除失败。卷内数据随之删除,不可恢复。',
    confirmLabel: '删除卷',
    variant: 'danger',
  })
  if (!ok) return
  busyVol.value = v.name
  try {
    const res = await removeVolume(props.group.serverId, v.name)
    if (res.ok) {
      toast.success('数据卷已删除', { detail: v.name })
      void loadVolumes()
    } else toast.error('删除失败', { detail: res.error || '卷可能正被容器使用' })
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
      netError.value = res.error || '该服务器不可达或无容器运行时'
      return
    }
    networks.value = res.networks
    netState.value = 'loaded'
  } catch (err) {
    netState.value = 'error'
    netError.value = err instanceof HttpError ? (err.apiError?.message ?? `加载网络失败(${err.status})`) : '加载网络失败'
  }
}
async function doCreateNet(): Promise<void> {
  const name = newNet.value.trim()
  if (!name) return
  busyNet.value = '@create'
  try {
    const res = await createNetwork(props.group.serverId, name)
    if (res.ok) {
      toast.success('网络已创建', { detail: name })
      newNet.value = ''
      void loadNetworks()
    } else toast.error('创建失败', { detail: res.error })
  } finally {
    busyNet.value = ''
  }
}
async function doRemoveNet(n: NetworkInfo): Promise<void> {
  const ok = await confirm.open({
    title: `删除网络 ${n.name}?`,
    body: '将执行 docker network rm。若有容器连接在该网络上会删除失败。',
    confirmLabel: '删除网络',
    variant: 'danger',
  })
  if (!ok) return
  busyNet.value = n.id
  try {
    const res = await removeNetwork(props.group.serverId, n.name)
    if (res.ok) {
      toast.success('网络已删除', { detail: n.name })
      void loadNetworks()
    } else toast.error('删除失败', { detail: res.error || '网络可能有容器连接' })
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
      netConnError.value = res.error || '读取失败'
      return
    }
    netConns.value = res.containers
    netConnState.value = 'loaded'
  } catch (err) {
    netConnState.value = 'error'
    netConnError.value = err instanceof HttpError ? (err.apiError?.message ?? '读取失败') : '读取失败'
  }
}

async function doDisconnect(network: string, c: NetworkContainer): Promise<void> {
  const ok = await confirm.open({
    title: `把容器 ${c.name} 从网络 ${network} 断开?`,
    body: '将执行 docker network disconnect。容器会失去该网络的连接(可能影响服务互通)。',
    confirmLabel: '断开',
    variant: 'danger',
  })
  if (!ok) return
  busyConn.value = c.id
  try {
    const res = await disconnectNetwork(props.group.serverId, network, c.name)
    if (res.ok) {
      toast.success('已断开', { detail: `${c.name} ⇠ ${network}` })
      netConns.value = netConns.value.filter((x) => x.id !== c.id)
    } else toast.error('断开失败', { detail: res.error })
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
      stackError.value = res.error || '该服务器不可达或无 docker compose'
      return
    }
    stacks.value = res.stacks
    stackState.value = 'loaded'
  } catch (err) {
    stackState.value = 'error'
    stackError.value =
      err instanceof HttpError ? (err.apiError?.message ?? `加载 Stacks 失败(${err.status})`) : '加载 Stacks 失败'
  }
}

const STACK_ACTIONS: { action: StackAction; label: string; danger?: boolean; title?: string }[] = [
  { action: 'update', label: '更新', danger: true, title: 'docker compose up -d --pull always:拉取新镜像 + 重建有变化的服务(升级到最新)' },
  { action: 'start', label: '启动' },
  { action: 'restart', label: '重启', danger: true },
  { action: 'stop', label: '停止', danger: true },
  { action: 'down', label: '销毁', danger: true },
]

async function doStackAction(s: StackInfo, action: StackAction, label: string, danger?: boolean): Promise<void> {
  if (action === 'update' && !s.configFiles) {
    toast.error('无法更新', { detail: '该 Stack 无可用 compose 配置文件' })
    return
  }
  if (danger) {
    const ok = await confirm.open({
      title: `对 Stack ${s.name} 执行${label}?`,
      body:
        action === 'down'
          ? '将销毁该 compose 项目的所有容器与网络(数据卷默认保留)。'
          : action === 'update'
            ? '将按现有 compose 文件拉取新镜像并重建有变化的服务(docker compose up -d --pull always),实现升级。'
            : `将对该 compose 项目的所有服务执行${label}。`,
      confirmLabel: label,
      variant: 'danger',
    })
    if (!ok) return
  }
  busyStack.value = s.name
  try {
    const res = await stackAction(props.group.serverId, s.name, action, s.configFiles)
    if (res.ok) {
      toast.success(`Stack ${label}成功`, { detail: s.name })
      void loadStacks()
      emit('changed')
    } else {
      toast.error(`Stack ${label}失败`, { detail: res.error || '远端命令以非零状态退出' })
    }
  } catch (err) {
    toast.error(`Stack ${label}失败`, {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    busyStack.value = ''
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
      toast.success('Stack 已部署', { detail: name })
      showDeploy.value = false
      deployName.value = ''
      deployCompose.value = ''
      void loadStacks()
      emit('changed')
    } else {
      toast.error('部署失败', { detail: res.error || 'docker compose up 失败' })
    }
  } catch (err) {
    toast.error('部署失败', {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
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
      toast.success('镜像已拉取', { detail: ref })
      pullRef.value = ''
      void loadImages()
    } else {
      toast.error('拉取失败', { detail: res.error || '请检查镜像名与网络' })
    }
  } catch (err) {
    toast.error('拉取失败', {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
    })
  } finally {
    pulling.value = false
  }
}

async function doRemoveImage(img: ImageInfo): Promise<void> {
  const tagStr = repoTag(img)
  const ok = await confirm.open({
    title: `删除镜像 ${tagStr}?`,
    body: '将执行 docker rmi 删除该镜像。若有容器正在使用会删除失败。',
    confirmLabel: '删除镜像',
    variant: 'danger',
  })
  if (!ok) return
  busyImage.value = img.id
  try {
    const res = await removeImage(props.group.serverId, img.id, false)
    if (res.ok) {
      toast.success('镜像已删除', { detail: tagStr })
      void loadImages()
    } else {
      toast.error('删除失败', { detail: res.error || '镜像可能正被容器使用' })
    }
  } catch (err) {
    toast.error('删除失败', {
      detail: err instanceof HttpError ? (err.apiError?.message ?? `请求失败(${err.status})`) : '网络错误',
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
          {{ !group.reachable ? '不可达' : group.runtime ? runtimeLabel : '无容器运行时' }}
        </span>
        <span v-if="group.reachable && group.runtime" class="panel__count mono">
          {{ group.running }}/{{ group.total }} 运行
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
          容器 <span class="ctab__n">{{ group.total }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'images' }" role="tab" @click="switchTab('images')">
          镜像 <span v-if="imgState === 'loaded'" class="ctab__n">{{ images.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'stacks' }" role="tab" @click="switchTab('stacks')">
          Stacks <span v-if="stackState === 'loaded'" class="ctab__n">{{ stacks.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'volumes' }" role="tab" @click="switchTab('volumes')">
          卷 <span v-if="volState === 'loaded'" class="ctab__n">{{ volumes.length }}</span>
        </button>
        <button class="ctab" :class="{ 'ctab--active': tab === 'networks' }" role="tab" @click="switchTab('networks')">
          网络 <span v-if="netState === 'loaded'" class="ctab__n">{{ networks.length }}</span>
        </button>
      </div>

      <!-- 容器 tab -->
      <template v-if="tab === 'containers'">
        <p v-if="group.total === 0" class="panel__hint">该服务器暂无容器。</p>
        <p v-else-if="visibleContainers.length === 0" class="panel__hint">无匹配当前筛选的容器(共 {{ group.total }} 个)。</p>
        <ul v-else class="clist" role="list">
          <li v-for="c in visibleContainers" :key="c.id" class="crow" :class="{ 'crow--busy': rowBusy(c.id) }">
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
              <button class="op op--ai" title="AI 诊断:取日志 → 根因 + 修复建议" @click="emit('diagnose', c)">✦ AI</button>
              <button class="op op--ghost" title="查看容器日志(docker logs)" @click="emit('logs', c)">日志</button>
              <button class="op op--ghost" :disabled="rowBusy(c.id)" title="进入容器终端(docker exec -it)" @click="emit('terminal', c)">终端</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 镜像 tab -->
      <template v-else-if="tab === 'images'">
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="pullRef" class="pull__in mono" placeholder="拉取镜像,例:nginx:latest" @keyup.enter="doPull" />
            <button class="pull__btn" :disabled="pulling || !pullRef.trim()" @click="doPull">
              {{ pulling ? '拉取中…' : '拉取' }}
            </button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="imgState === 'loading'" @click="loadImages">↻ 刷新</button>
        </div>
        <p v-if="imgState === 'error'" class="panel__hint panel__hint--down">⚠ {{ imgError }}</p>
        <p v-else-if="imgState === 'loading' && images.length === 0" class="panel__hint">正在加载镜像…</p>
        <p v-else-if="images.length === 0" class="panel__hint">该服务器暂无镜像。</p>
        <ul v-else class="ilist" role="list">
          <li v-for="img in images" :key="img.id + repoTag(img)" class="irow" :class="{ 'irow--busy': busyImage === img.id }">
            <div class="irow__main">
              <div class="irow__tag" :title="repoTag(img)">{{ repoTag(img) }}</div>
              <div class="irow__id mono">{{ img.id }}</div>
            </div>
            <div class="irow__size mono">{{ img.size }}</div>
            <div class="irow__age">{{ img.createdSince }}</div>
            <div class="irow__act">
              <button class="op op--ghost" :disabled="busyImage === img.id" title="删除镜像(docker rmi)" @click="doRemoveImage(img)">删除</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- Stacks tab -->
      <template v-else-if="tab === 'stacks'">
        <div class="img-toolbar">
          <button class="pull__btn" @click="showDeploy = !showDeploy">{{ showDeploy ? '收起部署' : '+ 部署 Stack' }}</button>
          <span class="grow" />
          <button class="refresh" :disabled="stackState === 'loading'" @click="loadStacks">↻ 刷新</button>
        </div>

        <!-- 部署表单(贴 compose yaml) -->
        <div v-if="showDeploy" class="deploy">
          <input v-model="deployName" class="deploy__name mono" placeholder="项目名,例:my-stack" />
          <textarea
            v-model="deployCompose"
            class="deploy__yaml mono"
            rows="8"
            placeholder="粘贴 docker-compose.yml 内容…&#10;services:&#10;  web:&#10;    image: nginx:latest&#10;    ports: [&quot;8088:80&quot;]"
          />
          <div class="deploy__act">
            <span class="deploy__hint">将写入主机受管目录并 docker compose up -d</span>
            <button class="pull__btn" :disabled="deploying || !deployName.trim() || !deployCompose.trim()" @click="doDeploy">
              {{ deploying ? '部署中…' : '部署' }}
            </button>
          </div>
        </div>

        <p v-if="stackState === 'error'" class="panel__hint panel__hint--down">⚠ {{ stackError }}</p>
        <p v-else-if="stackState === 'loading' && stacks.length === 0" class="panel__hint">正在加载 Stacks…</p>
        <p v-else-if="stacks.length === 0" class="panel__hint">该服务器暂无 compose 项目。点「+ 部署 Stack」贴 yaml 部署一个。</p>
        <ul v-else class="ilist" role="list">
          <li v-for="s in stacks" :key="s.name" class="srow" :class="{ 'irow--busy': busyStack === s.name }">
            <div class="srow__main">
              <div class="srow__name">{{ s.name }}</div>
              <div class="srow__cfg mono" :title="s.configFiles">{{ s.configFiles || '—' }}</div>
            </div>
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
          </li>
        </ul>
      </template>

      <!-- 卷 tab -->
      <template v-else-if="tab === 'volumes'">
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="newVol" class="pull__in mono" placeholder="新建数据卷名" @keyup.enter="doCreateVol" />
            <button class="pull__btn" :disabled="busyVol === '@create' || !newVol.trim()" @click="doCreateVol">创建</button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="volState === 'loading'" @click="loadVolumes">↻ 刷新</button>
        </div>
        <p v-if="volState === 'error'" class="panel__hint panel__hint--down">⚠ {{ volError }}</p>
        <p v-else-if="volState === 'loading' && volumes.length === 0" class="panel__hint">正在加载数据卷…</p>
        <p v-else-if="volumes.length === 0" class="panel__hint">该服务器暂无数据卷。</p>
        <ul v-else class="ilist" role="list">
          <li v-for="v in volumes" :key="v.name" class="srow" :class="{ 'irow--busy': busyVol === v.name }">
            <div class="srow__main">
              <div class="srow__name">{{ v.name }}</div>
            </div>
            <div class="srow__status mono">{{ v.driver }}</div>
            <div class="srow__act">
              <button class="op op--ghost" :disabled="busyVol === v.name" @click="doRemoveVol(v)">删除</button>
            </div>
          </li>
        </ul>
      </template>

      <!-- 网络 tab -->
      <template v-else>
        <div class="img-toolbar">
          <div class="pull">
            <input v-model="newNet" class="pull__in mono" placeholder="新建网络名(默认 bridge 驱动)" @keyup.enter="doCreateNet" />
            <button class="pull__btn" :disabled="busyNet === '@create' || !newNet.trim()" @click="doCreateNet">创建</button>
          </div>
          <span class="grow" />
          <button class="refresh" :disabled="netState === 'loading'" @click="loadNetworks">↻ 刷新</button>
        </div>
        <p v-if="netState === 'error'" class="panel__hint panel__hint--down">⚠ {{ netError }}</p>
        <p v-else-if="netState === 'loading' && networks.length === 0" class="panel__hint">正在加载网络…</p>
        <p v-else-if="networks.length === 0" class="panel__hint">该服务器暂无网络。</p>
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
                  :title="BUILTIN_NETWORKS.has(n.name) ? '内置网络不可删除' : '删除网络(docker network rm)'"
                  @click="doRemoveNet(n)"
                >
                  删除
                </button>
              </div>
            </div>
            <!-- 展开:该网络上连着的容器 -->
            <div v-if="expandedNet === n.name" class="netdetail">
              <p v-if="netConnState === 'loading'" class="netdetail__hint">正在读取连接的容器…</p>
              <p v-else-if="netConnState === 'error'" class="netdetail__hint netdetail__hint--err">⚠ {{ netConnError }}</p>
              <p v-else-if="netConns.length === 0" class="netdetail__hint">该网络上暂无容器连接。</p>
              <ul v-else class="connlist" role="list">
                <li v-for="c in netConns" :key="c.id" class="connrow" :class="{ 'irow--busy': busyConn === c.id }">
                  <span class="connrow__name">{{ c.name }}</span>
                  <span class="connrow__ip mono">{{ c.ipv4 || '—' }}</span>
                  <button class="op op--ghost" :disabled="busyConn === c.id" @click="doDisconnect(n.name, c)">断开</button>
                </li>
              </ul>
            </div>
          </li>
        </ul>
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
.deploy__name {
  font-size: var(--text-label);
  padding: 7px 10px;
  border-radius: var(--rounded-sm);
  border: 1px solid var(--color-border-strong);
  background: var(--color-card);
  color: var(--color-text);
}
.deploy__yaml {
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
  .crow, .irow { grid-template-columns: 1fr; gap: 8px; }
  .crow__actions { justify-content: flex-start; }
}
</style>
